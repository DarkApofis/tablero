package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/auth"
	"github.com/DarkApofis/tablero/backend/internal/db"
)

const MaxWorkspaceNameLen = 100

var (
	// ErrNotMember: the user does not belong to the workspace, or the
	// workspace does not exist. Callers must not tell the two apart.
	ErrNotMember = errors.New("not a member of the workspace")
	// ErrForbidden: the caller is a member but their role does not allow the action.
	ErrForbidden = errors.New("action not allowed for role")
	// ErrMemberNotFound: the target user of a membership change is not a member.
	ErrMemberNotFound = errors.New("member not found")
	// ErrLastAdmin: the change would leave the workspace without an admin.
	ErrLastAdmin = errors.New("workspace must keep at least one admin")
)

// WorkspaceQueries is the persistence the workspace service needs; *db.Queries satisfies it.
type WorkspaceQueries interface {
	CreateWorkspace(ctx context.Context, name string) (db.Workspace, error)
	AddWorkspaceMember(ctx context.Context, arg db.AddWorkspaceMemberParams) error
	ListWorkspacesForUser(ctx context.Context, userID uuid.UUID) ([]db.ListWorkspacesForUserRow, error)
	GetMemberRole(ctx context.Context, arg db.GetMemberRoleParams) (db.WorkspaceRole, error)
	ListWorkspaceMembers(ctx context.Context, workspaceID uuid.UUID) ([]db.ListWorkspaceMembersRow, error)
	GetWorkspaceMember(ctx context.Context, arg db.GetWorkspaceMemberParams) (db.GetWorkspaceMemberRow, error)
	LockWorkspace(ctx context.Context, id uuid.UUID) error
	CountWorkspaceAdmins(ctx context.Context, workspaceID uuid.UUID) (int64, error)
	UpdateMemberRole(ctx context.Context, arg db.UpdateMemberRoleParams) (int64, error)
	DeleteWorkspaceMember(ctx context.Context, arg db.DeleteWorkspaceMemberParams) (int64, error)
	UnassignMemberTasks(ctx context.Context, arg db.UnassignMemberTasksParams) (int64, error)
}

// WorkspaceStore adds transactions on top of the queries.
type WorkspaceStore interface {
	WorkspaceQueries
	InTx(ctx context.Context, fn func(q WorkspaceQueries) error) error
}

// PostgresWorkspaceStore implements WorkspaceStore with a pgx pool.
type PostgresWorkspaceStore struct {
	*db.Queries
	pool *pgxpool.Pool
}

func NewPostgresWorkspaceStore(pool *pgxpool.Pool) *PostgresWorkspaceStore {
	return &PostgresWorkspaceStore{Queries: db.New(pool), pool: pool}
}

func (s *PostgresWorkspaceStore) InTx(ctx context.Context, fn func(q WorkspaceQueries) error) error {
	return pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return fn(s.WithTx(tx))
	})
}

type WorkspaceService struct {
	store WorkspaceStore
}

func NewWorkspaceService(store WorkspaceStore) *WorkspaceService {
	return &WorkspaceService{store: store}
}

// Create makes a workspace with the creator as its only admin.
func (s *WorkspaceService) Create(ctx context.Context, creatorID uuid.UUID, name string) (db.ListWorkspacesForUserRow, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return db.ListWorkspacesForUserRow{}, &ValidationError{Fields: map[string]string{"name": "El nombre es obligatorio"}}
	}
	if utf8.RuneCountInString(name) > MaxWorkspaceNameLen {
		return db.ListWorkspacesForUserRow{}, &ValidationError{Fields: map[string]string{
			"name": fmt.Sprintf("El nombre no puede superar %d caracteres", MaxWorkspaceNameLen),
		}}
	}

	var ws db.Workspace
	err := s.store.InTx(ctx, func(q WorkspaceQueries) error {
		var err error
		if ws, err = q.CreateWorkspace(ctx, name); err != nil {
			return fmt.Errorf("create workspace: %w", err)
		}
		return q.AddWorkspaceMember(ctx, db.AddWorkspaceMemberParams{
			WorkspaceID: ws.ID, UserID: creatorID, Role: db.WorkspaceRoleAdmin,
		})
	})
	if err != nil {
		return db.ListWorkspacesForUserRow{}, err
	}
	return db.ListWorkspacesForUserRow{
		ID: ws.ID, Name: ws.Name, CreatedAt: ws.CreatedAt, Role: db.WorkspaceRoleAdmin, MemberCount: 1,
	}, nil
}

// ListForUser returns the workspaces userID belongs to, with their role and member count.
func (s *WorkspaceService) ListForUser(ctx context.Context, userID uuid.UUID) ([]db.ListWorkspacesForUserRow, error) {
	return s.store.ListWorkspacesForUser(ctx, userID)
}

// Authorize returns the user's role if it allows action. It returns
// ErrNotMember for non-members and unknown workspaces alike, and ErrForbidden
// when the role is insufficient.
func (s *WorkspaceService) Authorize(ctx context.Context, workspaceID, userID uuid.UUID, action auth.Action) (db.WorkspaceRole, error) {
	role, err := s.store.GetMemberRole(ctx, db.GetMemberRoleParams{WorkspaceID: workspaceID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotMember
	}
	if err != nil {
		return "", fmt.Errorf("get member role: %w", err)
	}
	if !auth.Can(role, action) {
		return role, ErrForbidden
	}
	return role, nil
}

func (s *WorkspaceService) ListMembers(ctx context.Context, workspaceID uuid.UUID) ([]db.ListWorkspaceMembersRow, error) {
	return s.store.ListWorkspaceMembers(ctx, workspaceID)
}

// ChangeRole sets targetID's role. It refuses to demote the last admin.
func (s *WorkspaceService) ChangeRole(ctx context.Context, workspaceID, actorID, targetID uuid.UUID, role db.WorkspaceRole) (db.GetWorkspaceMemberRow, error) {
	if !role.Valid() {
		return db.GetWorkspaceMemberRow{}, &ValidationError{Fields: map[string]string{
			"role": "El rol debe ser admin, member o viewer",
		}}
	}

	var member db.GetWorkspaceMemberRow
	err := s.store.InTx(ctx, func(q WorkspaceQueries) error {
		current, err := s.lockAndLoadTarget(ctx, q, workspaceID, actorID, targetID)
		if err != nil {
			return err
		}
		if current.Role == db.WorkspaceRoleAdmin && role != db.WorkspaceRoleAdmin {
			if err := ensureAnotherAdmin(ctx, q, workspaceID); err != nil {
				return err
			}
		}
		if _, err := q.UpdateMemberRole(ctx, db.UpdateMemberRoleParams{
			WorkspaceID: workspaceID, UserID: targetID, Role: role,
		}); err != nil {
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
func (s *WorkspaceService) RemoveMember(ctx context.Context, workspaceID, actorID, targetID uuid.UUID) error {
	return s.store.InTx(ctx, func(q WorkspaceQueries) error {
		current, err := s.lockAndLoadTarget(ctx, q, workspaceID, actorID, targetID)
		if err != nil {
			return err
		}
		if current.Role == db.WorkspaceRoleAdmin {
			if err := ensureAnotherAdmin(ctx, q, workspaceID); err != nil {
				return err
			}
		}
		if _, err := q.DeleteWorkspaceMember(ctx, db.DeleteWorkspaceMemberParams{
			WorkspaceID: workspaceID, UserID: targetID,
		}); err != nil {
			return fmt.Errorf("delete member: %w", err)
		}
		// After the delete: an assignment in flight holds the membership row
		// (LockMembership), so the delete waits for it and this sees its task.
		if _, err := q.UnassignMemberTasks(ctx, db.UnassignMemberTasksParams{
			WorkspaceID: workspaceID, UserID: targetID, ActorID: actorID,
		}); err != nil {
			return fmt.Errorf("unassign tasks: %w", err)
		}
		return nil
	})
}

// lockAndLoadTarget locks the workspace for membership changes, re-checks
// under the lock that the actor may still manage members (they could have
// been demoted since the request was authorized) and loads the target.
func (s *WorkspaceService) lockAndLoadTarget(ctx context.Context, q WorkspaceQueries, workspaceID, actorID, targetID uuid.UUID) (db.GetWorkspaceMemberRow, error) {
	if err := q.LockWorkspace(ctx, workspaceID); err != nil {
		return db.GetWorkspaceMemberRow{}, fmt.Errorf("lock workspace: %w", err)
	}

	actorRole, err := q.GetMemberRole(ctx, db.GetMemberRoleParams{WorkspaceID: workspaceID, UserID: actorID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.GetWorkspaceMemberRow{}, ErrNotMember
	}
	if err != nil {
		return db.GetWorkspaceMemberRow{}, fmt.Errorf("get actor role: %w", err)
	}
	if !auth.Can(actorRole, auth.ActionManageMembers) {
		return db.GetWorkspaceMemberRow{}, ErrForbidden
	}

	target, err := q.GetWorkspaceMember(ctx, db.GetWorkspaceMemberParams{WorkspaceID: workspaceID, UserID: targetID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.GetWorkspaceMemberRow{}, ErrMemberNotFound
	}
	if err != nil {
		return db.GetWorkspaceMemberRow{}, fmt.Errorf("get member: %w", err)
	}
	return target, nil
}

// ensureAnotherAdmin must run under LockWorkspace and before the change, when
// the target is still counted as an admin.
func ensureAnotherAdmin(ctx context.Context, q WorkspaceQueries, workspaceID uuid.UUID) error {
	admins, err := q.CountWorkspaceAdmins(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("count admins: %w", err)
	}
	if admins <= 1 {
		return ErrLastAdmin
	}
	return nil
}
