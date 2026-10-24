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
