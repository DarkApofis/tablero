package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/db"
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
	TaskID     uuid.UUID       `json:"task_id"`
	Title      string          `json:"title"`
	ColumnID   uuid.UUID       `json:"column_id"`
	Position   float64         `json:"position"`
	Priority   db.TaskPriority `json:"priority"`
	DueDate    *string         `json:"due_date"` // YYYY-MM-DD
	AssigneeID *uuid.UUID      `json:"assignee_id"`
	LabelIDs   []uuid.UUID     `json:"label_ids"`
}

type TaskUpdatedPayload struct {
	TaskID  uuid.UUID `json:"task_id"`
	Title   string    `json:"title"` // after the change, for display
	Version int32     `json:"version"`
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
	TaskID  uuid.UUID     `json:"task_id"`
	Title   string        `json:"title"`
	Version int32         `json:"version"`
	From    TaskPlacement `json:"from"`
	To      TaskPlacement `json:"to"`
}

type TaskPlacement struct {
	ColumnID uuid.UUID `json:"column_id"`
	Position float64   `json:"position"`
}

// logActivity records an entry with q, which must be the transaction of the
// change being logged: if the change rolls back, so does its entry, and if
// the entry cannot be written, the change fails.
func logActivity(ctx context.Context, q *db.Queries, columnID, actorID uuid.UUID, typ string, payload any) error {
	loc, err := q.GetColumnLocation(ctx, columnID)
	if err != nil {
		return fmt.Errorf("activity: locate column: %w", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("activity: encode payload: %w", err)
	}
	if err := q.InsertActivity(ctx, db.InsertActivityParams{
		WorkspaceID: loc.WorkspaceID,
		BoardID:     loc.BoardID,
		ActorID:     uuid.NullUUID{UUID: actorID, Valid: actorID != uuid.Nil},
		Type:        typ,
		Payload:     data,
	}); err != nil {
		return fmt.Errorf("activity: insert: %w", err)
	}
	return nil
}
