package domain

import "time"

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
