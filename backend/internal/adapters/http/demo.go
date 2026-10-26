package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/demo"
)

// GuestStarter creates a demo guest account and signs it in; *demo.Service is one.
type GuestStarter interface {
	StartGuest(ctx context.Context) (demo.GuestSession, error)
}

type demoHandler struct {
	guests   GuestStarter // nil: demo mode is off
	sessions *authHandler
}

// info tells the login screen whether to offer "Entrar como invitado".
func (h *demoHandler) info(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]bool{"guest_access": h.guests != nil})
}

type guestSessionResponse struct {
	sessionResponse
	WorkspaceID uuid.UUID `json:"workspace_id"`
	BoardID     uuid.UUID `json:"board_id"`
}

// startGuest signs in a new guest, member of the demo workspace, like a login
// (same cookies and body) plus the demo board to open.
func (h *demoHandler) startGuest(w http.ResponseWriter, r *http.Request) {
	if h.guests == nil {
		WriteError(w, http.StatusNotFound, CodeNotFound, "El modo demo no está activo")
		return
	}
	g, err := h.guests.StartGuest(r.Context())
	switch {
	case errors.Is(err, demo.ErrGuestLimit):
		WriteError(w, http.StatusTooManyRequests, CodeGuestLimit,
			"Hay muchos invitados ahora mismo. Prueba en un rato o entra con uno de los usuarios de demo.")
		return
	case errors.Is(err, demo.ErrNotSeeded):
		WriteError(w, http.StatusServiceUnavailable, CodeDemoUnavailable, "La demo se está preparando. Inténtalo en un minuto.")
		return
	case err != nil:
		internalError(w, r, "start guest", err)
		return
	}
	h.sessions.setSessionCookies(w, g.Session)
	WriteJSON(w, http.StatusCreated, guestSessionResponse{
		sessionResponse: newSessionResponse(g.Session),
		WorkspaceID:     uuid.UUID(g.WorkspaceID),
		BoardID:         uuid.UUID(g.BoardID),
	})
}
