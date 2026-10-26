package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres/db"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Tasks implements task.TaskRepository.
type Tasks struct{ conn }

func NewTasks(pool *pgxpool.Pool) *Tasks { return &Tasks{conn{pool}} }

func (r *Tasks) WorkspaceOfTask(ctx context.Context, taskID domain.ID) (domain.ID, error) {
	id, err := r.q(ctx).GetTaskWorkspaceID(ctx, uuid.UUID(taskID))
	return domain.ID(id), notFound(err, domain.ErrNotMember)
}

func (r *Tasks) Get(ctx context.Context, taskID domain.ID) (domain.Task, error) {
	t, err := r.q(ctx).GetTask(ctx, uuid.UUID(taskID))
	return toTask(t), notFound(err, domain.ErrTaskNotFound)
}

func (r *Tasks) GetForUpdate(ctx context.Context, taskID domain.ID) (domain.Task, error) {
	t, err := r.q(ctx).GetTaskForUpdate(ctx, uuid.UUID(taskID))
	return toTask(t), notFound(err, domain.ErrTaskNotFound)
}

func (r *Tasks) Detail(ctx context.Context, taskID domain.ID) (domain.TaskDetail, error) {
	q := r.q(ctx)
	row, err := q.GetTaskDetail(ctx, uuid.UUID(taskID))
	if err != nil {
		return domain.TaskDetail{}, notFound(err, domain.ErrTaskNotFound)
	}
	labels, err := q.ListTaskLabels(ctx, uuid.UUID(taskID))
	if err != nil {
		return domain.TaskDetail{}, err
	}
	return domain.TaskDetail{Task: toTask(row.Task), AssigneeName: row.AssigneeName.String, Labels: mapAll(labels, toLabel)}, nil
}

func (r *Tasks) Labels(ctx context.Context, taskID domain.ID) ([]domain.Label, error) {
	labels, err := r.q(ctx).ListTaskLabels(ctx, uuid.UUID(taskID))
	return mapAll(labels, toLabel), err
}

func (r *Tasks) Create(ctx context.Context, columnID domain.ID, position float64, f domain.TaskFields, createdBy domain.ID) (domain.Task, error) {
	t, err := r.q(ctx).CreateTask(ctx, db.CreateTaskParams{
		ColumnID: uuid.UUID(columnID), Title: f.Title, Description: f.Description, Position: position,
		CreatedBy: uuid.NullUUID{UUID: uuid.UUID(createdBy), Valid: true},
		Priority:  db.TaskPriority(f.Priority), DueDate: f.DueDate, AssigneeID: nullUUID(f.AssigneeID),
	})
	// The column was deleted between the lock and the insert.
	if pgCode(err) == pgForeignKeyViolation {
		return domain.Task{}, domain.ErrColumnNotFound
	}
	return toTask(t), err
}

func (r *Tasks) Update(ctx context.Context, taskID domain.ID, version int32, f domain.TaskFields) (domain.Task, error) {
	t, err := r.q(ctx).UpdateTask(ctx, db.UpdateTaskParams{
		ID: uuid.UUID(taskID), Version: version,
		Title: f.Title, Description: f.Description, Priority: db.TaskPriority(f.Priority),
		DueDate: f.DueDate, AssigneeID: nullUUID(f.AssigneeID), ArchivedAt: f.ArchivedAt,
	})
	return toTask(t), notFound(err, domain.ErrConflict) // zero rows: someone else changed it first
}

func (r *Tasks) Move(ctx context.Context, taskID domain.ID, version int32, columnID domain.ID, position float64) (domain.Task, error) {
	t, err := r.q(ctx).MoveTask(ctx, db.MoveTaskParams{
		ID: uuid.UUID(taskID), Version: version, ColumnID: uuid.UUID(columnID), Position: position,
	})
	return toTask(t), notFound(err, domain.ErrConflict)
}

func (r *Tasks) SetLabels(ctx context.Context, taskID domain.ID, labelIDs []domain.ID) error {
	q := r.q(ctx)
	if err := q.ClearTaskLabels(ctx, uuid.UUID(taskID)); err != nil {
		return err
	}
	if len(labelIDs) == 0 {
		return nil
	}
	return q.AddTaskLabels(ctx, db.AddTaskLabelsParams{TaskID: uuid.UUID(taskID), LabelIds: uuids(labelIDs)})
}

func (r *Tasks) Delete(ctx context.Context, taskID domain.ID) error {
	n, err := r.q(ctx).DeleteTask(ctx, uuid.UUID(taskID))
	return deleted(n, err, domain.ErrTaskNotFound)
}

func (r *Tasks) LastPosition(ctx context.Context, columnID, excludeID domain.ID) (*float64, error) {
	return optionalPosition(r.q(ctx).LastTaskPosition(ctx, db.LastTaskPositionParams{
		ColumnID: uuid.UUID(columnID), ExcludeID: uuid.UUID(excludeID),
	}))
}

func (r *Tasks) PositionAfter(ctx context.Context, columnID, excludeID domain.ID, neighbor domain.Task) (*float64, error) {
	return optionalPosition(r.q(ctx).TaskPositionAfter(ctx, db.TaskPositionAfterParams{
		ColumnID: uuid.UUID(columnID), ExcludeID: uuid.UUID(excludeID), Position: neighbor.Position, ID: uuid.UUID(neighbor.ID),
	}))
}

func (r *Tasks) PositionBefore(ctx context.Context, columnID, excludeID domain.ID, neighbor domain.Task) (*float64, error) {
	return optionalPosition(r.q(ctx).TaskPositionBefore(ctx, db.TaskPositionBeforeParams{
		ColumnID: uuid.UUID(columnID), ExcludeID: uuid.UUID(excludeID), Position: neighbor.Position, ID: uuid.UUID(neighbor.ID),
	}))
}

func (r *Tasks) Rebalance(ctx context.Context, columnID domain.ID) error {
	return r.q(ctx).RebalanceTasks(ctx, uuid.UUID(columnID))
}

// --- columns and labels, for the task use cases ---

func (r *Boards) LockColumn(ctx context.Context, columnID domain.ID) error {
	return r.q(ctx).LockColumn(ctx, uuid.UUID(columnID))
}

func (r *Boards) ColumnLocation(ctx context.Context, columnID domain.ID) (domain.Location, error) {
	loc, err := r.q(ctx).GetColumnLocation(ctx, uuid.UUID(columnID))
	return domain.Location{WorkspaceID: domain.ID(loc.WorkspaceID), BoardID: domain.ID(loc.BoardID)}, notFound(err, domain.ErrColumnNotFound)
}

func (r *Boards) LockBoardLabels(ctx context.Context, boardID domain.ID, ids []domain.ID) (int, error) {
	found, err := r.q(ctx).FilterBoardLabels(ctx, db.FilterBoardLabelsParams{BoardID: uuid.UUID(boardID), Ids: uuids(ids)})
	return len(found), err
}

// --- activity ---

// Append writes an entry after taking the board's activity lock, which it
// holds until the transaction ends: entries of a board commit in id order.
// It must run inside a transaction, as the last write of the use case.
// domain.ErrBoardNotFound if the board is gone.
func (r *Activity) Append(ctx context.Context, a domain.NewActivity) (domain.ActivityEntry, error) {
	q := r.q(ctx)
	if err := q.LockBoardActivity(ctx, uuid.UUID(a.BoardID)); err != nil {
		return domain.ActivityEntry{}, fmt.Errorf("lock board activity: %w", err)
	}
	row, err := q.InsertActivity(ctx, db.InsertActivityParams{
		BoardID: uuid.UUID(a.BoardID), ActorID: nullUUID(a.ActorID), Type: a.Type, Payload: a.Payload,
	})
	return toActivity(row, ""), notFound(err, domain.ErrBoardNotFound)
}

// ActivitySince returns up to limit entries of the board after afterID, oldest first.
func (r *Activity) ActivitySince(ctx context.Context, boardID domain.ID, afterID int64, limit int) ([]domain.ActivityEntry, error) {
	rows, err := r.q(ctx).ListBoardActivitySince(ctx, db.ListBoardActivitySinceParams{
		BoardID: uuid.UUID(boardID), AfterID: afterID, RowLimit: int32(limit),
	})
	return mapAll(rows, func(a db.ActivityLog) domain.ActivityEntry { return toActivity(a, "") }), err
}

// HasActivity reports whether entry id is in the board's log.
func (r *Activity) HasActivity(ctx context.Context, boardID domain.ID, id int64) (bool, error) {
	return r.q(ctx).BoardHasActivity(ctx, db.BoardHasActivityParams{BoardID: uuid.UUID(boardID), ID: id})
}

// LatestActivityID is the id of the board's newest entry, or 0.
func (r *Activity) LatestActivityID(ctx context.Context, boardID domain.ID) (int64, error) {
	return r.q(ctx).LatestBoardActivityID(ctx, uuid.UUID(boardID))
}

// ColumnPositions returns the position of every task in the column, in order.
func (r *Tasks) ColumnPositions(ctx context.Context, columnID domain.ID) ([]domain.TaskPosition, error) {
	rows, err := r.q(ctx).ListColumnTaskPositions(ctx, uuid.UUID(columnID))
	return mapAll(rows, func(row db.ListColumnTaskPositionsRow) domain.TaskPosition {
		return domain.TaskPosition{TaskID: domain.ID(row.ID), Position: row.Position}
	}), err
}

// ColumnTaskIDs returns the ids of the tasks in the column.
func (r *Boards) ColumnTaskIDs(ctx context.Context, columnID domain.ID) ([]domain.ID, error) {
	ids, err := r.q(ctx).ListColumnTaskIDs(ctx, uuid.UUID(columnID))
	return mapAll(ids, func(id uuid.UUID) domain.ID { return domain.ID(id) }), err
}

// --- helpers ---

// optionalPosition turns "no row" into a nil position.
func optionalPosition(pos float64, err error) (*float64, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pos, nil
}

func nullUUID(id *domain.ID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: uuid.UUID(*id), Valid: true}
}

func uuids(ids []domain.ID) []uuid.UUID {
	out := make([]uuid.UUID, len(ids))
	for i, id := range ids {
		out[i] = uuid.UUID(id)
	}
	return out
}
