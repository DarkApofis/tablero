package httpapi

// Integration tests for administering workspaces and boards: settings,
// ownership, deletion, invitations by email and link, private, archived,
// duplicated and moved boards.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres/db"
	"github.com/DarkApofis/tablero/backend/internal/app/auth"
)

func readJSON[T any](t *testing.T, body interface{ Read([]byte) (int, error) }) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

type wsDetail struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Slug        *string   `json:"slug"`
	Description string    `json:"description"`
	DefaultRole string    `json:"default_role"`
	Role        string    `json:"role"`
	IsOwner     bool      `json:"is_owner"`
	MayTransfer bool      `json:"may_transfer"`
	MemberCount int64     `json:"member_count"`
	BoardCount  int64     `json:"board_count"`
	TaskCount   int64     `json:"task_count"`
}

// ownWorkspace has as create a workspace through the API (they own it) and
// deletes it when the test ends.
func (f *taskFixture) ownWorkspace(as testUser, body string) wsDetail {
	f.t.Helper()
	rec := f.do("POST", "/workspaces", body, as)
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("create workspace: %d %s", rec.Code, rec.Body)
	}
	ws := readJSON[wsDetail](f.t, rec.Body)
	f.t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), "DELETE FROM workspaces WHERE id = $1", ws.ID) })
	return ws
}

func TestWorkspaceSettingsOwnershipAndDeletion(t *testing.T) {
	f := newTaskFixture(t)
	suffix := uuid.NewString()[:8]
	ws := f.ownWorkspace(f.ana, `{"name":"Diseño de producto `+suffix+`"}`)
	if ws.Slug == nil || *ws.Slug != "diseno-de-producto-"+suffix {
		t.Fatalf("slug = %v", ws.Slug)
	}
	path := "/workspaces/" + ws.ID.String()

	// The address is taken now; another one is free.
	avail := readJSON[struct{ Available bool }](t, f.do("GET", "/workspace-slugs?slug="+*ws.Slug, "", f.beto).Body)
	if avail.Available {
		t.Error("a taken address is reported available")
	}
	if rec := f.do("GET", "/workspace-slugs?slug=No%20vale", "", f.beto); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid address: %d", rec.Code)
	}
	if rec := f.do("POST", "/workspaces", `{"name":"Otro","slug":"`+*ws.Slug+`"}`, f.beto); rec.Code != http.StatusConflict || decodeError(t, rec).Code != CodeSlugTaken {
		t.Errorf("creating with a taken address: %d", rec.Code)
	}
	// Only members find it by its address.
	if rec := f.do("GET", "/workspace-slugs/"+*ws.Slug, "", f.beto); rec.Code != http.StatusForbidden {
		t.Errorf("outsider by slug: %d", rec.Code)
	}
	if got := readJSON[struct{ ID uuid.UUID }](t, f.do("GET", "/workspace-slugs/"+*ws.Slug, "", f.ana).Body); got.ID != ws.ID {
		t.Errorf("by slug = %v", got.ID)
	}

	rec := f.do("PATCH", path, `{"name":"Estudio","description":"  Equipo de diseño ","default_role":"viewer","slug":"estudio-`+suffix+`"}`, f.ana)
	d := readJSON[wsDetail](t, rec.Body)
	if rec.Code != http.StatusOK || d.Name != "Estudio" || d.Description != "Equipo de diseño" || d.DefaultRole != "viewer" || *d.Slug != "estudio-"+suffix || !d.IsOwner {
		t.Fatalf("PATCH = %d %+v", rec.Code, d)
	}
	if rec := f.do("PATCH", path, `{"name":" ","default_role":"owner"}`, f.ana); rec.Code != http.StatusUnprocessableEntity || len(decodeError(t, rec).Fields) != 2 {
		t.Errorf("invalid settings: %d", rec.Code)
	}

	// Beto joins as admin; the owner cannot be demoted, removed or leave.
	if err := f.q.AddWorkspaceMember(context.Background(), db.AddWorkspaceMemberParams{WorkspaceID: ws.ID, UserID: f.beto.ID, Role: db.WorkspaceRoleAdmin}); err != nil {
		t.Fatal(err)
	}
	ownerPath := path + "/members/" + f.ana.ID.String()
	for _, tc := range []struct{ method, body string }{{"PATCH", `{"role":"member"}`}, {"DELETE", ""}} {
		if rec := f.do(tc.method, ownerPath, tc.body, f.beto); rec.Code != http.StatusConflict || decodeError(t, rec).Code != CodeOwnerStaysAdmin {
			t.Errorf("%s owner: %d", tc.method, rec.Code)
		}
	}
	if rec := f.do("POST", path+"/transfer", `{"user_id":"`+f.ana.ID.String()+`"}`, f.beto); rec.Code != http.StatusForbidden {
		t.Errorf("transfer by a non-owner: %d", rec.Code)
	}
	rec = f.do("POST", path+"/transfer", `{"user_id":"`+f.beto.ID.String()+`"}`, f.ana)
	if rec.Code != http.StatusOK || readJSON[wsDetail](t, rec.Body).IsOwner {
		t.Fatalf("transfer: %d", rec.Code)
	}
	if got := readJSON[wsDetail](t, f.do("GET", path, "", f.beto).Body); !got.IsOwner || !got.MayTransfer {
		t.Errorf("new owner = %+v", got)
	}

	// Deleting it takes its boards and tasks along, and frees Ana's quota.
	board := f.dbEnv.board(ws.ID, "Tablero")
	col := f.column(board, "Por hacer")
	f.task(col, "Una", 1024, f.ana)
	if got := readJSON[wsDetail](t, f.do("GET", path, "", f.ana).Body); got.BoardCount != 1 || got.TaskCount != 1 {
		t.Errorf("counts = %+v", got)
	}
	if rec := f.do("DELETE", path, "", f.viewer); rec.Code != http.StatusForbidden {
		t.Errorf("non-member deleting: %d", rec.Code)
	}
	if rec := f.do("DELETE", path, "", f.ana); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("GET", "/boards/"+board.String(), "", f.ana); rec.Code != http.StatusForbidden {
		t.Errorf("board of a deleted workspace: %d", rec.Code)
	}
	list := readJSON[struct{ Created int }](t, f.do("GET", "/workspaces", "", f.ana).Body)
	if list.Created != 0 {
		t.Errorf("created after deleting = %d", list.Created)
	}
}

func TestInvitationsByEmailAndLink(t *testing.T) {
	f := newTaskFixture(t)
	path := "/workspaces/" + f.ws.String()
	newEmail := "invitada-" + uuid.NewString()[:8] + "@example.com"
	var outsiderEmail string
	_ = f.pool.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", f.outsider.ID).Scan(&outsiderEmail)

	rec := f.do("POST", path+"/invitations", jsonBody(map[string]any{"emails": []string{outsiderEmail, newEmail}, "role": "viewer"}), f.admin)
	res := readJSON[struct {
		Added []struct {
			UserID uuid.UUID `json:"user_id"`
			Role   string
		}
		Invited []struct {
			ID    uuid.UUID
			Email string
		}
	}](t, rec.Body)
	if rec.Code != http.StatusOK || len(res.Added) != 1 || res.Added[0].UserID != f.outsider.ID || res.Added[0].Role != "viewer" || len(res.Invited) != 1 {
		t.Fatalf("invite: %d %+v", rec.Code, res)
	}
	if rec := f.do("GET", "/boards/"+f.board.String(), "", f.outsider); rec.Code != http.StatusOK {
		t.Errorf("added outsider reading: %d", rec.Code)
	}
	if rec := f.do("POST", path+"/invitations", `{"emails":["julia@"],"role":"member"}`, f.admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid email: %d", rec.Code)
	}

	// Registering with the invited email accepts it.
	deps := f.deps
	deps.Auth = deps.Auth.WithOnboarding(postgres.NewTxManager(f.pool), auth.Onboarders{deps.Invitations})
	h := NewRouter(deps)
	rec = call(h, "POST", "/auth/register", `{"email":"`+newEmail+`","name":"Invitada","password":"supersecreta"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	session := readJSON[struct {
		AccessToken string `json:"access_token"`
		User        struct{ ID uuid.UUID }
	}](t, rec.Body)
	t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", session.User.ID) })
	ws := readJSON[struct {
		Workspaces []struct {
			ID   uuid.UUID
			Role string
		}
	}](t, call(h, "GET", "/workspaces", "", session.AccessToken).Body)
	if len(ws.Workspaces) != 1 || ws.Workspaces[0].ID != f.ws || ws.Workspaces[0].Role != "viewer" {
		t.Errorf("invited account's workspaces = %+v", ws.Workspaces)
	}
	if pending := readJSON[struct{ Invitations []any }](t, f.do("GET", path+"/invitations", "", f.admin).Body); len(pending.Invitations) != 0 {
		t.Errorf("accepted invitation still pending: %+v", pending)
	}

	// The link: whoever has it joins with its role, until it is replaced.
	link := readJSON[struct{ Link struct{ Token string } }](t, f.do("POST", path+"/invite-link", `{"role":"member"}`, f.admin).Body).Link
	stranger := f.user("Desconocida")
	info := readJSON[struct {
		WorkspaceName string `json:"workspace_name"`
		AlreadyMember bool   `json:"already_member"`
	}](t, f.do("GET", "/invite-links/"+link.Token, "", stranger).Body)
	if info.WorkspaceName != "Equipo" || info.AlreadyMember {
		t.Errorf("link info = %+v", info)
	}
	if rec := f.do("POST", "/invite-links/"+link.Token+"/join", "", stranger); rec.Code != http.StatusOK {
		t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("POST", "/columns/"+f.todo.String()+"/tasks", `{"title":"Por enlace"}`, stranger); rec.Code != http.StatusCreated {
		t.Errorf("joined as member creating a task: %d", rec.Code)
	}
	_ = f.do("POST", path+"/invite-link", `{"role":"member"}`, f.admin) // replaces it
	if rec := f.do("POST", "/invite-links/"+link.Token+"/join", "", f.user("Tarde")); rec.Code != http.StatusNotFound || decodeError(t, rec).Code != CodeInviteInvalid {
		t.Errorf("replaced link: %d", rec.Code)
	}
	if rec := f.do("POST", path+"/invite-link", `{"role":"member"}`, f.ana); rec.Code != http.StatusForbidden {
		t.Errorf("member creating a link: %d", rec.Code)
	}
}

func TestPrivateAndArchivedBoards(t *testing.T) {
	f := newTaskFixture(t)
	path := "/boards/" + f.board.String()
	boardNames := func(as testUser, query string) []string {
		list := readJSON[struct{ Boards []struct{ Name string } }](t, f.do("GET", "/workspaces/"+f.ws.String()+"/boards"+query, "", as).Body)
		names := []string{}
		for _, b := range list.Boards {
			names = append(names, b.Name)
		}
		return names
	}

	if rec := f.do("PATCH", path, `{"visibility":"private"}`, f.admin); rec.Code != http.StatusOK {
		t.Fatalf("make private: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("GET", path, "", f.ana); rec.Code != http.StatusForbidden {
		t.Errorf("member reading a private board: %d", rec.Code)
	}
	if rec := f.do("POST", "/columns/"+f.todo.String()+"/tasks", `{"title":"x"}`, f.ana); rec.Code != http.StatusForbidden {
		t.Errorf("member writing on a private board: %d", rec.Code)
	}
	if got := boardNames(f.ana, ""); len(got) != 0 {
		t.Errorf("member lists %v", got)
	}
	if got := boardNames(f.admin, ""); len(got) != 1 {
		t.Errorf("admin lists %v", got)
	}
	// Given to Ana, she sees it; not to an outsider.
	if rec := f.do("PUT", path+"/members", jsonBody(map[string]any{"user_ids": []uuid.UUID{f.ana.ID, f.outsider.ID}}), f.admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("giving it to an outsider: %d", rec.Code)
	}
	if rec := f.do("PUT", path+"/members", jsonBody(map[string]any{"user_ids": []uuid.UUID{f.ana.ID}}), f.admin); rec.Code != http.StatusOK {
		t.Fatalf("give to Ana: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("GET", path, "", f.ana); rec.Code != http.StatusOK {
		t.Errorf("Ana reading it: %d", rec.Code)
	}
	if rec := f.do("GET", path, "", f.beto); rec.Code != http.StatusForbidden {
		t.Errorf("Beto reading it: %d", rec.Code)
	}

	// Archived: listed apart, readable, and nothing changes on it.
	task := f.createTask(f.todo, "Antes", f.ana)
	if rec := f.do("PATCH", path, `{"archived":true}`, f.admin); rec.Code != http.StatusOK {
		t.Fatalf("archive: %d", rec.Code)
	}
	if got := boardNames(f.ana, "?archived=true"); len(got) != 1 {
		t.Errorf("archived list = %v", got)
	}
	if rec := f.do("GET", path, "", f.ana); rec.Code != http.StatusOK {
		t.Errorf("reading an archived board: %d", rec.Code)
	}
	if rec := f.do("PATCH", "/tasks/"+task.ID.String(), `{"title":"Después","version":1}`, f.ana); rec.Code != http.StatusConflict || decodeError(t, rec).Code != CodeBoardArchived {
		t.Errorf("editing on an archived board: %d", rec.Code)
	}
	if rec := f.do("PATCH", path, `{"archived":false}`, f.admin); rec.Code != http.StatusOK {
		t.Errorf("restore: %d", rec.Code)
	}
	if rec := f.do("PATCH", "/tasks/"+task.ID.String(), `{"title":"Después","version":1}`, f.ana); rec.Code != http.StatusOK {
		t.Errorf("editing after restoring: %d %s", rec.Code, rec.Body)
	}
}

func TestDuplicateAndMoveBoard(t *testing.T) {
	f := newTaskFixture(t)
	path := "/boards/" + f.board.String()
	f.exec("INSERT INTO labels (board_id, name, color) VALUES ($1, 'Bug', '#ff0000')", f.board)
	kept := f.createTask(f.todo, "Asignada a Ana", f.admin)
	f.patch(kept.ID, map[string]any{"assignee_id": f.ana.ID, "version": 1}, f.admin)
	archived := f.createTask(f.done, "Archivada", f.admin)
	f.patch(archived.ID, map[string]any{"archived": true, "version": 1}, f.admin)

	rec := f.do("POST", path+"/duplicate", "{}", f.admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body)
	}
	cp := readJSON[struct {
		ID   uuid.UUID
		Name string
	}](t, rec.Body)
	if cp.Name != "Sprint (copia)" || f.columnNames(cp.ID) != f.columnNames(f.board) {
		t.Errorf("copy = %q with columns %q", cp.Name, f.columnNames(cp.ID))
	}
	detail := readJSON[struct {
		Labels  []struct{ Name string }
		Columns []struct{ Tasks []taskResponse }
	}](t, f.do("GET", "/boards/"+cp.ID.String(), "", f.ana).Body)
	if len(detail.Labels) != 1 || len(detail.Columns[0].Tasks) != 1 || len(detail.Columns[2].Tasks) != 0 {
		t.Fatalf("copied content = %+v", detail)
	}
	if a := detail.Columns[0].Tasks[0].Assignee; a == nil || a.ID != f.ana.ID {
		t.Errorf("copied assignee = %+v", a)
	}
	if rec := f.do("POST", path+"/duplicate", "{}", f.ana); rec.Code != http.StatusForbidden {
		t.Errorf("member duplicating: %d", rec.Code)
	}

	// Moving the copy where Ana is not a member unassigns her.
	other := f.ownWorkspace(f.admin, `{"name":"Destino"}`)
	if rec := f.do("POST", "/boards/"+cp.ID.String()+"/move", `{"workspace_id":"`+f.ws.String()+`"}`, f.ana); rec.Code != http.StatusForbidden {
		t.Errorf("member moving: %d", rec.Code)
	}
	beto := f.ownWorkspace(f.beto, `{"name":"De Beto"}`)
	if rec := f.do("POST", "/boards/"+cp.ID.String()+"/move", `{"workspace_id":"`+beto.ID.String()+`"}`, f.admin); rec.Code != http.StatusForbidden {
		t.Errorf("moving to a workspace where the admin is not one: %d", rec.Code)
	}
	rec = f.do("POST", "/boards/"+cp.ID.String()+"/move", `{"workspace_id":"`+other.ID.String()+`"}`, f.admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("move: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("GET", "/boards/"+cp.ID.String(), "", f.ana); rec.Code != http.StatusForbidden {
		t.Errorf("Ana reading the moved board: %d", rec.Code)
	}
	moved := readJSON[struct {
		Columns []struct{ Tasks []taskResponse }
	}](t, f.do("GET", "/boards/"+cp.ID.String(), "", f.admin).Body)
	if moved.Columns[0].Tasks[0].Assignee != nil {
		t.Errorf("moved task still assigned to %+v", moved.Columns[0].Tasks[0].Assignee)
	}
	var activityWS uuid.UUID
	_ = f.pool.QueryRow(context.Background(), "SELECT DISTINCT workspace_id FROM activity_log WHERE board_id = $1", cp.ID).Scan(&activityWS)
	if activityWS != other.ID {
		t.Errorf("activity left in %v", activityWS)
	}
}
