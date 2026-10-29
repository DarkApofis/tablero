package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
	"github.com/DarkApofis/tablero/backend/internal/app/workspace"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// onboardingRouter is the fixture's API with new accounts joining wsID.
func (f *taskFixture) onboardingRouter(wsID uuid.UUID) http.Handler {
	deps := f.deps
	deps.Auth = deps.Auth.WithOnboarding(postgres.NewTxManager(f.pool),
		workspace.NewDefaultMembership(postgres.NewWorkspaces(f.pool), domain.ID(wsID)))
	return NewRouter(deps)
}

func call(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNewAccountsJoinTheDefaultWorkspaceAsMembers(t *testing.T) {
	f := newTaskFixture(t)
	h := f.onboardingRouter(f.ws)

	rec := call(h, "POST", "/auth/register", `{"email":"nueva@example.com","name":"Nueva","password":"supersecreta"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	var session struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&session)
	token := session.AccessToken

	var list struct {
		Workspaces []struct {
			ID   uuid.UUID
			Role string
		}
	}
	_ = json.NewDecoder(call(h, "GET", "/workspaces", "", token).Body).Decode(&list)
	if len(list.Workspaces) != 1 || list.Workspaces[0].ID != f.ws || list.Workspaces[0].Role != "member" {
		t.Fatalf("workspaces = %+v, want the default one as member", list.Workspaces)
	}

	// Tasks: create, edit, move and delete.
	rec = call(h, "POST", "/columns/"+f.todo.String()+"/tasks", `{"title":"De la nueva"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task: %d %s", rec.Code, rec.Body)
	}
	created := decodeTask(t, rec)
	if rec := call(h, "PATCH", "/tasks/"+created.ID.String(), `{"title":"Editada","version":1}`, token); rec.Code != http.StatusOK {
		t.Errorf("edit task: %d %s", rec.Code, rec.Body)
	}
	if rec := call(h, "PATCH", "/tasks/"+created.ID.String()+"/move", `{"columnId":"`+f.done.String()+`","version":2}`, token); rec.Code != http.StatusOK {
		t.Errorf("move task: %d %s", rec.Code, rec.Body)
	}
	if rec := call(h, "DELETE", "/tasks/"+created.ID.String(), "", token); rec.Code != http.StatusNoContent {
		t.Errorf("delete task: %d %s", rec.Code, rec.Body)
	}

	// Not the workspace's structure, members or settings.
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/workspaces/" + f.ws.String() + "/boards", `{"name":"Mío"}`},
		{"DELETE", "/boards/" + f.board.String(), ""},
		{"POST", "/boards/" + f.board.String() + "/columns", `{"name":"Mía"}`},
		{"DELETE", "/columns/" + f.todo.String(), ""},
		{"PATCH", "/workspaces/" + f.ws.String() + "/members/" + f.ana.ID.String(), `{"role":"viewer"}`},
		{"DELETE", "/workspaces/" + f.ws.String() + "/members/" + f.ana.ID.String(), ""},
	} {
		if rec := call(h, tc.method, tc.path, tc.body, token); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
	// There is no way to delete a workspace at all: no such route.
	if rec := call(h, "DELETE", "/workspaces/"+f.ws.String(), "", token); rec.Code != http.StatusNotFound {
		t.Errorf("DELETE /workspaces/{id}: %d, want 404 (no such operation)", rec.Code)
	}
	var exists bool
	if err := f.pool.QueryRow(context.Background(), "SELECT EXISTS (SELECT 1 FROM workspaces WHERE id = $1)", f.ws).Scan(&exists); err != nil || !exists {
		t.Errorf("the workspace is gone (%v)", err)
	}
}

// If joining fails, the registration rolls back: no account without its workspace.
func TestRegistrationRollsBackIfOnboardingFails(t *testing.T) {
	f := newTaskFixture(t)
	h := f.onboardingRouter(uuid.New()) // no such workspace

	rec := call(h, "POST", "/auth/register", `{"email":"huerfana@example.com","name":"Huérfana","password":"supersecreta"}`, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	var n int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM users WHERE email = 'huerfana@example.com'").Scan(&n); err != nil || n != 0 {
		t.Errorf("users left behind: %d (%v)", n, err)
	}
}
