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

	"github.com/DarkApofis/tablero/backend/internal/adapters/events"
	"github.com/DarkApofis/tablero/backend/internal/adapters/token"
	"github.com/DarkApofis/tablero/backend/internal/app/workspace"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// fakeWorkspaceStore is an in-memory workspace.WorkspaceRepository. Its
// transactions (fakeTx) run fn directly: the use cases do all their checks
// before writing, so a failed operation leaves nothing to roll back.
type fakeWorkspaceStore struct {
	users      *fakeStore
	workspaces map[domain.ID]domain.Workspace
	members    []fakeMember // insertion order = join order
}

type fakeMember struct {
	workspaceID, userID domain.ID
	role                domain.Role
	joinedAt            time.Time
}

type fakeTx struct{}

func (fakeTx) InTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

func newFakeWorkspaceStore(users *fakeStore) *fakeWorkspaceStore {
	return &fakeWorkspaceStore{users: users, workspaces: map[domain.ID]domain.Workspace{}}
}

func (f *fakeWorkspaceStore) Create(_ context.Context, name string) (domain.Workspace, error) {
	ws := domain.Workspace{ID: domain.NewID(), Name: name, CreatedAt: time.Now()}
	f.workspaces[ws.ID] = ws
	return ws, nil
}

func (f *fakeWorkspaceStore) AddMember(_ context.Context, wsID, userID domain.ID, role domain.Role) error {
	f.members = append(f.members, fakeMember{workspaceID: wsID, userID: userID, role: role, joinedAt: time.Now()})
	return nil
}

func (f *fakeWorkspaceStore) ListForUser(_ context.Context, userID domain.ID) ([]domain.WorkspaceSummary, error) {
	var rows []domain.WorkspaceSummary
	for _, m := range f.members {
		if m.userID == userID {
			rows = append(rows, domain.WorkspaceSummary{Workspace: f.workspaces[m.workspaceID], Role: m.role, MemberCount: f.count(m.workspaceID, "")})
		}
	}
	return rows, nil
}

func (f *fakeWorkspaceStore) Role(_ context.Context, wsID, userID domain.ID) (domain.Role, error) {
	i := f.find(wsID, userID)
	if i < 0 {
		return "", domain.ErrNotMember
	}
	return f.members[i].role, nil
}

func (f *fakeWorkspaceStore) member(m fakeMember) domain.Member {
	u := f.users.users[m.userID]
	return domain.Member{UserID: m.userID, Email: u.Email, Name: u.Name, Role: m.role, JoinedAt: m.joinedAt}
}

func (f *fakeWorkspaceStore) Members(_ context.Context, wsID domain.ID) ([]domain.Member, error) {
	var rows []domain.Member
	for _, m := range f.members {
		if m.workspaceID == wsID {
			rows = append(rows, f.member(m))
		}
	}
	return rows, nil
}

func (f *fakeWorkspaceStore) Member(_ context.Context, wsID, userID domain.ID) (domain.Member, error) {
	i := f.find(wsID, userID)
	if i < 0 {
		return domain.Member{}, domain.ErrMemberNotFound
	}
	return f.member(f.members[i]), nil
}

func (f *fakeWorkspaceStore) Lock(context.Context, domain.ID) error { return nil }

func (f *fakeWorkspaceStore) CountAdmins(_ context.Context, wsID domain.ID) (int64, error) {
	return f.count(wsID, domain.RoleAdmin), nil
}

func (f *fakeWorkspaceStore) UpdateRole(_ context.Context, wsID, userID domain.ID, role domain.Role) error {
	if i := f.find(wsID, userID); i >= 0 {
		f.members[i].role = role
	}
	return nil
}

func (f *fakeWorkspaceStore) RemoveMember(_ context.Context, wsID, userID domain.ID) error {
	if i := f.find(wsID, userID); i >= 0 {
		f.members = slices.Delete(f.members, i, i+1)
	}
	return nil
}

// The fake has no tasks to unassign.
func (f *fakeWorkspaceStore) UnassignTasks(context.Context, domain.ID, domain.ID, domain.ID) ([]domain.ActivityEntry, error) {
	return nil, nil
}

func (f *fakeWorkspaceStore) find(wsID, userID domain.ID) int {
	return slices.IndexFunc(f.members, func(m fakeMember) bool { return m.workspaceID == wsID && m.userID == userID })
}

func (f *fakeWorkspaceStore) count(wsID domain.ID, role domain.Role) int64 {
	var n int64
	for _, m := range f.members {
		if m.workspaceID == wsID && (role == "" || m.role == role) {
			n++
		}
	}
	return n
}

// --- test environment ---

type wsEnv struct {
	t      *testing.T
	router http.Handler
	tokens *token.Issuer
	users  *fakeStore
	store  *fakeWorkspaceStore
	svc    *workspace.Service
}

func newWSEnv(t *testing.T) *wsEnv {
	users := newFakeStore()
	store := newFakeWorkspaceStore(users)
	tokens := token.NewIssuer(testSecret, 15*time.Minute)
	svc := workspace.NewService(store, fakeTx{}, events.Noop{})
	return &wsEnv{
		t: t, tokens: tokens, users: users, store: store, svc: svc,
		router: NewRouter(Deps{
			Ready:      fakePinger{},
			Auth:       newAuthService(users, tokens, time.Hour),
			Authorizer: workspace.NewAuthorizer(store),
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
	u, err := e.users.Create(context.Background(), strings.ToLower(name)+"@example.com", name, "x")
	if err != nil {
		e.t.Fatal(err)
	}
	token, _, err := e.tokens.Issue(u.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return testUser{ID: uuid.UUID(u.ID), Token: token}
}

func (e *wsEnv) workspace(members map[testUser]domain.Role) uuid.UUID {
	e.t.Helper()
	ws, _ := e.store.Create(context.Background(), "Equipo")
	for u, role := range members {
		_ = e.store.AddMember(context.Background(), ws.ID, domain.ID(u.ID), role)
	}
	return uuid.UUID(ws.ID)
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

func (e *wsEnv) role(wsID uuid.UUID, u testUser) domain.Role {
	role, err := e.store.Role(context.Background(), domain.ID(wsID), domain.ID(u.ID))
	if errors.Is(err, domain.ErrNotMember) {
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
				ws := e.workspace(map[testUser]domain.Role{
					actors["admin"]: domain.RoleAdmin, actors["member"]: domain.RoleMember,
					actors["viewer"]: domain.RoleViewer, target: domain.RoleMember,
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
					if e.role(ws, target) != domain.RoleMember {
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
	existing := e.workspace(map[testUser]domain.Role{admin: domain.RoleAdmin})

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
	if got.Name != "Producto" || got.Role != domain.RoleAdmin || got.MemberCount != 1 {
		t.Errorf("workspace = %+v", got)
	}
	if role := e.role(got.ID, ana); role != domain.RoleAdmin {
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
	e.workspace(map[testUser]domain.Role{ana: domain.RoleAdmin, beto: domain.RoleViewer, carla: domain.RoleMember})
	e.workspace(map[testUser]domain.Role{beto: domain.RoleAdmin})
	e.workspace(map[testUser]domain.Role{carla: domain.RoleAdmin}) // Beto is not in this one

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

	got := map[domain.Role]int64{}
	for _, w := range body.Workspaces {
		got[w.Role] = w.MemberCount
	}
	want := map[domain.Role]int64{domain.RoleViewer: 3, domain.RoleAdmin: 1}
	if len(body.Workspaces) != 2 || got[domain.RoleViewer] != want[domain.RoleViewer] || got[domain.RoleAdmin] != want[domain.RoleAdmin] {
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
	ws := e.workspace(map[testUser]domain.Role{admin: domain.RoleAdmin, bob: domain.RoleMember})

	rec := e.do("PATCH", memberPath(ws, bob.ID), `{"role":"admin"}`, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body)
	}
	var m memberResponse
	_ = json.NewDecoder(rec.Body).Decode(&m)
	if m.UserID != bob.ID || m.Role != domain.RoleAdmin || m.Email != "bob@example.com" {
		t.Errorf("member = %+v", m)
	}
	if e.role(ws, bob) != domain.RoleAdmin {
		t.Error("role not persisted")
	}
}

func TestChangeRoleErrors(t *testing.T) {
	e := newWSEnv(t)
	admin, bob, stranger := e.user("Admin"), e.user("Bob"), e.user("Stranger")
	ws := e.workspace(map[testUser]domain.Role{admin: domain.RoleAdmin, bob: domain.RoleMember})

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
	if e.role(ws, bob) != domain.RoleMember {
		t.Error("failed requests changed Bob's role")
	}
}

func TestRemoveMember(t *testing.T) {
	e := newWSEnv(t)
	admin, bob := e.user("Admin"), e.user("Bob")
	ws := e.workspace(map[testUser]domain.Role{admin: domain.RoleAdmin, bob: domain.RoleViewer})

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
		ws := e.workspace(map[testUser]domain.Role{admin: domain.RoleAdmin, bob: domain.RoleMember})

		for _, role := range []string{"member", "viewer"} {
			rec := e.do("PATCH", memberPath(ws, admin.ID), `{"role":"`+role+`"}`, admin)
			if rec.Code != http.StatusConflict {
				t.Fatalf("to %s: status = %d, want 409", role, rec.Code)
			}
			if got := decodeError(t, rec); got.Code != CodeLastAdmin {
				t.Errorf("error = %+v", got)
			}
		}
		if e.role(ws, admin) != domain.RoleAdmin {
			t.Error("sole admin was demoted")
		}
	})

	t.Run("sole admin can keep the admin role", func(t *testing.T) {
		e := newWSEnv(t)
		admin := e.user("Admin")
		ws := e.workspace(map[testUser]domain.Role{admin: domain.RoleAdmin})
		if rec := e.do("PATCH", memberPath(ws, admin.ID), `{"role":"admin"}`, admin); rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("sole admin cannot be removed", func(t *testing.T) {
		e := newWSEnv(t)
		admin := e.user("Admin")
		ws := e.workspace(map[testUser]domain.Role{admin: domain.RoleAdmin})

		rec := e.do("DELETE", memberPath(ws, admin.ID), "", admin)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
		if e.role(ws, admin) != domain.RoleAdmin {
			t.Error("sole admin was removed")
		}
	})

	t.Run("with two admins one can step down, then the other cannot", func(t *testing.T) {
		e := newWSEnv(t)
		ana, beto := e.user("Ana"), e.user("Beto")
		ws := e.workspace(map[testUser]domain.Role{ana: domain.RoleAdmin, beto: domain.RoleAdmin})

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
		ws := e.workspace(map[testUser]domain.Role{ana: domain.RoleAdmin, beto: domain.RoleAdmin})

		if rec := e.do("DELETE", memberPath(ws, ana.ID), "", ana); rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if n, _ := e.store.CountAdmins(context.Background(), domain.ID(ws)); n != 1 {
			t.Errorf("admins = %d, want 1", n)
		}
	})
}

// The middleware authorizes before the transaction; the use case must re-check
// under the lock in case the actor was demoted in between. The Access below
// is stale: it says admin, but Beto is a member by now.
func TestServiceRechecksActorRoleInsideTransaction(t *testing.T) {
	e := newWSEnv(t)
	ana, beto, carla := e.user("Ana"), e.user("Beto"), e.user("Carla")
	ws := e.workspace(map[testUser]domain.Role{
		ana: domain.RoleAdmin, beto: domain.RoleMember, carla: domain.RoleMember,
	})
	ctx := context.Background()
	stale := domain.Access{UserID: domain.ID(beto.ID), WorkspaceID: domain.ID(ws), Role: domain.RoleAdmin}
	outsider := domain.Access{UserID: domain.NewID(), WorkspaceID: domain.ID(ws), Role: domain.RoleAdmin}

	if _, err := e.svc.ChangeRole(ctx, stale, domain.ID(carla.ID), domain.RoleViewer); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("ChangeRole by member: err = %v, want ErrForbidden", err)
	}
	if err := e.svc.RemoveMember(ctx, stale, domain.ID(carla.ID)); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("RemoveMember by member: err = %v, want ErrForbidden", err)
	}
	if err := e.svc.RemoveMember(ctx, outsider, domain.ID(carla.ID)); !errors.Is(err, domain.ErrNotMember) {
		t.Errorf("RemoveMember by outsider: err = %v, want ErrNotMember", err)
	}
	if e.role(ws, carla) != domain.RoleMember {
		t.Error("Carla's membership changed")
	}
}
