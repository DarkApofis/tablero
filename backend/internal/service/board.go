package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/db"
	"github.com/DarkApofis/tablero/backend/internal/position"
)

const (
	MaxBoardNameLen  = 100
	MaxColumnNameLen = 100
)

var (
	ErrBoardNotFound  = errors.New("board not found")
	ErrColumnNotFound = errors.New("column not found")
)

type BoardService struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewBoardService(pool *pgxpool.Pool) *BoardService {
	return &BoardService{pool: pool, q: db.New(pool)}
}

// BoardDetail is a board with its columns, each with its tasks, all ordered by position.
type BoardDetail struct {
	Board   db.Board
	Columns []ColumnWithTasks
}

type ColumnWithTasks struct {
	db.Column
	Tasks []db.Task
}

// --- workspace resolution, used for authorization ---

// WorkspaceOfBoard returns the workspace a board belongs to. A missing board
// is reported as ErrNotMember so it cannot be told apart from a forbidden one.
func (s *BoardService) WorkspaceOfBoard(ctx context.Context, boardID uuid.UUID) (uuid.UUID, error) {
	id, err := s.q.GetBoardWorkspaceID(ctx, boardID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotMember
	}
	return id, err
}

// WorkspaceOfColumn is WorkspaceOfBoard for columns.
func (s *BoardService) WorkspaceOfColumn(ctx context.Context, columnID uuid.UUID) (uuid.UUID, error) {
	id, err := s.q.GetColumnWorkspaceID(ctx, columnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotMember
	}
	return id, err
}

// --- boards ---

func (s *BoardService) CreateBoard(ctx context.Context, workspaceID uuid.UUID, name string) (db.Board, error) {
	name, err := validName(name, MaxBoardNameLen)
	if err != nil {
		return db.Board{}, err
	}
	return s.q.CreateBoard(ctx, db.CreateBoardParams{WorkspaceID: workspaceID, Name: name})
}

func (s *BoardService) ListBoards(ctx context.Context, workspaceID uuid.UUID) ([]db.Board, error) {
	return s.q.ListBoardsByWorkspace(ctx, workspaceID)
}

// GetBoardDetail reads the board, its columns and its tasks from one snapshot
// so the three lists are consistent with each other.
func (s *BoardService) GetBoardDetail(ctx context.Context, boardID uuid.UUID) (BoardDetail, error) {
	var detail BoardDetail
	err := db.InTx(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *db.Queries) error {
		board, err := q.GetBoard(ctx, boardID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBoardNotFound
		}
		if err != nil {
			return fmt.Errorf("get board: %w", err)
		}
		columns, err := q.ListColumnsByBoard(ctx, boardID)
		if err != nil {
			return fmt.Errorf("list columns: %w", err)
		}
		tasks, err := q.ListTasksByBoard(ctx, boardID)
		if err != nil {
			return fmt.Errorf("list tasks: %w", err)
		}

		// Tasks arrive ordered by position; bucketing keeps that order per column.
		byColumn := make(map[uuid.UUID][]db.Task, len(columns))
		for _, t := range tasks {
			byColumn[t.ColumnID] = append(byColumn[t.ColumnID], t)
		}
		detail = BoardDetail{Board: board, Columns: make([]ColumnWithTasks, 0, len(columns))}
		for _, c := range columns {
			detail.Columns = append(detail.Columns, ColumnWithTasks{Column: c, Tasks: byColumn[c.ID]})
		}
		return nil
	})
	return detail, err
}

func (s *BoardService) RenameBoard(ctx context.Context, boardID uuid.UUID, name string) (db.Board, error) {
	name, err := validName(name, MaxBoardNameLen)
	if err != nil {
		return db.Board{}, err
	}
	board, err := s.q.UpdateBoardName(ctx, db.UpdateBoardNameParams{ID: boardID, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Board{}, ErrBoardNotFound
	}
	return board, err
}

func (s *BoardService) DeleteBoard(ctx context.Context, boardID uuid.UUID) error {
	n, err := s.q.DeleteBoard(ctx, boardID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrBoardNotFound
	}
	return nil
}

// --- columns ---

// CreateColumn appends a column at the end of the board.
func (s *BoardService) CreateColumn(ctx context.Context, boardID uuid.UUID, name string) (db.Column, error) {
	name, err := validName(name, MaxColumnNameLen)
	if err != nil {
		return db.Column{}, err
	}

	var col db.Column
	err = db.InTx(ctx, s.pool, pgx.TxOptions{}, func(q *db.Queries) error {
		if err := q.LockBoard(ctx, boardID); err != nil {
			return fmt.Errorf("lock board: %w", err)
		}
		columns, err := q.ListColumnsByBoard(ctx, boardID)
		if err != nil {
			return fmt.Errorf("list columns: %w", err)
		}
		var last *float64
		if n := len(columns); n > 0 {
			last = &columns[n-1].Position
		}
		pos, _ := position.Between(last, nil) // appending always has room
		col, err = q.CreateColumn(ctx, db.CreateColumnParams{BoardID: boardID, Name: name, Position: pos})
		return err
	})
	return col, err
}

func (s *BoardService) RenameColumn(ctx context.Context, columnID uuid.UUID, name string) (db.Column, error) {
	name, err := validName(name, MaxColumnNameLen)
	if err != nil {
		return db.Column{}, err
	}
	col, err := s.q.UpdateColumnName(ctx, db.UpdateColumnNameParams{ID: columnID, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Column{}, ErrColumnNotFound
	}
	return col, err
}

func (s *BoardService) DeleteColumn(ctx context.Context, columnID uuid.UUID) error {
	n, err := s.q.DeleteColumn(ctx, columnID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrColumnNotFound
	}
	return nil
}

// MoveColumn places columnID right after afterID in its board, or first when
// afterID is nil. The board is locked so concurrent moves cannot pick the
// same position; if the gap is exhausted the board is rebalanced first.
func (s *BoardService) MoveColumn(ctx context.Context, columnID uuid.UUID, afterID *uuid.UUID) (db.Column, error) {
	if afterID != nil && *afterID == columnID {
		return db.Column{}, &ValidationError{Fields: map[string]string{"after_id": "Una columna no puede ir despuÃ©s de sÃ­ misma"}}
	}

	var moved db.Column
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(q *db.Queries) error {
		col, err := q.GetColumn(ctx, columnID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrColumnNotFound
		}
		if err != nil {
			return fmt.Errorf("get column: %w", err)
		}
		if err := q.LockBoard(ctx, col.BoardID); err != nil {
			return fmt.Errorf("lock board: %w", err)
		}

		for attempt := 0; ; attempt++ {
			others, err := q.ListColumnsByBoard(ctx, col.BoardID)
			if err != nil {
				return fmt.Errorf("list columns: %w", err)
			}
			others = slices.DeleteFunc(others, func(c db.Column) bool { return c.ID == columnID })

			prev, next, err := neighbors(others, afterID)
			if err != nil {
				return err
			}
			if pos, ok := position.Between(prev, next); ok {
				moved, err = q.UpdateColumnPosition(ctx, db.UpdateColumnPositionParams{ID: columnID, Position: pos})
				return err
			}
			if attempt > 0 {
				return errors.New("no room between columns even after rebalancing")
			}
			if err := q.RebalanceColumns(ctx, col.BoardID); err != nil {
				return fmt.Errorf("rebalance columns: %w", err)
			}
		}
	})
	return moved, err
}

// neighbors returns the positions around the slot right after afterID (or the
// first slot when afterID is nil) in ordered, which excludes the moving item.
func neighbors(ordered []db.Column, afterID *uuid.UUID) (prev, next *float64, err error) {
	idx := -1 // index of the item we go after; -1 means "at the start"
	if afterID != nil {
		idx = slices.IndexFunc(ordered, func(c db.Column) bool { return c.ID == *afterID })
		if idx < 0 {
			return nil, nil, &ValidationError{Fields: map[string]string{"after_id": "La columna indicada no pertenece a este tablero"}}
		}
		prev = &ordered[idx].Position
	}
	if idx+1 < len(ordered) {
		next = &ordered[idx+1].Position
	}
	return prev, next, nil
}

func validName(name string, maxLen int) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", &ValidationError{Fields: map[string]string{"name": "El nombre es obligatorio"}}
	case utf8.RuneCountInString(name) > maxLen:
		return "", &ValidationError{Fields: map[string]string{"name": fmt.Sprintf("El nombre no puede superar %d caracteres", maxLen)}}
	}
	return name, nil
}
