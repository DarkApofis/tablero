package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/app/auth"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

const (
	refreshCookieName = "refresh_token"
	// The refresh cookie is only sent to /auth/* (refresh and logout live there).
	refreshCookiePath = "/auth"
	// The access token also travels in a cookie for EventSource, which cannot
	// send headers. Only the board event stream accepts it (requireStreamAuth):
	// every other route needs the Authorization header, so the cookie cannot
	// be used for cross-site requests that change something.
	accessCookieName = "access_token"
	accessCookiePath = "/boards"

	msgInvalidCredentials = "Correo o contraseña incorrectos"
	msgInvalidSession     = "Sesión inválida o expirada"
)

// userResponse is the public shape of a user; never expose password_hash.
type userResponse struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func newUserResponse(u domain.User) userResponse {
	return userResponse{ID: uuid.UUID(u.ID), Email: u.Email, Name: u.Name, CreatedAt: u.CreatedAt}
}

type sessionResponse struct {
	User        userResponse `json:"user"`
	AccessToken string       `json:"access_token"`
	TokenType   string       `json:"token_type"`
	ExpiresIn   int          `json:"expires_in"` // seconds
}

type authHandler struct {
	auth         *auth.Service
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

	session, err := h.auth.Register(r.Context(), req.Email, req.Name, req.Password)
	if valErr, ok := errors.AsType[*domain.ValidationError](err); ok {
		WriteValidationError(w, valErr.Fields)
		return
	}
	switch {
	case errors.Is(err, domain.ErrEmailTaken):
		WriteError(w, http.StatusConflict, CodeEmailTaken, "Ya existe una cuenta con ese correo")
		return
	case err != nil:
		internalError(w, r, "register", err)
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
	case errors.Is(err, domain.ErrInvalidCredentials):
		WriteError(w, http.StatusUnauthorized, CodeInvalidCreds, msgInvalidCredentials)
		return
	case err != nil:
		internalError(w, r, "login", err)
		return
	}
	h.writeSession(w, http.StatusOK, session)
}

// refresh trades the refresh_token cookie for a new session, rotating it.
func (h *authHandler) refresh(w http.ResponseWriter, r *http.Request) {
	var plain string
	if c, err := r.Cookie(refreshCookieName); err == nil {
		plain = c.Value
	}
	session, err := h.auth.Refresh(r.Context(), plain)
	switch {
	case errors.Is(err, domain.ErrInvalidRefreshToken):
		if errors.Is(err, domain.ErrRefreshTokenReused) {
			slog.Warn("refresh token reused; revoked all the user's sessions", "request_id", RequestID(r.Context()))
		}
		h.clearSessionCookies(w)
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, msgInvalidSession)
		return
	case err != nil:
		internalError(w, r, "refresh", err)
		return
	}
	h.writeSession(w, http.StatusOK, session)
}

// logout revokes the refresh_token cookie and clears both session cookies.
// It needs no access token, so an expired session can still end cleanly.
func (h *authHandler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(refreshCookieName); err == nil {
		if err := h.auth.Logout(r.Context(), c.Value); err != nil {
			internalError(w, r, "logout", err)
			return
		}
	}
	h.clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *authHandler) me(w http.ResponseWriter, r *http.Request) {
	user, err := h.auth.Me(r.Context(), currentUser(r))
	switch {
	case errors.Is(err, domain.ErrUserNotFound):
		// Valid token for a user that no longer exists.
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "No autenticado")
		return
	case err != nil:
		internalError(w, r, "me", err)
		return
	}
	WriteJSON(w, http.StatusOK, newUserResponse(user))
}

func (h *authHandler) writeSession(w http.ResponseWriter, status int, s auth.Session) {
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
	http.SetCookie(w, &http.Cookie{
		Name:     accessCookieName,
		Value:    s.AccessToken,
		Path:     accessCookiePath,
		Expires:  s.AccessExpiresAt,
		MaxAge:   int(time.Until(s.AccessExpiresAt).Seconds()),
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

func (h *authHandler) clearSessionCookies(w http.ResponseWriter) {
	for _, c := range []struct{ name, path string }{
		{refreshCookieName, refreshCookiePath},
		{accessCookieName, accessCookiePath},
	} {
		http.SetCookie(w, &http.Cookie{
			Name: c.name, Path: c.path, MaxAge: -1,
			HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode,
		})
	}
	w.Header().Set("Cache-Control", "no-store")
}

// Authenticator turns a bearer token into a user id; *auth.Service is one.
type Authenticator interface {
	Authenticate(token string) (domain.ID, error)
}

// requireAuth rejects requests without a valid "Authorization: Bearer <jwt>"
// and stores the user id in the request context for the next handler.
func requireAuth(authn Authenticator, next http.Handler) http.Handler {
	return authenticate(authn, bearerToken, next)
}

// requireStreamAuth is requireAuth for the event stream: it also takes the
// token from the access_token cookie, since EventSource cannot set headers.
func requireStreamAuth(authn Authenticator, next http.Handler) http.Handler {
	return authenticate(authn, func(r *http.Request) (string, bool) {
		if c, err := r.Cookie(accessCookieName); err == nil && c.Value != "" {
			return c.Value, true
		}
		return bearerToken(r)
	}, next)
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	return token, ok && strings.EqualFold(scheme, "Bearer") && token != ""
}

func authenticate(authn Authenticator, tokenOf func(*http.Request) (string, bool), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := tokenOf(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer`)
			WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "No autenticado")
			return
		}

		userID, err := authn.Authenticate(token)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			WriteError(w, http.StatusUnauthorized, CodeUnauthorized, msgInvalidSession)
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, userID)))
	})
}

type userKey struct{}

// currentUser is the authenticated user; only called behind requireAuth.
func currentUser(r *http.Request) domain.ID {
	id, _ := r.Context().Value(userKey{}).(domain.ID)
	return id
}
