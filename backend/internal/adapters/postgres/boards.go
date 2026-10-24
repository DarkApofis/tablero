package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/db"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Boards implements board.BoardRepository (boards, columns and the board
// detail reads) and the column part of the task use cases' BoardRepository.
type Boards struct{ conn }

func NewBoards(pool *pgxpool.Pool) *Boards { return &Boards{conn{pool}} }

func (r *Boards) WorkspaceOfBoard(ctx context.Context, boardID domain.ID) (domain.ID, error) {
	id, err := r.q(ctx).GetBoardWorkspaceID(ctx, uuid.UUID(boardID))
	return domain.ID(id), notFound(err, domain.ErrNotMember)
}

func (r *Boards) WorkspaceOfColumn(ctx context.Context, columnID domain.ID) (domain.ID, error) {
	id, err := r.q(ctx).GetColumnWorkspaceID(ctx, uuid.UUID(columnID))
	return domain.ID(id), notFound(err, domain.ErrNotMember)
}

// --- boards ---

func (r *Boards) CreateBoard(ctx context.Context, workspaceID domain.ID, name string) (domain.Board, error) {
	b, err := r.q(ctx).CreateBoard(ctx, db.CreateBoardParams{WorkspaceID: uuid.UUID(workspaceID), Name: name})
	return toBoard(b), err
}

func (r *Boards) ListBoards(ctx context.Context, workspaceID domain.ID) ([]domain.Board, error) {
	rows, err := r.q(ctx).ListBoardsByWorkspace(ctx, uuid.UUID(workspaceID))
	return mapAll(rows, toBoard), err
}

func (r *Boards) GetBoard(ctx context.Context, boardID domain.ID) (domain.Board, error) {
	b, err := r.q(ctx).GetBoard(ctx, uuid.UUID(boardID))
	return toBoard(b), notFound(err, domain.ErrBoardNotFound)
}

func (r *Boards) RenameBoard(ctx context.Context, boardID domain.ID, name string) (domain.Board, error) {
	b, err := r.q(ctx).UpdateBoardName(ctx, db.UpdateBoardNameParams{ID: uuid.UUID(boardID), Name: name})
	return toBoard(b), notFound(err, domain.ErrBoardNotFound)
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
	return domain.Board{ID: domain.ID(b.ID), WorkspaceID: domain.ID(b.WorkspaceID), Name: b.Name, CreatedAt: b.CreatedAt}
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
