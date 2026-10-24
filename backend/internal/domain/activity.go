package domain

import (
	"encoding/json"
	"time"
)

// Activity types stored in activity_log.type.
const (
	ActivityTaskCreated = "task_created"
	ActivityTaskUpdated = "task_updated"
	ActivityTaskMoved   = "task_moved"
)

// Payloads, one per activity type. They are stored as jsonb and returned
// as-is by the API, so their JSON shape is part of the API.

type TaskCreatedPayload struct {
	TaskID     ID       `json:"task_id"`
	Title      string   `json:"title"`
	ColumnID   ID       `json:"column_id"`
	Position   float64  `json:"position"`
	Priority   Priority `json:"priority"`
	DueDate    *string  `json:"due_date"` // YYYY-MM-DD
	AssigneeID *ID      `json:"assignee_id"`
	LabelIDs   []ID     `json:"label_ids"`
}

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
}

type TaskPlacement struct {
	ColumnID ID      `json:"column_id"`
	Position float64 `json:"position"`
}

// NewActivity is an activity_log entry to write, in the same transaction as
// the change it records.
type NewActivity struct {
	WorkspaceID ID
	BoardID     ID
	ActorID     *ID // nil: no actor
	Type        string
	Payload     json.RawMessage
}

// ActivityEntry is a stored activity_log entry.
type ActivityEntry struct {
	ID          int64 // grows with every entry; the pagination cursor
	WorkspaceID ID
	BoardID     ID
	ActorID     *ID    // nil if the user was deleted
	ActorName   string // when read with the actor joined
	Type        string
	Payload     json.RawMessage
	CreatedAt   time.Time
}

// Event tells subscribers (later, SSE clients) that something changed. Use
// cases publish them only after the change has committed.
type Event struct {
	Type        string
	WorkspaceID ID
	BoardID     ID
	ActorID     *ID
	ActivityID  int64
	Payload     json.RawMessage
	OccurredAt  time.Time
}

// Event returns the event announcing the entry.
func (a ActivityEntry) Event() Event {
	return Event{
		Type: a.Type, WorkspaceID: a.WorkspaceID, BoardID: a.BoardID, ActorID: a.ActorID,
		ActivityID: a.ID, Payload: a.Payload, OccurredAt: a.CreatedAt,
	}
}
