// Package task holds the task use cases: create, read, edit, move and delete,
// with optimistic concurrency (docs/DECISIONS.md #1) and an activity entry
// written in the same transaction as each change.
package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Ports.

type TaskRepository interface {
	// WorkspaceOfTask returns domain.ErrNotMember when the task does not exist.
	WorkspaceOfTask(ctx context.Context, taskID domain.ID) (domain.ID, error)
	// Get, GetForUpdate and Detail return domain.ErrTaskNotFound.
	Get(ctx context.Context, taskID domain.ID) (domain.Task, error)
	// GetForUpdate also locks the task until the transaction ends.
	GetForUpdate(ctx context.Context, taskID domain.ID) (domain.Task, error)
	// Detail returns the task with its assignee's name and its labels.
	Detail(ctx context.Context, taskID domain.ID) (domain.TaskDetail, error)
	// Labels returns the task's labels ordered by name.
	Labels(ctx context.Context, taskID domain.ID) ([]domain.Label, error)
	// Create stores a task without labels; domain.ErrColumnNotFound if the
	// column is gone.
	Create(ctx context.Context, columnID domain.ID, position float64, fields domain.TaskFields, createdBy domain.ID) (domain.Task, error)
	// Update and Move apply only if version is the current one (and bump
	// it); otherwise they return domain.ErrConflict.
	Update(ctx context.Context, taskID domain.ID, version int32, fields domain.TaskFields) (domain.Task, error)
	Move(ctx context.Context, taskID domain.ID, version int32, columnID domain.ID, position float64) (domain.Task, error)
	// SetLabels replaces the task's labels.
	SetLabels(ctx context.Context, taskID domain.ID, labelIDs []domain.ID) error
	// Delete returns domain.ErrTaskNotFound.
	Delete(ctx context.Context, taskID domain.ID) error

	// Positions in a column, ordered by (position, id) and skipping excludeID;
	// nil when there is no such task.
	LastPosition(ctx context.Context, columnID, excludeID domain.ID) (*float64, error)
	PositionAfter(ctx context.Context, columnID, excludeID domain.ID, neighbor domain.Task) (*float64, error)
	PositionBefore(ctx context.Context, columnID, excludeID domain.ID, neighbor domain.Task) (*float64, error)
	// Rebalance spreads a column's tasks evenly, keeping their order.
	Rebalance(ctx context.Context, columnID domain.ID) error
}

// BoardRepository is the part of the boards the task use cases need.
type BoardRepository interface {
	// GetColumn and ColumnLocation return domain.ErrColumnNotFound.
	GetColumn(ctx context.Context, columnID domain.ID) (domain.Column, error)
	ColumnLocation(ctx context.Context, columnID domain.ID) (domain.Location, error)
	// LockColumn serializes position changes inside a column until the
	// transaction ends.
	LockColumn(ctx context.Context, columnID domain.ID) error
	// LockBoardLabels returns how many of ids are labels of the board, and
	// keeps those from being deleted until the transaction ends.
	LockBoardLabels(ctx context.Context, boardID domain.ID, ids []domain.ID) (int, error)
}

// WorkspaceRepository is the part of the workspaces the task use cases need.
type WorkspaceRepository interface {
	// LockMembership returns domain.ErrNotMember if userID is not a member,
	// and keeps the membership from being deleted until the transaction ends.
	LockMembership(ctx context.Context, workspaceID, userID domain.ID) error
}

type ActivityRepository interface {
	Append(ctx context.Context, entry domain.NewActivity) (domain.ActivityEntry, error)
}

type TxManager interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
	InReadTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event)
}

type Clock interface {
	Now() time.Time
}

type Service struct {
	tasks      TaskRepository
	boards     BoardRepository
	workspaces WorkspaceRepository
	activity   ActivityRepository
	tx         TxManager
	events     EventPublisher
	clock      Clock
}

func NewService(tasks TaskRepository, boards BoardRepository, workspaces WorkspaceRepository, activity ActivityRepository,
	tx TxManager, events EventPublisher, clock Clock) *Service {
	return &Service{tasks: tasks, boards: boards, workspaces: workspaces, activity: activity, tx: tx, events: events, clock: clock}
}

// WorkspaceOfTask resolves a task to its workspace for authorization.
func (s *Service) WorkspaceOfTask(ctx context.Context, taskID domain.ID) (domain.ID, error) {
	return s.tasks.WorkspaceOfTask(ctx, taskID)
}

// Get reads the task, its assignee and its labels from one snapshot.
func (s *Service) Get(ctx context.Context, acc domain.Access, taskID domain.ID) (domain.TaskDetail, error) {
	if err := acc.Require(domain.ActionRead); err != nil {
		return domain.TaskDetail{}, err
	}
	return s.read(ctx, taskID)
}

func (s *Service) read(ctx context.Context, taskID domain.ID) (domain.TaskDetail, error) {
	var task domain.TaskDetail
	err := s.tx.InReadTx(ctx, func(ctx context.Context) error {
		var err error
		task, err = s.tasks.Detail(ctx, taskID)
		return err
	})
	return task, err
}

type CreateInput struct {
	ColumnID    domain.ID
	Title       string
	Description string
	Priority    *domain.Priority // nil: medium
	DueDate     *time.Time
	AssigneeID  *domain.ID
	LabelIDs    []domain.ID
}

// Create appends a task at the end of its column. The assignee must be a
// member of the workspace and the labels must belong to the column's board.
func (s *Service) Create(ctx context.Context, acc domain.Access, in CreateInput) (domain.TaskDetail, error) {
	if err := acc.Require(domain.ActionEditTasks); err != nil {
		return domain.TaskDetail{}, err
	}
	fields := domain.TaskFields{
		Title: in.Title, Description: in.Description, Priority: domain.PriorityMedium,
		DueDate: in.DueDate, AssigneeID: in.AssigneeID, LabelIDs: domain.NormalizeIDs(in.LabelIDs),
	}
	if in.Priority != nil {
		fields.Priority = *in.Priority
	}
	if err := fields.Validate(); err != nil {
		return domain.TaskDetail{}, err
	}

	var task domain.TaskDetail
	var logged domain.ActivityEntry
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		// Lock order everywhere: membership, then column, then labels, then task.
		if err := s.lockAssignee(ctx, acc.WorkspaceID, fields.AssigneeID); err != nil {
			return err
		}
		if err := s.boards.LockColumn(ctx, in.ColumnID); err != nil {
			return fmt.Errorf("lock column: %w", err)
		}
		loc, err := s.boards.ColumnLocation(ctx, in.ColumnID) // domain.ErrColumnNotFound if deleted after authorization
		if err != nil {
			return err
		}
		if err := s.lockLabels(ctx, loc.BoardID, fields.LabelIDs); err != nil {
			return err
		}

		last, err := s.tasks.LastPosition(ctx, in.ColumnID, domain.NilID)
		if err != nil {
			return fmt.Errorf("last task position: %w", err)
		}
		pos, _ := domain.PositionBetween(last, nil) // appending always has room
		created, err := s.tasks.Create(ctx, in.ColumnID, pos, fields, acc.UserID)
		if err != nil {
			return err
		}
		if len(fields.LabelIDs) > 0 {
			if err := s.tasks.SetLabels(ctx, created.ID, fields.LabelIDs); err != nil {
				return fmt.Errorf("add labels: %w", err)
			}
		}

		if logged, err = s.log(ctx, loc, acc.UserID, domain.ActivityTaskCreated, domain.TaskCreatedPayload{
			TaskID: created.ID, Title: created.Title, ColumnID: created.ColumnID, Position: created.Position,
			Priority: created.Priority, DueDate: domain.FormatDate(created.DueDate),
			AssigneeID: created.AssigneeID, LabelIDs: fields.LabelIDs,
		}); err != nil {
			return err
		}
		task, err = s.tasks.Detail(ctx, created.ID)
		return err
	})
	if err != nil {
		return domain.TaskDetail{}, err
	}
	s.events.Publish(ctx, logged.Event())
	return task, nil
}

// Update applies a partial edit with optimistic concurrency: it only applies
// if version is still current, otherwise it returns *domain.ConflictError
// with the task as it is now. The change and its activity entry commit
// together; the event is published after the commit.
func (s *Service) Update(ctx context.Context, acc domain.Access, taskID domain.ID, version int32, edit domain.TaskEdit) (domain.TaskDetail, error) {
	if err := acc.Require(domain.ActionEditTasks); err != nil {
		return domain.TaskDetail{}, err
	}
	if edit.Empty() {
		return domain.TaskDetail{}, domain.Invalid("body", "Indica al menos un campo a cambiar")
	}

	var result domain.TaskDetail
	var logged domain.ActivityEntry
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		if edit.AssigneeID.Set {
			if err := s.lockAssignee(ctx, acc.WorkspaceID, edit.AssigneeID.Value); err != nil {
				return err
			}
		}
		// The row lock makes the version check below and the labels read
		// next consistent with the update: nobody can change them meanwhile.
		before, err := s.tasks.GetForUpdate(ctx, taskID)
		if err != nil {
			return err
		}
		if before.Version != version {
			return domain.ErrConflict
		}
		beforeLabels, err := s.tasks.Labels(ctx, taskID)
		if err != nil {
			return fmt.Errorf("list task labels: %w", err)
		}

		old := domain.FieldsOf(before, labelIDs(beforeLabels))
		next := old.Apply(edit, s.clock.Now())
		if err := next.Validate(); err != nil {
			return err
		}
		loc, err := s.boards.ColumnLocation(ctx, before.ColumnID)
		if err != nil {
			return fmt.Errorf("locate column: %w", err)
		}
		labelsChanged := !equalIDs(old.LabelIDs, next.LabelIDs)
		if labelsChanged {
			if err := s.lockLabels(ctx, loc.BoardID, next.LabelIDs); err != nil {
				return err
			}
		}

		updated, err := s.tasks.Update(ctx, taskID, version, next)
		if err != nil {
			return err // domain.ErrConflict: the version guard of the write itself
		}
		if labelsChanged {
			if err := s.tasks.SetLabels(ctx, taskID, next.LabelIDs); err != nil {
				return fmt.Errorf("set labels: %w", err)
			}
		}

		// Diff against what was stored, using the values the write returned
		// (archived_at as the database rounded it).
		stored := domain.FieldsOf(updated, next.LabelIDs)
		if logged, err = s.log(ctx, loc, acc.UserID, domain.ActivityTaskUpdated, domain.TaskUpdatedPayload{
			TaskID: updated.ID, Title: updated.Title, Version: updated.Version, Changes: old.Diff(stored),
		}); err != nil {
			return err
		}
		result, err = s.tasks.Detail(ctx, taskID)
		return err
	})
	if errors.Is(err, domain.ErrConflict) {
		// Read the current state after the rollback, outside the transaction.
		return domain.TaskDetail{}, s.conflict(ctx, taskID, domain.ConflictVersion)
	}
	if err != nil {
		return domain.TaskDetail{}, err
	}
	s.events.Publish(ctx, logged.Event())
	return result, nil
}

// MoveInput places a task in ColumnID between AfterTaskID and BeforeTaskID.
// With only one of them, the other neighbor is whatever is adjacent to it now;
// with neither, the task goes to the end of the column.
type MoveInput struct {
	ColumnID     domain.ID
	AfterTaskID  *domain.ID
	BeforeTaskID *domain.ID
	Version      int32
}

// errNeighborsChanged aborts a move; Move turns it into a *ConflictError.
var errNeighborsChanged = errors.New("neighbors changed")

// Move changes a task's column and/or position under the same version guard
// as Update. The target column is locked first and then the task, so
// concurrent moves into a column cannot compute the same position.
func (s *Service) Move(ctx context.Context, acc domain.Access, taskID domain.ID, in MoveInput) (domain.TaskDetail, error) {
	if err := acc.Require(domain.ActionEditTasks); err != nil {
		return domain.TaskDetail{}, err
	}
	for _, n := range []*domain.ID{in.AfterTaskID, in.BeforeTaskID} {
		if n != nil && *n == taskID {
			return domain.TaskDetail{}, domain.Invalid("afterTaskId", "Una tarea no puede ser su propia vecina")
		}
	}

	var result domain.TaskDetail
	var logged domain.ActivityEntry
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		target, err := s.boards.GetColumn(ctx, in.ColumnID)
		if errors.Is(err, domain.ErrColumnNotFound) {
			return domain.Invalid("columnId", "La columna no existe")
		}
		if err != nil {
			return fmt.Errorf("get column: %w", err)
		}
		if err := s.boards.LockColumn(ctx, target.ID); err != nil {
			return fmt.Errorf("lock column: %w", err)
		}

		task, err := s.tasks.GetForUpdate(ctx, taskID)
		if err != nil {
			return err
		}
		if task.Version != in.Version {
			return domain.ErrConflict
		}

		source, err := s.boards.GetColumn(ctx, task.ColumnID)
		if err != nil {
			return fmt.Errorf("get source column: %w", err)
		}
		// Authorization covered the task's workspace; staying within the board
		// keeps the move inside it too (and keeps its labels valid).
		if source.BoardID != target.BoardID {
			return domain.Invalid("columnId", "La columna debe pertenecer al mismo tablero")
		}

		for attempt := 0; ; attempt++ {
			prev, next, err := s.neighbors(ctx, task, in)
			if err != nil {
				return err
			}
			if pos, ok := domain.PositionBetween(prev, next); ok {
				moved, err := s.tasks.Move(ctx, taskID, in.Version, target.ID, pos)
				if err != nil {
					return err // the row is locked and the version checked, so no conflict here
				}
				// task.Position is pre-rebalance; it is where the task was as far
				// as anyone outside this transaction could have seen.
				loc, err := s.boards.ColumnLocation(ctx, moved.ColumnID)
				if err != nil {
					return fmt.Errorf("locate column: %w", err)
				}
				if logged, err = s.log(ctx, loc, acc.UserID, domain.ActivityTaskMoved, domain.TaskMovedPayload{
					TaskID: moved.ID, Title: moved.Title, Version: moved.Version,
					From: domain.TaskPlacement{ColumnID: task.ColumnID, Position: task.Position},
					To:   domain.TaskPlacement{ColumnID: moved.ColumnID, Position: moved.Position},
				}); err != nil {
					return err
				}
				result, err = s.tasks.Detail(ctx, taskID)
				return err
			}
			if attempt > 0 {
				return errors.New("no room between tasks even after rebalancing")
			}
			if err := s.tasks.Rebalance(ctx, target.ID); err != nil {
				return fmt.Errorf("rebalance tasks: %w", err)
			}
		}
	})
	switch {
	case errors.Is(err, domain.ErrConflict):
		return domain.TaskDetail{}, s.conflict(ctx, taskID, domain.ConflictVersion)
	case errors.Is(err, errNeighborsChanged):
		return domain.TaskDetail{}, s.conflict(ctx, taskID, domain.ConflictNeighbors)
	case err != nil:
		return domain.TaskDetail{}, err
	}
	s.events.Publish(ctx, logged.Event())
	return result, nil
}

// neighbors returns the positions the moved task must fall between in the
// target column. They are re-read on every call so a rebalance in between
// is taken into account.
func (s *Service) neighbors(ctx context.Context, moving domain.Task, in MoveInput) (prev, next *float64, err error) {
	load := func(id *domain.ID, field string) (*domain.Task, error) {
		if id == nil {
			return nil, nil
		}
		t, err := s.tasks.Get(ctx, *id)
		if errors.Is(err, domain.ErrTaskNotFound) || (err == nil && t.ColumnID != in.ColumnID) {
			return nil, domain.Invalid(field, "La tarea indicada no está en la columna destino")
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
		if !domain.TaskBefore(*after, *before) {
			return nil, nil, errNeighborsChanged
		}
		return &after.Position, &before.Position, nil
	case after != nil:
		next, err := s.tasks.PositionAfter(ctx, in.ColumnID, moving.ID, *after)
		return &after.Position, next, err
	case before != nil:
		prev, err := s.tasks.PositionBefore(ctx, in.ColumnID, moving.ID, *before)
		return prev, &before.Position, err
	default:
		last, err := s.tasks.LastPosition(ctx, in.ColumnID, moving.ID)
		return last, nil, err
	}
}

func (s *Service) Delete(ctx context.Context, acc domain.Access, taskID domain.ID) error {
	if err := acc.Require(domain.ActionEditTasks); err != nil {
		return err
	}
	return s.tasks.Delete(ctx, taskID)
}

// conflict builds the 409 error with the task's current state, or
// domain.ErrTaskNotFound if it was deleted in the meantime.
func (s *Service) conflict(ctx context.Context, taskID domain.ID, reason domain.ConflictReason) error {
	current, err := s.read(ctx, taskID)
	if err != nil {
		return err
	}
	return &domain.ConflictError{Reason: reason, Current: current}
}

// lockAssignee checks that assignee (if any) is a member of the workspace and
// keeps them from being removed until the transaction ends. Removing a member
// unassigns their tasks; without the lock, a removal racing with this
// assignment could miss the task and leave a non-member assigned.
func (s *Service) lockAssignee(ctx context.Context, workspaceID domain.ID, assignee *domain.ID) error {
	if assignee == nil {
		return nil
	}
	err := s.workspaces.LockMembership(ctx, workspaceID, *assignee)
	if errors.Is(err, domain.ErrNotMember) {
		return domain.Invalid("assignee_id", "El responsable debe ser miembro del workspace")
	}
	if err != nil {
		return fmt.Errorf("lock membership: %w", err)
	}
	return nil
}

// lockLabels checks that every id is a label of the board and keeps those
// labels from being deleted until the transaction ends.
func (s *Service) lockLabels(ctx context.Context, boardID domain.ID, ids []domain.ID) error {
	if len(ids) == 0 {
		return nil
	}
	found, err := s.boards.LockBoardLabels(ctx, boardID, ids)
	if err != nil {
		return fmt.Errorf("lock labels: %w", err)
	}
	if found != len(ids) {
		return domain.Invalid("label_ids", "Alguna etiqueta no existe o no es de este tablero")
	}
	return nil
}

// log records an entry with the transaction in ctx: if the change rolls
// back, so does its entry, and if the entry cannot be written, the change fails.
func (s *Service) log(ctx context.Context, loc domain.Location, actorID domain.ID, typ string, payload any) (domain.ActivityEntry, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return domain.ActivityEntry{}, fmt.Errorf("activity: encode payload: %w", err)
	}
	var actor *domain.ID
	if !actorID.IsZero() {
		actor = &actorID
	}
	entry, err := s.activity.Append(ctx, domain.NewActivity{
		WorkspaceID: loc.WorkspaceID, BoardID: loc.BoardID, ActorID: actor, Type: typ, Payload: data,
	})
	if err != nil {
		return domain.ActivityEntry{}, fmt.Errorf("activity: insert: %w", err)
	}
	return entry, nil
}

func labelIDs(labels []domain.Label) []domain.ID {
	ids := make([]domain.ID, len(labels))
	for i, l := range labels {
		ids[i] = l.ID
	}
	return domain.NormalizeIDs(ids)
}

func equalIDs(a, b []domain.ID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
