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

	msgInvalidCredentials = "Correo o contraseña incorrectos" // #nosec G101 -- a message, not a credential
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
	auth       *auth.Service
	cookies    CookieOptions
	limits     *authLimiter // nil: no rate limits
	trustProxy bool
}

// allow takes one attempt of the caller's IP from l; if there is none left it
// answers 429 and returns false.
func (h *authHandler) allow(w http.ResponseWriter, r *http.Request, l func(*authLimiter) *limiter) bool {
	if h.limits == nil {
		return true
	}
	if ok, wait := l(h.limits).Take(limitKey(clientIP(r, h.trustProxy))); !ok {
		writeRateLimited(w, wait)
		return false
	}
	return true
}

func (h *authHandler) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !h.allow(w, r, func(l *authLimiter) *limiter { return l.registerIP }) || !decodeJSON(w, r, &req) {
		return
	}
	if h.limits != nil {
		if ok, wait := h.limits.registerEmail.Take(domain.NormalizeEmail(req.Email)); !ok {
			writeRateLimited(w, wait)
			return
		}
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
	if !h.allow(w, r, func(l *authLimiter) *limiter { return l.loginIP }) || !decodeJSON(w, r, &req) {
		return
	}
	// Per account, only failures count, and they are checked before trying:
	// a locked account gets 429 whether or not the password is right.
	email := domain.NormalizeEmail(req.Email)
	if h.limits != nil {
		if ok, wait := h.limits.loginEmail.Check(email); !ok {
			writeRateLimited(w, wait)
			return
		}
	}

	session, err := h.auth.Login(r.Context(), req.Email, req.Password)
	switch {
	case errors.Is(err, domain.ErrInvalidCredentials):
		if h.limits != nil {
			h.limits.loginEmail.Take(email)
		}
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
	h.setSessionCookies(w, s)
	WriteJSON(w, status, newSessionResponse(s))
}

func newSessionResponse(s auth.Session) sessionResponse {
	return sessionResponse{
		User:        newUserResponse(s.User),
		AccessToken: s.AccessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int(time.Until(s.AccessExpiresAt).Round(time.Second).Seconds()),
	}
}

// setSessionCookies sets the refresh and access cookies of a new session.
func (h *authHandler) setSessionCookies(w http.ResponseWriter, s auth.Session) {
	for _, c := range []struct {
		name, value, path string
		expires           time.Time
	}{
		{refreshCookieName, s.RefreshToken, refreshCookiePath, s.RefreshExpiresAt},
		{accessCookieName, s.AccessToken, accessCookiePath, s.AccessExpiresAt},
	} {
		cookie := h.cookies.cookie(c.name, c.value, c.path) // #nosec G124 -- HttpOnly always; Secure and SameSite from config (CookieOptions)
		cookie.Expires, cookie.MaxAge = c.expires, int(time.Until(c.expires).Seconds())
		http.SetCookie(w, cookie)
	}
	w.Header().Set("Cache-Control", "no-store")
}

func (h *authHandler) clearSessionCookies(w http.ResponseWriter) {
	for _, c := range []struct{ name, path string }{
		{refreshCookieName, refreshCookiePath},
		{accessCookieName, accessCookiePath},
	} {
		cookie := h.cookies.cookie(c.name, "", c.path) // #nosec G124 -- HttpOnly always; Secure and SameSite from config (CookieOptions)
		cookie.MaxAge = -1
		http.SetCookie(w, cookie)
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
