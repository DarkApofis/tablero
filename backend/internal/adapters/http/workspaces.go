package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/app/workspace"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

const (
	// Same reply for "not a member", "workspace does not exist" and "bad id",
	// so outsiders cannot probe which workspaces exist.
	msgNoWorkspaceAccess = "No tienes acceso a este workspace"
	msgRoleNotAllowed    = "Tu rol no permite esta acción"
)

type workspaceResponse struct {
	ID          uuid.UUID   `json:"id"`
	Name        string      `json:"name"`
	CreatedAt   time.Time   `json:"created_at"`
	Role        domain.Role `json:"role"`
	MemberCount int64       `json:"member_count"`
}

func newWorkspaceResponse(w domain.WorkspaceSummary) workspaceResponse {
	return workspaceResponse{ID: uuid.UUID(w.ID), Name: w.Name, CreatedAt: w.CreatedAt, Role: w.Role, MemberCount: w.MemberCount}
}

type memberResponse struct {
	UserID   uuid.UUID   `json:"user_id"`
	Email    string      `json:"email"`
	Name     string      `json:"name"`
	Role     domain.Role `json:"role"`
	JoinedAt time.Time   `json:"joined_at"`
}

func newMemberResponse(m domain.Member) memberResponse {
	return memberResponse{UserID: uuid.UUID(m.UserID), Email: m.Email, Name: m.Name, Role: m.Role, JoinedAt: m.JoinedAt}
}

type workspaceHandler struct {
	ws *workspace.Service
}

func (h *workspaceHandler) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ws, err := h.ws.Create(r.Context(), currentUser(r), req.Name)
	if err != nil {
		writeWorkspaceError(w, r, "create workspace", err)
		return
	}
	WriteJSON(w, http.StatusCreated, newWorkspaceResponse(ws))
}

func (h *workspaceHandler) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.ws.ListForUser(r.Context(), currentUser(r))
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
	members, err := h.ws.ListMembers(r.Context(), access(r))
	if err != nil {
		writeWorkspaceError(w, r, "list members", err)
		return
	}
	out := make([]memberResponse, 0, len(members))
	for _, m := range members {
		out = append(out, newMemberResponse(m))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (h *workspaceHandler) changeRole(w http.ResponseWriter, r *http.Request) {
	targetID, ok := memberIDFromPath(w, r)
	if !ok {
		return
	}
	var req struct {
		Role domain.Role `json:"role"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	m, err := h.ws.ChangeRole(r.Context(), access(r), targetID, req.Role)
	if err != nil {
		writeWorkspaceError(w, r, "change role", err)
		return
	}
	WriteJSON(w, http.StatusOK, newMemberResponse(m))
}

func (h *workspaceHandler) removeMember(w http.ResponseWriter, r *http.Request) {
	targetID, ok := memberIDFromPath(w, r)
	if !ok {
		return
	}
	if err := h.ws.RemoveMember(r.Context(), access(r), targetID); err != nil {
		writeWorkspaceError(w, r, "remove member", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// workspaceResolver finds the workspace a request targets: from the path, or
// through the board, column, task or label it names. A missing resource must
// be reported as domain.ErrNotMember so it is indistinguishable from a
// forbidden one.
type workspaceResolver func(r *http.Request) (domain.ID, error)

var errBadPathID = errors.New("invalid id in path")

// pathUUID parses the {id} path segment.
func pathUUID(r *http.Request) (domain.ID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return domain.NilID, errBadPathID
	}
	return domain.ID(id), nil
}

// workspaceFromPath is the resolver for /workspaces/{id}/... routes.
func workspaceFromPath(r *http.Request) (domain.ID, error) { return pathUUID(r) }

// requireAccess resolves the target workspace, has the authorizer check the
// caller's role for action and stores the resulting domain.Access in the
// context for the handler, which hands it to the use case. It must run after
// requireAuth. Every route under a workspace goes through it, before the
// body is read, so outsiders learn nothing from validation errors.
func requireAccess(authz *workspace.Authorizer, action domain.Action, resolve workspaceResolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsID, err := resolve(r)
		if errors.Is(err, errBadPathID) || errors.Is(err, domain.ErrNotMember) {
			WriteError(w, http.StatusForbidden, CodeForbidden, msgNoWorkspaceAccess)
			return
		}
		if err != nil {
			internalError(w, r, "resolve workspace", err)
			return
		}

		acc, err := authz.Authorize(r.Context(), currentUser(r), wsID, action)
		if err != nil {
			writeWorkspaceError(w, r, "authorize workspace", err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accessKey{}, acc)))
	})
}

type accessKey struct{}

// access is the caller's Access to the request's workspace; only called
// behind requireAccess.
func access(r *http.Request) domain.Access {
	acc, _ := r.Context().Value(accessKey{}).(domain.Access)
	return acc
}

// workspaceID is the workspace the request was authorized against.
func workspaceID(r *http.Request) uuid.UUID {
	return uuid.UUID(access(r).WorkspaceID)
}

// memberIDFromPath parses {userId}. The caller is already an admin of the
// workspace here, so a bad id can honestly be reported as an unknown member.
func memberIDFromPath(w http.ResponseWriter, r *http.Request) (domain.ID, bool) {
	id, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		WriteError(w, http.StatusNotFound, CodeMemberNotFound, "Ese usuario no es miembro del workspace")
		return domain.NilID, false
	}
	return domain.ID(id), true
}

func writeWorkspaceError(w http.ResponseWriter, r *http.Request, op string, err error) {
	if valErr, ok := errors.AsType[*domain.ValidationError](err); ok {
		WriteValidationError(w, valErr.Fields)
		return
	}
	switch {
	case errors.Is(err, domain.ErrNotMember):
		WriteError(w, http.StatusForbidden, CodeForbidden, msgNoWorkspaceAccess)
	case errors.Is(err, domain.ErrForbidden):
		WriteError(w, http.StatusForbidden, CodeForbidden, msgRoleNotAllowed)
	case errors.Is(err, domain.ErrMemberNotFound):
		WriteError(w, http.StatusNotFound, CodeMemberNotFound, "Ese usuario no es miembro del workspace")
	case errors.Is(err, domain.ErrLastAdmin):
		WriteError(w, http.StatusConflict, CodeLastAdmin, "El workspace debe tener al menos un admin")
	default:
		internalError(w, r, op, err)
	}
}
