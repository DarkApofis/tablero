// Package board holds the use cases of boards, their columns, their labels
// and their activity log.
package board

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Ports.

type BoardRepository interface {
	// WorkspaceOfBoard and WorkspaceOfColumn return domain.ErrNotMember when
	// the board or column does not exist, so it looks like a forbidden one.
	WorkspaceOfBoard(ctx context.Context, boardID domain.ID) (domain.ID, error)
	WorkspaceOfColumn(ctx context.Context, columnID domain.ID) (domain.ID, error)

	CreateBoard(ctx context.Context, workspaceID domain.ID, name string) (domain.Board, error)
	ListBoards(ctx context.Context, workspaceID domain.ID) ([]domain.Board, error)
	// GetBoard, RenameBoard and DeleteBoard return domain.ErrBoardNotFound.
	GetBoard(ctx context.Context, boardID domain.ID) (domain.Board, error)
	RenameBoard(ctx context.Context, boardID domain.ID, name string) (domain.Board, error)
	DeleteBoard(ctx context.Context, boardID domain.ID) error
	// LockBoard serializes position changes inside a board until the transaction ends.
	LockBoard(ctx context.Context, boardID domain.ID) error

	// ListColumns returns a board's columns ordered by position.
	ListColumns(ctx context.Context, boardID domain.ID) ([]domain.Column, error)
	// GetColumn, RenameColumn and DeleteColumn return domain.ErrColumnNotFound.
	GetColumn(ctx context.Context, columnID domain.ID) (domain.Column, error)
	// LockColumn keeps the column from changing until the transaction ends.
	LockColumn(ctx context.Context, columnID domain.ID) error
	// ColumnTaskIDs returns the ids of the column's tasks.
	ColumnTaskIDs(ctx context.Context, columnID domain.ID) ([]domain.ID, error)
	CreateColumn(ctx context.Context, boardID domain.ID, name string, position float64) (domain.Column, error)
	RenameColumn(ctx context.Context, columnID domain.ID, name string) (domain.Column, error)
	SetColumnPosition(ctx context.Context, columnID domain.ID, position float64) (domain.Column, error)
	DeleteColumn(ctx context.Context, columnID domain.ID) error
	// RebalanceColumns spreads a board's columns evenly, keeping their order.
	RebalanceColumns(ctx context.Context, boardID domain.ID) error

	// ListBoardTasks returns every task of the board ordered by position,
	// with the assignee's name but without labels (see ListBoardTaskLabels).
	ListBoardTasks(ctx context.Context, boardID domain.ID, includeArchived bool) ([]domain.TaskDetail, error)
	// ListBoardTaskLabels returns, for every task of the board, its labels
	// ordered by name.
	ListBoardTaskLabels(ctx context.Context, boardID domain.ID) (map[domain.ID][]domain.Label, error)
}

type LabelRepository interface {
	// WorkspaceOfLabel returns domain.ErrNotMember when the label does not exist.
	WorkspaceOfLabel(ctx context.Context, labelID domain.ID) (domain.ID, error)
	// ListLabels returns a board's labels ordered by name.
	ListLabels(ctx context.Context, boardID domain.ID) ([]domain.Label, error)
	// CreateLabel returns domain.ErrLabelNameTaken or, if the board is gone,
	// domain.ErrBoardNotFound.
	CreateLabel(ctx context.Context, boardID domain.ID, name, color string) (domain.Label, error)
	// GetLabelForUpdate reads a label and locks it until the transaction ends;
	// domain.ErrLabelNotFound.
	GetLabelForUpdate(ctx context.Context, labelID domain.ID) (domain.Label, error)
	// UpdateLabel changes the fields that are not nil; domain.ErrLabelNotFound
	// or domain.ErrLabelNameTaken.
	UpdateLabel(ctx context.Context, labelID domain.ID, name, color *string) (domain.Label, error)
	DeleteLabel(ctx context.Context, labelID domain.ID) error
}

type ActivityRepository interface {
	// ListBoardActivity returns up to limit entries of the board with an id
	// below beforeID, newest first, with the actor's name.
	ListBoardActivity(ctx context.Context, boardID domain.ID, beforeID int64, limit int) ([]domain.ActivityEntry, error)
}

type TxManager interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
	// InReadTx runs fn on one consistent snapshot, read-only.
	InReadTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Service implements the board and column use cases. Column changes are
// logged and published as events, like task changes.
type Service struct {
	boards BoardRepository
	labels LabelRepository
	tx     TxManager
	rec    recorder
}

func NewService(boards BoardRepository, labels LabelRepository, activity ActivityWriter, tx TxManager, events EventPublisher) *Service {
	return &Service{boards: boards, labels: labels, tx: tx, rec: recorder{activity: activity, events: events}}
}

// WorkspaceOfBoard resolves a board to its workspace for authorization.
func (s *Service) WorkspaceOfBoard(ctx context.Context, boardID domain.ID) (domain.ID, error) {
	return s.boards.WorkspaceOfBoard(ctx, boardID)
}

// WorkspaceOfColumn resolves a column to its workspace for authorization.
func (s *Service) WorkspaceOfColumn(ctx context.Context, columnID domain.ID) (domain.ID, error) {
	return s.boards.WorkspaceOfColumn(ctx, columnID)
}

// --- boards ---

// MaxInitialColumns caps the columns a board can be created with.
const MaxInitialColumns = 20

// CreateBoard makes a board with the given columns, in order, all in one
// transaction: a board is never left half made.
func (s *Service) CreateBoard(ctx context.Context, acc domain.Access, name string, columns []string) (domain.Board, error) {
	if err := acc.Require(domain.ActionManageBoards); err != nil {
		return domain.Board{}, err
	}
	name, err := domain.Name(name, domain.MaxBoardNameLen)
	if err != nil {
		return domain.Board{}, err
	}
	if len(columns) > MaxInitialColumns {
		return domain.Board{}, domain.Invalid("columns", fmt.Sprintf("Como mucho %d columnas", MaxInitialColumns))
	}
	for i, c := range columns {
		if columns[i], err = domain.Name(c, domain.MaxColumnNameLen); err != nil {
			return domain.Board{}, domain.Invalid("columns", "Cada columna necesita un nombre de hasta 100 caracteres")
		}
	}

	var b domain.Board
	var logged []domain.ActivityEntry
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		var err error
		if b, err = s.boards.CreateBoard(ctx, acc.WorkspaceID, name); err != nil {
			return err
		}
		for _, c := range columns {
			_, entry, err := s.appendColumn(ctx, acc, b.ID, c)
			if err != nil {
				return err
			}
			logged = append(logged, entry)
		}
		return nil
	})
	if err != nil {
		return domain.Board{}, err
	}
	for _, entry := range logged {
		s.rec.publish(ctx, entry)
	}
	return b, nil
}

func (s *Service) ListBoards(ctx context.Context, acc domain.Access) ([]domain.Board, error) {
	if err := acc.Require(domain.ActionRead); err != nil {
		return nil, err
	}
	return s.boards.ListBoards(ctx, acc.WorkspaceID)
}

// GetBoardDetail reads the board, its labels, its columns and their tasks
// (with assignee and labels) from one snapshot so they are consistent with
// each other. It runs a fixed number of queries however many tasks there
// are. Archived tasks are left out unless includeArchived.
func (s *Service) GetBoardDetail(ctx context.Context, acc domain.Access, boardID domain.ID, includeArchived bool) (domain.BoardDetail, error) {
	if err := acc.Require(domain.ActionRead); err != nil {
		return domain.BoardDetail{}, err
	}
	var detail domain.BoardDetail
	err := s.tx.InReadTx(ctx, func(ctx context.Context) error {
		board, err := s.boards.GetBoard(ctx, boardID)
		if err != nil {
			return err
		}
		labels, err := s.labels.ListLabels(ctx, boardID)
		if err != nil {
			return fmt.Errorf("list labels: %w", err)
		}
		columns, err := s.boards.ListColumns(ctx, boardID)
		if err != nil {
			return fmt.Errorf("list columns: %w", err)
		}
		tasks, err := s.boards.ListBoardTasks(ctx, boardID, includeArchived)
		if err != nil {
			return fmt.Errorf("list tasks: %w", err)
		}
		taskLabels, err := s.boards.ListBoardTaskLabels(ctx, boardID)
		if err != nil {
			return fmt.Errorf("list task labels: %w", err)
		}

		// Tasks arrive ordered by position; bucketing keeps that order per column.
		byColumn := make(map[domain.ID][]domain.TaskDetail, len(columns))
		for _, t := range tasks {
			t.Labels = taskLabels[t.ID]
			byColumn[t.ColumnID] = append(byColumn[t.ColumnID], t)
		}
		detail = domain.BoardDetail{Board: board, Labels: labels, Columns: make([]domain.ColumnWithTasks, 0, len(columns))}
		for _, c := range columns {
			detail.Columns = append(detail.Columns, domain.ColumnWithTasks{Column: c, Tasks: byColumn[c.ID]})
		}
		return nil
	})
	return detail, err
}

func (s *Service) RenameBoard(ctx context.Context, acc domain.Access, boardID domain.ID, name string) (domain.Board, error) {
	if err := acc.Require(domain.ActionManageBoards); err != nil {
		return domain.Board{}, err
	}
	name, err := domain.Name(name, domain.MaxBoardNameLen)
	if err != nil {
		return domain.Board{}, err
	}
	return s.boards.RenameBoard(ctx, boardID, name)
}

// DeleteBoard deletes the board with its columns, tasks, labels and activity.
func (s *Service) DeleteBoard(ctx context.Context, acc domain.Access, boardID domain.ID) error {
	if err := acc.Require(domain.ActionManageBoards); err != nil {
		return err
	}
	return s.boards.DeleteBoard(ctx, boardID)
}

// --- columns ---

// appendColumn adds a column (name already validated) at the end of the
// board and logs it, in the caller's transaction; the caller publishes.
func (s *Service) appendColumn(ctx context.Context, acc domain.Access, boardID domain.ID, name string) (domain.Column, domain.ActivityEntry, error) {
	if err := s.boards.LockBoard(ctx, boardID); err != nil {
		return domain.Column{}, domain.ActivityEntry{}, fmt.Errorf("lock board: %w", err)
	}
	columns, err := s.boards.ListColumns(ctx, boardID)
	if err != nil {
		return domain.Column{}, domain.ActivityEntry{}, fmt.Errorf("list columns: %w", err)
	}
	var last *float64
	if n := len(columns); n > 0 {
		last = &columns[n-1].Position
	}
	pos, _ := domain.PositionBetween(last, nil) // appending always has room
	col, err := s.boards.CreateColumn(ctx, boardID, name, pos)
	if err != nil {
		return domain.Column{}, domain.ActivityEntry{}, err
	}
	logged, err := s.rec.log(ctx, acc, boardID, domain.ActivityColumnCreated, domain.ColumnCreatedPayload{
		ColumnID: col.ID, Name: col.Name, Position: col.Position,
	})
	return col, logged, err
}

// CreateColumn appends a column at the end of the board.
func (s *Service) CreateColumn(ctx context.Context, acc domain.Access, boardID domain.ID, name string) (domain.Column, error) {
	if err := acc.Require(domain.ActionManageBoards); err != nil {
		return domain.Column{}, err
	}
	name, err := domain.Name(name, domain.MaxColumnNameLen)
	if err != nil {
		return domain.Column{}, err
	}

	var col domain.Column
	var logged domain.ActivityEntry
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		var err error
		col, logged, err = s.appendColumn(ctx, acc, boardID, name)
		return err
	})
	if err != nil {
		return domain.Column{}, err
	}
	s.rec.publish(ctx, logged)
	return col, nil
}

func (s *Service) RenameColumn(ctx context.Context, acc domain.Access, columnID domain.ID, name string) (domain.Column, error) {
	if err := acc.Require(domain.ActionManageBoards); err != nil {
		return domain.Column{}, err
	}
	name, err := domain.Name(name, domain.MaxColumnNameLen)
	if err != nil {
		return domain.Column{}, err
	}

	var col domain.Column
	var logged domain.ActivityEntry
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.boards.LockColumn(ctx, columnID); err != nil {
			return fmt.Errorf("lock column: %w", err)
		}
		before, err := s.boards.GetColumn(ctx, columnID)
		if err != nil {
			return err
		}
		if col, err = s.boards.RenameColumn(ctx, columnID, name); err != nil {
			return err
		}
		changes := map[string]domain.FieldChange{}
		if before.Name != col.Name {
			changes["name"] = domain.FieldChange{From: before.Name, To: col.Name}
		}
		logged, err = s.rec.log(ctx, acc, col.BoardID, domain.ActivityColumnUpdated, domain.ColumnUpdatedPayload{
			ColumnID: col.ID, Name: col.Name, Changes: changes,
		})
		return err
	})
	if err != nil {
		return domain.Column{}, err
	}
	s.rec.publish(ctx, logged)
	return col, nil
}

// DeleteColumn deletes the column with its tasks; the event lists them.
func (s *Service) DeleteColumn(ctx context.Context, acc domain.Access, columnID domain.ID) error {
	if err := acc.Require(domain.ActionManageBoards); err != nil {
		return err
	}
	var logged domain.ActivityEntry
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		// Locked first, so no task can be added to it between listing and deleting.
		if err := s.boards.LockColumn(ctx, columnID); err != nil {
			return fmt.Errorf("lock column: %w", err)
		}
		col, err := s.boards.GetColumn(ctx, columnID)
		if err != nil {
			return err
		}
		taskIDs, err := s.boards.ColumnTaskIDs(ctx, columnID)
		if err != nil {
			return fmt.Errorf("list column tasks: %w", err)
		}
		if err := s.boards.DeleteColumn(ctx, columnID); err != nil {
			return err
		}
		if taskIDs == nil {
			taskIDs = []domain.ID{}
		}
		logged, err = s.rec.log(ctx, acc, col.BoardID, domain.ActivityColumnDeleted, domain.ColumnDeletedPayload{
			ColumnID: col.ID, Name: col.Name, DeletedTaskIDs: taskIDs,
		})
		return err
	})
	if err != nil {
		return err
	}
	s.rec.publish(ctx, logged)
	return nil
}

// MoveColumn places columnID right after afterID in its board, or first when
// afterID is nil. The board is locked so concurrent moves cannot pick the
// same position; if the gap is exhausted the board is rebalanced first.
func (s *Service) MoveColumn(ctx context.Context, acc domain.Access, columnID domain.ID, afterID *domain.ID) (domain.Column, error) {
	if err := acc.Require(domain.ActionManageBoards); err != nil {
		return domain.Column{}, err
	}
	if afterID != nil && *afterID == columnID {
		return domain.Column{}, domain.Invalid("after_id", "Una columna no puede ir después de sí misma")
	}

	var moved domain.Column
	var logged domain.ActivityEntry
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		col, err := s.boards.GetColumn(ctx, columnID)
		if err != nil {
			return err
		}
		if err := s.boards.LockBoard(ctx, col.BoardID); err != nil {
			return fmt.Errorf("lock board: %w", err)
		}

		rebalanced := false
		for attempt := 0; ; attempt++ {
			others, err := s.boards.ListColumns(ctx, col.BoardID)
			if err != nil {
				return fmt.Errorf("list columns: %w", err)
			}
			others = slices.DeleteFunc(others, func(c domain.Column) bool { return c.ID == columnID })

			prev, next, err := domain.ColumnNeighbors(others, afterID)
			if err != nil {
				return err
			}
			if pos, ok := domain.PositionBetween(prev, next); ok {
				if moved, err = s.boards.SetColumnPosition(ctx, columnID, pos); err != nil {
					return err
				}
				payload := domain.ColumnReorderedPayload{ColumnID: moved.ID, Name: moved.Name, From: col.Position, To: moved.Position}
				if rebalanced {
					// The other columns moved too; clients need their positions.
					all, err := s.boards.ListColumns(ctx, col.BoardID)
					if err != nil {
						return fmt.Errorf("list columns: %w", err)
					}
					for _, c := range all {
						payload.Rebalanced = append(payload.Rebalanced, domain.ColumnPosition{ColumnID: c.ID, Position: c.Position})
					}
				}
				logged, err = s.rec.log(ctx, acc, col.BoardID, domain.ActivityColumnReordered, payload)
				return err
			}
			if attempt > 0 {
				return errors.New("no room between columns even after rebalancing")
			}
			if err := s.boards.RebalanceColumns(ctx, col.BoardID); err != nil {
				return fmt.Errorf("rebalance columns: %w", err)
			}
			rebalanced = true
		}
	})
	if err != nil {
		return domain.Column{}, err
	}
	s.rec.publish(ctx, logged)
	return moved, nil
}
