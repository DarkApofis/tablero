package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

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

// --- reading ---

const (
	DefaultActivityLimit = 50
	MaxActivityLimit     = 100
)

var ErrInvalidCursor = errors.New("invalid cursor")

type ActivityService struct {
	q *db.Queries
}

func NewActivityService(pool *pgxpool.Pool) *ActivityService {
	return &ActivityService{q: db.New(pool)}
}

type ActivityPage struct {
	Items []db.ListBoardActivityRow
	// NextCursor fetches the next (older) page; empty when this is the last one.
	NextCursor string
}

// ListBoard returns a page of a board's activity, newest first. cursor is
// empty for the first page or a NextCursor from a previous page.
func (s *ActivityService) ListBoard(ctx context.Context, boardID uuid.UUID, cursor string, limit int) (ActivityPage, error) {
	before := int64(math.MaxInt64)
	if cursor != "" {
		id, err := decodeCursor(cursor)
		if err != nil {
			return ActivityPage{}, err
		}
		before = id
	}

	// One extra row tells us whether there is a next page without a COUNT.
	rows, err := s.q.ListBoardActivity(ctx, db.ListBoardActivityParams{
		BoardID: boardID, BeforeID: before, RowLimit: int32(limit + 1),
	})
	if err != nil {
		return ActivityPage{}, err
	}

	page := ActivityPage{Items: rows}
	if len(rows) > limit {
		page.Items = rows[:limit]
		page.NextCursor = encodeCursor(page.Items[limit-1].ID)
	}
	return page, nil
}

// Cursors are opaque to clients: base64url of the last id they saw.
func encodeCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

func decodeCursor(c string) (int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, ErrInvalidCursor
	}
	id, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrInvalidCursor
	}
	return id, nil
}
