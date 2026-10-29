package httpapi

import (
	"context"
	"errors"
	"fmt"
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
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	// Slug is the address; null for a workspace that has none yet.
	Slug        *string     `json:"slug"`
	CreatedAt   time.Time   `json:"created_at"`
	Role        domain.Role `json:"role"`
	MemberCount int64       `json:"member_count"`
}

func newWorkspaceResponse(w domain.WorkspaceSummary) workspaceResponse {
	resp := workspaceResponse{ID: uuid.UUID(w.ID), Name: w.Name, CreatedAt: w.CreatedAt, Role: w.Role, MemberCount: w.MemberCount}
	if w.Slug != "" {
		resp.Slug = &w.Slug
	}
	return resp
}

type workspaceDetailResponse struct {
	workspaceResponse
	Description string        `json:"description"`
	DefaultRole domain.Role   `json:"default_role"`
	OwnerID     uuid.NullUUID `json:"owner_id"`
	// IsOwner: the caller owns it. MayTransfer: the caller may hand it over.
	IsOwner     bool  `json:"is_owner"`
	MayTransfer bool  `json:"may_transfer"`
	BoardCount  int64 `json:"board_count"`
	TaskCount   int64 `json:"task_count"`
	// IsDefault: every new account joins it, and it cannot be deleted.
	IsDefault bool `json:"is_default"`
}

func newWorkspaceDetailResponse(d domain.WorkspaceDetail, userID domain.ID) workspaceDetailResponse {
	return workspaceDetailResponse{
		workspaceResponse: newWorkspaceResponse(d.WorkspaceSummary),
		Description:       d.Description, DefaultRole: d.DefaultRole, OwnerID: nullUUID(d.OwnerID),
		IsOwner: d.IsOwner(userID), MayTransfer: d.Role == domain.RoleAdmin && d.MayTransfer(userID),
		BoardCount: d.BoardCount, TaskCount: d.TaskCount, IsDefault: d.IsDefault,
	}
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

// create: POST /workspaces {name, slug?}. Without a slug the workspace gets
// one from its name.
func (h *workspaceHandler) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ws, err := h.ws.Create(r.Context(), currentUser(r), req.Name, req.Slug)
	if err != nil {
		writeWorkspaceError(w, r, "create workspace", err)
		return
	}
	WriteJSON(w, http.StatusCreated, newWorkspaceResponse(ws))
}

// list: GET /workspaces, the caller's workspaces, plus how many they created
// out of the most they may (so a client knows whether to offer creating one).
func (h *workspaceHandler) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.ws.ListForUser(r.Context(), currentUser(r))
	if err != nil {
		internalError(w, r, "list workspaces", err)
		return
	}
	created, err := h.ws.CreatedCount(r.Context(), currentUser(r))
	if err != nil {
		internalError(w, r, "count created workspaces", err)
		return
	}
	out := make([]workspaceResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, newWorkspaceResponse(row))
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"workspaces": out, "created": created, "max_created": domain.MaxCreatedWorkspaces,
	})
}

// slugAvailability: GET /workspace-slugs?slug=x tells whether
// an address is free, or ?name=x suggests a free one for that name.
func (h *workspaceHandler) slugAvailability(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if name := q.Get("name"); name != "" && q.Get("slug") == "" {
		slug, err := h.ws.SuggestSlug(r.Context(), name)
		if err != nil && !errors.Is(err, domain.ErrSlugTaken) {
			internalError(w, r, "suggest slug", err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"slug": slug, "available": err == nil})
		return
	}
	slug, available, err := h.ws.SlugAvailable(r.Context(), q.Get("slug"))
	if err != nil {
		writeWorkspaceError(w, r, "check slug", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"slug": slug, "available": available})
}

// bySlug: GET /workspace-slugs/{slug}, the workspace with that address,
// for its members; to anyone else it is like an address nobody has.
func (h *workspaceHandler) bySlug(w http.ResponseWriter, r *http.Request) {
	id, err := h.ws.IDBySlug(r.Context(), currentUser(r), r.PathValue("slug"))
	if err != nil {
		writeWorkspaceError(w, r, "find workspace by slug", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"id": uuid.UUID(id)})
}

func (h *workspaceHandler) get(w http.ResponseWriter, r *http.Request) {
	d, err := h.ws.Get(r.Context(), access(r))
	if err != nil {
		writeWorkspaceError(w, r, "get workspace", err)
		return
	}
	WriteJSON(w, http.StatusOK, newWorkspaceDetailResponse(d, currentUser(r)))
}

// update: PATCH /workspaces/{id} {name?, slug?, description?, default_role?}.
func (h *workspaceHandler) update(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        *string      `json:"name"`
		Slug        *string      `json:"slug"`
		Description *string      `json:"description"`
		DefaultRole *domain.Role `json:"default_role"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	d, err := h.ws.Update(r.Context(), access(r), domain.WorkspaceChanges{
		Name: req.Name, Slug: req.Slug, Description: req.Description, DefaultRole: req.DefaultRole,
	})
	if err != nil {
		writeWorkspaceError(w, r, "update workspace", err)
		return
	}
	WriteJSON(w, http.StatusOK, newWorkspaceDetailResponse(d, currentUser(r)))
}

func (h *workspaceHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.ws.Delete(r.Context(), access(r)); err != nil {
		writeWorkspaceError(w, r, "delete workspace", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// transfer: POST /workspaces/{id}/transfer {user_id}, to another admin.
func (h *workspaceHandler) transfer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID uuid.UUID `json:"user_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	d, err := h.ws.Transfer(r.Context(), access(r), domain.ID(req.UserID))
	if err != nil {
		writeWorkspaceError(w, r, "transfer workspace", err)
		return
	}
	WriteJSON(w, http.StatusOK, newWorkspaceDetailResponse(d, currentUser(r)))
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

// workspaceResolver finds the workspace a request targets, and the board if
// it targets something on one: from the path, or through the board, column,
// task or label it names. A missing resource must be reported as
// domain.ErrNotMember so it is indistinguishable from a forbidden one.
type workspaceResolver func(r *http.Request) (domain.Location, error)

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
func workspaceFromPath(r *http.Request) (domain.Location, error) {
	id, err := pathUUID(r)
	return domain.Location{WorkspaceID: id}, err
}

// requireAccess resolves the target workspace, has the authorizer check the
// caller's role for action and stores the resulting domain.Access in the
// context for the handler, which hands it to the use case. It must run after
// requireAuth. Every route under a workspace goes through it, before the
// body is read, so outsiders learn nothing from validation errors.
func requireAccess(authz *workspace.Authorizer, action domain.Action, resolve workspaceResolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loc, err := resolve(r)
		if errors.Is(err, errBadPathID) || errors.Is(err, domain.ErrNotMember) {
			WriteError(w, http.StatusForbidden, CodeForbidden, msgNoWorkspaceAccess)
			return
		}
		if err != nil {
			internalError(w, r, "resolve workspace", err)
			return
		}

		acc, err := authz.AuthorizeAt(r.Context(), currentUser(r), loc, action)
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
	case errors.Is(err, domain.ErrWorkspaceLimit):
		WriteError(w, http.StatusConflict, CodeWorkspaceLimit,
			fmt.Sprintf("Ya creaste %d workspaces, el máximo por persona", domain.MaxCreatedWorkspaces))
	case errors.Is(err, domain.ErrSlugTaken):
		WriteError(w, http.StatusConflict, CodeSlugTaken, "Esa dirección ya la usa otro workspace")
	case errors.Is(err, domain.ErrOwnerStaysAdmin):
		WriteError(w, http.StatusConflict, CodeOwnerStaysAdmin,
			"El propietario sigue siendo admin: antes tiene que transferir el workspace a otro admin")
	case errors.Is(err, domain.ErrNotOwner):
		WriteError(w, http.StatusForbidden, CodeForbidden, "Solo el propietario puede transferir el workspace")
	case errors.Is(err, domain.ErrDefaultWorkspace):
		WriteError(w, http.StatusConflict, CodeDefaultWorkspace, "El workspace por defecto no se puede eliminar")
	case errors.Is(err, domain.ErrBoardArchived):
		WriteError(w, http.StatusConflict, CodeBoardArchived, "El tablero está archivado: restáuralo para editarlo")
	case errors.Is(err, domain.ErrInvitationNotFound):
		WriteError(w, http.StatusNotFound, CodeNotFound, "La invitación no existe")
	case errors.Is(err, domain.ErrInviteLinkInvalid):
		WriteError(w, http.StatusNotFound, CodeInviteInvalid, "Este enlace de invitación no es válido o ha caducado")
	default:
		internalError(w, r, op, err)
	}
}
