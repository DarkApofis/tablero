// Package board holds the use cases of boards, their columns, their labels
// and their activity log.
package board

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Ports.

type BoardRepository interface {
	// LocationOfBoard and LocationOfColumn return domain.ErrNotMember when
	// the board or column does not exist, so it looks like a forbidden one.
	LocationOfBoard(ctx context.Context, boardID domain.ID) (domain.Location, error)
	LocationOfColumn(ctx context.Context, columnID domain.ID) (domain.Location, error)

	CreateBoard(ctx context.Context, workspaceID domain.ID, name string, visibility domain.Visibility, createdBy domain.ID) (domain.Board, error)
	// ListBoards lists the workspace's boards that are archived (or not) and
	// that userID may see: with seeAll every one, otherwise those open to the
	// workspace and the private ones given to them.
	ListBoards(ctx context.Context, workspaceID, userID domain.ID, seeAll, archived bool) ([]domain.BoardSummary, error)
	// GetBoard, UpdateBoard and DeleteBoard return domain.ErrBoardNotFound.
	GetBoard(ctx context.Context, boardID domain.ID) (domain.Board, error)
	// UpdateBoard stores its name, visibility and archived time.
	UpdateBoard(ctx context.Context, b domain.Board) (domain.Board, error)
	DeleteBoard(ctx context.Context, boardID domain.ID) error
	// BoardMembers lists who was given a private board.
	BoardMembers(ctx context.Context, boardID domain.ID) ([]domain.BoardMember, error)
	// SetBoardMembers replaces them; it returns the ids that are not members
	// of the board's workspace, which were left out.
	SetBoardMembers(ctx context.Context, boardID domain.ID, userIDs []domain.ID) (rejected []domain.ID, err error)
	// CopyBoard copies the columns, labels and tasks that are not archived of
	// source into target, an empty board, and who source was given to if
	// withMembers; actorID is the creator of the copied tasks.
	CopyBoard(ctx context.Context, sourceID, targetID, actorID domain.ID, withMembers bool) error
	// MoveBoard puts the board, with its log, in another workspace: whoever
	// is not a member there is taken off its private list and unassigned
	// from its tasks (logged as edits by actorID). It must run in a
	// transaction, and returns the entries it logged.
	MoveBoard(ctx context.Context, boardID, workspaceID, actorID domain.ID) (domain.Board, []domain.ActivityEntry, error)
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
	// LocationOfLabel returns domain.ErrNotMember when the label does not exist.
	LocationOfLabel(ctx context.Context, labelID domain.ID) (domain.Location, error)
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

// LocationOfBoard resolves a board to its workspace for authorization.
func (s *Service) LocationOfBoard(ctx context.Context, boardID domain.ID) (domain.Location, error) {
	return s.boards.LocationOfBoard(ctx, boardID)
}

// LocationOfColumn resolves a column to its board and workspace for authorization.
func (s *Service) LocationOfColumn(ctx context.Context, columnID domain.ID) (domain.Location, error) {
	return s.boards.LocationOfColumn(ctx, columnID)
}

// --- boards ---

// MaxInitialColumns caps the columns a board can be created with.
const MaxInitialColumns = 20

// CreateBoard makes a board with the given columns, in order, all in one
// transaction: a board is never left half made. An empty visibility is
// domain.VisibilityWorkspace; a private board is given to its creator, so
// they keep it if they stop being an admin.
func (s *Service) CreateBoard(ctx context.Context, acc domain.Access, name string, columns []string, visibility domain.Visibility) (domain.Board, error) {
	if err := acc.Require(domain.ActionManageBoards); err != nil {
		return domain.Board{}, err
	}
	name, err := domain.Name(name, domain.MaxBoardNameLen)
	if err != nil {
		return domain.Board{}, err
	}
	if visibility == "" {
		visibility = domain.VisibilityWorkspace
	}
	if !visibility.Valid() {
		return domain.Board{}, domain.Invalid("visibility", "La visibilidad debe ser workspace o private")
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
		if b, err = s.boards.CreateBoard(ctx, acc.WorkspaceID, name, visibility, acc.UserID); err != nil {
			return err
		}
		if visibility == domain.VisibilityPrivate {
			if _, err := s.boards.SetBoardMembers(ctx, b.ID, []domain.ID{acc.UserID}); err != nil {
				return fmt.Errorf("give board to creator: %w", err)
			}
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

// ListBoards lists the workspace's boards acc's user may see (admins see the
// private ones too): the archived ones, or the rest.
func (s *Service) ListBoards(ctx context.Context, acc domain.Access, archived bool) ([]domain.BoardSummary, error) {
	if err := acc.Require(domain.ActionRead); err != nil {
		return nil, err
	}
	return s.boards.ListBoards(ctx, acc.WorkspaceID, acc.UserID, acc.Role == domain.RoleAdmin, archived)
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

// UpdateBoard renames the board, changes who may open it, or archives or
// restores it.
func (s *Service) UpdateBoard(ctx context.Context, acc domain.Access, boardID domain.ID, c domain.BoardChanges) (domain.Board, error) {
	if err := acc.Require(domain.ActionManageBoards); err != nil {
		return domain.Board{}, err
	}
	if c == (domain.BoardChanges{}) {
		return domain.Board{}, domain.Invalid("name", "Indica qué cambiar: el nombre, la visibilidad o si está archivado")
	}
	var name string
	if c.Name != nil {
		var err error
		if name, err = domain.Name(*c.Name, domain.MaxBoardNameLen); err != nil {
			return domain.Board{}, err
		}
	}
	if c.Visibility != nil && !c.Visibility.Valid() {
		return domain.Board{}, domain.Invalid("visibility", "La visibilidad debe ser workspace o private")
	}

	var b domain.Board
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.boards.LockBoard(ctx, boardID); err != nil {
			return fmt.Errorf("lock board: %w", err)
		}
		var err error
		if b, err = s.boards.GetBoard(ctx, boardID); err != nil {
			return err
		}
		if c.Name != nil {
			b.Name = name
		}
		if c.Visibility != nil {
			b.Visibility = *c.Visibility
		}
		if c.Archived != nil {
			switch {
			case *c.Archived && b.ArchivedAt == nil:
				now := time.Now()
				b.ArchivedAt = &now
			case !*c.Archived:
				b.ArchivedAt = nil
			}
		}
		b, err = s.boards.UpdateBoard(ctx, b)
		return err
	})
	return b, err
}

// BoardMembers lists who was given a private board; admins see it anyway.
func (s *Service) BoardMembers(ctx context.Context, acc domain.Access, boardID domain.ID) ([]domain.BoardMember, error) {
	if err := acc.Require(domain.ActionManageBoards); err != nil {
		return nil, err
	}
	return s.boards.BoardMembers(ctx, boardID)
}

// SetBoardMembers replaces who was given a private board. Each must be a
// member of the board's workspace.
func (s *Service) SetBoardMembers(ctx context.Context, acc domain.Access, boardID domain.ID, userIDs []domain.ID) ([]domain.BoardMember, error) {
	if err := acc.Require(domain.ActionManageBoards); err != nil {
		return nil, err
	}
	if len(userIDs) > domain.MaxBoardMembers {
		return nil, domain.Invalid("user_ids", fmt.Sprintf("Como mucho %d personas", domain.MaxBoardMembers))
	}
	ids := slices.Clone(userIDs)
	slices.SortFunc(ids, func(a, b domain.ID) int { return strings.Compare(a.String(), b.String()) })
	ids = slices.Compact(ids)

	var members []domain.BoardMember
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.boards.LockBoard(ctx, boardID); err != nil {
			return fmt.Errorf("lock board: %w", err)
		}
		rejected, err := s.boards.SetBoardMembers(ctx, boardID, ids)
		if err != nil {
			return err
		}
		if len(rejected) > 0 {
			return domain.Invalid("user_ids", "Solo puedes dar el tablero a miembros del workspace")
		}
		members, err = s.boards.BoardMembers(ctx, boardID)
		return err
	})
	return members, err
}

// copySuffix is what the name of a copy ends with.
const copySuffix = " (copia)"

// DuplicateBoard makes a copy of the board in its workspace, named after it
// unless a name is given: its columns, labels and the tasks that are not
// archived and, if it is private, who it was given to. Its activity is not
// copied, and the copy is not archived.
func (s *Service) DuplicateBoard(ctx context.Context, acc domain.Access, boardID domain.ID, name string) (domain.Board, error) {
	if err := acc.Require(domain.ActionManageBoards); err != nil {
		return domain.Board{}, err
	}
	var copied domain.Board
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		// Locked so no column is added or moved while it is copied.
		if err := s.boards.LockBoard(ctx, boardID); err != nil {
			return fmt.Errorf("lock board: %w", err)
		}
		source, err := s.boards.GetBoard(ctx, boardID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(name) == "" {
			name = truncate(source.Name, domain.MaxBoardNameLen-utf8.RuneCountInString(copySuffix)) + copySuffix
		}
		if name, err = domain.Name(name, domain.MaxBoardNameLen); err != nil {
			return err
		}
		if copied, err = s.boards.CreateBoard(ctx, source.WorkspaceID, name, source.Visibility, acc.UserID); err != nil {
			return err
		}
		return s.boards.CopyBoard(ctx, source.ID, copied.ID, acc.UserID, source.Visibility == domain.VisibilityPrivate)
	})
	return copied, err
}

// MoveBoard puts the board in target's workspace, where acc's user must be
// allowed to manage boards too. Whoever is not a member there loses the
// board: they are taken off its private list and unassigned from its tasks.
func (s *Service) MoveBoard(ctx context.Context, acc, target domain.Access, boardID domain.ID) (domain.Board, error) {
	if err := acc.Require(domain.ActionManageBoards); err != nil {
		return domain.Board{}, err
	}
	if err := target.Require(domain.ActionManageBoards); err != nil {
		return domain.Board{}, err
	}
	var moved domain.Board
	var logged []domain.ActivityEntry
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.boards.LockBoard(ctx, boardID); err != nil {
			return fmt.Errorf("lock board: %w", err)
		}
		var err error
		moved, logged, err = s.boards.MoveBoard(ctx, boardID, target.WorkspaceID, acc.UserID)
		return err
	})
	if err != nil {
		return domain.Board{}, err
	}
	for _, entry := range logged {
		s.rec.publish(ctx, entry)
	}
	return moved, nil
}

// truncate cuts s to at most n characters.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
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
