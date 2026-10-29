package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres/db"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Workspaces implements workspace.WorkspaceRepository.
type Workspaces struct{ conn }

func NewWorkspaces(pool *pgxpool.Pool) *Workspaces { return &Workspaces{conn{pool}} }

func (r *Workspaces) Create(ctx context.Context, name, slug string, createdBy domain.ID) (domain.Workspace, error) {
	ws, err := r.q(ctx).CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: name, Slug: optionalText(slug), CreatedBy: uuid.NullUUID{UUID: uuid.UUID(createdBy), Valid: true},
	})
	if pgCode(err) == pgUniqueViolation {
		return domain.Workspace{}, domain.ErrSlugTaken
	}
	return toWorkspace(ws), err
}

func (r *Workspaces) Get(ctx context.Context, workspaceID domain.ID) (domain.Workspace, error) {
	ws, err := r.q(ctx).GetWorkspace(ctx, uuid.UUID(workspaceID))
	return toWorkspace(ws), notFound(err, domain.ErrNotMember)
}

func (r *Workspaces) Stats(ctx context.Context, workspaceID domain.ID) (members, boards, tasks int64, err error) {
	s, err := r.q(ctx).GetWorkspaceStats(ctx, uuid.UUID(workspaceID))
	return s.MemberCount, s.BoardCount, s.TaskCount, err
}

func (r *Workspaces) SlugTaken(ctx context.Context, slug string) (bool, error) {
	return r.q(ctx).WorkspaceSlugTaken(ctx, optionalText(slug))
}

func (r *Workspaces) IDBySlug(ctx context.Context, slug string) (domain.ID, error) {
	id, err := r.q(ctx).GetWorkspaceIDBySlug(ctx, optionalText(slug))
	return domain.ID(id), notFound(err, domain.ErrNotMember)
}

func (r *Workspaces) Update(ctx context.Context, ws domain.Workspace) (domain.Workspace, error) {
	row, err := r.q(ctx).UpdateWorkspace(ctx, db.UpdateWorkspaceParams{
		ID: uuid.UUID(ws.ID), Name: ws.Name, Slug: optionalText(ws.Slug),
		Description: ws.Description, DefaultRole: db.WorkspaceRole(ws.DefaultRole),
	})
	if pgCode(err) == pgUniqueViolation {
		return domain.Workspace{}, domain.ErrSlugTaken
	}
	return toWorkspace(row), notFound(err, domain.ErrNotMember)
}

func (r *Workspaces) SetOwner(ctx context.Context, workspaceID, ownerID domain.ID) error {
	return r.q(ctx).SetWorkspaceOwner(ctx, db.SetWorkspaceOwnerParams{
		ID: uuid.UUID(workspaceID), OwnerID: uuid.NullUUID{UUID: uuid.UUID(ownerID), Valid: true},
	})
}

// Delete deletes its boards, members, invitations and activity too (ON
// DELETE CASCADE).
func (r *Workspaces) Delete(ctx context.Context, workspaceID domain.ID) error {
	n, err := r.q(ctx).DeleteWorkspace(ctx, uuid.UUID(workspaceID))
	return deleted(n, err, domain.ErrNotMember)
}

func (r *Workspaces) CountCreatedBy(ctx context.Context, userID domain.ID) (int64, error) {
	return r.q(ctx).CountWorkspacesCreatedBy(ctx, uuid.NullUUID{UUID: uuid.UUID(userID), Valid: true})
}

func (r *Workspaces) LockCreator(ctx context.Context, userID domain.ID) error {
	return r.q(ctx).LockWorkspaceCreator(ctx, uuid.UUID(userID))
}

// Exists reports whether the workspace exists.
func (r *Workspaces) Exists(ctx context.Context, id domain.ID) (bool, error) {
	return r.q(ctx).WorkspaceExists(ctx, uuid.UUID(id))
}

func (r *Workspaces) AddMember(ctx context.Context, workspaceID, userID domain.ID, role domain.Role) error {
	return r.q(ctx).AddWorkspaceMember(ctx, db.AddWorkspaceMemberParams{
		WorkspaceID: uuid.UUID(workspaceID), UserID: uuid.UUID(userID), Role: db.WorkspaceRole(role),
	})
}

func (r *Workspaces) ListForUser(ctx context.Context, userID domain.ID) ([]domain.WorkspaceSummary, error) {
	rows, err := r.q(ctx).ListWorkspacesForUser(ctx, uuid.UUID(userID))
	if err != nil {
		return nil, err
	}
	out := make([]domain.WorkspaceSummary, len(rows))
	for i, w := range rows {
		out[i] = domain.WorkspaceSummary{
			Workspace: domain.Workspace{ID: domain.ID(w.ID), Name: w.Name, Slug: w.Slug.String, CreatedAt: w.CreatedAt},
			Role:      domain.Role(w.Role), MemberCount: w.MemberCount,
		}
	}
	return out, nil
}

func (r *Workspaces) Role(ctx context.Context, workspaceID, userID domain.ID) (domain.Role, error) {
	role, err := r.q(ctx).GetMemberRole(ctx, db.GetMemberRoleParams{WorkspaceID: uuid.UUID(workspaceID), UserID: uuid.UUID(userID)})
	return domain.Role(role), notFound(err, domain.ErrNotMember)
}

func (r *Workspaces) Members(ctx context.Context, workspaceID domain.ID) ([]domain.Member, error) {
	rows, err := r.q(ctx).ListWorkspaceMembers(ctx, uuid.UUID(workspaceID))
	if err != nil {
		return nil, err
	}
	out := make([]domain.Member, len(rows))
	for i, m := range rows {
		out[i] = domain.Member{UserID: domain.ID(m.UserID), Email: m.Email, Name: m.Name, Role: domain.Role(m.Role), JoinedAt: m.CreatedAt}
	}
	return out, nil
}

func (r *Workspaces) Member(ctx context.Context, workspaceID, userID domain.ID) (domain.Member, error) {
	m, err := r.q(ctx).GetWorkspaceMember(ctx, db.GetWorkspaceMemberParams{WorkspaceID: uuid.UUID(workspaceID), UserID: uuid.UUID(userID)})
	if err != nil {
		return domain.Member{}, notFound(err, domain.ErrMemberNotFound)
	}
	return domain.Member{UserID: domain.ID(m.UserID), Email: m.Email, Name: m.Name, Role: domain.Role(m.Role), JoinedAt: m.CreatedAt}, nil
}

func (r *Workspaces) Lock(ctx context.Context, workspaceID domain.ID) error {
	return r.q(ctx).LockWorkspace(ctx, uuid.UUID(workspaceID))
}

func (r *Workspaces) CountAdmins(ctx context.Context, workspaceID domain.ID) (int64, error) {
	return r.q(ctx).CountWorkspaceAdmins(ctx, uuid.UUID(workspaceID))
}

func (r *Workspaces) UpdateRole(ctx context.Context, workspaceID, userID domain.ID, role domain.Role) error {
	_, err := r.q(ctx).UpdateMemberRole(ctx, db.UpdateMemberRoleParams{
		WorkspaceID: uuid.UUID(workspaceID), UserID: uuid.UUID(userID), Role: db.WorkspaceRole(role),
	})
	return err
}

// RemoveMember must run inside a transaction: it takes them off the
// workspace's private boards too.
func (r *Workspaces) RemoveMember(ctx context.Context, workspaceID, userID domain.ID) error {
	q := r.q(ctx)
	if _, err := q.DeleteWorkspaceMember(ctx, db.DeleteWorkspaceMemberParams{
		WorkspaceID: uuid.UUID(workspaceID), UserID: uuid.UUID(userID),
	}); err != nil {
		return err
	}
	return q.RemoveMemberFromBoards(ctx, db.RemoveMemberFromBoardsParams{
		WorkspaceID: uuid.UUID(workspaceID), UserID: uuid.UUID(userID),
	})
}

// AddMemberIfAbsent adds userID unless they are a member already, who keeps
// their role; it reports whether it added them.
func (r *Workspaces) AddMemberIfAbsent(ctx context.Context, workspaceID, userID domain.ID, role domain.Role) (bool, error) {
	n, err := r.q(ctx).AddWorkspaceMemberIfAbsent(ctx, db.AddWorkspaceMemberIfAbsentParams{
		WorkspaceID: uuid.UUID(workspaceID), UserID: uuid.UUID(userID), Role: db.WorkspaceRole(role),
	})
	return n > 0, err
}

// UnassignTasks must run inside a transaction. It locks the tasks first and
// then, in a fixed order, the activity lock of each board involved, like
// every other writer of the log (see Activity.Append), before writing.
func (r *Workspaces) UnassignTasks(ctx context.Context, workspaceID, userID, actorID domain.ID) ([]domain.ActivityEntry, error) {
	q := r.q(ctx)
	boards, err := q.LockMemberTasks(ctx, db.LockMemberTasksParams{WorkspaceID: uuid.UUID(workspaceID), UserID: uuid.UUID(userID)})
	if err != nil {
		return nil, fmt.Errorf("lock member tasks: %w", err)
	}
	if len(boards) == 0 {
		return nil, nil
	}
	slices.SortFunc(boards, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for _, b := range slices.Compact(boards) {
		if err := q.LockBoardActivity(ctx, b); err != nil {
			return nil, fmt.Errorf("lock board activity: %w", err)
		}
	}
	rows, err := q.UnassignMemberTasks(ctx, db.UnassignMemberTasksParams{
		WorkspaceID: uuid.UUID(workspaceID), UserID: uuid.UUID(userID), ActorID: uuid.UUID(actorID),
	})
	if err != nil {
		return nil, err
	}
	out := make([]domain.ActivityEntry, len(rows))
	for i, row := range rows {
		out[i] = toActivity(row, "")
	}
	return out, nil
}

// LockMembership blocks the membership row from being deleted until the
// transaction ends (FOR KEY SHARE: the role can still change). It returns
// domain.ErrNotMember if userID is not a member.
func (r *Workspaces) LockMembership(ctx context.Context, workspaceID, userID domain.ID) error {
	_, err := r.q(ctx).LockMembership(ctx, db.LockMembershipParams{WorkspaceID: uuid.UUID(workspaceID), UserID: uuid.UUID(userID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotMember
	}
	return err
}

func toWorkspace(w db.Workspace) domain.Workspace {
	return domain.Workspace{
		ID: domain.ID(w.ID), Name: w.Name, Slug: w.Slug.String, Description: w.Description,
		DefaultRole: domain.Role(w.DefaultRole), OwnerID: optionalID(w.OwnerID), CreatedAt: w.CreatedAt,
	}
}

// optionalText stores "" as NULL.
func optionalText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

func toActivity(a db.ActivityLog, actorName string) domain.ActivityEntry {
	return domain.ActivityEntry{
		ID: a.ID, WorkspaceID: domain.ID(a.WorkspaceID), BoardID: domain.ID(a.BoardID), ActorID: optionalID(a.ActorID),
		ActorName: actorName, Type: a.Type, Payload: a.Payload, CreatedAt: a.CreatedAt,
	}
}

func optionalID(id uuid.NullUUID) *domain.ID {
	if !id.Valid {
		return nil
	}
	d := domain.ID(id.UUID)
	return &d
}
