package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/adapters/token"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// fakeStore is an in-memory auth.UserRepository and auth.RefreshTokenRepository
// that mimics the database's behavior for the cases the handlers care about.
type fakeStore struct {
	users   map[domain.ID]domain.User
	refresh []savedRefresh
}

type savedRefresh struct {
	UserID    domain.ID
	Hash      []byte
	ExpiresAt time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{users: map[domain.ID]domain.User{}}
}

func (f *fakeStore) Create(_ context.Context, email, name, passwordHash string) (domain.User, error) {
	for _, u := range f.users {
		if u.Email == email {
			return domain.User{}, domain.ErrEmailTaken
		}
	}
	u := domain.User{ID: domain.NewID(), Email: email, PasswordHash: passwordHash, Name: name, CreatedAt: time.Now()}
	f.users[u.ID] = u
	return u, nil
}

func (f *fakeStore) ByEmail(_ context.Context, email string) (domain.User, error) {
	for _, u := range f.users {
		if u.Email == email {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrUserNotFound
}

func (f *fakeStore) ByID(_ context.Context, id domain.ID) (domain.User, error) {
	u, ok := f.users[id]
	if !ok {
		return domain.User{}, domain.ErrUserNotFound
	}
	return u, nil
}

func (f *fakeStore) Save(_ context.Context, userID domain.ID, hash []byte, expiresAt time.Time) error {
	f.refresh = append(f.refresh, savedRefresh{UserID: userID, Hash: hash, ExpiresAt: expiresAt})
	return nil
}

type authEnv struct {
	router http.Handler
	store  *fakeStore
	tokens *token.Issuer
}

func newAuthEnv() authEnv {
	store := newFakeStore()
	tokens := token.NewIssuer(testSecret, 15*time.Minute)
	return authEnv{
		store:  store,
		tokens: tokens,
		router: NewRouter(Deps{
			CookieSecure: true,
			Health:       fakePinger{},
			Auth:         newAuthService(store, tokens, 30*24*time.Hour),
		}),
	}
}
func (e authEnv) do(method, path, body, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e authEnv) register(t *testing.T, email, name, password string) sessionResponse {
	t.Helper()
	rec := e.do("POST", "/auth/register", jsonBody(map[string]string{
		"email": email, "name": name, "password": password,
	}), "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: status = %d, body = %s", rec.Code, rec.Body)
	}
	return decodeSession(t, rec)
}

func jsonBody(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func decodeSession(t *testing.T, rec *httptest.ResponseRecorder) sessionResponse {
	t.Helper()
	var s sessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&s); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	return s
}

func refreshCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == refreshCookieName {
			return c
		}
	}
	t.Fatal("refresh_token cookie not set")
	return nil
}

func TestRegister(t *testing.T) {
	e := newAuthEnv()
	rec := e.do("POST", "/auth/register",
		`{"email":"  Ana@Example.COM ","name":" Ana ","password":"supersecreta"}`, "")

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "argon2id") || strings.Contains(strings.ToLower(rec.Body.String()), "password") {
		t.Fatalf("response leaks password data: %s", rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	cookie := refreshCookie(t, rec)
	s := decodeSession(t, rec)

	t.Run("user is normalized", func(t *testing.T) {
		if s.User.Email != "ana@example.com" || s.User.Name != "Ana" {
			t.Errorf("user = %+v", s.User)
		}
	})

	t.Run("access token is a valid JWT for the user", func(t *testing.T) {
		if s.TokenType != "Bearer" || s.ExpiresIn != 900 {
			t.Errorf("token_type = %q, expires_in = %d", s.TokenType, s.ExpiresIn)
		}
		id, err := e.tokens.Verify(s.AccessToken)
		if err != nil || id != domain.ID(s.User.ID) {
			t.Errorf("Verify = %v, %v; want %v", id, err, s.User.ID)
		}
	})

	t.Run("refresh cookie is httpOnly and scoped", func(t *testing.T) {
		if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/auth" {
			t.Errorf("cookie = %+v", cookie)
		}
		if cookie.MaxAge <= 0 {
			t.Errorf("MaxAge = %d, want > 0", cookie.MaxAge)
		}
	})

	t.Run("only the refresh token hash is stored", func(t *testing.T) {
		if len(e.store.refresh) != 1 {
			t.Fatalf("stored refresh tokens = %d, want 1", len(e.store.refresh))
		}
		stored := e.store.refresh[0]
		if !bytes.Equal(stored.Hash, token.HashRefreshToken(cookie.Value)) || stored.UserID != domain.ID(s.User.ID) {
			t.Errorf("stored = %+v", stored)
		}
	})
}

func TestRegisterValidation(t *testing.T) {
	tests := []struct {
		name       string
		body       map[string]string
		wantFields []string
	}{
		{"invalid email", map[string]string{"email": "no-es-correo", "name": "Ana", "password": "supersecreta"}, []string{"email"}},
		{"email with display name", map[string]string{"email": "Ana <ana@example.com>", "name": "Ana", "password": "supersecreta"}, []string{"email"}},
		{"email without domain dot", map[string]string{"email": "ana@localhost", "name": "Ana", "password": "supersecreta"}, []string{"email"}},
		{"short password", map[string]string{"email": "ana@example.com", "name": "Ana", "password": "1234567"}, []string{"password"}},
		{"missing name", map[string]string{"email": "ana@example.com", "name": "  ", "password": "supersecreta"}, []string{"name"}},
		{"everything wrong", map[string]string{}, []string{"email", "name", "password"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAuthEnv()
			rec := e.do("POST", "/auth/register", jsonBody(tt.body), "")

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body)
			}
			got := decodeError(t, rec)
			if got.Code != CodeValidation || len(got.Fields) != len(tt.wantFields) {
				t.Fatalf("error = %+v, want fields %v", got, tt.wantFields)
			}
			for _, f := range tt.wantFields {
				if got.Fields[f] == "" {
					t.Errorf("missing message for field %q", f)
				}
			}
			if len(e.store.users) != 0 {
				t.Error("user was created despite validation errors")
			}
		})
	}
}

func TestRegisterPasswordLengthCountsCharacters(t *testing.T) {
	// 8 multi-byte characters must pass the 8-character minimum.
	e := newAuthEnv()
	e.register(t, "ana@example.com", "Ana", "ñññññññ€")
}

func TestRegisterDuplicateEmail(t *testing.T) {
	e := newAuthEnv()
	e.register(t, "ana@example.com", "Ana", "supersecreta")

	rec := e.do("POST", "/auth/register",
		jsonBody(map[string]string{"email": "ANA@example.com", "name": "Otra", "password": "otraclave1"}), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rec.Code, rec.Body)
	}
	if got := decodeError(t, rec); got.Code != CodeEmailTaken {
		t.Errorf("error = %+v", got)
	}
}

func TestAuthRejectsMalformedJSON(t *testing.T) {
	for _, path := range []string{"/auth/register", "/auth/login"} {
		t.Run(path, func(t *testing.T) {
			rec := newAuthEnv().do("POST", path, `{"email":`, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if got := decodeError(t, rec); got.Code != CodeInvalidJSON {
				t.Errorf("error = %+v", got)
			}
		})
	}
}

func TestLogin(t *testing.T) {
	e := newAuthEnv()
	registered := e.register(t, "ana@example.com", "Ana", "supersecreta")

	rec := e.do("POST", "/auth/login", `{"email":" ANA@example.com","password":"supersecreta"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body)
	}
	refreshCookie(t, rec)
	s := decodeSession(t, rec)
	if s.User.ID != registered.User.ID {
		t.Errorf("logged in as %v, want %v", s.User.ID, registered.User.ID)
	}
	if id, err := e.tokens.Verify(s.AccessToken); err != nil || id != domain.ID(registered.User.ID) {
		t.Errorf("access token: %v, %v", id, err)
	}
}

func TestLoginInvalidCredentialsAreIndistinguishable(t *testing.T) {
	e := newAuthEnv()
	e.register(t, "ana@example.com", "Ana", "supersecreta")

	cases := map[string]string{
		"wrong password":  `{"email":"ana@example.com","password":"incorrecta"}`,
		"unknown email":   `{"email":"nadie@example.com","password":"supersecreta"}`,
		"malformed email": `{"email":"no-es-correo","password":"supersecreta"}`,
		"empty fields":    `{}`,
	}

	var firstBody string
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := e.do("POST", "/auth/login", body, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			for _, c := range rec.Result().Cookies() {
				if c.Name == refreshCookieName {
					t.Error("refresh cookie set on failed login")
				}
			}

			raw := rec.Body.String()
			if firstBody == "" {
				firstBody = raw
			} else if raw != firstBody {
				t.Errorf("body differs between cases:\n%s\n%s", raw, firstBody)
			}
			if got := decodeError(t, rec); got.Code != CodeInvalidCreds || got.Message != "Correo o contraseña incorrectos" {
				t.Errorf("error = %+v", got)
			}
		})
	}
}

func TestMe(t *testing.T) {
	e := newAuthEnv()
	s := e.register(t, "ana@example.com", "Ana", "supersecreta")

	t.Run("valid token returns the user", func(t *testing.T) {
		rec := e.do("GET", "/me", "", s.AccessToken)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body)
		}
		var u userResponse
		if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
			t.Fatal(err)
		}
		if u.ID != s.User.ID || u.Email != "ana@example.com" || u.Name != "Ana" {
			t.Errorf("user = %+v", u)
		}
	})

	expired, _, _ := token.NewIssuer(testSecret, -time.Minute).Issue(domain.ID(s.User.ID))
	forged, _, _ := token.NewIssuer([]byte("another-secret-another-secret-123"), time.Minute).Issue(domain.ID(s.User.ID))
	unknownUser, _, _ := e.tokens.Issue(domain.NewID())

	unauthorized := map[string]*http.Request{
		"no header":         httptest.NewRequest("GET", "/me", nil),
		"not bearer":        withHeader("Authorization", "Basic YW5hOnB3"),
		"empty bearer":      withHeader("Authorization", "Bearer "),
		"garbage token":     withHeader("Authorization", "Bearer abc.def.ghi"),
		"expired token":     withHeader("Authorization", "Bearer "+expired),
		"wrong signature":   withHeader("Authorization", "Bearer "+forged),
		"user no longer ex": withHeader("Authorization", "Bearer "+unknownUser),
	}
	for name, req := range unauthorized {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			e.router.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body = %s", rec.Code, rec.Body)
			}
			if got := decodeError(t, rec); got.Code != CodeUnauthorized {
				t.Errorf("error = %+v", got)
			}
		})
	}
}

func TestRequireAuthPutsUserInContext(t *testing.T) {
	tokens := token.NewIssuer(testSecret, time.Minute)
	want := domain.NewID()
	access, _, _ := tokens.Issue(want)

	var got domain.ID
	h := requireAuth(newAuthService(newFakeStore(), tokens, time.Hour), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = currentUser(r)
	}))
	req := withHeader("Authorization", "bearer "+access) // scheme is case-insensitive
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != want {
		t.Errorf("user id in context = %v, want %v", got, want)
	}
}

func withHeader(key, value string) *http.Request {
	req := httptest.NewRequest("GET", "/me", nil)
	req.Header.Set(key, value)
	return req
}
