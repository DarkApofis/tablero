package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/auth"
	"github.com/DarkApofis/tablero/backend/internal/db"
	"github.com/DarkApofis/tablero/backend/internal/service"
)

const (
	// Same reply for "not a member", "workspace does not exist" and "bad id",
	// so outsiders cannot probe which workspaces exist.
	msgNoWorkspaceAccess = "No tienes acceso a este workspace"
	msgRoleNotAllowed    = "Tu rol no permite esta acción"
)

type workspaceResponse struct {
	ID          uuid.UUID        `json:"id"`
	Name        string           `json:"name"`
	CreatedAt   time.Time        `json:"created_at"`
	Role        db.WorkspaceRole `json:"role"`
	MemberCount int64            `json:"member_count"`
}

func newWorkspaceResponse(w db.ListWorkspacesForUserRow) workspaceResponse {
	return workspaceResponse{ID: w.ID, Name: w.Name, CreatedAt: w.CreatedAt, Role: w.Role, MemberCount: w.MemberCount}
}

type memberResponse struct {
	UserID   uuid.UUID        `json:"user_id"`
	Email    string           `json:"email"`
	Name     string           `json:"name"`
	Role     db.WorkspaceRole `json:"role"`
	JoinedAt time.Time        `json:"joined_at"`
}

type workspaceHandler struct {
	ws *service.WorkspaceService
}

func (h *workspaceHandler) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	userID, _ := auth.UserID(r.Context())

	ws, err := h.ws.Create(r.Context(), userID, req.Name)
	if err != nil {
		writeWorkspaceError(w, r, "create workspace", err)
		return
	}
	WriteJSON(w, http.StatusCreated, newWorkspaceResponse(ws))
}

func (h *workspaceHandler) list(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserID(r.Context())

	rows, err := h.ws.ListForUser(r.Context(), userID)
	if err != nil {
		internalError(w, r, "list workspaces", err)
		return
	}
	out := make([]workspaceResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, newWorkspaceResponse(row))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"workspaces": out})
}

func (h *workspaceHandler) listMembers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.ws.ListMembers(r.Context(), workspaceID(r))
	if err != nil {
		internalError(w, r, "list members", err)
		return
	}
	out := make([]memberResponse, 0, len(rows))
	for _, m := range rows {
		out = append(out, memberResponse{UserID: m.UserID, Email: m.Email, Name: m.Name, Role: m.Role, JoinedAt: m.CreatedAt})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (h *workspaceHandler) changeRole(w http.ResponseWriter, r *http.Request) {
	targetID, ok := memberIDFromPath(w, r)
	if !ok {
		return
	}
	var req struct {
		Role db.WorkspaceRole `json:"role"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	actorID, _ := auth.UserID(r.Context())

	m, err := h.ws.ChangeRole(r.Context(), workspaceID(r), actorID, targetID, req.Role)
	if err != nil {
		writeWorkspaceError(w, r, "change role", err)
		return
	}
	WriteJSON(w, http.StatusOK, memberResponse{UserID: m.UserID, Email: m.Email, Name: m.Name, Role: m.Role, JoinedAt: m.CreatedAt})
}

func (h *workspaceHandler) removeMember(w http.ResponseWriter, r *http.Request) {
	targetID, ok := memberIDFromPath(w, r)
	if !ok {
		return
	}
	actorID, _ := auth.UserID(r.Context())

	if err := h.ws.RemoveMember(r.Context(), workspaceID(r), actorID, targetID); err != nil {
		writeWorkspaceError(w, r, "remove member", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireWorkspace authorizes action on the {id} workspace of the path and
// stores the caller's role in the context. It must run after requireAuth.
func requireWorkspace(ws *service.WorkspaceService, action auth.Action, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			WriteError(w, http.StatusForbidden, CodeForbidden, msgNoWorkspaceAccess)
			return
		}
		userID, _ := auth.UserID(r.Context())

		role, err := ws.Authorize(r.Context(), id, userID, action)
		if err != nil {
			writeWorkspaceError(w, r, "authorize workspace", err)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithWorkspaceRole(r.Context(), role)))
	})
}

// workspaceID is only called behind requireWorkspace, which validated it.
func workspaceID(r *http.Request) uuid.UUID {
	return uuid.MustParse(r.PathValue("id"))
}

// memberIDFromPath parses {userId}. The caller is already an admin of the
// workspace here, so a bad id can honestly be reported as an unknown member.
func memberIDFromPath(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		WriteError(w, http.StatusNotFound, CodeMemberNotFound, "Ese usuario no es miembro del workspace")
		return uuid.Nil, false
	}
	return id, true
}

func writeWorkspaceError(w http.ResponseWriter, r *http.Request, op string, err error) {
	if valErr, ok := errors.AsType[*service.ValidationError](err); ok {
		WriteValidationError(w, valErr.Fields)
		return
	}
	switch {
	case errors.Is(err, service.ErrNotMember):
		WriteError(w, http.StatusForbidden, CodeForbidden, msgNoWorkspaceAccess)
	case errors.Is(err, service.ErrForbidden):
		WriteError(w, http.StatusForbidden, CodeForbidden, msgRoleNotAllowed)
	case errors.Is(err, service.ErrMemberNotFound):
		WriteError(w, http.StatusNotFound, CodeMemberNotFound, "Ese usuario no es miembro del workspace")
	case errors.Is(err, service.ErrLastAdmin):
		WriteError(w, http.StatusConflict, CodeLastAdmin, "El workspace debe tener al menos un admin")
	default:
		internalError(w, r, op, err)
	}
}
