package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/auth"
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

func (h *taskHandler) taskWorkspace(r *http.Request) (uuid.UUID, error) {
	id, err := pathUUID(r)
	if err != nil {
		return uuid.Nil, err
	}
	return h.tasks.WorkspaceOfTask(r.Context(), id)
}

// create: POST /columns/{id}/tasks appends a task to the column.
func (h *taskHandler) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	userID, _ := auth.UserID(r.Context())

	task, err := h.tasks.Create(r.Context(), service.CreateTaskInput{
		ColumnID: pathID(r), Title: req.Title, Description: req.Description, CreatedBy: userID,
	})
	if err != nil {
		writeTaskError(w, r, "create task", err)
		return
	}
	WriteJSON(w, http.StatusCreated, newTaskResponse(task))
}

func (h *taskHandler) get(w http.ResponseWriter, r *http.Request) {
	task, err := h.tasks.Get(r.Context(), pathID(r))
	if err != nil {
		writeTaskError(w, r, "get task", err)
		return
	}
	WriteJSON(w, http.StatusOK, newTaskResponse(task))
}

// update: PATCH /tasks/{id} with {"title"?, "description"?, "version"}.
func (h *taskHandler) update(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Version     *int32  `json:"version"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !requireVersion(w, req.Version) {
		return
	}

	actorID, _ := auth.UserID(r.Context())
	task, err := h.tasks.Update(r.Context(), pathID(r), service.UpdateInput{
		Title: req.Title, Description: req.Description, Version: *req.Version, ActorID: actorID,
	})
	if err != nil {
		writeTaskError(w, r, "update task", err)
		return
	}
	WriteJSON(w, http.StatusOK, newTaskResponse(task))
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

	actorID, _ := auth.UserID(r.Context())
	task, err := h.tasks.Move(r.Context(), pathID(r), service.MoveInput{
		ColumnID: *req.ColumnID, AfterTaskID: req.AfterTaskID, BeforeTaskID: req.BeforeTaskID,
		Version: *req.Version, ActorID: actorID,
	})
	if err != nil {
		writeTaskError(w, r, "move task", err)
		return
	}
	WriteJSON(w, http.StatusOK, newTaskResponse(task))
}

func (h *taskHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.tasks.Delete(r.Context(), pathID(r)); err != nil {
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

func writeTaskError(w http.ResponseWriter, r *http.Request, op string, err error) {
	if conflict, ok := errors.AsType[*service.ConflictError](err); ok {
		detail := errorDetail{Code: CodeVersionConflict, Message: "La tarea cambió desde que la cargaste"}
		if conflict.Reason == service.ConflictNeighbors {
			detail = errorDetail{Code: CodeNeighborsChanged, Message: "Las tareas vecinas cambiaron de lugar; recarga la columna"}
		}
		WriteJSON(w, http.StatusConflict, conflictResponse{Error: detail, Current: newTaskResponse(conflict.Current)})
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
