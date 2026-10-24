package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/service"
)

// userRef is how responses point at a user: activity actors, task assignees.
type userRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type activityItem struct {
	ID        int64           `json:"id"`
	Type      string          `json:"type"`
	Actor     *userRef        `json:"actor"` // null if the user was deleted
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type activityPageResponse struct {
	Items      []activityItem `json:"items"`
	NextCursor *string        `json:"next_cursor"` // null on the last page
}

type activityHandler struct {
	activity *service.ActivityService
}

// list: GET /boards/{id}/activity?limit=50&cursor=... newest first.
func (h *activityHandler) list(w http.ResponseWriter, r *http.Request) {
	limit := service.DefaultActivityLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > service.MaxActivityLimit {
			WriteValidationError(w, map[string]string{"limit": fmt.Sprintf("limit debe estar entre 1 y %d", service.MaxActivityLimit)})
			return
		}
		limit = n
	}

	page, err := h.activity.ListBoard(r.Context(), pathID(r), r.URL.Query().Get("cursor"), limit)
	if errors.Is(err, service.ErrInvalidCursor) {
		WriteValidationError(w, map[string]string{"cursor": "Cursor inválido"})
		return
	}
	if err != nil {
		internalError(w, r, "list activity", err)
		return
	}

	resp := activityPageResponse{Items: make([]activityItem, 0, len(page.Items))}
	for _, row := range page.Items {
		item := activityItem{ID: row.ID, Type: row.Type, Payload: row.Payload, CreatedAt: row.CreatedAt}
		if row.ActorID.Valid {
			item.Actor = &userRef{ID: row.ActorID.UUID, Name: row.ActorName.String}
		}
		resp.Items = append(resp.Items, item)
	}
	if page.NextCursor != "" {
		resp.NextCursor = &page.NextCursor
	}
	WriteJSON(w, http.StatusOK, resp)
}
