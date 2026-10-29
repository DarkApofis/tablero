// Package workspace holds the workspace use cases (create, list, members,
// roles) and the Authorizer every workspace-scoped use case relies on.
package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Ports.

type WorkspaceRepository interface {
	Create(ctx context.Context, name string) (domain.Workspace, error)
	AddMember(ctx context.Context, workspaceID, userID domain.ID, role domain.Role) error
	ListForUser(ctx context.Context, userID domain.ID) ([]domain.WorkspaceSummary, error)
	// Role returns domain.ErrNotMember for non-members and unknown workspaces alike.
	Role(ctx context.Context, workspaceID, userID domain.ID) (domain.Role, error)
	Members(ctx context.Context, workspaceID domain.ID) ([]domain.Member, error)
	// Member returns domain.ErrMemberNotFound if userID is not a member.
	Member(ctx context.Context, workspaceID, userID domain.ID) (domain.Member, error)
	// Lock serializes membership changes of one workspace until the transaction ends.
	Lock(ctx context.Context, workspaceID domain.ID) error
	CountAdmins(ctx context.Context, workspaceID domain.ID) (int64, error)
	UpdateRole(ctx context.Context, workspaceID, userID domain.ID, role domain.Role) error
	RemoveMember(ctx context.Context, workspaceID, userID domain.ID) error
	// UnassignTasks unassigns userID's tasks in the workspace, bumping each
	// version and logging one task_updated entry per task; it returns the entries.
	UnassignTasks(ctx context.Context, workspaceID, userID, actorID domain.ID) ([]domain.ActivityEntry, error)
}

type TxManager interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event)
}

// Authorizer establishes a user's Access to a workspace. Resources inside a
// workspace (boards, tasks, ...) are first resolved to their workspace.
type Authorizer struct {
	repo interface {
		Role(ctx context.Context, workspaceID, userID domain.ID) (domain.Role, error)
	}
}

func NewAuthorizer(repo WorkspaceRepository) *Authorizer { return &Authorizer{repo: repo} }

// Authorize returns the user's Access if their role allows action. It returns
// domain.ErrNotMember for non-members and unknown workspaces alike, and
// domain.ErrForbidden when the role is insufficient.
func (a *Authorizer) Authorize(ctx context.Context, userID, workspaceID domain.ID, action domain.Action) (domain.Access, error) {
	role, err := a.repo.Role(ctx, workspaceID, userID)
	if errors.Is(err, domain.ErrNotMember) {
		return domain.Access{}, err
	}
	if err != nil {
		return domain.Access{}, fmt.Errorf("get member role: %w", err)
	}
	acc := domain.Access{UserID: userID, WorkspaceID: workspaceID, Role: role}
	return acc, acc.Require(action)
}

// DefaultMembership puts every new account in one workspace as a member: they
// can create, edit, move and delete its tasks and manage its labels, but not
// its boards, columns, members or settings. auth.Service.WithOnboarding runs it.
type DefaultMembership struct {
	repo        WorkspaceRepository
	workspaceID domain.ID
}

func NewDefaultMembership(repo WorkspaceRepository, workspaceID domain.ID) *DefaultMembership {
	return &DefaultMembership{repo: repo, workspaceID: workspaceID}
}

func (d *DefaultMembership) Onboard(ctx context.Context, userID domain.ID) error {
	return d.repo.AddMember(ctx, d.workspaceID, userID, domain.RoleMember)
}

type Service struct {
	repo   WorkspaceRepository
	tx     TxManager
	events EventPublisher
}

func NewService(repo WorkspaceRepository, tx TxManager, events EventPublisher) *Service {
	return &Service{repo: repo, tx: tx, events: events}
}

// Create makes a workspace with the creator as its only admin.
func (s *Service) Create(ctx context.Context, creatorID domain.ID, name string) (domain.WorkspaceSummary, error) {
	name, err := domain.WorkspaceName(name)
	if err != nil {
		return domain.WorkspaceSummary{}, err
	}
	var ws domain.Workspace
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		var err error
		if ws, err = s.repo.Create(ctx, name); err != nil {
			return fmt.Errorf("create workspace: %w", err)
		}
		return s.repo.AddMember(ctx, ws.ID, creatorID, domain.RoleAdmin)
	})
	if err != nil {
		return domain.WorkspaceSummary{}, err
	}
	return domain.WorkspaceSummary{Workspace: ws, Role: domain.RoleAdmin, MemberCount: 1}, nil
}

// ListForUser returns the workspaces userID belongs to, with their role and member count.
func (s *Service) ListForUser(ctx context.Context, userID domain.ID) ([]domain.WorkspaceSummary, error) {
	return s.repo.ListForUser(ctx, userID)
}

func (s *Service) ListMembers(ctx context.Context, acc domain.Access) ([]domain.Member, error) {
	if err := acc.Require(domain.ActionRead); err != nil {
		return nil, err
	}
	return s.repo.Members(ctx, acc.WorkspaceID)
}

// ChangeRole sets targetID's role. It refuses to demote the last admin.
func (s *Service) ChangeRole(ctx context.Context, acc domain.Access, targetID domain.ID, role domain.Role) (domain.Member, error) {
	if err := acc.Require(domain.ActionManageMembers); err != nil {
		return domain.Member{}, err
	}
	if !role.Valid() {
		return domain.Member{}, domain.Invalid("role", "El rol debe ser admin, member o viewer")
	}

	var member domain.Member
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		current, err := s.lockAndLoadTarget(ctx, acc, targetID)
		if err != nil {
			return err
		}
		if current.Role == domain.RoleAdmin && role != domain.RoleAdmin {
			if err := s.ensureAnotherAdmin(ctx, acc.WorkspaceID); err != nil {
				return err
			}
		}
		if err := s.repo.UpdateRole(ctx, acc.WorkspaceID, targetID, role); err != nil {
			return fmt.Errorf("update member role: %w", err)
		}
		current.Role = role
		member = current
		return nil
	})
	return member, err
}

// RemoveMember deletes targetID from the workspace and unassigns their tasks
// there, since only members can be assignees. It refuses to remove the last admin.
func (s *Service) RemoveMember(ctx context.Context, acc domain.Access, targetID domain.ID) error {
	if err := acc.Require(domain.ActionManageMembers); err != nil {
		return err
	}
	var logged []domain.ActivityEntry
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		current, err := s.lockAndLoadTarget(ctx, acc, targetID)
		if err != nil {
			return err
		}
		if current.Role == domain.RoleAdmin {
			if err := s.ensureAnotherAdmin(ctx, acc.WorkspaceID); err != nil {
				return err
			}
		}
		if err := s.repo.RemoveMember(ctx, acc.WorkspaceID, targetID); err != nil {
			return fmt.Errorf("delete member: %w", err)
		}
		// After the delete: an assignment in flight holds the membership row
		// (see the task use cases), so the delete waits for it and this sees its task.
		if logged, err = s.repo.UnassignTasks(ctx, acc.WorkspaceID, targetID, acc.UserID); err != nil {
			return fmt.Errorf("unassign tasks: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, entry := range logged {
		s.events.Publish(ctx, entry.Event())
	}
	return nil
}

// lockAndLoadTarget locks the workspace for membership changes, re-checks
// under the lock that the actor may still manage members (they could have
// been demoted since acc was established) and loads the target.
func (s *Service) lockAndLoadTarget(ctx context.Context, acc domain.Access, targetID domain.ID) (domain.Member, error) {
	if err := s.repo.Lock(ctx, acc.WorkspaceID); err != nil {
		return domain.Member{}, fmt.Errorf("lock workspace: %w", err)
	}

	actorRole, err := s.repo.Role(ctx, acc.WorkspaceID, acc.UserID)
	if errors.Is(err, domain.ErrNotMember) {
		return domain.Member{}, err
	}
	if err != nil {
		return domain.Member{}, fmt.Errorf("get actor role: %w", err)
	}
	if !domain.Can(actorRole, domain.ActionManageMembers) {
		return domain.Member{}, domain.ErrForbidden
	}

	target, err := s.repo.Member(ctx, acc.WorkspaceID, targetID)
	if errors.Is(err, domain.ErrMemberNotFound) {
		return domain.Member{}, err
	}
	if err != nil {
		return domain.Member{}, fmt.Errorf("get member: %w", err)
	}
	return target, nil
}

// ensureAnotherAdmin must run under Lock and before the change, when the
// target is still counted as an admin.
func (s *Service) ensureAnotherAdmin(ctx context.Context, workspaceID domain.ID) error {
	admins, err := s.repo.CountAdmins(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("count admins: %w", err)
	}
	if admins <= 1 {
		return domain.ErrLastAdmin
	}
	return nil
}
