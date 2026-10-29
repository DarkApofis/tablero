package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/app/board"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

type boardResponse struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
}

func newBoardResponse(b domain.Board) boardResponse {
	return boardResponse{ID: uuid.UUID(b.ID), WorkspaceID: uuid.UUID(b.WorkspaceID), Name: b.Name, CreatedAt: b.CreatedAt}
}

type columnResponse struct {
	ID        uuid.UUID `json:"id"`
	BoardID   uuid.UUID `json:"board_id"`
	Name      string    `json:"name"`
	Position  float64   `json:"position"`
	CreatedAt time.Time `json:"created_at"`
}

func newColumnResponse(c domain.Column) columnResponse {
	return columnResponse{ID: uuid.UUID(c.ID), BoardID: uuid.UUID(c.BoardID), Name: c.Name, Position: c.Position, CreatedAt: c.CreatedAt}
}

type taskResponse struct {
	ID          uuid.UUID       `json:"id"`
	ColumnID    uuid.UUID       `json:"column_id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Position    float64         `json:"position"`
	Version     int32           `json:"version"`
	Priority    domain.Priority `json:"priority"`
	DueDate     *string         `json:"due_date"` // YYYY-MM-DD
	Assignee    *userRef        `json:"assignee"`
	Labels      []labelResponse `json:"labels"`
	ArchivedAt  *time.Time      `json:"archived_at"`
	CreatedBy   uuid.NullUUID   `json:"created_by"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func newTaskResponse(t domain.TaskDetail) taskResponse {
	resp := taskResponse{
		ID: uuid.UUID(t.ID), ColumnID: uuid.UUID(t.ColumnID), Title: t.Title, Description: t.Description,
		Position: t.Position, Version: t.Version, Priority: t.Priority,
		Labels: make([]labelResponse, 0, len(t.Labels)), ArchivedAt: t.ArchivedAt,
		CreatedBy: nullUUID(t.CreatedBy), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
	if t.DueDate != nil {
		d := t.DueDate.Format(domain.DateLayout)
		resp.DueDate = &d
	}
	if t.AssigneeID != nil {
		resp.Assignee = &userRef{ID: uuid.UUID(*t.AssigneeID), Name: t.AssigneeName}
	}
	for _, l := range t.Labels {
		resp.Labels = append(resp.Labels, newLabelResponse(l))
	}
	return resp
}

func nullUUID(id *domain.ID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: uuid.UUID(*id), Valid: true}
}

type boardDetailResponse struct {
	boardResponse
	// Role is the caller's role in the board's workspace, so the client knows
	// which actions to offer.
	Role    domain.Role               `json:"role"`
	Labels  []labelResponse           `json:"labels"`
	Columns []columnWithTasksResponse `json:"columns"`
}

type columnWithTasksResponse struct {
	columnResponse
	Tasks []taskResponse `json:"tasks"`
}

type boardHandler struct {
	boards *board.Service
}

// boardWorkspace and columnWorkspace resolve /boards/{id} and /columns/{id}
// to their workspace for requireAccess.
func (h *boardHandler) boardWorkspace(r *http.Request) (domain.ID, error) {
	id, err := pathUUID(r)
	if err != nil {
		return domain.NilID, err
	}
	return h.boards.WorkspaceOfBoard(r.Context(), id)
}

func (h *boardHandler) columnWorkspace(r *http.Request) (domain.ID, error) {
	id, err := pathUUID(r)
	if err != nil {
		return domain.NilID, err
	}
	return h.boards.WorkspaceOfColumn(r.Context(), id)
}

// pathID is only called behind requireAccess, which already parsed {id}.
func pathID(r *http.Request) domain.ID {
	return domain.ID(uuid.MustParse(r.PathValue("id")))
}

type nameRequest struct {
	Name string `json:"name"`
}

// --- boards ---

func (h *boardHandler) listBoards(w http.ResponseWriter, r *http.Request) {
	boards, err := h.boards.ListBoards(r.Context(), access(r))
	if err != nil {
		writeBoardError(w, r, "list boards", err)
		return
	}
	out := make([]boardResponse, 0, len(boards))
	for _, b := range boards {
		out = append(out, newBoardResponse(b))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"boards": out})
}

// createBoard: POST /workspaces/{id}/boards {name, columns?}. The columns, if
// any, are created with the board, in that order.
func (h *boardHandler) createBoard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string   `json:"name"`
		Columns []string `json:"columns"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	b, err := h.boards.CreateBoard(r.Context(), access(r), req.Name, req.Columns)
	if err != nil {
		writeBoardError(w, r, "create board", err)
		return
	}
	WriteJSON(w, http.StatusCreated, newBoardResponse(b))
}

// getBoard: GET /boards/{id}[?include_archived=true]. Archived tasks are left
// out by default.
func (h *boardHandler) getBoard(w http.ResponseWriter, r *http.Request) {
	includeArchived := false
	if v := r.URL.Query().Get("include_archived"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			WriteValidationError(w, map[string]string{"include_archived": "include_archived debe ser true o false"})
			return
		}
		includeArchived = b
	}

	acc := access(r)
	detail, err := h.boards.GetBoardDetail(r.Context(), acc, pathID(r), includeArchived)
	if err != nil {
		writeBoardError(w, r, "get board", err)
		return
	}

	resp := boardDetailResponse{
		boardResponse: newBoardResponse(detail.Board),
		Role:          acc.Role,
		Labels:        make([]labelResponse, 0, len(detail.Labels)),
		Columns:       make([]columnWithTasksResponse, 0, len(detail.Columns)),
	}
	for _, l := range detail.Labels {
		resp.Labels = append(resp.Labels, newLabelResponse(l))
	}
	for _, c := range detail.Columns {
		col := columnWithTasksResponse{columnResponse: newColumnResponse(c.Column), Tasks: make([]taskResponse, 0, len(c.Tasks))}
		for _, t := range c.Tasks {
			col.Tasks = append(col.Tasks, newTaskResponse(t))
		}
		resp.Columns = append(resp.Columns, col)
	}
	WriteJSON(w, http.StatusOK, resp)
}

func (h *boardHandler) renameBoard(w http.ResponseWriter, r *http.Request) {
	var req nameRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	b, err := h.boards.RenameBoard(r.Context(), access(r), pathID(r), req.Name)
	if err != nil {
		writeBoardError(w, r, "rename board", err)
		return
	}
	WriteJSON(w, http.StatusOK, newBoardResponse(b))
}

func (h *boardHandler) deleteBoard(w http.ResponseWriter, r *http.Request) {
	if err := h.boards.DeleteBoard(r.Context(), access(r), pathID(r)); err != nil {
		writeBoardError(w, r, "delete board", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- columns ---

func (h *boardHandler) createColumn(w http.ResponseWriter, r *http.Request) {
	var req nameRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	col, err := h.boards.CreateColumn(r.Context(), access(r), pathID(r), req.Name)
	if err != nil {
		writeBoardError(w, r, "create column", err)
		return
	}
	WriteJSON(w, http.StatusCreated, newColumnResponse(col))
}

func (h *boardHandler) renameColumn(w http.ResponseWriter, r *http.Request) {
	var req nameRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	col, err := h.boards.RenameColumn(r.Context(), access(r), pathID(r), req.Name)
	if err != nil {
		writeBoardError(w, r, "rename column", err)
		return
	}
	WriteJSON(w, http.StatusOK, newColumnResponse(col))
}

func (h *boardHandler) deleteColumn(w http.ResponseWriter, r *http.Request) {
	if err := h.boards.DeleteColumn(r.Context(), access(r), pathID(r)); err != nil {
		writeBoardError(w, r, "delete column", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// optional distinguishes a missing key (Set false) from an explicit null
// (Set with a nil Value).
type optional[T any] struct {
	Set   bool
	Value *T
}

func (o *optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

// moveColumn: {"after_id": "<column id>"} places the column right after that
// one; {"after_id": null} places it first. The key is required so an empty
// body cannot silently move a column to the start.
func (h *boardHandler) moveColumn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AfterID optional[uuid.UUID] `json:"after_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !req.AfterID.Set {
		WriteValidationError(w, map[string]string{"after_id": "Indica after_id (null para mover al inicio)"})
		return
	}
	var afterID *domain.ID
	if req.AfterID.Value != nil {
		id := domain.ID(*req.AfterID.Value)
		afterID = &id
	}
	col, err := h.boards.MoveColumn(r.Context(), access(r), pathID(r), afterID)
	if err != nil {
		writeBoardError(w, r, "move column", err)
		return
	}
	WriteJSON(w, http.StatusOK, newColumnResponse(col))
}

func writeBoardError(w http.ResponseWriter, r *http.Request, op string, err error) {
	if valErr, ok := errors.AsType[*domain.ValidationError](err); ok {
		WriteValidationError(w, valErr.Fields)
		return
	}
	switch {
	case errors.Is(err, domain.ErrNotMember), errors.Is(err, domain.ErrForbidden):
		writeWorkspaceError(w, r, op, err)
	// Only reachable if the resource was deleted after authorization.
	case errors.Is(err, domain.ErrBoardNotFound):
		WriteError(w, http.StatusNotFound, CodeNotFound, "El tablero no existe")
	case errors.Is(err, domain.ErrColumnNotFound):
		WriteError(w, http.StatusNotFound, CodeNotFound, "La columna no existe")
	default:
		internalError(w, r, op, err)
	}
}
