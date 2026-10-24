package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/db"
	"github.com/DarkApofis/tablero/backend/internal/domain"
	"github.com/DarkApofis/tablero/backend/internal/service"
)

// conflictResponse is the 409 body: the uniform error plus the task as it is
// now, so the client can reconcile without another request.
type conflictResponse struct {
	Error   errorDetail  `json:"error"`
	Current taskResponse `json:"current"`
}

type taskHandler struct {
	tasks *service.TaskService
}

func (h *taskHandler) taskWorkspace(r *http.Request) (domain.ID, error) {
	id, err := pathUUID(r)
	if err != nil {
		return domain.NilID, err
	}
	ws, err := h.tasks.WorkspaceOfTask(r.Context(), uuid.UUID(id))
	return domain.ID(ws), err
}

// create: POST /columns/{id}/tasks appends a task to the column.
func (h *taskHandler) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string           `json:"title"`
		Description string           `json:"description"`
		Priority    *db.TaskPriority `json:"priority"`
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
	userID := uuid.UUID(currentUser(r))

	task, err := h.tasks.Create(r.Context(), service.CreateTaskInput{
		ColumnID: uuid.UUID(pathID(r)), WorkspaceID: workspaceID(r),
		Title: req.Title, Description: req.Description, Priority: req.Priority,
		DueDate: dueDate, AssigneeID: req.AssigneeID, LabelIDs: req.LabelIDs, CreatedBy: userID,
	})
	if err != nil {
		writeTaskError(w, r, "create task", err)
		return
	}
	WriteJSON(w, http.StatusCreated, newTaskResponse(fromServiceTask(task)))
}

func (h *taskHandler) get(w http.ResponseWriter, r *http.Request) {
	task, err := h.tasks.Get(r.Context(), uuid.UUID(pathID(r)))
	if err != nil {
		writeTaskError(w, r, "get task", err)
		return
	}
	WriteJSON(w, http.StatusOK, newTaskResponse(fromServiceTask(task)))
}

// update: PATCH /tasks/{id} with any of the editable fields and "version".
// Omitted fields keep their value; null clears due_date and assignee_id.
func (h *taskHandler) update(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       *string             `json:"title"`
		Description *string             `json:"description"`
		Priority    *db.TaskPriority    `json:"priority"`
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
	dueDate := service.Optional[time.Time]{Set: req.DueDate.Set}
	if req.DueDate.Set {
		var ok bool
		if dueDate.Value, ok = parseDueDate(w, req.DueDate.Value); !ok {
			return
		}
	}

	actorID := uuid.UUID(currentUser(r))
	task, err := h.tasks.Update(r.Context(), uuid.UUID(pathID(r)), service.UpdateInput{
		Title: req.Title, Description: req.Description, Priority: req.Priority,
		DueDate: dueDate, AssigneeID: service.Optional[uuid.UUID](req.AssigneeID),
		LabelIDs: req.LabelIDs, Archived: req.Archived,
		WorkspaceID: workspaceID(r), Version: *req.Version, ActorID: actorID,
	})
	if err != nil {
		writeTaskError(w, r, "update task", err)
		return
	}
	WriteJSON(w, http.StatusOK, newTaskResponse(fromServiceTask(task)))
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

	actorID := uuid.UUID(currentUser(r))
	task, err := h.tasks.Move(r.Context(), uuid.UUID(pathID(r)), service.MoveInput{
		ColumnID: *req.ColumnID, AfterTaskID: req.AfterTaskID, BeforeTaskID: req.BeforeTaskID,
		Version: *req.Version, ActorID: actorID,
	})
	if err != nil {
		writeTaskError(w, r, "move task", err)
		return
	}
	WriteJSON(w, http.StatusOK, newTaskResponse(fromServiceTask(task)))
}

func (h *taskHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.tasks.Delete(r.Context(), uuid.UUID(pathID(r))); err != nil {
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
	d, err := time.Parse(service.DateLayout, *s)
	if err != nil {
		WriteValidationError(w, map[string]string{"due_date": "La fecha debe tener el formato AAAA-MM-DD"})
		return nil, false
	}
	return &d, true
}

func writeTaskError(w http.ResponseWriter, r *http.Request, op string, err error) {
	if conflict, ok := errors.AsType[*service.ConflictError](err); ok {
		detail := errorDetail{Code: CodeVersionConflict, Message: "La tarea cambió desde que la cargaste"}
		if conflict.Reason == service.ConflictNeighbors {
			detail = errorDetail{Code: CodeNeighborsChanged, Message: "Las tareas vecinas cambiaron de lugar; recarga la columna"}
		}
		WriteJSON(w, http.StatusConflict, conflictResponse{Error: detail, Current: newTaskResponse(fromServiceTask(conflict.Current))})
		return
	}
	switch {
	// Only reachable if the resource was deleted after authorization.
	case errors.Is(err, service.ErrTaskNotFound):
		WriteError(w, http.StatusNotFound, CodeNotFound, "La tarea no existe")
	case errors.Is(err, service.ErrColumnNotFound):
		WriteError(w, http.StatusNotFound, CodeNotFound, "La columna no existe")
	default:
		writeBoardError(w, r, op, err) // validation errors and 500s
	}
}

// fromServiceTask maps the task service's rows to the domain entity the
// responses are built from. Temporary: it goes away when the task use cases
// move to internal/app.
func fromServiceTask(t service.TaskDetail) domain.TaskDetail {
	opt := func(id uuid.NullUUID) *domain.ID {
		if !id.Valid {
			return nil
		}
		d := domain.ID(id.UUID)
		return &d
	}
	labels := make([]domain.Label, len(t.Labels))
	for i, l := range t.Labels {
		labels[i] = domain.Label{ID: domain.ID(l.ID), BoardID: domain.ID(l.BoardID), Name: l.Name, Color: l.Color, CreatedAt: l.CreatedAt}
	}
	return domain.TaskDetail{
		Task: domain.Task{
			ID: domain.ID(t.ID), ColumnID: domain.ID(t.ColumnID), Title: t.Title, Description: t.Description,
			Position: t.Position, Version: t.Version, Priority: domain.Priority(t.Priority),
			DueDate: t.DueDate, AssigneeID: opt(t.AssigneeID), ArchivedAt: t.ArchivedAt,
			CreatedBy: opt(t.CreatedBy), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		},
		AssigneeName: t.AssigneeName,
		Labels:       labels,
	}
}
