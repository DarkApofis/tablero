package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxBoardNameLen  = 100
	MaxColumnNameLen = 100
)

type Board struct {
	ID          ID
	WorkspaceID ID
	Name        string
	CreatedAt   time.Time
}

type Column struct {
	ID        ID
	BoardID   ID
	Name      string
	Position  float64
	CreatedAt time.Time
}

// Location is where a column lives: its board and that board's workspace.
type Location struct {
	WorkspaceID ID
	BoardID     ID
}

// BoardDetail is a board with its labels and its columns, each with its
// tasks; columns and tasks ordered by position.
type BoardDetail struct {
	Board   Board
	Labels  []Label
	Columns []ColumnWithTasks
}

type ColumnWithTasks struct {
	Column
	Tasks []TaskDetail
}

// Name trims and checks the name of a board or column.
func Name(name string, maxLen int) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", Invalid("name", "El nombre es obligatorio")
	case utf8.RuneCountInString(name) > maxLen:
		return "", Invalid("name", fmt.Sprintf("El nombre no puede superar %d caracteres", maxLen))
	}
	return name, nil
}

// ColumnNeighbors returns the positions around the slot right after afterID
// (or the first slot when afterID is nil) in ordered, which must exclude the
// column being moved.
func ColumnNeighbors(ordered []Column, afterID *ID) (prev, next *float64, err error) {
	idx := -1 // index of the column we go after; -1 means "at the start"
	if afterID != nil {
		idx = slices.IndexFunc(ordered, func(c Column) bool { return c.ID == *afterID })
		if idx < 0 {
			return nil, nil, Invalid("after_id", "La columna indicada no pertenece a este tablero")
		}
		prev = &ordered[idx].Position
	}
	if idx+1 < len(ordered) {
		next = &ordered[idx+1].Position
	}
	return prev, next, nil
}
