package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/DarkApofis/tablero/backend/internal/auth"
	"github.com/DarkApofis/tablero/backend/internal/db"
	"github.com/DarkApofis/tablero/backend/internal/service"
)

// fakeWorkspaceStore is an in-memory service.WorkspaceStore. InTx runs fn
// directly: the service does all its checks before writing, so a failed
// operation leaves nothing to roll back.
type fakeWorkspaceStore struct {
	users      *fakeStore
	workspaces map[uuid.UUID]db.Workspace
	members    []db.WorkspaceMember // insertion order = join order
}

func newFakeWorkspaceStore(users *fakeStore) *fakeWorkspaceStore {
	return &fakeWorkspaceStore{users: users, workspaces: map[uuid.UUID]db.Workspace{}}
}

func (f *fakeWorkspaceStore) InTx(_ context.Context, fn func(q service.WorkspaceQueries) error) error {
	return fn(f)
}

func (f *fakeWorkspaceStore) CreateWorkspace(_ context.Context, name string) (db.Workspace, error) {
	ws := db.Workspace{ID: uuid.New(), Name: name, CreatedAt: time.Now()}
	f.workspaces[ws.ID] = ws
	return ws, nil
}

func (f *fakeWorkspaceStore) AddWorkspaceMember(_ context.Context, p db.AddWorkspaceMemberParams) error {
	f.members = append(f.members, db.WorkspaceMember{WorkspaceID: p.WorkspaceID, UserID: p.UserID, Role: p.Role, CreatedAt: time.Now()})
	return nil
}

func (f *fakeWorkspaceStore) ListWorkspacesForUser(_ context.Context, userID uuid.UUID) ([]db.ListWorkspacesForUserRow, error) {
	var rows []db.ListWorkspacesForUserRow
	for _, m := range f.members {
		if m.UserID != userID {
			continue
		}
		ws := f.workspaces[m.WorkspaceID]
		rows = append(rows, db.ListWorkspacesForUserRow{
			ID: ws.ID, Name: ws.Name, CreatedAt: ws.CreatedAt, Role: m.Role, MemberCount: f.count(ws.ID, ""),
		})
	}
	return rows, nil
}

func (f *fakeWorkspaceStore) GetMemberRole(_ context.Context, p db.GetMemberRoleParams) (db.WorkspaceRole, error) {
	i := f.find(p.WorkspaceID, p.UserID)
	if i < 0 {
		return "", pgx.ErrNoRows
	}
	return f.members[i].Role, nil
}

func (f *fakeWorkspaceStore) ListWorkspaceMembers(_ context.Context, wsID uuid.UUID) ([]db.ListWorkspaceMembersRow, error) {
	var rows []db.ListWorkspaceMembersRow
	for _, m := range f.members {
		if m.WorkspaceID == wsID {
			u := f.users.users[m.UserID]
			rows = append(rows, db.ListWorkspaceMembersRow{UserID: m.UserID, Email: u.Email, Name: u.Name, Role: m.Role, CreatedAt: m.CreatedAt})
		}
	}
	return rows, nil
}

func (f *fakeWorkspaceStore) GetWorkspaceMember(_ context.Context, p db.GetWorkspaceMemberParams) (db.GetWorkspaceMemberRow, error) {
	i := f.find(p.WorkspaceID, p.UserID)
	if i < 0 {
		return db.GetWorkspaceMemberRow{}, pgx.ErrNoRows
	}
	m, u := f.members[i], f.users.users[p.UserID]
	return db.GetWorkspaceMemberRow{UserID: m.UserID, Email: u.Email, Name: u.Name, Role: m.Role, CreatedAt: m.CreatedAt}, nil
}

func (f *fakeWorkspaceStore) LockWorkspace(context.Context, uuid.UUID) error { return nil }

func (f *fakeWorkspaceStore) CountWorkspaceAdmins(_ context.Context, wsID uuid.UUID) (int64, error) {
	return f.count(wsID, db.WorkspaceRoleAdmin), nil
}

func (f *fakeWorkspaceStore) UpdateMemberRole(_ context.Context, p db.UpdateMemberRoleParams) (int64, error) {
	i := f.find(p.WorkspaceID, p.UserID)
	if i < 0 {
		return 0, nil
	}
	f.members[i].Role = p.Role
	return 1, nil
}

func (f *fakeWorkspaceStore) DeleteWorkspaceMember(_ context.Context, p db.DeleteWorkspaceMemberParams) (int64, error) {
	i := f.find(p.WorkspaceID, p.UserID)
	if i < 0 {
		return 0, nil
	}
	f.members = slices.Delete(f.members, i, i+1)
	return 1, nil
}

// The fake has no tasks to unassign.
func (f *fakeWorkspaceStore) UnassignMemberTasks(context.Context, db.UnassignMemberTasksParams) (int64, error) {
	return 0, nil
}

func (f *fakeWorkspaceStore) find(wsID, userID uuid.UUID) int {
	return slices.IndexFunc(f.members, func(m db.WorkspaceMember) bool {
		return m.WorkspaceID == wsID && m.UserID == userID
	})
}

func (f *fakeWorkspaceStore) count(wsID uuid.UUID, role db.WorkspaceRole) int64 {
	var n int64
	for _, m := range f.members {
		if m.WorkspaceID == wsID && (role == "" || m.Role == role) {
			n++
		}
	}
	return n
}

// --- test environment ---

type wsEnv struct {
	t      *testing.T
	router http.Handler
	tokens *auth.TokenIssuer
	users  *fakeStore
	store  *fakeWorkspaceStore
	svc    *service.WorkspaceService
}

func newWSEnv(t *testing.T) *wsEnv {
	users := newFakeStore()
	store := newFakeWorkspaceStore(users)
	tokens := auth.NewTokenIssuer(testSecret, 15*time.Minute)
	svc := service.NewWorkspaceService(store)
	return &wsEnv{
		t: t, tokens: tokens, users: users, store: store, svc: svc,
		router: NewRouter(Deps{
			Health:     service.NewHealthService(fakePinger{}),
			Auth:       service.NewAuthService(users, tokens, time.Hour),
			Tokens:     tokens,
			Workspaces: svc,
		}),
	}
}

type testUser struct {
	ID    uuid.UUID
	Token string
}

// user creates a user directly in the store (no password hashing) and a token for it.
func (e *wsEnv) user(name string) testUser {
	e.t.Helper()
	u, err := e.users.CreateUser(context.Background(), db.CreateUserParams{
		Email: strings.ToLower(name) + "@example.com", Name: name, PasswordHash: "x",
	})
	if err != nil {
		e.t.Fatal(err)
	}
	token, _, err := e.tokens.Issue(u.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return testUser{ID: u.ID, Token: token}
}

func (e *wsEnv) workspace(members map[testUser]db.WorkspaceRole) uuid.UUID {
	e.t.Helper()
	ws, _ := e.store.CreateWorkspace(context.Background(), "Equipo")
	for u, role := range members {
		_ = e.store.AddWorkspaceMember(context.Background(), db.AddWorkspaceMemberParams{WorkspaceID: ws.ID, UserID: u.ID, Role: role})
	}
	return ws.ID
}

func (e *wsEnv) do(method, path, body string, as testUser) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if as.Token != "" {
		req.Header.Set("Authorization", "Bearer "+as.Token)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *wsEnv) role(wsID uuid.UUID, u testUser) db.WorkspaceRole {
	role, err := e.store.GetMemberRole(context.Background(), db.GetMemberRoleParams{WorkspaceID: wsID, UserID: u.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	return role
}

func membersPath(wsID uuid.UUID) string { return "/workspaces/" + wsID.String() + "/members" }
func memberPath(wsID, userID uuid.UUID) string {
	return membersPath(wsID) + "/" + userID.String()
}

// --- permission matrix over HTTP ---

func TestWorkspacePermissionMatrix(t *testing.T) {
	type expectation struct {
		status  int
		message string // for 403s: which of the two messages
	}
	ok := func(status int) expectation { return expectation{status: status} }
	noAccess := expectation{http.StatusForbidden, msgNoWorkspaceAccess}
	roleDenied := expectation{http.StatusForbidden, msgRoleNotAllowed}

	endpoints := []struct {
		name   string
		method string
		path   func(ws, target uuid.UUID) string
		body   string
		want   map[string]expectation // by actor
	}{
		{
			name: "list members", method: "GET",
			path: func(ws, _ uuid.UUID) string { return membersPath(ws) },
			want: map[string]expectation{"admin": ok(200), "member": ok(200), "viewer": ok(200), "outsider": noAccess},
		},
		{
			name: "change role", method: "PATCH", body: `{"role":"viewer"}`,
			path: memberPath,
			want: map[string]expectation{"admin": ok(200), "member": roleDenied, "viewer": roleDenied, "outsider": noAccess},
		},
		{
			name: "remove member", method: "DELETE",
			path: memberPath,
			want: map[string]expectation{"admin": ok(204), "member": roleDenied, "viewer": roleDenied, "outsider": noAccess},
		},
	}

	for _, ep := range endpoints {
		for _, actorName := range []string{"admin", "member", "viewer", "outsider"} {
			t.Run(ep.name+"/"+actorName, func(t *testing.T) {
				e := newWSEnv(t)
				actors := map[string]testUser{
					"admin": e.user("Admin"), "member": e.user("Member"),
					"viewer": e.user("Viewer"), "outsider": e.user("Outsider"),
				}
				target := e.user("Target")
				ws := e.workspace(map[testUser]db.WorkspaceRole{
					actors["admin"]: db.WorkspaceRoleAdmin, actors["member"]: db.WorkspaceRoleMember,
					actors["viewer"]: db.WorkspaceRoleViewer, target: db.WorkspaceRoleMember,
				})

				rec := e.do(ep.method, ep.path(ws, target.ID), ep.body, actors[actorName])

				want := ep.want[actorName]
				if rec.Code != want.status {
					t.Fatalf("status = %d, want %d; body = %s", rec.Code, want.status, rec.Body)
				}
				if want.status == http.StatusForbidden {
					if got := decodeError(t, rec); got.Code != CodeForbidden || got.Message != want.message {
						t.Errorf("error = %+v, want message %q", got, want.message)
					}
					if e.role(ws, target) != db.WorkspaceRoleMember {
						t.Error("a forbidden request changed the target's membership")
					}
				}
			})
		}
	}
}

func TestWorkspaceRoutesRequireAuth(t *testing.T) {
	e := newWSEnv(t)
	ws, target := uuid.New(), uuid.New()
	for _, r := range []struct{ method, path string }{
		{"POST", "/workspaces"}, {"GET", "/workspaces"},
		{"GET", membersPath(ws)}, {"PATCH", memberPath(ws, target)}, {"DELETE", memberPath(ws, target)},
	} {
		if rec := e.do(r.method, r.path, `{}`, testUser{}); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401", r.method, r.path, rec.Code)
		}
	}
}

// A non-member must not be able to tell an existing workspace from a missing one.
func TestNonMemberCannotProbeWorkspaceExistence(t *testing.T) {
	e := newWSEnv(t)
	admin, outsider := e.user("Admin"), e.user("Outsider")
	existing := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin})

	for _, ep := range []struct {
		method, body string
		path         func(ws string) string
	}{
		{"GET", "", func(ws string) string { return "/workspaces/" + ws + "/members" }},
		{"PATCH", `{"role":"viewer"}`, func(ws string) string { return "/workspaces/" + ws + "/members/" + admin.ID.String() }},
		{"DELETE", "", func(ws string) string { return "/workspaces/" + ws + "/members/" + admin.ID.String() }},
	} {
		t.Run(ep.method, func(t *testing.T) {
			var bodies []string
			for _, ws := range []string{existing.String(), uuid.NewString(), "no-es-un-uuid"} {
				rec := e.do(ep.method, ep.path(ws), ep.body, outsider)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("workspace %s: status = %d, want 403", ws, rec.Code)
				}
				bodies = append(bodies, rec.Body.String())
			}
			if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
				t.Errorf("responses differ:\n%s", strings.Join(bodies, "\n"))
			}
		})
	}
}

// --- create and list ---

func TestCreateWorkspaceMakesCreatorAdmin(t *testing.T) {
	e := newWSEnv(t)
	ana := e.user("Ana")

	rec := e.do("POST", "/workspaces", `{"name":"  Producto  "}`, ana)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body)
	}
	var got workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Producto" || got.Role != db.WorkspaceRoleAdmin || got.MemberCount != 1 {
		t.Errorf("workspace = %+v", got)
	}
	if role := e.role(got.ID, ana); role != db.WorkspaceRoleAdmin {
		t.Errorf("creator role in store = %q, want admin", role)
	}
}

func TestCreateWorkspaceValidation(t *testing.T) {
	e := newWSEnv(t)
	for _, body := range []string{`{"name":"   "}`, `{"name":"` + strings.Repeat("a", 101) + `"}`} {
		rec := e.do("POST", "/workspaces", body, e.user("Ana"+uuid.NewString()[:4]))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		if got := decodeError(t, rec); got.Fields["name"] == "" {
			t.Errorf("error = %+v", got)
		}
	}
	if len(e.store.workspaces) != 0 {
		t.Error("workspace created despite validation error")
	}
}

func TestListWorkspacesShowsMyRoleAndMemberCount(t *testing.T) {
	e := newWSEnv(t)
	ana, beto, carla := e.user("Ana"), e.user("Beto"), e.user("Carla")
	e.workspace(map[testUser]db.WorkspaceRole{ana: db.WorkspaceRoleAdmin, beto: db.WorkspaceRoleViewer, carla: db.WorkspaceRoleMember})
	e.workspace(map[testUser]db.WorkspaceRole{beto: db.WorkspaceRoleAdmin})
	e.workspace(map[testUser]db.WorkspaceRole{carla: db.WorkspaceRoleAdmin}) // Beto is not in this one

	rec := e.do("GET", "/workspaces", "", beto)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body)
	}
	var body struct {
		Workspaces []workspaceResponse `json:"workspaces"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	got := map[db.WorkspaceRole]int64{}
	for _, w := range body.Workspaces {
		got[w.Role] = w.MemberCount
	}
	want := map[db.WorkspaceRole]int64{db.WorkspaceRoleViewer: 3, db.WorkspaceRoleAdmin: 1}
	if len(body.Workspaces) != 2 || got[db.WorkspaceRoleViewer] != want[db.WorkspaceRoleViewer] || got[db.WorkspaceRoleAdmin] != want[db.WorkspaceRoleAdmin] {
		t.Errorf("workspaces = %+v", body.Workspaces)
	}
}

func TestListWorkspacesEmptyIsArray(t *testing.T) {
	e := newWSEnv(t)
	rec := e.do("GET", "/workspaces", "", e.user("Ana"))
	if got := strings.TrimSpace(rec.Body.String()); got != `{"workspaces":[]}` {
		t.Errorf("body = %s", got)
	}
}

// --- membership changes ---

func TestChangeRole(t *testing.T) {
	e := newWSEnv(t)
	admin, bob := e.user("Admin"), e.user("Bob")
	ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin, bob: db.WorkspaceRoleMember})

	rec := e.do("PATCH", memberPath(ws, bob.ID), `{"role":"admin"}`, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body)
	}
	var m memberResponse
	_ = json.NewDecoder(rec.Body).Decode(&m)
	if m.UserID != bob.ID || m.Role != db.WorkspaceRoleAdmin || m.Email != "bob@example.com" {
		t.Errorf("member = %+v", m)
	}
	if e.role(ws, bob) != db.WorkspaceRoleAdmin {
		t.Error("role not persisted")
	}
}

func TestChangeRoleErrors(t *testing.T) {
	e := newWSEnv(t)
	admin, bob, stranger := e.user("Admin"), e.user("Bob"), e.user("Stranger")
	ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin, bob: db.WorkspaceRoleMember})

	tests := []struct {
		name       string
		path, body string
		status     int
		code       string
	}{
		{"invalid role", memberPath(ws, bob.ID), `{"role":"owner"}`, 422, CodeValidation},
		{"missing role", memberPath(ws, bob.ID), `{}`, 422, CodeValidation},
		{"target not a member", memberPath(ws, stranger.ID), `{"role":"viewer"}`, 404, CodeMemberNotFound},
		{"target id not a uuid", membersPath(ws) + "/abc", `{"role":"viewer"}`, 404, CodeMemberNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do("PATCH", tt.path, tt.body, admin)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tt.status, rec.Body)
			}
			if got := decodeError(t, rec); got.Code != tt.code {
				t.Errorf("error = %+v", got)
			}
		})
	}
	if e.role(ws, bob) != db.WorkspaceRoleMember {
		t.Error("failed requests changed Bob's role")
	}
}

func TestRemoveMember(t *testing.T) {
	e := newWSEnv(t)
	admin, bob := e.user("Admin"), e.user("Bob")
	ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin, bob: db.WorkspaceRoleViewer})

	if rec := e.do("DELETE", memberPath(ws, bob.ID), "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body)
	}
	if e.role(ws, bob) != "" {
		t.Error("Bob is still a member")
	}
	// Removed members lose access immediately.
	if rec := e.do("GET", membersPath(ws), "", bob); rec.Code != http.StatusForbidden {
		t.Errorf("removed member: status = %d, want 403", rec.Code)
	}
	if rec := e.do("DELETE", memberPath(ws, bob.ID), "", admin); rec.Code != http.StatusNotFound {
		t.Errorf("second delete: status = %d, want 404", rec.Code)
	}
}

// --- at least one admin ---

func TestWorkspaceKeepsAtLeastOneAdmin(t *testing.T) {
	t.Run("sole admin cannot demote themselves", func(t *testing.T) {
		e := newWSEnv(t)
		admin, bob := e.user("Admin"), e.user("Bob")
		ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin, bob: db.WorkspaceRoleMember})

		for _, role := range []string{"member", "viewer"} {
			rec := e.do("PATCH", memberPath(ws, admin.ID), `{"role":"`+role+`"}`, admin)
			if rec.Code != http.StatusConflict {
				t.Fatalf("to %s: status = %d, want 409", role, rec.Code)
			}
			if got := decodeError(t, rec); got.Code != CodeLastAdmin {
				t.Errorf("error = %+v", got)
			}
		}
		if e.role(ws, admin) != db.WorkspaceRoleAdmin {
			t.Error("sole admin was demoted")
		}
	})

	t.Run("sole admin can keep the admin role", func(t *testing.T) {
		e := newWSEnv(t)
		admin := e.user("Admin")
		ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin})
		if rec := e.do("PATCH", memberPath(ws, admin.ID), `{"role":"admin"}`, admin); rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("sole admin cannot be removed", func(t *testing.T) {
		e := newWSEnv(t)
		admin := e.user("Admin")
		ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin})

		rec := e.do("DELETE", memberPath(ws, admin.ID), "", admin)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
		if e.role(ws, admin) != db.WorkspaceRoleAdmin {
			t.Error("sole admin was removed")
		}
	})

	t.Run("with two admins one can step down, then the other cannot", func(t *testing.T) {
		e := newWSEnv(t)
		ana, beto := e.user("Ana"), e.user("Beto")
		ws := e.workspace(map[testUser]db.WorkspaceRole{ana: db.WorkspaceRoleAdmin, beto: db.WorkspaceRoleAdmin})

		if rec := e.do("PATCH", memberPath(ws, beto.ID), `{"role":"member"}`, ana); rec.Code != http.StatusOK {
			t.Fatalf("demote Beto: status = %d, body = %s", rec.Code, rec.Body)
		}
		if rec := e.do("PATCH", memberPath(ws, ana.ID), `{"role":"viewer"}`, ana); rec.Code != http.StatusConflict {
			t.Fatalf("demote Ana (now sole admin): status = %d, want 409", rec.Code)
		}
	})

	t.Run("an admin can leave when another admin remains", func(t *testing.T) {
		e := newWSEnv(t)
		ana, beto := e.user("Ana"), e.user("Beto")
		ws := e.workspace(map[testUser]db.WorkspaceRole{ana: db.WorkspaceRoleAdmin, beto: db.WorkspaceRoleAdmin})

		if rec := e.do("DELETE", memberPath(ws, ana.ID), "", ana); rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if n, _ := e.store.CountWorkspaceAdmins(context.Background(), ws); n != 1 {
			t.Errorf("admins = %d, want 1", n)
		}
	})
}

// The middleware authorizes before the transaction; the service must re-check
// under the lock in case the actor was demoted in between.
func TestServiceRechecksActorRoleInsideTransaction(t *testing.T) {
	e := newWSEnv(t)
	ana, beto, carla := e.user("Ana"), e.user("Beto"), e.user("Carla")
	ws := e.workspace(map[testUser]db.WorkspaceRole{
		ana: db.WorkspaceRoleAdmin, beto: db.WorkspaceRoleMember, carla: db.WorkspaceRoleMember,
	})
	ctx := context.Background()

	if _, err := e.svc.ChangeRole(ctx, ws, beto.ID, carla.ID, db.WorkspaceRoleViewer); !errors.Is(err, service.ErrForbidden) {
		t.Errorf("ChangeRole by member: err = %v, want ErrForbidden", err)
	}
	if err := e.svc.RemoveMember(ctx, ws, beto.ID, carla.ID); !errors.Is(err, service.ErrForbidden) {
		t.Errorf("RemoveMember by member: err = %v, want ErrForbidden", err)
	}
	if err := e.svc.RemoveMember(ctx, ws, uuid.New(), carla.ID); !errors.Is(err, service.ErrNotMember) {
		t.Errorf("RemoveMember by outsider: err = %v, want ErrNotMember", err)
	}
	if e.role(ws, carla) != db.WorkspaceRoleMember {
		t.Error("Carla's membership changed")
	}
}
