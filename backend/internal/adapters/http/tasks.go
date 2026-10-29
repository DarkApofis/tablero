package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/app/task"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// conflictResponse is the 409 body: the uniform error plus the task as it is
// now, so the client can reconcile without another request.
type conflictResponse struct {
	Error   errorDetail  `json:"error"`
	Current taskResponse `json:"current"`
}

type taskHandler struct {
	tasks *task.Service
}

func (h *taskHandler) taskWorkspace(r *http.Request) (domain.Location, error) {
	id, err := pathUUID(r)
	if err != nil {
		return domain.Location{}, err
	}
	return h.tasks.LocationOfTask(r.Context(), id)
}

// create: POST /columns/{id}/tasks appends a task to the column.
func (h *taskHandler) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string           `json:"title"`
		Description string           `json:"description"`
		Priority    *domain.Priority `json:"priority"`
		DueDate     *string          `json:"due_date"`
		AssigneeID  *uuid.UUID       `json:"assignee_id"`
		LabelIDs    []uuid.UUID      `json:"label_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	dueDate, ok := parseDueDate(w, req.DueDate)
	if !ok {
		return
	}

	t, err := h.tasks.Create(r.Context(), access(r), task.CreateInput{
		ColumnID: pathID(r), Title: req.Title, Description: req.Description, Priority: req.Priority,
		DueDate: dueDate, AssigneeID: idPtr(req.AssigneeID), LabelIDs: ids(req.LabelIDs),
	})
	if err != nil {
		writeTaskError(w, r, "create task", err)
		return
	}
	WriteJSON(w, http.StatusCreated, newTaskResponse(t))
}

func (h *taskHandler) get(w http.ResponseWriter, r *http.Request) {
	t, err := h.tasks.Get(r.Context(), access(r), pathID(r))
	if err != nil {
		writeTaskError(w, r, "get task", err)
		return
	}
	WriteJSON(w, http.StatusOK, newTaskResponse(t))
}

// update: PATCH /tasks/{id} with any of the editable fields and "version".
// Omitted fields keep their value; null clears due_date and assignee_id.
func (h *taskHandler) update(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       *string             `json:"title"`
		Description *string             `json:"description"`
		Priority    *domain.Priority    `json:"priority"`
		DueDate     optional[string]    `json:"due_date"`
		AssigneeID  optional[uuid.UUID] `json:"assignee_id"`
		LabelIDs    *[]uuid.UUID        `json:"label_ids"`
		Archived    *bool               `json:"archived"`
		Version     *int32              `json:"version"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !requireVersion(w, req.Version) {
		return
	}
	edit := domain.TaskEdit{
		Title: req.Title, Description: req.Description, Priority: req.Priority, Archived: req.Archived,
		DueDate:    domain.Optional[time.Time]{Set: req.DueDate.Set},
		AssigneeID: domain.Optional[domain.ID]{Set: req.AssigneeID.Set, Value: idPtr(req.AssigneeID.Value)},
	}
	if req.DueDate.Set {
		var ok bool
		if edit.DueDate.Value, ok = parseDueDate(w, req.DueDate.Value); !ok {
			return
		}
	}
	if req.LabelIDs != nil {
		labels := ids(*req.LabelIDs)
		edit.LabelIDs = &labels
	}

	t, err := h.tasks.Update(r.Context(), access(r), pathID(r), *req.Version, edit)
	if err != nil {
		writeTaskError(w, r, "update task", err)
		return
	}
	WriteJSON(w, http.StatusOK, newTaskResponse(t))
}

// move: PATCH /tasks/{id}/move with {"columnId", "afterTaskId"?, "beforeTaskId"?, "version"}.
func (h *taskHandler) move(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ColumnID     *uuid.UUID `json:"columnId"`
		AfterTaskID  *uuid.UUID `json:"afterTaskId"`
		BeforeTaskID *uuid.UUID `json:"beforeTaskId"`
		Version      *int32     `json:"version"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ColumnID == nil {
		WriteValidationError(w, map[string]string{"columnId": "La columna destino es obligatoria"})
		return
	}
	if !requireVersion(w, req.Version) {
		return
	}

	t, err := h.tasks.Move(r.Context(), access(r), pathID(r), task.MoveInput{
		ColumnID: domain.ID(*req.ColumnID), AfterTaskID: idPtr(req.AfterTaskID), BeforeTaskID: idPtr(req.BeforeTaskID),
		Version: *req.Version,
	})
	if err != nil {
		writeTaskError(w, r, "move task", err)
		return
	}
	WriteJSON(w, http.StatusOK, newTaskResponse(t))
}

func (h *taskHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.tasks.Delete(r.Context(), access(r), pathID(r)); err != nil {
		writeTaskError(w, r, "delete task", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func requireVersion(w http.ResponseWriter, v *int32) bool {
	if v == nil || *v < 1 {
		WriteValidationError(w, map[string]string{"version": "Indica la versión de la tarea que estás editando"})
		return false
	}
	return true
}

// parseDueDate parses a YYYY-MM-DD date; nil stays nil. On failure it has
// already written a 422 and returns false.
func parseDueDate(w http.ResponseWriter, s *string) (*time.Time, bool) {
	if s == nil {
		return nil, true
	}
	d, err := time.Parse(domain.DateLayout, *s)
	if err != nil {
		WriteValidationError(w, map[string]string{"due_date": "La fecha debe tener el formato AAAA-MM-DD"})
		return nil, false
	}
	return &d, true
}

// writeTaskError maps errors.Is(err, domain.ErrConflict) to 409 with the
// task's current state, then defers to the board errors.
func writeTaskError(w http.ResponseWriter, r *http.Request, op string, err error) {
	if conflict, ok := errors.AsType[*domain.ConflictError](err); ok {
		detail := errorDetail{Code: CodeVersionConflict, Message: "La tarea cambió desde que la cargaste"}
		if conflict.Reason == domain.ConflictNeighbors {
			detail = errorDetail{Code: CodeNeighborsChanged, Message: "Las tareas vecinas cambiaron de lugar; recarga la columna"}
		}
		WriteJSON(w, http.StatusConflict, conflictResponse{Error: detail, Current: newTaskResponse(conflict.Current)})
		return
	}
	switch {
	// Only reachable if the resource was deleted after authorization.
	case errors.Is(err, domain.ErrTaskNotFound):
		WriteError(w, http.StatusNotFound, CodeNotFound, "La tarea no existe")
	case errors.Is(err, domain.ErrColumnNotFound):
		WriteError(w, http.StatusNotFound, CodeNotFound, "La columna no existe")
	default:
		writeBoardError(w, r, op, err) // validation, authorization and 500s
	}
}

func idPtr(id *uuid.UUID) *domain.ID {
	if id == nil {
		return nil
	}
	d := domain.ID(*id)
	return &d
}

func ids(in []uuid.UUID) []domain.ID {
	out := make([]domain.ID, len(in))
	for i, id := range in {
		out[i] = domain.ID(id)
	}
	return out
}
