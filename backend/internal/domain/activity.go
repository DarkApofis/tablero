package domain

import (
	"encoding/json"
	"time"
)

// Activity types stored in activity_log.type (and returned by the activity
// API). Every change of a board's content writes one entry, in the same
// transaction; each entry is also a domain event (see EventType).
const (
	ActivityTaskCreated     = "task_created"
	ActivityTaskUpdated     = "task_updated"
	ActivityTaskArchived    = "task_archived" // an update that archived the task
	ActivityTaskMoved       = "task_moved"
	ActivityTaskDeleted     = "task_deleted"
	ActivityColumnCreated   = "column_created"
	ActivityColumnUpdated   = "column_updated"
	ActivityColumnReordered = "column_reordered"
	ActivityColumnDeleted   = "column_deleted"
	ActivityLabelChanged    = "label_changed"
)

// Payloads, one per activity type. They are stored as jsonb and returned
// as-is by the API and the event stream, so their JSON shape is part of the
// API. Task payloads carry the task's version after the change.

// TaskCreatedPayload is the whole task as created.
type TaskCreatedPayload struct {
	TaskID      ID       `json:"task_id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	ColumnID    ID       `json:"column_id"`
	Position    float64  `json:"position"`
	Version     int32    `json:"version"`
	Priority    Priority `json:"priority"`
	DueDate     *string  `json:"due_date"` // YYYY-MM-DD
	AssigneeID  *ID      `json:"assignee_id"`
	LabelIDs    []ID     `json:"label_ids"`
}

// TaskUpdatedPayload is the payload of task_updated and task_archived.
type TaskUpdatedPayload struct {
	TaskID  ID     `json:"task_id"`
	Title   string `json:"title"` // after the change, for display
	Version int32  `json:"version"`
	// Changes has only the fields that actually changed: title, description,
	// priority, due_date, assignee_id, archived_at and/or label_ids.
	Changes map[string]FieldChange `json:"changes"`
}

// FieldChange is a field's value before and after. Values are encoded as in
// the API (strings, dates as YYYY-MM-DD, ids, sorted id lists); null means
// the field was empty.
type FieldChange struct {
	From any `json:"from"`
	To   any `json:"to"`
}

type TaskMovedPayload struct {
	TaskID  ID            `json:"task_id"`
	Title   string        `json:"title"`
	Version int32         `json:"version"`
	From    TaskPlacement `json:"from"`
	To      TaskPlacement `json:"to"`
	// Rebalanced is set when the move had to respread the target column: the
	// new position of every task in it (their versions do not change).
	Rebalanced []TaskPosition `json:"rebalanced,omitempty"`
}

type TaskPlacement struct {
	ColumnID ID      `json:"column_id"`
	Position float64 `json:"position"`
}

type TaskPosition struct {
	TaskID   ID      `json:"task_id"`
	Position float64 `json:"position"`
}

type TaskDeletedPayload struct {
	TaskID   ID     `json:"task_id"`
	Title    string `json:"title"`
	ColumnID ID     `json:"column_id"`
	Version  int32  `json:"version"` // the last one it had
}

// ColumnCreatedPayload is the whole column as created.
type ColumnCreatedPayload struct {
	ColumnID ID      `json:"column_id"`
	Name     string  `json:"name"`
	Position float64 `json:"position"`
}

type ColumnUpdatedPayload struct {
	ColumnID ID                     `json:"column_id"`
	Name     string                 `json:"name"` // after the change
	Changes  map[string]FieldChange `json:"changes"`
}

type ColumnReorderedPayload struct {
	ColumnID ID      `json:"column_id"`
	Name     string  `json:"name"`
	From     float64 `json:"from"`
	To       float64 `json:"to"`
	// Rebalanced is set when the board's columns had to be respread: the new
	// position of every column.
	Rebalanced []ColumnPosition `json:"rebalanced,omitempty"`
}

type ColumnPosition struct {
	ColumnID ID      `json:"column_id"`
	Position float64 `json:"position"`
}

type ColumnDeletedPayload struct {
	ColumnID ID     `json:"column_id"`
	Name     string `json:"name"`
	// DeletedTaskIDs are the tasks that went with it.
	DeletedTaskIDs []ID `json:"deleted_task_ids"`
}

const (
	LabelCreated = "created"
	LabelUpdated = "updated"
	LabelDeleted = "deleted"
)

// LabelChangedPayload: a label was created, updated or deleted. Label is the
// label after the change (before it, for a delete); deleting it also removes
// it from every task, without changing their versions.
type LabelChangedPayload struct {
	Action  string                 `json:"action"` // created, updated or deleted
	Label   LabelSnapshot          `json:"label"`
	Changes map[string]FieldChange `json:"changes,omitempty"` // updated only
}

type LabelSnapshot struct {
	ID    ID     `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

func SnapshotOf(l Label) LabelSnapshot { return LabelSnapshot{ID: l.ID, Name: l.Name, Color: l.Color} }

// NewActivity is an activity_log entry to write, in the same transaction as
// the change it records. Its workspace is the board's.
type NewActivity struct {
	BoardID ID
	ActorID *ID // nil: no actor
	Type    string
	Payload json.RawMessage
}

// ActivityEntry is a stored activity_log entry.
type ActivityEntry struct {
	// ID grows with every entry and, within a board, entries become visible
	// in ID order (the log serializes appends per board). It is the activity
	// pagination cursor and the event id.
	ID          int64
	WorkspaceID ID
	BoardID     ID
	ActorID     *ID    // nil if the user was deleted
	ActorName   string // when read with the actor joined
	Type        string
	Payload     json.RawMessage
	CreatedAt   time.Time
}

// --- events ---

// EventType is the public name of a domain event, as the event stream sends it.
type EventType string

const (
	EventTaskCreated     EventType = "task.created"
	EventTaskUpdated     EventType = "task.updated"
	EventTaskArchived    EventType = "task.archived"
	EventTaskMoved       EventType = "task.moved"
	EventTaskDeleted     EventType = "task.deleted"
	EventColumnCreated   EventType = "column.created"
	EventColumnUpdated   EventType = "column.updated"
	EventColumnReordered EventType = "column.reordered"
	EventColumnDeleted   EventType = "column.deleted"
	EventLabelChanged    EventType = "label.changed"
)

var eventTypes = map[string]EventType{
	ActivityTaskCreated:     EventTaskCreated,
	ActivityTaskUpdated:     EventTaskUpdated,
	ActivityTaskArchived:    EventTaskArchived,
	ActivityTaskMoved:       EventTaskMoved,
	ActivityTaskDeleted:     EventTaskDeleted,
	ActivityColumnCreated:   EventColumnCreated,
	ActivityColumnUpdated:   EventColumnUpdated,
	ActivityColumnReordered: EventColumnReordered,
	ActivityColumnDeleted:   EventColumnDeleted,
	ActivityLabelChanged:    EventLabelChanged,
}

// Event is a committed change of a board. Use cases publish one per activity
// entry, only after the transaction commits; its ID is the entry's.
type Event struct {
	ID          int64
	Type        EventType
	WorkspaceID ID
	BoardID     ID
	ActorID     *ID
	// TaskVersion is the task's version after the change, for task events.
	TaskVersion *int32
	Payload     json.RawMessage
	OccurredAt  time.Time
}

// Event returns the domain event the entry records.
func (a ActivityEntry) Event() Event {
	ev := Event{
		ID: a.ID, Type: eventTypes[a.Type], WorkspaceID: a.WorkspaceID, BoardID: a.BoardID, ActorID: a.ActorID,
		Payload: a.Payload, OccurredAt: a.CreatedAt,
	}
	if ev.Type == "" {
		ev.Type = EventType(a.Type) // unknown to this build: pass it through
	}
	if taskEvents[ev.Type] {
		var v struct {
			Version *int32 `json:"version"`
		}
		if json.Unmarshal(a.Payload, &v) == nil {
			ev.TaskVersion = v.Version
		}
	}
	return ev
}

var taskEvents = map[EventType]bool{
	EventTaskCreated: true, EventTaskUpdated: true, EventTaskArchived: true, EventTaskMoved: true, EventTaskDeleted: true,
}
