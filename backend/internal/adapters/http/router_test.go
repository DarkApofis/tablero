package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/adapters/token"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func newTestRouter(pingErr error) http.Handler {
	return NewRouter(Deps{
		CORSOrigins: []string{"http://localhost:3000"},
		Health:      fakePinger{err: pingErr},
		Auth:        newAuthService(newFakeStore(), token.NewIssuer(testSecret, 15*time.Minute), time.Hour),
	})
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorDetail {
	t.Helper()
	var body errorBody
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return body.Error
}

func TestHealth(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		rec := httptest.NewRecorder()
		newTestRouter(nil).ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Body.String(); got != "{\"status\":\"ok\"}\n" {
			t.Errorf("body = %q", got)
		}
		if rec.Header().Get(requestIDHeader) == "" {
			t.Error("missing X-Request-ID response header")
		}
	})

	t.Run("db down returns 503", func(t *testing.T) {
		rec := httptest.NewRecorder()
		newTestRouter(errors.New("boom")).ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
		if e := decodeError(t, rec); e.Code != CodeDBUnavailable || e.Message == "" {
			t.Errorf("error = %+v", e)
		}
	})
}

func TestNotFoundIsUniformJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(nil).ServeHTTP(rec, httptest.NewRequest("GET", "/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if e := decodeError(t, rec); e.Code != CodeNotFound {
		t.Errorf("error = %+v", e)
	}
}

func TestWrongMethodIsUniformJSON405(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(nil).ServeHTTP(rec, httptest.NewRequest("POST", "/health", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow == "" {
		t.Error("missing Allow header")
	}
	if e := decodeError(t, rec); e.Code != CodeMethodNotAllowed {
		t.Errorf("error = %+v", e)
	}
}

func TestRequestIDIsPropagated(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	req.Header.Set(requestIDHeader, "abc123")
	rec := httptest.NewRecorder()
	newTestRouter(nil).ServeHTTP(rec, req)

	if got := rec.Header().Get(requestIDHeader); got != "abc123" {
		t.Errorf("X-Request-ID = %q, want abc123", got)
	}
}

func TestRecoverReturns500(t *testing.T) {
	h := withRecover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("kaboom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if e := decodeError(t, rec); e.Code != CodeInternal {
		t.Errorf("error = %+v", e)
	}
}
