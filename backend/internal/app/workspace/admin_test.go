package workspace_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/app/workspace"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// ownedEnv is newEnv with a workspace created through the service, so its
// admin is also its owner, plus a second admin.
func ownedEnv(t *testing.T) (e *env, owner, admin domain.Access) {
	t.Helper()
	e = newEnv(t)
	ctx := context.Background()
	ownerID := domain.NewID()
	ws, err := e.svc.Create(ctx, ownerID, "Propio", "")
	if err != nil {
		t.Fatal(err)
	}
	e.ws = ws.ID
	second := domain.NewID()
	_ = e.repo.AddMember(ctx, ws.ID, second, domain.RoleAdmin)
	member := domain.NewID()
	_ = e.repo.AddMember(ctx, ws.ID, member, domain.RoleMember)
	e.users = map[domain.Role]domain.ID{domain.RoleAdmin: second, domain.RoleMember: member}
	owner, _ = e.authz.Authorize(ctx, ownerID, ws.ID, domain.ActionRead)
	admin, _ = e.authz.Authorize(ctx, second, ws.ID, domain.ActionRead)
	return e, owner, admin
}

func TestCreateGivesAFreeAddress(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ana, beto := domain.NewID(), domain.NewID()

	first, _ := e.svc.Create(ctx, ana, "Diseño de Producto", "")
	second, _ := e.svc.Create(ctx, beto, "diseño de producto", "")
	if first.Slug != "diseno-de-producto" || second.Slug != "diseno-de-producto-2" {
		t.Errorf("slugs = %q, %q", first.Slug, second.Slug)
	}
	if _, err := e.svc.Create(ctx, beto, "Otro", "Diseno-De-Producto"); !errors.Is(err, domain.ErrSlugTaken) {
		t.Errorf("chosen slug taken: err = %v, want ErrSlugTaken", err)
	}
	if _, err := e.svc.Create(ctx, beto, "Otro", "con espacios"); !errors.As(err, new(*domain.ValidationError)) {
		t.Errorf("invalid slug: err = %v", err)
	}
	if slug, ok, err := e.svc.SlugAvailable(ctx, " Libre-1 "); err != nil || !ok || slug != "libre-1" {
		t.Errorf("SlugAvailable = %q, %v, %v", slug, ok, err)
	}
	if _, ok, _ := e.svc.SlugAvailable(ctx, "diseno-de-producto"); ok {
		t.Error("a taken slug is reported available")
	}
}

func TestOwnerStaysAdminUntilTheyTransfer(t *testing.T) {
	e, owner, admin := ownedEnv(t)
	ctx := context.Background()

	if _, err := e.svc.ChangeRole(ctx, admin, owner.UserID, domain.RoleMember); !errors.Is(err, domain.ErrOwnerStaysAdmin) {
		t.Errorf("demote owner: err = %v", err)
	}
	if err := e.svc.RemoveMember(ctx, admin, owner.UserID); !errors.Is(err, domain.ErrOwnerStaysAdmin) {
		t.Errorf("remove owner: err = %v", err)
	}
	if err := e.svc.RemoveMember(ctx, owner, owner.UserID); !errors.Is(err, domain.ErrOwnerStaysAdmin) {
		t.Errorf("owner leaving: err = %v", err)
	}

	// Only the owner hands it over, and only to another admin.
	if _, err := e.svc.Transfer(ctx, admin, admin.UserID); !errors.Is(err, domain.ErrNotOwner) {
		t.Errorf("transfer by another admin: err = %v", err)
	}
	if _, err := e.svc.Transfer(ctx, owner, e.users[domain.RoleMember]); !errors.As(err, new(*domain.ValidationError)) {
		t.Errorf("transfer to a member: err = %v", err)
	}
	if _, err := e.svc.Transfer(ctx, owner, domain.NewID()); !errors.Is(err, domain.ErrMemberNotFound) {
		t.Errorf("transfer to an outsider: err = %v", err)
	}
	d, err := e.svc.Transfer(ctx, owner, admin.UserID)
	if err != nil || !d.IsOwner(admin.UserID) {
		t.Fatalf("Transfer = %+v, %v", d, err)
	}
	// Now the former owner is an admin like any other.
	if _, err := e.svc.ChangeRole(ctx, admin, owner.UserID, domain.RoleMember); err != nil {
		t.Errorf("demote former owner: err = %v", err)
	}
}

func TestDeleteWorkspace(t *testing.T) {
	e, owner, admin := ownedEnv(t)
	ctx := context.Background()
	member, _ := e.authz.Authorize(ctx, e.users[domain.RoleMember], e.ws, domain.ActionRead)

	if err := e.svc.Delete(ctx, member); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member: err = %v", err)
	}
	if err := e.svc.WithDefault(e.ws).Delete(ctx, owner); !errors.Is(err, domain.ErrDefaultWorkspace) {
		t.Errorf("default workspace: err = %v", err)
	}
	if err := e.svc.Delete(ctx, admin); err != nil {
		t.Fatalf("admin: err = %v", err)
	}
	if _, err := e.repo.Get(ctx, e.ws); !errors.Is(err, domain.ErrNotMember) {
		t.Error("the workspace still exists")
	}
}

func TestUpdateWorkspace(t *testing.T) {
	e, owner, _ := ownedEnv(t)
	ctx := context.Background()
	_, _ = e.svc.Create(ctx, domain.NewID(), "Ocupado", "")

	name, desc, slug, role := "  Nuevo  ", " Equipo de diseño ", "nueva-casa", domain.RoleViewer
	d, err := e.svc.Update(ctx, owner, domain.WorkspaceChanges{Name: &name, Description: &desc, Slug: &slug, DefaultRole: &role})
	if err != nil || d.Name != "Nuevo" || d.Description != "Equipo de diseño" || d.Slug != "nueva-casa" || d.DefaultRole != domain.RoleViewer {
		t.Fatalf("Update = %+v, %v", d, err)
	}
	bad, badRole, empty := "No vale", domain.Role("owner"), " "
	_, err = e.svc.Update(ctx, owner, domain.WorkspaceChanges{Name: &empty, Slug: &bad, DefaultRole: &badRole})
	v, ok := errors.AsType[*domain.ValidationError](err)
	if !ok || len(v.Fields) != 3 {
		t.Errorf("invalid fields: err = %v, want name, slug and default_role", err)
	}
	taken := "ocupado"
	if _, err := e.svc.Update(ctx, owner, domain.WorkspaceChanges{Slug: &taken}); !errors.Is(err, domain.ErrSlugTaken) {
		t.Errorf("slug taken: err = %v", err)
	}
	member, _ := e.authz.Authorize(ctx, e.users[domain.RoleMember], e.ws, domain.ActionRead)
	if _, err := e.svc.Update(ctx, member, domain.WorkspaceChanges{Name: &name}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member: err = %v", err)
	}
}

func TestIDBySlugIsForMembersOnly(t *testing.T) {
	e, owner, _ := ownedEnv(t)
	ctx := context.Background()
	ws, _ := e.repo.Get(ctx, e.ws)
	if id, err := e.svc.IDBySlug(ctx, owner.UserID, " "+ws.Slug+" "); err != nil || id != e.ws {
		t.Errorf("member: %v, %v", id, err)
	}
	if _, err := e.svc.IDBySlug(ctx, domain.NewID(), ws.Slug); !errors.Is(err, domain.ErrNotMember) {
		t.Errorf("outsider: err = %v", err)
	}
}

// --- boards: private and archived ---

type gate map[domain.ID]struct{ visible, archived bool }

func (g gate) BoardAccess(_ context.Context, board, _ domain.ID) (bool, bool, error) {
	b, ok := g[board]
	if !ok {
		return false, false, domain.ErrNotMember
	}
	return b.visible, b.archived, nil
}

func TestAuthorizerEnforcesPrivateAndArchivedBoards(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	private, archived, open := domain.NewID(), domain.NewID(), domain.NewID()
	authz := e.authz.WithBoards(gate{
		private:  {visible: false},
		archived: {visible: true, archived: true},
		open:     {visible: true},
	})
	at := func(role domain.Role, board domain.ID, action domain.Action) error {
		_, err := authz.AuthorizeAt(ctx, e.users[role], domain.Location{WorkspaceID: e.ws, BoardID: board}, action)
		return err
	}

	if err := at(domain.RoleMember, private, domain.ActionRead); !errors.Is(err, domain.ErrNotMember) {
		t.Errorf("member on a private board: err = %v, want ErrNotMember", err)
	}
	if err := at(domain.RoleAdmin, private, domain.ActionEditTasks); err != nil {
		t.Errorf("admin on a private board: err = %v", err)
	}
	if err := at(domain.RoleMember, archived, domain.ActionRead); err != nil {
		t.Errorf("read an archived board: err = %v", err)
	}
	if err := at(domain.RoleMember, archived, domain.ActionEditTasks); !errors.Is(err, domain.ErrBoardArchived) {
		t.Errorf("edit tasks on an archived board: err = %v", err)
	}
	if err := at(domain.RoleAdmin, archived, domain.ActionManageBoards); err != nil {
		t.Errorf("restore an archived board: err = %v", err)
	}
	if err := at(domain.RoleViewer, open, domain.ActionEditTasks); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer: err = %v, want ErrForbidden", err)
	}
	if err := at(domain.RoleMember, domain.NewID(), domain.ActionRead); !errors.Is(err, domain.ErrNotMember) {
		t.Errorf("unknown board: err = %v", err)
	}
}

// --- invitations ---

type memInvites struct {
	*memRepo
	invitations []domain.Invitation
	link        *domain.InviteLink
}

func (m *memInvites) UpsertInvitation(_ context.Context, inv domain.Invitation, _ domain.ID) (domain.Invitation, error) {
	for i, old := range m.invitations {
		if old.WorkspaceID == inv.WorkspaceID && old.Email == inv.Email {
			inv.ID = old.ID
			m.invitations[i] = inv
			return inv, nil
		}
	}
	inv.ID = domain.NewID()
	m.invitations = append(m.invitations, inv)
	return inv, nil
}

func (m *memInvites) ListInvitations(context.Context, domain.ID) ([]domain.Invitation, error) {
	return m.invitations, nil
}

func (m *memInvites) RenewInvitation(_ context.Context, _, id domain.ID, at time.Time) (domain.Invitation, error) {
	for i := range m.invitations {
		if m.invitations[i].ID == id {
			m.invitations[i].ExpiresAt = at
			return m.invitations[i], nil
		}
	}
	return domain.Invitation{}, domain.ErrInvitationNotFound
}

func (m *memInvites) DeleteInvitation(_ context.Context, _, id domain.ID) error {
	for i := range m.invitations {
		if m.invitations[i].ID == id {
			m.invitations = append(m.invitations[:i], m.invitations[i+1:]...)
			return nil
		}
	}
	return domain.ErrInvitationNotFound
}

func (m *memInvites) TakeInvitations(_ context.Context, email string, now time.Time) ([]domain.Invitation, error) {
	var taken, kept []domain.Invitation
	for _, inv := range m.invitations {
		if inv.Email == email && now.Before(inv.ExpiresAt) {
			taken = append(taken, inv)
		} else {
			kept = append(kept, inv)
		}
	}
	m.invitations = kept
	return taken, nil
}

func (m *memInvites) InviteLink(context.Context, domain.ID) (domain.InviteLink, error) {
	if m.link == nil {
		return domain.InviteLink{}, domain.ErrNotFound
	}
	return *m.link, nil
}

func (m *memInvites) SaveInviteLink(_ context.Context, l domain.InviteLink, _ domain.ID) (domain.InviteLink, error) {
	m.link = &l
	return l, nil
}

func (m *memInvites) DeleteInviteLink(context.Context, domain.ID) error {
	m.link = nil
	return nil
}

func (m *memInvites) InviteLinkByToken(_ context.Context, token string) (domain.InviteLinkInfo, error) {
	if m.link == nil || m.link.Token != token {
		return domain.InviteLinkInfo{}, domain.ErrInviteLinkInvalid
	}
	return domain.InviteLinkInfo{WorkspaceID: m.link.WorkspaceID, WorkspaceName: "Equipo", Role: m.link.Role, ExpiresAt: m.link.ExpiresAt}, nil
}

func (m *memInvites) AddMemberIfAbsent(ctx context.Context, ws, user domain.ID, role domain.Role) (bool, error) {
	if m.find(ws, user) >= 0 {
		return false, nil
	}
	return true, m.AddMember(ctx, ws, user, role)
}

type users map[string]domain.User

func (u users) ByEmail(_ context.Context, email string) (domain.User, error) {
	if user, ok := u[email]; ok {
		return user, nil
	}
	return domain.User{}, domain.ErrUserNotFound
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func invitesEnv(t *testing.T) (*env, *memInvites, users, *clock, *workspace.Invitations) {
	t.Helper()
	e := newEnv(t)
	repo := &memInvites{memRepo: e.repo}
	known := users{"bea@x.com": {ID: domain.NewID(), Email: "bea@x.com"}}
	c := &clock{now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	return e, repo, known, c, workspace.NewInvitations(repo, known, e.tx, c)
}

func TestInviteByEmail(t *testing.T) {
	e, repo, known, c, inv := invitesEnv(t)
	ctx := context.Background()
	admin := e.access(t, domain.RoleAdmin)
	memberEmail := "miembro@x.com"
	known[memberEmail] = domain.User{ID: e.users[domain.RoleMember], Email: memberEmail}

	res, err := inv.Invite(ctx, admin, []string{" Bea@X.com", "nuevo@x.com", memberEmail, "bea@x.com"}, domain.RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 1 || res.Added[0].UserID != known["bea@x.com"].ID || res.Added[0].Role != domain.RoleViewer {
		t.Errorf("added = %+v", res.Added)
	}
	if len(res.Invited) != 1 || res.Invited[0].Email != "nuevo@x.com" || !res.Invited[0].ExpiresAt.Equal(c.now.Add(domain.InvitationTTL)) {
		t.Errorf("invited = %+v", res.Invited)
	}
	if len(res.AlreadyMembers) != 1 || res.AlreadyMembers[0] != memberEmail {
		t.Errorf("already members = %v", res.AlreadyMembers)
	}
	if role, _ := repo.Role(ctx, e.ws, e.users[domain.RoleMember]); role != domain.RoleMember {
		t.Errorf("an existing member's role changed to %q", role)
	}

	if _, err := inv.Invite(ctx, admin, []string{"julia@"}, domain.RoleMember); !errors.As(err, new(*domain.ValidationError)) {
		t.Errorf("invalid email: err = %v", err)
	}
	if _, err := inv.Invite(ctx, admin, []string{"a@x.com"}, "owner"); !errors.As(err, new(*domain.ValidationError)) {
		t.Errorf("invalid role: err = %v", err)
	}
	if _, err := inv.Invite(ctx, e.access(t, domain.RoleMember), []string{"a@x.com"}, domain.RoleMember); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member inviting: err = %v", err)
	}
}

func TestRegisteringAcceptsValidInvitations(t *testing.T) {
	e, repo, _, c, inv := invitesEnv(t)
	ctx := context.Background()
	admin := e.access(t, domain.RoleAdmin)
	if _, err := inv.Invite(ctx, admin, []string{"nuevo@x.com", "tarde@x.com"}, domain.RoleMember); err != nil {
		t.Fatal(err)
	}

	c.now = c.now.Add(domain.InvitationTTL - time.Minute)
	nuevo := domain.User{ID: domain.NewID(), Email: "nuevo@x.com"}
	if err := inv.Onboard(ctx, nuevo); err != nil {
		t.Fatal(err)
	}
	if role, err := repo.Role(ctx, e.ws, nuevo.ID); err != nil || role != domain.RoleMember {
		t.Errorf("invited account: role = %q, %v", role, err)
	}

	c.now = c.now.Add(2 * time.Minute) // past the second one's expiry
	tarde := domain.User{ID: domain.NewID(), Email: "tarde@x.com"}
	_ = inv.Onboard(ctx, tarde)
	if _, err := repo.Role(ctx, e.ws, tarde.ID); !errors.Is(err, domain.ErrNotMember) {
		t.Error("an expired invitation was accepted")
	}
	// It stays pending for the admin to renew.
	pending, _ := inv.List(ctx, admin)
	if len(pending) != 1 || pending[0].Email != "tarde@x.com" {
		t.Fatalf("pending = %+v", pending)
	}
	renewed, err := inv.Renew(ctx, admin, pending[0].ID)
	if err != nil || !renewed.ExpiresAt.Equal(c.now.Add(domain.InvitationTTL)) {
		t.Errorf("Renew = %+v, %v", renewed, err)
	}
}

func TestInviteLink(t *testing.T) {
	e, repo, _, c, inv := invitesEnv(t)
	ctx := context.Background()
	admin := e.access(t, domain.RoleAdmin)

	if link, err := inv.Link(ctx, admin); err != nil || link != nil {
		t.Fatalf("no link yet: %v, %v", link, err)
	}
	first, err := inv.CreateLink(ctx, admin, domain.RoleViewer)
	if err != nil || len(first.Token) < 20 {
		t.Fatalf("CreateLink = %+v, %v", first, err)
	}
	second, _ := inv.CreateLink(ctx, admin, domain.RoleMember)
	if second.Token == first.Token {
		t.Error("a new link reused the token")
	}
	if _, err := inv.Join(ctx, domain.NewID(), first.Token); !errors.Is(err, domain.ErrInviteLinkInvalid) {
		t.Errorf("replaced link: err = %v", err)
	}

	joiner := domain.NewID()
	if info, member, err := inv.LinkInfo(ctx, joiner, second.Token); err != nil || member || info.WorkspaceID != e.ws {
		t.Errorf("LinkInfo = %+v, %v, %v", info, member, err)
	}
	if ws, err := inv.Join(ctx, joiner, second.Token); err != nil || ws != e.ws {
		t.Fatalf("Join = %v, %v", ws, err)
	}
	if role, _ := repo.Role(ctx, e.ws, joiner); role != domain.RoleMember {
		t.Errorf("joined as %q", role)
	}
	// Joining again keeps the role; an admin stays an admin.
	if _, err := inv.Join(ctx, admin.UserID, second.Token); err != nil {
		t.Fatal(err)
	}
	if role, _ := repo.Role(ctx, e.ws, admin.UserID); role != domain.RoleAdmin {
		t.Errorf("admin rejoining became %q", role)
	}

	c.now = c.now.Add(domain.InvitationTTL)
	if _, err := inv.Join(ctx, domain.NewID(), second.Token); !errors.Is(err, domain.ErrInviteLinkInvalid) {
		t.Errorf("expired link: err = %v", err)
	}
	if err := inv.DeleteLink(ctx, admin); err != nil || repo.link != nil {
		t.Errorf("DeleteLink: %v", err)
	}
	if _, err := inv.CreateLink(ctx, e.access(t, domain.RoleMember), domain.RoleMember); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member creating a link: err = %v", err)
	}
}
