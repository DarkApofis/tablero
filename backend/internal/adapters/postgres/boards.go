package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres/db"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Boards implements board.BoardRepository (boards, columns and the board
// detail reads) and the column part of the task use cases' BoardRepository.
type Boards struct{ conn }

func NewBoards(pool *pgxpool.Pool) *Boards { return &Boards{conn{pool}} }

func (r *Boards) LocationOfBoard(ctx context.Context, boardID domain.ID) (domain.Location, error) {
	row, err := r.q(ctx).GetBoardWorkspaceID(ctx, uuid.UUID(boardID))
	return domain.Location{WorkspaceID: domain.ID(row.WorkspaceID), BoardID: domain.ID(row.BoardID)}, notFound(err, domain.ErrNotMember)
}

func (r *Boards) LocationOfColumn(ctx context.Context, columnID domain.ID) (domain.Location, error) {
	row, err := r.q(ctx).GetColumnWorkspaceID(ctx, uuid.UUID(columnID))
	return domain.Location{WorkspaceID: domain.ID(row.WorkspaceID), BoardID: domain.ID(row.BoardID)}, notFound(err, domain.ErrNotMember)
}

// BoardAccess implements workspace.BoardGate.
func (r *Boards) BoardAccess(ctx context.Context, boardID, userID domain.ID) (visible, archived bool, err error) {
	row, err := r.q(ctx).GetBoardAccess(ctx, db.GetBoardAccessParams{BoardID: uuid.UUID(boardID), UserID: uuid.UUID(userID)})
	return row.Visible, row.Archived, notFound(err, domain.ErrNotMember)
}

// --- boards ---

func (r *Boards) CreateBoard(ctx context.Context, workspaceID domain.ID, name string, visibility domain.Visibility, createdBy domain.ID) (domain.Board, error) {
	b, err := r.q(ctx).CreateBoard(ctx, db.CreateBoardParams{
		WorkspaceID: uuid.UUID(workspaceID), Name: name, Visibility: db.BoardVisibility(visibility),
		CreatedBy: uuid.NullUUID{UUID: uuid.UUID(createdBy), Valid: true},
	})
	return toBoard(b), err
}

// ListBoards runs two queries however many boards there are: the boards,
// then who changed each.
func (r *Boards) ListBoards(ctx context.Context, workspaceID, userID domain.ID, seeAll, archived bool) ([]domain.BoardSummary, error) {
	q := r.q(ctx)
	rows, err := q.ListBoardSummaries(ctx, db.ListBoardSummariesParams{
		WorkspaceID: uuid.UUID(workspaceID), UserID: uuid.UUID(userID), SeeAll: seeAll, Archived: archived,
	})
	if err != nil {
		return nil, err
	}
	out := make([]domain.BoardSummary, len(rows))
	ids := make([]uuid.UUID, len(rows))
	index := make(map[uuid.UUID]int, len(rows))
	for i, row := range rows {
		out[i] = domain.BoardSummary{Board: toBoard(row.Board), TaskCount: row.TaskCount, UpdatedAt: row.UpdatedAt, Actors: []domain.PresenceUser{}}
		ids[i] = row.Board.ID
		index[row.Board.ID] = i
	}
	if len(ids) == 0 {
		return out, nil
	}
	actors, err := q.ListBoardActors(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, a := range actors { // ordered by board, most recent first
		i := index[a.BoardID]
		out[i].Actors = append(out[i].Actors, domain.PresenceUser{ID: domain.ID(a.UserID.UUID), Name: a.Name})
	}
	return out, nil
}

func (r *Boards) GetBoard(ctx context.Context, boardID domain.ID) (domain.Board, error) {
	b, err := r.q(ctx).GetBoard(ctx, uuid.UUID(boardID))
	return toBoard(b), notFound(err, domain.ErrBoardNotFound)
}

func (r *Boards) UpdateBoard(ctx context.Context, b domain.Board) (domain.Board, error) {
	row, err := r.q(ctx).UpdateBoard(ctx, db.UpdateBoardParams{
		ID: uuid.UUID(b.ID), Name: b.Name, Visibility: db.BoardVisibility(b.Visibility), ArchivedAt: b.ArchivedAt,
	})
	return toBoard(row), notFound(err, domain.ErrBoardNotFound)
}

func (r *Boards) BoardMembers(ctx context.Context, boardID domain.ID) ([]domain.BoardMember, error) {
	rows, err := r.q(ctx).ListBoardMembers(ctx, uuid.UUID(boardID))
	return mapAll(rows, func(m db.ListBoardMembersRow) domain.BoardMember {
		return domain.BoardMember{UserID: domain.ID(m.UserID), Name: m.Name, Email: m.Email}
	}), err
}

// SetBoardMembers must run inside a transaction.
func (r *Boards) SetBoardMembers(ctx context.Context, boardID domain.ID, userIDs []domain.ID) ([]domain.ID, error) {
	q := r.q(ctx)
	if err := q.DeleteBoardMembers(ctx, uuid.UUID(boardID)); err != nil {
		return nil, err
	}
	var rejected []domain.ID
	for _, id := range userIDs {
		n, err := q.AddBoardMember(ctx, db.AddBoardMemberParams{BoardID: uuid.UUID(boardID), UserID: uuid.UUID(id)})
		if err != nil {
			return nil, err
		}
		if n == 0 {
			rejected = append(rejected, id)
		}
	}
	return rejected, nil
}

// CopyBoard must run inside a transaction.
func (r *Boards) CopyBoard(ctx context.Context, sourceID, targetID, actorID domain.ID, withMembers bool) error {
	q := r.q(ctx)
	if err := q.CopyBoardContent(ctx, db.CopyBoardContentParams{
		SourceID: uuid.UUID(sourceID), TargetID: uuid.UUID(targetID), ActorID: uuid.UUID(actorID),
	}); err != nil {
		return err
	}
	if !withMembers {
		return nil
	}
	return q.CopyBoardMembers(ctx, db.CopyBoardMembersParams{SourceID: uuid.UUID(sourceID), TargetID: uuid.UUID(targetID)})
}

// MoveBoard must run inside a transaction. Like every writer of the log, it
// locks the tasks it changes before the board's activity lock.
func (r *Boards) MoveBoard(ctx context.Context, boardID, workspaceID, actorID domain.ID) (domain.Board, []domain.ActivityEntry, error) {
	q := r.q(ctx)
	board, ws := uuid.UUID(boardID), uuid.UUID(workspaceID)
	moved, err := q.MoveBoard(ctx, db.MoveBoardParams{ID: board, WorkspaceID: ws})
	if err != nil {
		return domain.Board{}, nil, notFound(err, domain.ErrBoardNotFound)
	}
	if err := q.MoveBoardActivity(ctx, db.MoveBoardActivityParams{BoardID: board, WorkspaceID: ws}); err != nil {
		return domain.Board{}, nil, fmt.Errorf("move activity: %w", err)
	}
	if err := q.DropBoardNonMembers(ctx, db.DropBoardNonMembersParams{BoardID: board, WorkspaceID: ws}); err != nil {
		return domain.Board{}, nil, fmt.Errorf("drop board members: %w", err)
	}
	locked, err := q.LockBoardNonMemberTasks(ctx, db.LockBoardNonMemberTasksParams{BoardID: board, WorkspaceID: ws})
	if err != nil {
		return domain.Board{}, nil, fmt.Errorf("lock tasks: %w", err)
	}
	if len(locked) == 0 {
		return toBoard(moved), nil, nil
	}
	if err := q.LockBoardActivity(ctx, board); err != nil {
		return domain.Board{}, nil, fmt.Errorf("lock board activity: %w", err)
	}
	rows, err := q.UnassignBoardNonMembers(ctx, db.UnassignBoardNonMembersParams{
		BoardID: board, WorkspaceID: ws, ActorID: uuid.UUID(actorID),
	})
	if err != nil {
		return domain.Board{}, nil, fmt.Errorf("unassign tasks: %w", err)
	}
	return toBoard(moved), mapAll(rows, func(a db.ActivityLog) domain.ActivityEntry { return toActivity(a, "") }), nil
}

// DeleteBoard deletes its columns, tasks, labels and activity too (ON DELETE CASCADE).
func (r *Boards) DeleteBoard(ctx context.Context, boardID domain.ID) error {
	n, err := r.q(ctx).DeleteBoard(ctx, uuid.UUID(boardID))
	return deleted(n, err, domain.ErrBoardNotFound)
}

func (r *Boards) LockBoard(ctx context.Context, boardID domain.ID) error {
	return r.q(ctx).LockBoard(ctx, uuid.UUID(boardID))
}

// --- columns ---

func (r *Boards) ListColumns(ctx context.Context, boardID domain.ID) ([]domain.Column, error) {
	rows, err := r.q(ctx).ListColumnsByBoard(ctx, uuid.UUID(boardID))
	return mapAll(rows, toColumn), err
}

func (r *Boards) GetColumn(ctx context.Context, columnID domain.ID) (domain.Column, error) {
	c, err := r.q(ctx).GetColumn(ctx, uuid.UUID(columnID))
	return toColumn(c), notFound(err, domain.ErrColumnNotFound)
}

func (r *Boards) CreateColumn(ctx context.Context, boardID domain.ID, name string, position float64) (domain.Column, error) {
	c, err := r.q(ctx).CreateColumn(ctx, db.CreateColumnParams{BoardID: uuid.UUID(boardID), Name: name, Position: position})
	return toColumn(c), err
}

func (r *Boards) RenameColumn(ctx context.Context, columnID domain.ID, name string) (domain.Column, error) {
	c, err := r.q(ctx).UpdateColumnName(ctx, db.UpdateColumnNameParams{ID: uuid.UUID(columnID), Name: name})
	return toColumn(c), notFound(err, domain.ErrColumnNotFound)
}

func (r *Boards) SetColumnPosition(ctx context.Context, columnID domain.ID, position float64) (domain.Column, error) {
	c, err := r.q(ctx).UpdateColumnPosition(ctx, db.UpdateColumnPositionParams{ID: uuid.UUID(columnID), Position: position})
	return toColumn(c), notFound(err, domain.ErrColumnNotFound)
}

// DeleteColumn deletes its tasks too (ON DELETE CASCADE).
func (r *Boards) DeleteColumn(ctx context.Context, columnID domain.ID) error {
	n, err := r.q(ctx).DeleteColumn(ctx, uuid.UUID(columnID))
	return deleted(n, err, domain.ErrColumnNotFound)
}

func (r *Boards) RebalanceColumns(ctx context.Context, boardID domain.ID) error {
	return r.q(ctx).RebalanceColumns(ctx, uuid.UUID(boardID))
}

// --- board detail ---

func (r *Boards) ListBoardTasks(ctx context.Context, boardID domain.ID, includeArchived bool) ([]domain.TaskDetail, error) {
	rows, err := r.q(ctx).ListTasksByBoard(ctx, db.ListTasksByBoardParams{BoardID: uuid.UUID(boardID), IncludeArchived: includeArchived})
	return mapAll(rows, func(row db.ListTasksByBoardRow) domain.TaskDetail {
		return domain.TaskDetail{Task: toTask(row.Task), AssigneeName: row.AssigneeName.String}
	}), err
}

func (r *Boards) ListBoardTaskLabels(ctx context.Context, boardID domain.ID) (map[domain.ID][]domain.Label, error) {
	rows, err := r.q(ctx).ListBoardTaskLabels(ctx, uuid.UUID(boardID))
	if err != nil {
		return nil, err
	}
	byTask := make(map[domain.ID][]domain.Label)
	for _, row := range rows { // ordered by label name
		id := domain.ID(row.TaskID)
		byTask[id] = append(byTask[id], toLabel(row.Label))
	}
	return byTask, nil
}

// --- mapping ---

func toBoard(b db.Board) domain.Board {
	return domain.Board{
		ID: domain.ID(b.ID), WorkspaceID: domain.ID(b.WorkspaceID), Name: b.Name,
		Visibility: domain.Visibility(b.Visibility), ArchivedAt: b.ArchivedAt, CreatedBy: optionalID(b.CreatedBy),
		CreatedAt: b.CreatedAt,
	}
}

func toColumn(c db.Column) domain.Column {
	return domain.Column{ID: domain.ID(c.ID), BoardID: domain.ID(c.BoardID), Name: c.Name, Position: c.Position, CreatedAt: c.CreatedAt}
}

func toTask(t db.Task) domain.Task {
	return domain.Task{
		ID: domain.ID(t.ID), ColumnID: domain.ID(t.ColumnID), Title: t.Title, Description: t.Description,
		Position: t.Position, Version: t.Version, Priority: domain.Priority(t.Priority),
		DueDate: t.DueDate, AssigneeID: optionalID(t.AssigneeID), ArchivedAt: t.ArchivedAt,
		CreatedBy: optionalID(t.CreatedBy), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

func mapAll[From, To any](rows []From, f func(From) To) []To {
	out := make([]To, len(rows))
	for i, row := range rows {
		out[i] = f(row)
	}
	return out
}

// deleted turns "zero rows deleted" into the given domain error.
func deleted(n int64, err, domainErr error) error {
	if err != nil {
		return err
	}
	if n == 0 {
		return domainErr
	}
	return nil
}
