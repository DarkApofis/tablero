package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/app/board"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

type labelResponse struct {
	ID        uuid.UUID `json:"id"`
	BoardID   uuid.UUID `json:"board_id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
}

func newLabelResponse(l domain.Label) labelResponse {
	return labelResponse{ID: uuid.UUID(l.ID), BoardID: uuid.UUID(l.BoardID), Name: l.Name, Color: l.Color, CreatedAt: l.CreatedAt}
}

type labelHandler struct {
	labels *board.LabelService
}

// labelWorkspace resolves /labels/{id} to its workspace for requireAccess.
func (h *labelHandler) labelWorkspace(r *http.Request) (domain.ID, error) {
	id, err := pathUUID(r)
	if err != nil {
		return domain.NilID, err
	}
	return h.labels.WorkspaceOfLabel(r.Context(), id)
}

// list: GET /boards/{id}/labels, ordered by name.
func (h *labelHandler) list(w http.ResponseWriter, r *http.Request) {
	labels, err := h.labels.List(r.Context(), access(r), pathID(r))
	if err != nil {
		writeLabelError(w, r, "list labels", err)
		return
	}
	out := make([]labelResponse, 0, len(labels))
	for _, l := range labels {
		out = append(out, newLabelResponse(l))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"labels": out})
}

// create: POST /boards/{id}/labels with {"name", "color"}.
func (h *labelHandler) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	label, err := h.labels.Create(r.Context(), access(r), pathID(r), req.Name, req.Color)
	if err != nil {
		writeLabelError(w, r, "create label", err)
		return
	}
	WriteJSON(w, http.StatusCreated, newLabelResponse(label))
}

// update: PATCH /labels/{id} with {"name"?, "color"?}.
func (h *labelHandler) update(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  *string `json:"name"`
		Color *string `json:"color"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	label, err := h.labels.Update(r.Context(), access(r), pathID(r), req.Name, req.Color)
	if err != nil {
		writeLabelError(w, r, "update label", err)
		return
	}
	WriteJSON(w, http.StatusOK, newLabelResponse(label))
}

func (h *labelHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.labels.Delete(r.Context(), access(r), pathID(r)); err != nil {
		writeLabelError(w, r, "delete label", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeLabelError(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, domain.ErrLabelNameTaken):
		WriteError(w, http.StatusConflict, CodeLabelExists, "Ya existe una etiqueta con ese nombre en este tablero")
	// Only reachable if the resource was deleted after authorization.
	case errors.Is(err, domain.ErrLabelNotFound):
		WriteError(w, http.StatusNotFound, CodeNotFound, "La etiqueta no existe")
	default:
		writeBoardError(w, r, op, err) // validation errors, missing board and 500s
	}
}
