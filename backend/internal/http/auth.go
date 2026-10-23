package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/auth"
	"github.com/DarkApofis/tablero/backend/internal/db"
	"github.com/DarkApofis/tablero/backend/internal/service"
)

const (
	refreshCookieName = "refresh_token"
	// The refresh cookie is only sent to /auth/* (refresh and logout live there).
	refreshCookiePath = "/auth"

	msgInvalidCredentials = "Correo o contraseña incorrectos"
)

// userResponse is the public shape of a user; never expose password_hash.
type userResponse struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func newUserResponse(u db.User) userResponse {
	return userResponse{ID: u.ID, Email: u.Email, Name: u.Name, CreatedAt: u.CreatedAt}
}

type sessionResponse struct {
	User        userResponse `json:"user"`
	AccessToken string       `json:"access_token"`
	TokenType   string       `json:"token_type"`
	ExpiresIn   int          `json:"expires_in"` // seconds
}

type authHandler struct {
	auth         *service.AuthService
	cookieSecure bool
}

func (h *authHandler) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	session, err := h.auth.Register(r.Context(), service.RegisterInput{
		Email: req.Email, Name: req.Name, Password: req.Password,
	})
	if valErr, ok := errors.AsType[*service.ValidationError](err); ok {
		WriteValidationError(w, valErr.Fields)
		return
	}
	switch {
	case errors.Is(err, service.ErrEmailTaken):
		WriteError(w, http.StatusConflict, CodeEmailTaken, "Ya existe una cuenta con ese correo")
		return
	case err != nil:
		h.internalError(w, r, "register", err)
		return
	}
	h.writeSession(w, http.StatusCreated, session)
}

func (h *authHandler) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	session, err := h.auth.Login(r.Context(), req.Email, req.Password)
	switch {
	case errors.Is(err, service.ErrInvalidCredentials):
		WriteError(w, http.StatusUnauthorized, CodeInvalidCreds, msgInvalidCredentials)
		return
	case err != nil:
		h.internalError(w, r, "login", err)
		return
	}
	h.writeSession(w, http.StatusOK, session)
}

func (h *authHandler) me(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.UserID(r.Context())
	user, err := h.auth.Me(r.Context(), id)
	switch {
	case errors.Is(err, service.ErrUserNotFound):
		// Valid token for a user that no longer exists.
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "No autenticado")
		return
	case err != nil:
		h.internalError(w, r, "me", err)
		return
	}
	WriteJSON(w, http.StatusOK, newUserResponse(user))
}

func (h *authHandler) writeSession(w http.ResponseWriter, status int, s service.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    s.RefreshToken,
		Path:     refreshCookiePath,
		Expires:  s.RefreshExpiresAt,
		MaxAge:   int(time.Until(s.RefreshExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, status, sessionResponse{
		User:        newUserResponse(s.User),
		AccessToken: s.AccessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int(time.Until(s.AccessExpiresAt).Round(time.Second).Seconds()),
	})
}

func (h *authHandler) internalError(w http.ResponseWriter, r *http.Request, op string, err error) {
	slog.Error(op+" failed", "err", err, "request_id", RequestID(r.Context()))
	WriteError(w, http.StatusInternalServerError, CodeInternal, "internal server error")
}

// requireAuth rejects requests without a valid "Authorization: Bearer <jwt>"
// and stores the user id in the request context for the next handler.
func requireAuth(tokens *auth.TokenIssuer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
			w.Header().Set("WWW-Authenticate", `Bearer`)
			WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "No autenticado")
			return
		}

		userID, err := tokens.Verify(token)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "Sesión inválida o expirada")
			return
		}

		next.ServeHTTP(w, r.WithContext(auth.WithUserID(r.Context(), userID)))
	})
}
