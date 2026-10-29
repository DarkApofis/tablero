// Package workspace holds the workspace use cases (create, list, members,
// roles) and the Authorizer every workspace-scoped use case relies on.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Ports.

type WorkspaceRepository interface {
	// Create stores a workspace created, and owned, by createdBy. It returns
	// domain.ErrSlugTaken if another workspace has the slug.
	Create(ctx context.Context, name, slug string, createdBy domain.ID) (domain.Workspace, error)
	// Get returns domain.ErrNotMember if the workspace does not exist.
	Get(ctx context.Context, workspaceID domain.ID) (domain.Workspace, error)
	// Stats counts the workspace's members, boards (not archived) and tasks.
	Stats(ctx context.Context, workspaceID domain.ID) (members, boards, tasks int64, err error)
	SlugTaken(ctx context.Context, slug string) (bool, error)
	// IDBySlug returns domain.ErrNotMember if no workspace has the slug.
	IDBySlug(ctx context.Context, slug string) (domain.ID, error)
	// Update stores the workspace's name, slug, description and default
	// role; domain.ErrSlugTaken.
	Update(ctx context.Context, ws domain.Workspace) (domain.Workspace, error)
	SetOwner(ctx context.Context, workspaceID, ownerID domain.ID) error
	// Delete deletes the workspace with everything in it.
	Delete(ctx context.Context, workspaceID domain.ID) error
	// CountCreatedBy is how many existing workspaces userID created.
	CountCreatedBy(ctx context.Context, userID domain.ID) (int64, error)
	// LockCreator serializes userID's workspace creations until the transaction ends.
	LockCreator(ctx context.Context, userID domain.ID) error
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
	// RemoveMember also takes them off the workspace's private boards.
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

// BoardGate tells, for a board, what the workspace role alone does not.
type BoardGate interface {
	// BoardAccess reports whether userID may see the board when they are not
	// an admin of its workspace, and whether it is archived. It returns
	// domain.ErrNotMember if the board does not exist.
	BoardAccess(ctx context.Context, boardID, userID domain.ID) (visible, archived bool, err error)
}

// Authorizer establishes a user's Access to a workspace. Resources inside a
// workspace (boards, tasks, ...) are first resolved to their workspace and
// board.
type Authorizer struct {
	repo interface {
		Role(ctx context.Context, workspaceID, userID domain.ID) (domain.Role, error)
	}
	boards BoardGate
}

func NewAuthorizer(repo WorkspaceRepository) *Authorizer { return &Authorizer{repo: repo} }

// WithBoards returns a copy that also enforces private and archived boards
// (see AuthorizeAt). Without it every board is open to its workspace.
func (a *Authorizer) WithBoards(gate BoardGate) *Authorizer {
	c := *a
	c.boards = gate
	return &c
}

// Authorize returns the user's Access if their role allows action. It returns
// domain.ErrNotMember for non-members and unknown workspaces alike, and
// domain.ErrForbidden when the role is insufficient.
func (a *Authorizer) Authorize(ctx context.Context, userID, workspaceID domain.ID, action domain.Action) (domain.Access, error) {
	return a.AuthorizeAt(ctx, userID, domain.Location{WorkspaceID: workspaceID}, action)
}

// archivedAllows is what can be done on an archived board: read it, and
// what admins do to boards (restore, rename, delete it).
var archivedAllows = map[domain.Action]bool{domain.ActionRead: true, domain.ActionManageBoards: true}

// AuthorizeAt is Authorize for something on a board (loc.BoardID set; for
// the workspace itself it is domain.NilID). A private board is, to anyone
// who is not an admin nor was given it, like one that does not exist
// (domain.ErrNotMember); on an archived one only reading and managing boards
// are allowed (domain.ErrBoardArchived).
func (a *Authorizer) AuthorizeAt(ctx context.Context, userID domain.ID, loc domain.Location, action domain.Action) (domain.Access, error) {
	role, err := a.repo.Role(ctx, loc.WorkspaceID, userID)
	if errors.Is(err, domain.ErrNotMember) {
		return domain.Access{}, err
	}
	if err != nil {
		return domain.Access{}, fmt.Errorf("get member role: %w", err)
	}
	acc := domain.Access{UserID: userID, WorkspaceID: loc.WorkspaceID, Role: role}
	if loc.BoardID != domain.NilID && a.boards != nil {
		visible, archived, err := a.boards.BoardAccess(ctx, loc.BoardID, userID)
		if errors.Is(err, domain.ErrNotMember) {
			return domain.Access{}, err
		}
		if err != nil {
			return domain.Access{}, fmt.Errorf("get board access: %w", err)
		}
		if !visible && role != domain.RoleAdmin {
			return domain.Access{}, domain.ErrNotMember
		}
		if err := acc.Require(action); err != nil {
			return acc, err
		}
		if archived && !archivedAllows[action] {
			return acc, domain.ErrBoardArchived
		}
		return acc, nil
	}
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

func (d *DefaultMembership) Onboard(ctx context.Context, user domain.User) error {
	return d.repo.AddMember(ctx, d.workspaceID, user.ID, domain.RoleMember)
}

type Service struct {
	repo   WorkspaceRepository
	tx     TxManager
	events EventPublisher
	// defaultID is the workspace every new account joins (NilID: none).
	defaultID domain.ID
}

func NewService(repo WorkspaceRepository, tx TxManager, events EventPublisher) *Service {
	return &Service{repo: repo, tx: tx, events: events}
}

// WithDefault returns a copy that knows the workspace every new account
// joins, which cannot be deleted.
func (s *Service) WithDefault(workspaceID domain.ID) *Service {
	c := *s
	c.defaultID = workspaceID
	return &c
}

// maxSlugTries bounds the numbered addresses tried for a name ("equipo-2",
// "equipo-3", ...) before giving up.
const maxSlugTries = 50

// Create makes a workspace with the creator as its owner and only admin,
// unless they already created domain.MaxCreatedWorkspaces
// (domain.ErrWorkspaceLimit). Without a slug it gets one from its name,
// numbered if taken; a slug given that is taken is domain.ErrSlugTaken.
func (s *Service) Create(ctx context.Context, creatorID domain.ID, name, slug string) (domain.WorkspaceSummary, error) {
	name, err := domain.WorkspaceName(name)
	if err != nil {
		return domain.WorkspaceSummary{}, err
	}
	chosen := strings.TrimSpace(slug) != ""
	if chosen {
		if slug, err = domain.WorkspaceSlug(slug); err != nil {
			return domain.WorkspaceSummary{}, err
		}
	}
	var ws domain.Workspace
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.repo.LockCreator(ctx, creatorID); err != nil {
			return fmt.Errorf("lock creator: %w", err)
		}
		created, err := s.repo.CountCreatedBy(ctx, creatorID)
		if err != nil {
			return fmt.Errorf("count created workspaces: %w", err)
		}
		if created >= domain.MaxCreatedWorkspaces {
			return domain.ErrWorkspaceLimit
		}
		if !chosen {
			if slug, err = s.freeSlug(ctx, domain.Slugify(name)); err != nil {
				return err
			}
		}
		ws, err = s.repo.Create(ctx, name, slug, creatorID)
		if errors.Is(err, domain.ErrSlugTaken) {
			return err
		}
		if err != nil {
			return fmt.Errorf("create workspace: %w", err)
		}
		return s.repo.AddMember(ctx, ws.ID, creatorID, domain.RoleAdmin)
	})
	if err != nil {
		return domain.WorkspaceSummary{}, err
	}
	return domain.WorkspaceSummary{Workspace: ws, Role: domain.RoleAdmin, MemberCount: 1}, nil
}

// freeSlug returns base, or base numbered ("base-2", ...), whichever is
// free; past maxSlugTries numbers, base with a random suffix.
func (s *Service) freeSlug(ctx context.Context, base string) (string, error) {
	for n := 1; n <= maxSlugTries+3; n++ {
		candidate := base
		switch {
		case n > maxSlugTries:
			// #nosec G404 -- an address suffix, not a secret
			candidate = fmt.Sprintf("%s-%04x", base[:min(len(base), domain.MaxSlugLen-5)], rand.IntN(1<<16))
		case n > 1:
			candidate = fmt.Sprintf("%s-%d", base, n)
		}
		taken, err := s.repo.SlugTaken(ctx, candidate)
		if err != nil {
			return "", fmt.Errorf("check slug: %w", err)
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", domain.ErrSlugTaken
}

// SlugAvailable checks an address and tells whether no workspace has it,
// returning it normalized (trimmed, lowercase).
func (s *Service) SlugAvailable(ctx context.Context, slug string) (string, bool, error) {
	slug, err := domain.WorkspaceSlug(slug)
	if err != nil {
		return "", false, err
	}
	taken, err := s.repo.SlugTaken(ctx, slug)
	return slug, !taken, err
}

// SuggestSlug is the address Create would give a workspace with that name.
func (s *Service) SuggestSlug(ctx context.Context, name string) (string, error) {
	return s.freeSlug(ctx, domain.Slugify(name))
}

// IDBySlug finds a workspace by its address for one of its members; anyone
// else gets domain.ErrNotMember, as for an address nobody has.
func (s *Service) IDBySlug(ctx context.Context, userID domain.ID, slug string) (domain.ID, error) {
	id, err := s.repo.IDBySlug(ctx, strings.ToLower(strings.TrimSpace(slug)))
	if err != nil {
		return domain.NilID, err
	}
	if _, err := s.repo.Role(ctx, id, userID); err != nil {
		return domain.NilID, err
	}
	return id, nil
}

// Get returns the workspace as acc's user sees it, with what it holds.
func (s *Service) Get(ctx context.Context, acc domain.Access) (domain.WorkspaceDetail, error) {
	if err := acc.Require(domain.ActionRead); err != nil {
		return domain.WorkspaceDetail{}, err
	}
	ws, err := s.repo.Get(ctx, acc.WorkspaceID)
	if err != nil {
		return domain.WorkspaceDetail{}, err
	}
	members, boards, tasks, err := s.repo.Stats(ctx, acc.WorkspaceID)
	if err != nil {
		return domain.WorkspaceDetail{}, fmt.Errorf("workspace stats: %w", err)
	}
	return domain.WorkspaceDetail{
		WorkspaceSummary: domain.WorkspaceSummary{Workspace: ws, Role: acc.Role, MemberCount: members},
		BoardCount:       boards, TaskCount: tasks, IsDefault: ws.ID == s.defaultID,
	}, nil
}

// Update changes the workspace's settings: name, address, description and
// the role new members get by default.
func (s *Service) Update(ctx context.Context, acc domain.Access, c domain.WorkspaceChanges) (domain.WorkspaceDetail, error) {
	if err := acc.Require(domain.ActionManageSettings); err != nil {
		return domain.WorkspaceDetail{}, err
	}
	fields := map[string]string{}
	check := func(field string, err error) {
		if v, ok := errors.AsType[*domain.ValidationError](err); ok {
			fields[field] = v.Fields[field]
		}
	}
	var name, slug, description string
	var err error
	if c.Name != nil {
		name, err = domain.WorkspaceName(*c.Name)
		check("name", err)
	}
	if c.Slug != nil {
		slug, err = domain.WorkspaceSlug(*c.Slug)
		check("slug", err)
	}
	if c.Description != nil {
		description, err = domain.WorkspaceDescription(*c.Description)
		check("description", err)
	}
	if c.DefaultRole != nil && !c.DefaultRole.Valid() {
		fields["default_role"] = "El rol debe ser admin, member o viewer"
	}
	if len(fields) > 0 {
		return domain.WorkspaceDetail{}, &domain.ValidationError{Fields: fields}
	}

	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.lockAndCheckActor(ctx, acc, domain.ActionManageSettings); err != nil {
			return err
		}
		ws, err := s.repo.Get(ctx, acc.WorkspaceID)
		if err != nil {
			return err
		}
		if c.Name != nil {
			ws.Name = name
		}
		if c.Slug != nil {
			ws.Slug = slug
		}
		if c.Description != nil {
			ws.Description = description
		}
		if c.DefaultRole != nil {
			ws.DefaultRole = *c.DefaultRole
		}
		_, err = s.repo.Update(ctx, ws)
		if err != nil && !errors.Is(err, domain.ErrSlugTaken) {
			return fmt.Errorf("update workspace: %w", err)
		}
		return err
	})
	if err != nil {
		return domain.WorkspaceDetail{}, err
	}
	return s.Get(ctx, acc)
}

// Delete deletes the workspace with its boards, tasks, members and
// invitations. The default workspace cannot be deleted.
func (s *Service) Delete(ctx context.Context, acc domain.Access) error {
	if err := acc.Require(domain.ActionManageSettings); err != nil {
		return err
	}
	if acc.WorkspaceID == s.defaultID {
		return domain.ErrDefaultWorkspace
	}
	return s.tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.lockAndCheckActor(ctx, acc, domain.ActionManageSettings); err != nil {
			return err
		}
		return s.repo.Delete(ctx, acc.WorkspaceID)
	})
}

// Transfer makes newOwnerID, another admin of the workspace, its owner. Only
// the owner may (any admin when it has none: domain.Workspace.MayTransfer).
func (s *Service) Transfer(ctx context.Context, acc domain.Access, newOwnerID domain.ID) (domain.WorkspaceDetail, error) {
	if err := acc.Require(domain.ActionManageSettings); err != nil {
		return domain.WorkspaceDetail{}, err
	}
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.lockAndCheckActor(ctx, acc, domain.ActionManageSettings); err != nil {
			return err
		}
		ws, err := s.repo.Get(ctx, acc.WorkspaceID)
		if err != nil {
			return err
		}
		if !ws.MayTransfer(acc.UserID) {
			return domain.ErrNotOwner
		}
		target, err := s.repo.Member(ctx, acc.WorkspaceID, newOwnerID)
		if errors.Is(err, domain.ErrMemberNotFound) {
			return err
		}
		if err != nil {
			return fmt.Errorf("get member: %w", err)
		}
		if target.Role != domain.RoleAdmin {
			return domain.Invalid("user_id", "La propiedad solo puede pasar a otro admin")
		}
		return s.repo.SetOwner(ctx, acc.WorkspaceID, newOwnerID)
	})
	if err != nil {
		return domain.WorkspaceDetail{}, err
	}
	return s.Get(ctx, acc)
}

// CreatedCount is how many workspaces userID created, out of
// domain.MaxCreatedWorkspaces.
func (s *Service) CreatedCount(ctx context.Context, userID domain.ID) (int64, error) {
	return s.repo.CountCreatedBy(ctx, userID)
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
			if err := s.ensureNotOwner(ctx, acc.WorkspaceID, targetID); err != nil {
				return err
			}
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
			if err := s.ensureNotOwner(ctx, acc.WorkspaceID, targetID); err != nil {
				return err
			}
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

// lockAndCheckActor locks the workspace for membership and settings changes
// and re-checks under the lock that the actor may still do action (they
// could have been demoted since acc was established).
func (s *Service) lockAndCheckActor(ctx context.Context, acc domain.Access, action domain.Action) error {
	if err := s.repo.Lock(ctx, acc.WorkspaceID); err != nil {
		return fmt.Errorf("lock workspace: %w", err)
	}
	actorRole, err := s.repo.Role(ctx, acc.WorkspaceID, acc.UserID)
	if errors.Is(err, domain.ErrNotMember) {
		return err
	}
	if err != nil {
		return fmt.Errorf("get actor role: %w", err)
	}
	if !domain.Can(actorRole, action) {
		return domain.ErrForbidden
	}
	return nil
}

// lockAndLoadTarget locks the workspace, re-checks that the actor may still
// manage members and loads the target.
func (s *Service) lockAndLoadTarget(ctx context.Context, acc domain.Access, targetID domain.ID) (domain.Member, error) {
	if err := s.lockAndCheckActor(ctx, acc, domain.ActionManageMembers); err != nil {
		return domain.Member{}, err
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

// ensureNotOwner refuses to demote or remove the workspace's owner. It must
// run under Lock, which transfers take too.
func (s *Service) ensureNotOwner(ctx context.Context, workspaceID, targetID domain.ID) error {
	ws, err := s.repo.Get(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}
	if ws.IsOwner(targetID) {
		return domain.ErrOwnerStaysAdmin
	}
	return nil
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
