package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres/db"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Demo implements demo.Store: what seeding and resetting the demo need beyond
// the use cases. Inside a transaction (TxManager) it uses it.
type Demo struct{ conn }

func NewDemo(pool *pgxpool.Pool) *Demo { return &Demo{conn{pool}} }

func (r *Demo) UpsertUser(ctx context.Context, email, name, passwordHash string) (domain.User, error) {
	u, err := r.q(ctx).UpsertDemoUser(ctx, db.UpsertDemoUserParams{Email: email, Name: name, PasswordHash: passwordHash})
	return toUser(u), err
}

func (r *Demo) UserByEmail(ctx context.Context, email string) (domain.User, bool, error) {
	u, err := r.q(ctx).GetUserByEmail(ctx, email)
	return toUser(u), err == nil, noRows(err)
}

func (r *Demo) AdminWorkspace(ctx context.Context, adminID domain.ID, name string) (domain.ID, bool, error) {
	id, err := r.q(ctx).FindAdminWorkspace(ctx, db.FindAdminWorkspaceParams{UserID: uuid.UUID(adminID), Name: name})
	return domain.ID(id), err == nil, noRows(err)
}

func (r *Demo) SetMember(ctx context.Context, workspaceID, userID domain.ID, role domain.Role) error {
	return r.q(ctx).UpsertWorkspaceMember(ctx, db.UpsertWorkspaceMemberParams{
		WorkspaceID: uuid.UUID(workspaceID), UserID: uuid.UUID(userID), Role: db.WorkspaceRole(role),
	})
}

func (r *Demo) BoardByName(ctx context.Context, workspaceID domain.ID, name string) (domain.ID, bool, error) {
	id, err := r.q(ctx).FindBoardByName(ctx, db.FindBoardByNameParams{WorkspaceID: uuid.UUID(workspaceID), Name: name})
	return domain.ID(id), err == nil, noRows(err)
}

func (r *Demo) DeleteOtherBoards(ctx context.Context, workspaceID, keepID domain.ID) error {
	return r.q(ctx).DeleteOtherBoards(ctx, db.DeleteOtherBoardsParams{WorkspaceID: uuid.UUID(workspaceID), ID: uuid.UUID(keepID)})
}

// ClearBoard deletes the board's columns, tasks, labels and activity, and
// keeps the board.
func (r *Demo) ClearBoard(ctx context.Context, boardID domain.ID) error {
	q := r.q(ctx)
	for _, clear := range []func(context.Context, uuid.UUID) error{q.ClearBoardColumns, q.ClearBoardLabels, q.ClearBoardActivity} {
		if err := clear(ctx, uuid.UUID(boardID)); err != nil {
			return err
		}
	}
	return nil
}

func (r *Demo) SetActivityTimes(ctx context.Context, boardID domain.ID, times []time.Time) error {
	return r.q(ctx).SetActivityTimes(ctx, db.SetActivityTimesParams{BoardID: uuid.UUID(boardID), Times: times})
}

func (r *Demo) CountUsersSince(ctx context.Context, emailSuffix string, since time.Time) (int64, error) {
	return r.q(ctx).CountUsersByEmailSuffixSince(ctx, db.CountUsersByEmailSuffixSinceParams{Suffix: emailSuffix, Since: since})
}

func (r *Demo) DeleteUsersBefore(ctx context.Context, emailSuffix string, before time.Time) (int64, error) {
	return r.q(ctx).DeleteUsersByEmailSuffixBefore(ctx, db.DeleteUsersByEmailSuffixBeforeParams{Suffix: emailSuffix, Before: before})
}

// TryLock takes the demo's lock until the transaction in ctx ends; false if
// another transaction holds it.
func (r *Demo) TryLock(ctx context.Context) (bool, error) {
	return r.q(ctx).TryDemoLock(ctx)
}

// noRows turns "no rows" into a nil error (the caller reports not found).
func noRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}
