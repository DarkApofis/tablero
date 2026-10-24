package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxTaskTitleLen       = 200
	MaxTaskDescriptionLen = 10_000
)

// DateLayout is how due dates travel in the API and the activity log.
const DateLayout = "2006-01-02"

type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityMedium Priority = "medium"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

func (p Priority) Valid() bool {
	switch p {
	case PriorityLow, PriorityMedium, PriorityHigh, PriorityUrgent:
		return true
	}
	return false
}

type Task struct {
	ID          ID
	ColumnID    ID
	Title       string
	Description string
	Position    float64
	// Version grows with every edit or move; edits must name the version
	// they started from (optimistic concurrency, docs/DECISIONS.md #1).
	Version    int32
	Priority   Priority
	DueDate    *time.Time // a date: midnight UTC
	AssigneeID *ID
	ArchivedAt *time.Time
	CreatedBy  *ID // nil if the user was deleted
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// TaskDetail is a task with what the API shows alongside it.
type TaskDetail struct {
	Task
	AssigneeName string  // empty when unassigned
	Labels       []Label // ordered by name
}

// TaskBefore orders tasks the way a column does: by position, then id.
func TaskBefore(a, b Task) bool {
	if a.Position != b.Position {
		return a.Position < b.Position
	}
	return a.ID.String() < b.ID.String()
}

// --- conflicts ---

// ErrConflict: the task changed since the client read it. Repositories
// return it when the version guard rejects a write; use cases turn it into
// a *ConflictError carrying the current state.
var ErrConflict = errors.New("conflict")

type ConflictReason string

const (
	// ConflictVersion: the client's version is not the current one.
	ConflictVersion ConflictReason = "version"
	// ConflictNeighbors: afterTaskId and beforeTaskId are no longer in that
	// order, so the client's view of the column is stale.
	ConflictNeighbors ConflictReason = "neighbors"
)

// ConflictError reports a conflict with the task as it is now, for the
// client to reconcile (docs/DECISIONS.md #1). errors.Is(err, ErrConflict) holds.
type ConflictError struct {
	Reason  ConflictReason
	Current TaskDetail
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("task %s conflict (%s, current version %d)", e.Current.ID, e.Reason, e.Current.Version)
}

func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

// --- editable fields ---

// Optional is a field of a partial edit that can also be cleared: Set false
// keeps the current value, Set with a nil Value clears it.
type Optional[T any] struct {
	Set   bool
	Value *T
}

// TaskFields are the editable fields of a task, with labels as sorted ids.
type TaskFields struct {
	Title       string
	Description string
	Priority    Priority
	DueDate     *time.Time
	AssigneeID  *ID
	ArchivedAt  *time.Time
	LabelIDs    []ID
}

// FieldsOf returns the editable fields of t, with the given label ids.
func FieldsOf(t Task, labelIDs []ID) TaskFields {
	return TaskFields{
		Title: t.Title, Description: t.Description, Priority: t.Priority,
		DueDate: t.DueDate, AssigneeID: t.AssigneeID, ArchivedAt: t.ArchivedAt, LabelIDs: NormalizeIDs(labelIDs),
	}
}

// TaskEdit is a partial edit; nil (or unset) fields keep their value.
type TaskEdit struct {
	Title       *string
	Description *string
	Priority    *Priority
	DueDate     Optional[time.Time]
	AssigneeID  Optional[ID]
	LabelIDs    *[]ID // replaces the whole set
	Archived    *bool
}

func (e TaskEdit) Empty() bool {
	return e.Title == nil && e.Description == nil && e.Priority == nil && !e.DueDate.Set &&
		!e.AssigneeID.Set && e.LabelIDs == nil && e.Archived == nil
}

// Apply returns f with the edit applied; now is the archiving time.
func (f TaskFields) Apply(e TaskEdit, now time.Time) TaskFields {
	if e.Title != nil {
		f.Title = *e.Title
	}
	if e.Description != nil {
		f.Description = *e.Description
	}
	if e.Priority != nil {
		f.Priority = *e.Priority
	}
	if e.DueDate.Set {
		f.DueDate = e.DueDate.Value
	}
	if e.AssigneeID.Set {
		f.AssigneeID = e.AssigneeID.Value
	}
	if e.LabelIDs != nil {
		f.LabelIDs = NormalizeIDs(*e.LabelIDs)
	}
	switch {
	case e.Archived == nil:
	case !*e.Archived:
		f.ArchivedAt = nil
	case f.ArchivedAt == nil: // archiving an archived task keeps the original time
		f.ArchivedAt = &now
	}
	return f
}

// Validate checks the values and trims the title.
func (f *TaskFields) Validate() error {
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

// Diff lists the fields that differ from f to g, for the activity log.
func (f TaskFields) Diff(g TaskFields) map[string]FieldChange {
	changes := map[string]FieldChange{}
	add := func(name string, from, to any, changed bool) {
		if changed {
			changes[name] = FieldChange{From: from, To: to}
		}
	}
	add("title", f.Title, g.Title, f.Title != g.Title)
	add("description", f.Description, g.Description, f.Description != g.Description)
	add("priority", f.Priority, g.Priority, f.Priority != g.Priority)
	add("due_date", FormatDate(f.DueDate), FormatDate(g.DueDate), !equalTime(f.DueDate, g.DueDate))
	add("assignee_id", f.AssigneeID, g.AssigneeID, !equalID(f.AssigneeID, g.AssigneeID))
	add("archived_at", f.ArchivedAt, g.ArchivedAt, !equalTime(f.ArchivedAt, g.ArchivedAt))
	add("label_ids", f.LabelIDs, g.LabelIDs, !slices.Equal(f.LabelIDs, g.LabelIDs))
	return changes
}

// FormatDate renders a due date as the API does, or nil.
func FormatDate(d *time.Time) *string {
	if d == nil {
		return nil
	}
	s := d.Format(DateLayout)
	return &s
}

// NormalizeIDs sorts and dedupes, so label sets compare and log stably. It
// never returns nil, so an empty set is logged as [] rather than null.
func NormalizeIDs(ids []ID) []ID {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b ID) int { return strings.Compare(a.String(), b.String()) })
	out = slices.Compact(out)
	if out == nil {
		out = []ID{}
	}
	return out
}

func equalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func equalID(a, b *ID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
