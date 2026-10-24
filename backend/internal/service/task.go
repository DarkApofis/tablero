package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

	pgForeignKeyViolation = "23503"
)

var ErrTaskNotFound = errors.New("task not found")

// ConflictError reports that the task changed since the client read it
// (docs/DECISIONS.md #1). Current is the task as it is now, for the 409 body.
type ConflictError struct {
	Reason  ConflictReason
	Current db.Task
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

func (s *TaskService) Get(ctx context.Context, taskID uuid.UUID) (db.Task, error) {
	t, err := s.q.GetTask(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Task{}, ErrTaskNotFound
	}
	return t, err
}

type CreateTaskInput struct {
	ColumnID    uuid.UUID
	Title       string
	Description string
	CreatedBy   uuid.UUID
}

// Create appends a task at the end of its column.
func (s *TaskService) Create(ctx context.Context, in CreateTaskInput) (db.Task, error) {
	title, description, err := validTaskContent(in.Title, in.Description)
	if err != nil {
		return db.Task{}, err
	}

	var task db.Task
	err = db.InTx(ctx, s.pool, pgx.TxOptions{}, func(q *db.Queries) error {
		if err := q.LockColumn(ctx, in.ColumnID); err != nil {
			return fmt.Errorf("lock column: %w", err)
		}
		last, err := optionalPosition(q.LastTaskPosition(ctx, db.LastTaskPositionParams{ColumnID: in.ColumnID, ExcludeID: uuid.Nil}))
		if err != nil {
			return fmt.Errorf("last task position: %w", err)
		}
		pos, _ := position.Between(last, nil) // appending always has room
		task, err = q.CreateTask(ctx, db.CreateTaskParams{
			ColumnID: in.ColumnID, Title: title, Description: description, Position: pos,
			CreatedBy: uuid.NullUUID{UUID: in.CreatedBy, Valid: true},
		})
		// The column was deleted between authorization and insert.
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgForeignKeyViolation {
			return ErrColumnNotFound
		}
		return err
	})
	return task, err
}

// UpdateInput carries a partial edit. Nil fields keep their current value;
// Version is the version the client last saw.
type UpdateInput struct {
	Title       *string
	Description *string
	Version     int32
}

// Update edits title and/or description with optimistic concurrency: it only
// applies if Version is still current, otherwise it returns *ConflictError
// with the task as it is now.
func (s *TaskService) Update(ctx context.Context, taskID uuid.UUID, in UpdateInput) (db.Task, error) {
	if in.Title == nil && in.Description == nil {
		return db.Task{}, &ValidationError{Fields: map[string]string{"title": "Indica title y/o description"}}
	}

	// The unchanged field is filled in from the stored task. If that read is
	// already at another version, the UPDATE below fails and we report it.
	current, err := s.Get(ctx, taskID)
	if err != nil {
		return db.Task{}, err
	}
	title, description := current.Title, current.Description
	if in.Title != nil {
		title = *in.Title
	}
	if in.Description != nil {
		description = *in.Description
	}
	if title, description, err = validTaskContent(title, description); err != nil {
		return db.Task{}, err
	}

	updated, err := s.q.UpdateTaskContent(ctx, db.UpdateTaskContentParams{
		ID: taskID, Version: in.Version, Title: title, Description: description,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Task{}, s.conflict(ctx, taskID, ConflictVersion)
	}
	return updated, err
}

// MoveInput places a task in ColumnID between AfterTaskID and BeforeTaskID.
// With only one of them, the other neighbor is whatever is adjacent to it now;
// with neither, the task goes to the end of the column.
type MoveInput struct {
	ColumnID     uuid.UUID
	AfterTaskID  *uuid.UUID
	BeforeTaskID *uuid.UUID
	Version      int32
}

// Move changes a task's column and/or position under the same version guard
// as Update. The target column is locked first and then the task, so
// concurrent moves into a column cannot compute the same position.
func (s *TaskService) Move(ctx context.Context, taskID uuid.UUID, in MoveInput) (db.Task, error) {
	for _, n := range []*uuid.UUID{in.AfterTaskID, in.BeforeTaskID} {
		if n != nil && *n == taskID {
			return db.Task{}, &ValidationError{Fields: map[string]string{"afterTaskId": "Una tarea no puede ser su propia vecina"}}
		}
	}

	var moved db.Task
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
			return &ConflictError{Reason: ConflictVersion, Current: task}
		}

		source, err := q.GetColumn(ctx, task.ColumnID)
		if err != nil {
			return fmt.Errorf("get source column: %w", err)
		}
		// Authorization covered the task's workspace; staying within the board
		// keeps the move inside it too.
		if source.BoardID != target.BoardID {
			return &ValidationError{Fields: map[string]string{"columnId": "La columna debe pertenecer al mismo tablero"}}
		}

		for attempt := 0; ; attempt++ {
			prev, next, err := taskNeighbors(ctx, q, task, in)
			if err != nil {
				return err
			}
			if pos, ok := position.Between(prev, next); ok {
				moved, err = q.MoveTask(ctx, db.MoveTaskParams{
					ID: taskID, Version: in.Version, ColumnID: target.ID, Position: pos,
				})
				return err // the row is locked and the version checked, so no ErrNoRows here
			}
			if attempt > 0 {
				return errors.New("no room between tasks even after rebalancing")
			}
			if err := q.RebalanceTasks(ctx, target.ID); err != nil {
				return fmt.Errorf("rebalance tasks: %w", err)
			}
		}
	})
	return moved, err
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
			return nil, nil, &ConflictError{Reason: ConflictNeighbors, Current: moving}
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

func validTaskContent(title, description string) (string, string, error) {
	title = strings.TrimSpace(title)
	fields := map[string]string{}
	switch {
	case title == "":
		fields["title"] = "El título es obligatorio"
	case utf8.RuneCountInString(title) > MaxTaskTitleLen:
		fields["title"] = fmt.Sprintf("El título no puede superar %d caracteres", MaxTaskTitleLen)
	}
	if utf8.RuneCountInString(description) > MaxTaskDescriptionLen {
		fields["description"] = fmt.Sprintf("La descripción no puede superar %d caracteres", MaxTaskDescriptionLen)
	}
	if len(fields) > 0 {
		return "", "", &ValidationError{Fields: fields}
	}
	return title, description, nil
}
