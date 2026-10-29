package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/app/workspace"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

type invitationResponse struct {
	ID        uuid.UUID   `json:"id"`
	Email     string      `json:"email"`
	Role      domain.Role `json:"role"`
	CreatedAt time.Time   `json:"created_at"`
	ExpiresAt time.Time   `json:"expires_at"`
}

func newInvitationResponse(i domain.Invitation) invitationResponse {
	return invitationResponse{ID: uuid.UUID(i.ID), Email: i.Email, Role: i.Role, CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt}
}

type inviteLinkResponse struct {
	Token     string      `json:"token"`
	Role      domain.Role `json:"role"`
	CreatedAt time.Time   `json:"created_at"`
	ExpiresAt time.Time   `json:"expires_at"`
}

func newInviteLinkResponse(l domain.InviteLink) inviteLinkResponse {
	return inviteLinkResponse{Token: l.Token, Role: l.Role, CreatedAt: l.CreatedAt, ExpiresAt: l.ExpiresAt}
}

type invitationHandler struct {
	inv *workspace.Invitations
}

// invite: POST /workspaces/{id}/invitations {emails, role}. Whoever has an
// account becomes a member now; the rest get a pending invitation.
func (h *invitationHandler) invite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Emails []string    `json:"emails"`
		Role   domain.Role `json:"role"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := h.inv.Invite(r.Context(), access(r), req.Emails, req.Role)
	if err != nil {
		writeWorkspaceError(w, r, "invite", err)
		return
	}
	added := make([]memberResponse, 0, len(res.Added))
	for _, m := range res.Added {
		added = append(added, newMemberResponse(m))
	}
	invited := make([]invitationResponse, 0, len(res.Invited))
	for _, i := range res.Invited {
		invited = append(invited, newInvitationResponse(i))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"added": added, "invited": invited, "already_members": res.AlreadyMembers})
}

func (h *invitationHandler) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.inv.List(r.Context(), access(r))
	if err != nil {
		writeWorkspaceError(w, r, "list invitations", err)
		return
	}
	out := make([]invitationResponse, 0, len(rows))
	for _, i := range rows {
		out = append(out, newInvitationResponse(i))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"invitations": out})
}

// invitationIDFromPath parses {invitationId}; the caller already manages the
// workspace's members, so a bad id is just an unknown invitation.
func invitationIDFromPath(w http.ResponseWriter, r *http.Request) (domain.ID, bool) {
	id, err := uuid.Parse(r.PathValue("invitationId"))
	if err != nil {
		WriteError(w, http.StatusNotFound, CodeNotFound, "La invitación no existe")
		return domain.NilID, false
	}
	return domain.ID(id), true
}

func (h *invitationHandler) renew(w http.ResponseWriter, r *http.Request) {
	id, ok := invitationIDFromPath(w, r)
	if !ok {
		return
	}
	inv, err := h.inv.Renew(r.Context(), access(r), id)
	if err != nil {
		writeWorkspaceError(w, r, "renew invitation", err)
		return
	}
	WriteJSON(w, http.StatusOK, newInvitationResponse(inv))
}

func (h *invitationHandler) cancel(w http.ResponseWriter, r *http.Request) {
	id, ok := invitationIDFromPath(w, r)
	if !ok {
		return
	}
	if err := h.inv.Cancel(r.Context(), access(r), id); err != nil {
		writeWorkspaceError(w, r, "cancel invitation", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getLink: GET /workspaces/{id}/invite-link, {"link": ...} or {"link": null}.
func (h *invitationHandler) getLink(w http.ResponseWriter, r *http.Request) {
	link, err := h.inv.Link(r.Context(), access(r))
	if err != nil {
		writeWorkspaceError(w, r, "get invite link", err)
		return
	}
	var out *inviteLinkResponse
	if link != nil {
		resp := newInviteLinkResponse(*link)
		out = &resp
	}
	WriteJSON(w, http.StatusOK, map[string]any{"link": out})
}

// createLink: POST /workspaces/{id}/invite-link {role}, a new link that
// replaces the previous one.
func (h *invitationHandler) createLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Role domain.Role `json:"role"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	link, err := h.inv.CreateLink(r.Context(), access(r), req.Role)
	if err != nil {
		writeWorkspaceError(w, r, "create invite link", err)
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"link": newInviteLinkResponse(link)})
}

func (h *invitationHandler) deleteLink(w http.ResponseWriter, r *http.Request) {
	if err := h.inv.DeleteLink(r.Context(), access(r)); err != nil {
		writeWorkspaceError(w, r, "delete invite link", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// linkInfo: GET /invite-links/{token}, where a link leads.
func (h *invitationHandler) linkInfo(w http.ResponseWriter, r *http.Request) {
	info, member, err := h.inv.LinkInfo(r.Context(), currentUser(r), r.PathValue("token"))
	if err != nil {
		writeWorkspaceError(w, r, "invite link info", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"workspace_id": uuid.UUID(info.WorkspaceID), "workspace_name": info.WorkspaceName,
		"role": info.Role, "expires_at": info.ExpiresAt, "already_member": member,
	})
}

// join: POST /invite-links/{token}/join. A member already keeps their role.
func (h *invitationHandler) join(w http.ResponseWriter, r *http.Request) {
	id, err := h.inv.Join(r.Context(), currentUser(r), r.PathValue("token"))
	if err != nil {
		writeWorkspaceError(w, r, "join workspace", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"workspace_id": uuid.UUID(id)})
}
