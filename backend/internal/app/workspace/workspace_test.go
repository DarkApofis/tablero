package workspace_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/app/workspace"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// --- fakes of the ports ---

type member struct {
	ws, user domain.ID
	role     domain.Role
}

// memRepo is an in-memory WorkspaceRepository. unassigned simulates the
// tasks a removed member had: UnassignTasks returns one entry per task.
type memRepo struct {
	workspaces map[domain.ID]domain.Workspace
	members    []member
	tasksOf    map[domain.ID]int
	failRemove error
}

func newRepo() *memRepo {
	return &memRepo{workspaces: map[domain.ID]domain.Workspace{}, tasksOf: map[domain.ID]int{}}
}

func (r *memRepo) Create(_ context.Context, name string) (domain.Workspace, error) {
	ws := domain.Workspace{ID: domain.NewID(), Name: name, CreatedAt: time.Now()}
	r.workspaces[ws.ID] = ws
	return ws, nil
}

func (r *memRepo) AddMember(_ context.Context, ws, user domain.ID, role domain.Role) error {
	r.members = append(r.members, member{ws, user, role})
	return nil
}

func (r *memRepo) ListForUser(_ context.Context, user domain.ID) ([]domain.WorkspaceSummary, error) {
	var out []domain.WorkspaceSummary
	for _, m := range r.members {
		if m.user == user {
			out = append(out, domain.WorkspaceSummary{Workspace: r.workspaces[m.ws], Role: m.role})
		}
	}
	return out, nil
}

func (r *memRepo) find(ws, user domain.ID) int {
	return slices.IndexFunc(r.members, func(m member) bool { return m.ws == ws && m.user == user })
}

func (r *memRepo) Role(_ context.Context, ws, user domain.ID) (domain.Role, error) {
	if i := r.find(ws, user); i >= 0 {
		return r.members[i].role, nil
	}
	return "", domain.ErrNotMember
}

func (r *memRepo) Members(_ context.Context, ws domain.ID) ([]domain.Member, error) {
	var out []domain.Member
	for _, m := range r.members {
		if m.ws == ws {
			out = append(out, domain.Member{UserID: m.user, Role: m.role})
		}
	}
	return out, nil
}

func (r *memRepo) Member(_ context.Context, ws, user domain.ID) (domain.Member, error) {
	if i := r.find(ws, user); i >= 0 {
		return domain.Member{UserID: user, Role: r.members[i].role}, nil
	}
	return domain.Member{}, domain.ErrMemberNotFound
}

func (r *memRepo) Lock(context.Context, domain.ID) error { return nil }

func (r *memRepo) CountAdmins(_ context.Context, ws domain.ID) (int64, error) {
	var n int64
	for _, m := range r.members {
		if m.ws == ws && m.role == domain.RoleAdmin {
			n++
		}
	}
	return n, nil
}

func (r *memRepo) UpdateRole(_ context.Context, ws, user domain.ID, role domain.Role) error {
	r.members[r.find(ws, user)].role = role
	return nil
}

func (r *memRepo) RemoveMember(_ context.Context, ws, user domain.ID) error {
	if r.failRemove != nil {
		return r.failRemove
	}
	i := r.find(ws, user)
	r.members = slices.Delete(r.members, i, i+1)
	return nil
}

func (r *memRepo) UnassignTasks(_ context.Context, ws, user, actor domain.ID) ([]domain.ActivityEntry, error) {
	var out []domain.ActivityEntry
	for i := range r.tasksOf[user] {
		out = append(out, domain.ActivityEntry{ID: int64(i + 1), WorkspaceID: ws, ActorID: &actor, Type: domain.ActivityTaskUpdated})
	}
	r.tasksOf[user] = 0
	return out, nil
}

// fakeTx records whether fn ran inside a transaction that committed.
type fakeTx struct{ committed, rolledBack int }

func (f *fakeTx) InTx(ctx context.Context, fn func(context.Context) error) error {
	if err := fn(ctx); err != nil {
		f.rolledBack++
		return err
	}
	f.committed++
	return nil
}

type recorder struct{ events []domain.Event }

func (r *recorder) Publish(_ context.Context, events ...domain.Event) {
	r.events = append(r.events, events...)
}

type env struct {
	repo   *memRepo
	tx     *fakeTx
	events *recorder
	svc    *workspace.Service
	authz  *workspace.Authorizer
	ws     domain.ID
	users  map[domain.Role]domain.ID
}

// newEnv builds a workspace with one admin, one member and one viewer.
func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{repo: newRepo(), tx: &fakeTx{}, events: &recorder{}, users: map[domain.Role]domain.ID{}}
	e.svc = workspace.NewService(e.repo, e.tx, e.events)
	e.authz = workspace.NewAuthorizer(e.repo)
	ws, _ := e.repo.Create(context.Background(), "Equipo")
	e.ws = ws.ID
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleMember, domain.RoleViewer} {
		e.users[role] = domain.NewID()
		_ = e.repo.AddMember(context.Background(), ws.ID, e.users[role], role)
	}
	return e
}

func (e *env) access(t *testing.T, role domain.Role) domain.Access {
	t.Helper()
	acc, err := e.authz.Authorize(context.Background(), e.users[role], e.ws, domain.ActionRead)
	if err != nil {
		t.Fatal(err)
	}
	return acc
}

// --- authorization ---

func TestAuthorizerAppliesTheRoleMatrix(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	actions := []domain.Action{
		domain.ActionRead, domain.ActionEditTasks, domain.ActionManageMembers,
		domain.ActionManageSettings, domain.ActionManageBoards, domain.ActionManageLabels,
	}
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleMember, domain.RoleViewer} {
		for _, action := range actions {
			acc, err := e.authz.Authorize(ctx, e.users[role], e.ws, action)
			switch {
			case domain.Can(role, action):
				if err != nil || acc.Role != role || acc.WorkspaceID != e.ws || acc.UserID != e.users[role] {
					t.Errorf("%s/%s: acc = %+v, err = %v", role, action, acc, err)
				}
			case !errors.Is(err, domain.ErrForbidden):
				t.Errorf("%s/%s: err = %v, want ErrForbidden", role, action, err)
			}
		}
	}
	// Outsiders and unknown workspaces are the same ErrNotMember.
	for name, c := range map[string]struct{ user, ws domain.ID }{
		"outsider":          {domain.NewID(), e.ws},
		"unknown workspace": {e.users[domain.RoleAdmin], domain.NewID()},
	} {
		if _, err := e.authz.Authorize(ctx, c.user, c.ws, domain.ActionRead); !errors.Is(err, domain.ErrNotMember) {
			t.Errorf("%s: err = %v, want ErrNotMember", name, err)
		}
	}
}

func TestUseCasesRequireTheirAction(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	target := e.users[domain.RoleViewer]
	for _, role := range []domain.Role{domain.RoleMember, domain.RoleViewer} {
		acc := e.access(t, role)
		if _, err := e.svc.ChangeRole(ctx, acc, target, domain.RoleMember); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s ChangeRole: err = %v", role, err)
		}
		if err := e.svc.RemoveMember(ctx, acc, target); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s RemoveMember: err = %v", role, err)
		}
		if _, err := e.svc.ListMembers(ctx, acc); err != nil {
			t.Errorf("%s ListMembers: err = %v", role, err)
		}
	}
	if _, err := e.svc.ListMembers(ctx, domain.Access{UserID: domain.NewID(), WorkspaceID: e.ws}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("ListMembers without a role: err = %v", err)
	}
	if e.tx.committed+e.tx.rolledBack != 0 {
		t.Error("a forbidden call opened a transaction")
	}
}

// The Access may be stale: the actor could have been demoted after it was
// established. Membership changes re-read the role under the workspace lock.
func TestMembershipChangesRecheckTheActorUnderTheLock(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	stale := e.access(t, domain.RoleAdmin)
	e.repo.members[e.repo.find(e.ws, stale.UserID)].role = domain.RoleMember // demoted meanwhile

	if _, err := e.svc.ChangeRole(ctx, stale, e.users[domain.RoleViewer], domain.RoleAdmin); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("ChangeRole: err = %v, want ErrForbidden", err)
	}
	if err := e.svc.RemoveMember(ctx, stale, e.users[domain.RoleViewer]); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("RemoveMember: err = %v, want ErrForbidden", err)
	}
}

// --- use cases ---

func TestCreateMakesCreatorAdmin(t *testing.T) {
	e := newEnv(t)
	creator := domain.NewID()
	ws, err := e.svc.Create(context.Background(), creator, "  Producto ")
	if err != nil || ws.Name != "Producto" || ws.Role != domain.RoleAdmin || ws.MemberCount != 1 {
		t.Fatalf("Create = %+v, %v", ws, err)
	}
	if role, _ := e.repo.Role(context.Background(), ws.ID, creator); role != domain.RoleAdmin || e.tx.committed != 1 {
		t.Errorf("creator role = %q, commits = %d", role, e.tx.committed)
	}
	if _, err := e.svc.Create(context.Background(), creator, " "); !errors.As(err, new(*domain.ValidationError)) {
		t.Errorf("empty name: err = %v", err)
	}
}

func TestChangeRole(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.access(t, domain.RoleAdmin)

	m, err := e.svc.ChangeRole(ctx, admin, e.users[domain.RoleViewer], domain.RoleMember)
	if err != nil || m.Role != domain.RoleMember {
		t.Fatalf("ChangeRole = %+v, %v", m, err)
	}
	if _, err := e.svc.ChangeRole(ctx, admin, e.users[domain.RoleViewer], "owner"); !errors.As(err, new(*domain.ValidationError)) {
		t.Errorf("invalid role: err = %v", err)
	}
	if _, err := e.svc.ChangeRole(ctx, admin, domain.NewID(), domain.RoleViewer); !errors.Is(err, domain.ErrMemberNotFound) {
		t.Errorf("unknown target: err = %v", err)
	}
}

func TestWorkspaceKeepsAtLeastOneAdmin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.access(t, domain.RoleAdmin)

	if _, err := e.svc.ChangeRole(ctx, admin, admin.UserID, domain.RoleViewer); !errors.Is(err, domain.ErrLastAdmin) {
		t.Errorf("demote sole admin: err = %v", err)
	}
	if err := e.svc.RemoveMember(ctx, admin, admin.UserID); !errors.Is(err, domain.ErrLastAdmin) {
		t.Errorf("remove sole admin: err = %v", err)
	}
	// With a second admin, the first may step down.
	if _, err := e.svc.ChangeRole(ctx, admin, e.users[domain.RoleMember], domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ChangeRole(ctx, admin, admin.UserID, domain.RoleMember); err != nil {
		t.Errorf("step down with another admin: err = %v", err)
	}
}

func TestRemoveMemberPublishesUnassignmentsAfterCommit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.access(t, domain.RoleAdmin)
	target := e.users[domain.RoleMember]
	e.repo.tasksOf[target] = 3

	if err := e.svc.RemoveMember(ctx, admin, target); err != nil {
		t.Fatal(err)
	}
	if _, err := e.repo.Role(ctx, e.ws, target); !errors.Is(err, domain.ErrNotMember) {
		t.Error("target is still a member")
	}
	if len(e.events.events) != 3 || e.tx.committed != 1 {
		t.Fatalf("events = %d, commits = %d; want 3 events after 1 commit", len(e.events.events), e.tx.committed)
	}
	for _, ev := range e.events.events {
		if ev.Type != domain.EventTaskUpdated || ev.WorkspaceID != e.ws || ev.ActorID == nil || *ev.ActorID != admin.UserID {
			t.Errorf("event = %+v", ev)
		}
	}

	// A failed removal rolls back and publishes nothing.
	e.events.events = nil
	e.repo.failRemove = errors.New("boom")
	e.repo.tasksOf[e.users[domain.RoleViewer]] = 2
	if err := e.svc.RemoveMember(ctx, admin, e.users[domain.RoleViewer]); err == nil || len(e.events.events) != 0 || e.tx.rolledBack != 1 {
		t.Errorf("err = %v, events = %d, rollbacks = %d", err, len(e.events.events), e.tx.rolledBack)
	}
}
