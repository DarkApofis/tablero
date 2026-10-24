package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/db"
	"github.com/DarkApofis/tablero/backend/internal/position"
)

const (
	MaxTaskTitleLen       = 200
	MaxTaskDescriptionLen = 10_000

	// DateLayout is how due dates travel in the API and the activity log.
	DateLayout = "2006-01-02"

	pgForeignKeyViolation = "23503"
)

var ErrTaskNotFound = errors.New("task not found")

// TaskDetail is a task with what the API shows alongside it.
type TaskDetail struct {
	db.Task
	AssigneeName string     // empty when unassigned
	Labels       []db.Label // ordered by name
}

// ConflictError reports that the task changed since the client read it
// (docs/DECISIONS.md #1). Current is the task as it is now, for the 409 body.
type ConflictError struct {
	Reason  ConflictReason
	Current TaskDetail
}

type ConflictReason string

const (
	// ConflictVersion: the client's version is not the current one.
	ConflictVersion ConflictReason = "version"
	// ConflictNeighbors: afterTaskId and beforeTaskId are no longer in that
	// order, so the client's view of the column is stale.
	ConflictNeighbors ConflictReason = "neighbors"
)

func (e *ConflictError) Error() string {
	return fmt.Sprintf("task %s conflict (%s, current version %d)", e.Current.ID, e.Reason, e.Current.Version)
}

// errVersionMismatch and errNeighborsChanged abort a transaction; the caller
// turns them into a *ConflictError with the state read after the rollback.
var (
	errVersionMismatch  = errors.New("version mismatch")
	errNeighborsChanged = errors.New("neighbors changed")
)

// Optional is a field of a partial edit that can also be cleared: Set false
// keeps the current value, Set with a nil Value clears it.
type Optional[T any] struct {
	Set   bool
	Value *T
}

type TaskService struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewTaskService(pool *pgxpool.Pool) *TaskService {
	return &TaskService{pool: pool, q: db.New(pool)}
}

// WorkspaceOfTask resolves a task to its workspace for authorization. A
// missing task is ErrNotMember, like a forbidden one.
func (s *TaskService) WorkspaceOfTask(ctx context.Context, taskID uuid.UUID) (uuid.UUID, error) {
	id, err := s.q.GetTaskWorkspaceID(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotMember
	}
	return id, err
}

// Get reads the task, its assignee and its labels from one snapshot.
func (s *TaskService) Get(ctx context.Context, taskID uuid.UUID) (TaskDetail, error) {
	var task TaskDetail
	err := db.InTx(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *db.Queries) error {
		var err error
		task, err = loadTaskDetail(ctx, q, taskID)
		return err
	})
	return task, err
}

func loadTaskDetail(ctx context.Context, q *db.Queries, taskID uuid.UUID) (TaskDetail, error) {
	row, err := q.GetTaskDetail(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskDetail{}, ErrTaskNotFound
	}
	if err != nil {
		return TaskDetail{}, fmt.Errorf("get task: %w", err)
	}
	labels, err := q.ListTaskLabels(ctx, taskID)
	if err != nil {
		return TaskDetail{}, fmt.Errorf("list task labels: %w", err)
	}
	return TaskDetail{Task: row.Task, AssigneeName: row.AssigneeName.String, Labels: labels}, nil
}

type CreateTaskInput struct {
	ColumnID uuid.UUID
	// WorkspaceID is the column's workspace, as authorized; the assignee must
	// be a member of it.
	WorkspaceID uuid.UUID
	Title       string
	Description string
	Priority    *db.TaskPriority // nil: medium
	DueDate     *time.Time
	AssigneeID  *uuid.UUID
	LabelIDs    []uuid.UUID
	CreatedBy   uuid.UUID
}

// Create appends a task at the end of its column.
func (s *TaskService) Create(ctx context.Context, in CreateTaskInput) (TaskDetail, error) {
	fields := taskFields{
		Title: in.Title, Description: in.Description, Priority: db.TaskPriorityMedium,
		DueDate: in.DueDate, AssigneeID: nullUUID(in.AssigneeID), LabelIDs: normalizeIDs(in.LabelIDs),
	}
	if in.Priority != nil {
		fields.Priority = *in.Priority
	}
	if err := fields.validate(); err != nil {
		return TaskDetail{}, err
	}

	var task TaskDetail
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(q *db.Queries) error {
		// Lock order everywhere: membership, then column, then labels, then task.
		if err := lockAssignee(ctx, q, in.WorkspaceID, fields.AssigneeID); err != nil {
			return err
		}
		if err := q.LockColumn(ctx, in.ColumnID); err != nil {
			return fmt.Errorf("lock column: %w", err)
		}
		loc, err := q.GetColumnLocation(ctx, in.ColumnID)
		if errors.Is(err, pgx.ErrNoRows) { // deleted after authorization
			return ErrColumnNotFound
		}
		if err != nil {
			return fmt.Errorf("locate column: %w", err)
		}
		if err := lockBoardLabels(ctx, q, loc.BoardID, fields.LabelIDs); err != nil {
			return err
		}

		last, err := optionalPosition(q.LastTaskPosition(ctx, db.LastTaskPositionParams{ColumnID: in.ColumnID, ExcludeID: uuid.Nil}))
		if err != nil {
			return fmt.Errorf("last task position: %w", err)
		}
		pos, _ := position.Between(last, nil) // appending always has room
		created, err := q.CreateTask(ctx, db.CreateTaskParams{
			ColumnID: in.ColumnID, Title: fields.Title, Description: fields.Description, Position: pos,
			CreatedBy: uuid.NullUUID{UUID: in.CreatedBy, Valid: true},
			Priority:  fields.Priority, DueDate: fields.DueDate, AssigneeID: fields.AssigneeID,
		})
		// The column was deleted between the lock and the insert.
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgForeignKeyViolation {
			return ErrColumnNotFound
		}
		if err != nil {
			return err
		}
		if len(fields.LabelIDs) > 0 {
			if err := q.AddTaskLabels(ctx, db.AddTaskLabelsParams{TaskID: created.ID, LabelIds: fields.LabelIDs}); err != nil {
				return fmt.Errorf("add labels: %w", err)
			}
		}

		if err := logActivity(ctx, q, created.ColumnID, in.CreatedBy, ActivityTaskCreated, TaskCreatedPayload{
			TaskID: created.ID, Title: created.Title, ColumnID: created.ColumnID, Position: created.Position,
			Priority: created.Priority, DueDate: formatDate(created.DueDate),
			AssigneeID: uuidPtr(created.AssigneeID), LabelIDs: fields.LabelIDs,
		}); err != nil {
			return err
		}
		task, err = loadTaskDetail(ctx, q, created.ID)
		return err
	})
	return task, err
}

// UpdateInput carries a partial edit. Nil (or unset) fields keep their
// current value; Version is the version the client last saw.
type UpdateInput struct {
	Title       *string
	Description *string
	Priority    *db.TaskPriority
	DueDate     Optional[time.Time]
	AssigneeID  Optional[uuid.UUID]
	LabelIDs    *[]uuid.UUID // replaces the whole set
	Archived    *bool
	// WorkspaceID is the task's workspace, as authorized; the assignee must
	// be a member of it.
	WorkspaceID uuid.UUID
	Version     int32
	ActorID     uuid.UUID
}

func (in UpdateInput) empty() bool {
	return in.Title == nil && in.Description == nil && in.Priority == nil && !in.DueDate.Set &&
		!in.AssigneeID.Set && in.LabelIDs == nil && in.Archived == nil
}

// Update applies a partial edit with optimistic concurrency: it only applies
// if Version is still current, otherwise it returns *ConflictError with the
// task as it is now. The change and its activity entry commit together.
func (s *TaskService) Update(ctx context.Context, taskID uuid.UUID, in UpdateInput) (TaskDetail, error) {
	if in.empty() {
		return TaskDetail{}, &ValidationError{Fields: map[string]string{"body": "Indica al menos un campo a cambiar"}}
	}

	var result TaskDetail
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(q *db.Queries) error {
		if in.AssigneeID.Set {
			if err := lockAssignee(ctx, q, in.WorkspaceID, nullUUID(in.AssigneeID.Value)); err != nil {
				return err
			}
		}
		// The row lock makes the version check below and the labels read
		// next consistent with the UPDATE: nobody can change them meanwhile.
		before, err := q.GetTaskForUpdate(ctx, taskID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTaskNotFound
		}
		if err != nil {
			return fmt.Errorf("get task: %w", err)
		}
		if before.Version != in.Version {
			return errVersionMismatch
		}
		beforeLabels, err := q.ListTaskLabels(ctx, taskID)
		if err != nil {
			return fmt.Errorf("list task labels: %w", err)
		}

		old := fieldsOf(before, labelIDs(beforeLabels))
		next := old.apply(in, time.Now())
		if err := next.validate(); err != nil {
			return err
		}
		labelsChanged := !slices.Equal(old.LabelIDs, next.LabelIDs)
		if labelsChanged && len(next.LabelIDs) > 0 {
			loc, err := q.GetColumnLocation(ctx, before.ColumnID)
			if err != nil {
				return fmt.Errorf("locate column: %w", err)
			}
			if err := lockBoardLabels(ctx, q, loc.BoardID, next.LabelIDs); err != nil {
				return err
			}
		}

		updated, err := q.UpdateTask(ctx, db.UpdateTaskParams{
			ID: taskID, Version: in.Version,
			Title: next.Title, Description: next.Description, Priority: next.Priority,
			DueDate: next.DueDate, AssigneeID: next.AssigneeID, ArchivedAt: next.ArchivedAt,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errVersionMismatch
		}
		if err != nil {
			return err
		}
		if labelsChanged {
			if err := q.ClearTaskLabels(ctx, taskID); err != nil {
				return fmt.Errorf("clear labels: %w", err)
			}
			if len(next.LabelIDs) > 0 {
				if err := q.AddTaskLabels(ctx, db.AddTaskLabelsParams{TaskID: taskID, LabelIds: next.LabelIDs}); err != nil {
					return fmt.Errorf("add labels: %w", err)
				}
			}
		}

		// Diff against what was stored, using the values the UPDATE returned
		// (archived_at as the database rounded it).
		stored := fieldsOf(updated, next.LabelIDs)
		if err := logActivity(ctx, q, updated.ColumnID, in.ActorID, ActivityTaskUpdated, TaskUpdatedPayload{
			TaskID: updated.ID, Title: updated.Title, Version: updated.Version, Changes: old.diff(stored),
		}); err != nil {
			return err
		}
		result, err = loadTaskDetail(ctx, q, taskID)
		return err
	})
	if errors.Is(err, errVersionMismatch) {
		// Read the current state after the rollback, outside the transaction.
		return TaskDetail{}, s.conflict(ctx, taskID, ConflictVersion)
	}
	return result, err
}

// MoveInput places a task in ColumnID between AfterTaskID and BeforeTaskID.
// With only one of them, the other neighbor is whatever is adjacent to it now;
// with neither, the task goes to the end of the column.
type MoveInput struct {
	ColumnID     uuid.UUID
	AfterTaskID  *uuid.UUID
	BeforeTaskID *uuid.UUID
	Version      int32
	ActorID      uuid.UUID
}

// Move changes a task's column and/or position under the same version guard
// as Update. The target column is locked first and then the task, so
// concurrent moves into a column cannot compute the same position.
func (s *TaskService) Move(ctx context.Context, taskID uuid.UUID, in MoveInput) (TaskDetail, error) {
	for _, n := range []*uuid.UUID{in.AfterTaskID, in.BeforeTaskID} {
		if n != nil && *n == taskID {
			return TaskDetail{}, &ValidationError{Fields: map[string]string{"afterTaskId": "Una tarea no puede ser su propia vecina"}}
		}
	}

	var result TaskDetail
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(q *db.Queries) error {
		target, err := q.GetColumn(ctx, in.ColumnID)
		if errors.Is(err, pgx.ErrNoRows) {
			return &ValidationError{Fields: map[string]string{"columnId": "La columna no existe"}}
		}
		if err != nil {
			return fmt.Errorf("get column: %w", err)
		}
		if err := q.LockColumn(ctx, target.ID); err != nil {
			return fmt.Errorf("lock column: %w", err)
		}

		task, err := q.GetTaskForUpdate(ctx, taskID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTaskNotFound
		}
		if err != nil {
			return fmt.Errorf("get task: %w", err)
		}
		if task.Version != in.Version {
			return errVersionMismatch
		}

		source, err := q.GetColumn(ctx, task.ColumnID)
		if err != nil {
			return fmt.Errorf("get source column: %w", err)
		}
		// Authorization covered the task's workspace; staying within the board
		// keeps the move inside it too (and keeps its labels valid).
		if source.BoardID != target.BoardID {
			return &ValidationError{Fields: map[string]string{"columnId": "La columna debe pertenecer al mismo tablero"}}
		}

		for attempt := 0; ; attempt++ {
			prev, next, err := taskNeighbors(ctx, q, task, in)
			if err != nil {
				return err
			}
			if pos, ok := position.Between(prev, next); ok {
				moved, err := q.MoveTask(ctx, db.MoveTaskParams{
					ID: taskID, Version: in.Version, ColumnID: target.ID, Position: pos,
				})
				if err != nil {
					return err // the row is locked and the version checked, so no ErrNoRows here
				}
				// task.Position is pre-rebalance; it is where the task was as far
				// as anyone outside this transaction could have seen.
				if err := logActivity(ctx, q, moved.ColumnID, in.ActorID, ActivityTaskMoved, TaskMovedPayload{
					TaskID: moved.ID, Title: moved.Title, Version: moved.Version,
					From: TaskPlacement{ColumnID: task.ColumnID, Position: task.Position},
					To:   TaskPlacement{ColumnID: moved.ColumnID, Position: moved.Position},
				}); err != nil {
					return err
				}
				result, err = loadTaskDetail(ctx, q, taskID)
				return err
			}
			if attempt > 0 {
				return errors.New("no room between tasks even after rebalancing")
			}
			if err := q.RebalanceTasks(ctx, target.ID); err != nil {
				return fmt.Errorf("rebalance tasks: %w", err)
			}
		}
	})
	switch {
	case errors.Is(err, errVersionMismatch):
		return TaskDetail{}, s.conflict(ctx, taskID, ConflictVersion)
	case errors.Is(err, errNeighborsChanged):
		return TaskDetail{}, s.conflict(ctx, taskID, ConflictNeighbors)
	}
	return result, err
}

// taskNeighbors returns the positions the moved task must fall between in
// the target column. Neighbors are re-read on every call so a rebalance in
// between is taken into account.
func taskNeighbors(ctx context.Context, q *db.Queries, moving db.Task, in MoveInput) (prev, next *float64, err error) {
	load := func(id *uuid.UUID, field string) (*db.Task, error) {
		if id == nil {
			return nil, nil
		}
		t, err := q.GetTask(ctx, *id)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && t.ColumnID != in.ColumnID) {
			return nil, &ValidationError{Fields: map[string]string{field: "La tarea indicada no está en la columna destino"}}
		}
		if err != nil {
			return nil, fmt.Errorf("get neighbor: %w", err)
		}
		return &t, nil
	}
	after, err := load(in.AfterTaskID, "afterTaskId")
	if err != nil {
		return nil, nil, err
	}
	before, err := load(in.BeforeTaskID, "beforeTaskId")
	if err != nil {
		return nil, nil, err
	}

	switch {
	case after != nil && before != nil:
		if !taskBefore(*after, *before) {
			return nil, nil, errNeighborsChanged
		}
		return &after.Position, &before.Position, nil

	case after != nil:
		next, err := optionalPosition(q.TaskPositionAfter(ctx, db.TaskPositionAfterParams{
			ColumnID: in.ColumnID, ExcludeID: moving.ID, Position: after.Position, ID: after.ID,
		}))
		return &after.Position, next, err

	case before != nil:
		prev, err := optionalPosition(q.TaskPositionBefore(ctx, db.TaskPositionBeforeParams{
			ColumnID: in.ColumnID, ExcludeID: moving.ID, Position: before.Position, ID: before.ID,
		}))
		return prev, &before.Position, err

	default:
		last, err := optionalPosition(q.LastTaskPosition(ctx, db.LastTaskPositionParams{
			ColumnID: in.ColumnID, ExcludeID: moving.ID,
		}))
		return last, nil, err
	}
}

// taskBefore orders tasks the way the column does: by position, then id.
func taskBefore(a, b db.Task) bool {
	if a.Position != b.Position {
		return a.Position < b.Position
	}
	return a.ID.String() < b.ID.String()
}

func (s *TaskService) Delete(ctx context.Context, taskID uuid.UUID) error {
	n, err := s.q.DeleteTask(ctx, taskID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// conflict builds the 409 error with the task's current state, or
// ErrTaskNotFound if it was deleted in the meantime.
func (s *TaskService) conflict(ctx context.Context, taskID uuid.UUID, reason ConflictReason) error {
	current, err := s.Get(ctx, taskID)
	if err != nil {
		return err
	}
	return &ConflictError{Reason: reason, Current: current}
}

// lockAssignee checks that assignee (if any) is a member of the workspace and
// keeps them from being removed until the transaction ends. Removing a member
// unassigns their tasks; without the lock, a removal racing with this
// assignment could miss the task and leave a non-member assigned.
func lockAssignee(ctx context.Context, q *db.Queries, workspaceID uuid.UUID, assignee uuid.NullUUID) error {
	if !assignee.Valid {
		return nil
	}
	_, err := q.LockMembership(ctx, db.LockMembershipParams{WorkspaceID: workspaceID, UserID: assignee.UUID})
	if errors.Is(err, pgx.ErrNoRows) {
		return &ValidationError{Fields: map[string]string{"assignee_id": "El responsable debe ser miembro del workspace"}}
	}
	if err != nil {
		return fmt.Errorf("lock membership: %w", err)
	}
	return nil
}

// lockBoardLabels checks that every id is a label of the board and keeps
// those labels from being deleted until the transaction ends.
func lockBoardLabels(ctx context.Context, q *db.Queries, boardID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	found, err := q.FilterBoardLabels(ctx, db.FilterBoardLabelsParams{BoardID: boardID, Ids: ids})
	if err != nil {
		return fmt.Errorf("filter labels: %w", err)
	}
	if len(found) != len(ids) {
		return &ValidationError{Fields: map[string]string{"label_ids": "Alguna etiqueta no existe o no es de este tablero"}}
	}
	return nil
}

// taskFields are the editable fields of a task, with labels as sorted ids.
type taskFields struct {
	Title       string
	Description string
	Priority    db.TaskPriority
	DueDate     *time.Time
	AssigneeID  uuid.NullUUID
	ArchivedAt  *time.Time
	LabelIDs    []uuid.UUID
}

func fieldsOf(t db.Task, labelIDs []uuid.UUID) taskFields {
	return taskFields{
		Title: t.Title, Description: t.Description, Priority: t.Priority,
		DueDate: t.DueDate, AssigneeID: t.AssigneeID, ArchivedAt: t.ArchivedAt, LabelIDs: labelIDs,
	}
}

// apply returns f with the edit in applied; now is the archiving time.
func (f taskFields) apply(in UpdateInput, now time.Time) taskFields {
	if in.Title != nil {
		f.Title = *in.Title
	}
	if in.Description != nil {
		f.Description = *in.Description
	}
	if in.Priority != nil {
		f.Priority = *in.Priority
	}
	if in.DueDate.Set {
		f.DueDate = in.DueDate.Value
	}
	if in.AssigneeID.Set {
		f.AssigneeID = nullUUID(in.AssigneeID.Value)
	}
	if in.LabelIDs != nil {
		f.LabelIDs = normalizeIDs(*in.LabelIDs)
	}
	switch {
	case in.Archived == nil:
	case !*in.Archived:
		f.ArchivedAt = nil
	case f.ArchivedAt == nil: // archiving an archived task keeps the original time
		f.ArchivedAt = &now
	}
	return f
}

// validate checks the values and trims the title.
func (f *taskFields) validate() error {
	f.Title = strings.TrimSpace(f.Title)
	fields := map[string]string{}
	switch {
	case f.Title == "":
		fields["title"] = "El título es obligatorio"
	case utf8.RuneCountInString(f.Title) > MaxTaskTitleLen:
		fields["title"] = fmt.Sprintf("El título no puede superar %d caracteres", MaxTaskTitleLen)
	}
	if utf8.RuneCountInString(f.Description) > MaxTaskDescriptionLen {
		fields["description"] = fmt.Sprintf("La descripción no puede superar %d caracteres", MaxTaskDescriptionLen)
	}
	if !f.Priority.Valid() {
		fields["priority"] = "La prioridad debe ser low, medium, high o urgent"
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

// diff lists the fields that differ from f to g, for the activity log.
func (f taskFields) diff(g taskFields) map[string]FieldChange {
	changes := map[string]FieldChange{}
	add := func(name string, from, to any, changed bool) {
		if changed {
			changes[name] = FieldChange{From: from, To: to}
		}
	}
	add("title", f.Title, g.Title, f.Title != g.Title)
	add("description", f.Description, g.Description, f.Description != g.Description)
	add("priority", f.Priority, g.Priority, f.Priority != g.Priority)
	add("due_date", formatDate(f.DueDate), formatDate(g.DueDate), !equalPtr(f.DueDate, g.DueDate))
	add("assignee_id", uuidPtr(f.AssigneeID), uuidPtr(g.AssigneeID), f.AssigneeID != g.AssigneeID)
	add("archived_at", f.ArchivedAt, g.ArchivedAt, !equalPtr(f.ArchivedAt, g.ArchivedAt))
	add("label_ids", f.LabelIDs, g.LabelIDs, !slices.Equal(f.LabelIDs, g.LabelIDs))
	return changes
}

func equalPtr(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// formatDate renders a due date as the API does, or nil.
func formatDate(d *time.Time) *string {
	if d == nil {
		return nil
	}
	s := d.Format(DateLayout)
	return &s
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func uuidPtr(id uuid.NullUUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	return &id.UUID
}

// normalizeIDs sorts and dedupes, so label sets compare and log stably. It
// never returns nil, so an empty set is logged as [] rather than null.
func normalizeIDs(ids []uuid.UUID) []uuid.UUID {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	out = slices.Compact(out)
	if out == nil {
		out = []uuid.UUID{}
	}
	return out
}

func labelIDs(labels []db.Label) []uuid.UUID {
	ids := make([]uuid.UUID, len(labels))
	for i, l := range labels {
		ids[i] = l.ID
	}
	return normalizeIDs(ids)
}

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
