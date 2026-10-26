package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/app/auth"
	"github.com/DarkApofis/tablero/backend/internal/demo"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

type fakeGuests struct {
	session demo.GuestSession
	err     error
}

func (f fakeGuests) StartGuest(context.Context) (demo.GuestSession, error) { return f.session, f.err }

func demoRouter(guests GuestStarter) http.Handler {
	return NewRouter(Deps{CORSOrigins: []string{"http://localhost:3000"}, Ready: fakePinger{}, Guests: guests})
}

func TestDemoOff(t *testing.T) {
	router := demoRouter(nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/demo", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"guest_access\":false}\n" {
		t.Errorf("GET /demo = %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("POST", "/auth/guest", nil))
	if rec.Code != http.StatusNotFound || decodeError(t, rec).Code != CodeNotFound {
		t.Errorf("POST /auth/guest = %d %s", rec.Code, rec.Body)
	}
}

func TestStartGuest(t *testing.T) {
	session := demo.GuestSession{
		Session: auth.Session{
			User:        domain.User{ID: domain.NewID(), Email: "invitado-1@invitados.estudionube.example", Name: "Invitado 1A2B"},
			AccessToken: "access", AccessExpiresAt: time.Now().Add(15 * time.Minute),
			RefreshToken: "refresh", RefreshExpiresAt: time.Now().Add(time.Hour),
		},
		WorkspaceID: domain.NewID(),
		BoardID:     domain.NewID(),
	}
	router := demoRouter(fakeGuests{session: session})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/demo", nil))
	if rec.Body.String() != "{\"guest_access\":true}\n" {
		t.Errorf("GET /demo = %s", rec.Body)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("POST", "/auth/guest", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	var body struct {
		User        struct{ Name string }
		AccessToken string `json:"access_token"`
		BoardID     string `json:"board_id"`
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.AccessToken != "access" || body.User.Name != "Invitado 1A2B" ||
		body.BoardID != session.BoardID.String() || body.WorkspaceID != session.WorkspaceID.String() {
		t.Errorf("body = %+v", body)
	}
	cookies := map[string]string{}
	for _, c := range rec.Result().Cookies() {
		cookies[c.Name] = c.Value
	}
	if cookies[refreshCookieName] != "refresh" || cookies[accessCookieName] != "access" {
		t.Errorf("cookies = %v", cookies)
	}
}

func TestStartGuestErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{demo.ErrGuestLimit, http.StatusTooManyRequests, CodeGuestLimit},
		{demo.ErrNotSeeded, http.StatusServiceUnavailable, CodeDemoUnavailable},
		{errors.New("boom"), http.StatusInternalServerError, CodeInternal},
	} {
		rec := httptest.NewRecorder()
		demoRouter(fakeGuests{err: tc.err}).ServeHTTP(rec, httptest.NewRequest("POST", "/auth/guest", nil))
		if rec.Code != tc.status || decodeError(t, rec).Code != tc.code {
			t.Errorf("%v: %d %s", tc.err, rec.Code, rec.Body)
		}
	}
}
