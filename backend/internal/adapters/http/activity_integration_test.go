package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

func (f *taskFixture) activity(board uuid.UUID, query string, as testUser) activityPageResponse {
	f.t.Helper()
	path := "/boards/" + board.String() + "/activity"
	if query != "" {
		path += "?" + query
	}
	rec := f.do("GET", path, "", as)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
	}
	var page activityPageResponse
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		f.t.Fatal(err)
	}
	return page
}

func (f *taskFixture) activityCount() int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM activity_log WHERE board_id = $1", f.board).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func decodePayload[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode payload %s: %v", raw, err)
	}
	return v
}

func TestActivityRecordsTaskChanges(t *testing.T) {
	f := newTaskFixture(t)

	task := f.createTask(f.todo, "Login", f.ana)
	rec := f.patch(task.ID, map[string]any{"title": "Login con Google", "version": 1}, f.beto)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit title: %d %s", rec.Code, rec.Body)
	}
	rec = f.patch(task.ID, map[string]any{"description": "OAuth", "version": 2}, f.ana)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit description: %d %s", rec.Code, rec.Body)
	}
	rec = f.move(task.ID, map[string]any{"columnId": f.doing, "version": 3}, f.beto)
	if rec.Code != http.StatusOK {
		t.Fatalf("move: %d %s", rec.Code, rec.Body)
	}
	moved := decodeTask(t, rec)

	page := f.activity(f.board, "", f.viewer)
	if len(page.Items) != 4 || page.NextCursor != nil {
		t.Fatalf("items = %d, next = %v; want 4 and no next page", len(page.Items), page.NextCursor)
	}
	// Newest first.
	types := []string{page.Items[0].Type, page.Items[1].Type, page.Items[2].Type, page.Items[3].Type}
	if strings.Join(types, ",") != "task_moved,task_updated,task_updated,task_created" {
		t.Fatalf("types = %v", types)
	}
	actors := []uuid.UUID{f.beto.ID, f.ana.ID, f.beto.ID, f.ana.ID}
	for i, item := range page.Items {
		if item.Actor == nil || item.Actor.ID != actors[i] {
			t.Errorf("item %d actor = %+v, want %v", i, item.Actor, actors[i])
		}
		if i > 0 && item.ID >= page.Items[i-1].ID {
			t.Errorf("ids not strictly decreasing: %d then %d", page.Items[i-1].ID, item.ID)
		}
	}
	if page.Items[3].Actor.Name != "Ana" {
		t.Errorf("actor name = %q", page.Items[3].Actor.Name)
	}

	created := decodePayload[domain.TaskCreatedPayload](t, page.Items[3].Payload)
	if created.TaskID != domain.ID(task.ID) || created.Title != "Login" || created.ColumnID != domain.ID(f.todo) || created.Position != 1024 {
		t.Errorf("created payload = %+v", created)
	}

	titleEdit := decodePayload[domain.TaskUpdatedPayload](t, page.Items[2].Payload)
	if titleEdit.Version != 2 || len(titleEdit.Changes) != 1 ||
		titleEdit.Changes["title"] != (domain.FieldChange{From: "Login", To: "Login con Google"}) {
		t.Errorf("title edit payload = %+v", titleEdit)
	}

	descEdit := decodePayload[domain.TaskUpdatedPayload](t, page.Items[1].Payload)
	if descEdit.Version != 3 || len(descEdit.Changes) != 1 ||
		descEdit.Changes["description"] != (domain.FieldChange{From: "desc Login", To: "OAuth"}) {
		t.Errorf("description edit payload = %+v", descEdit)
	}

	move := decodePayload[domain.TaskMovedPayload](t, page.Items[0].Payload)
	if move.Version != 4 || move.Title != "Login con Google" ||
		move.From != (domain.TaskPlacement{ColumnID: domain.ID(f.todo), Position: 1024}) ||
		move.To != (domain.TaskPlacement{ColumnID: domain.ID(f.doing), Position: moved.Position}) {
		t.Errorf("move payload = %+v", move)
	}
}

func TestRejectedChangesAreNotLogged(t *testing.T) {
	f := newTaskFixture(t)
	task := f.createTask(f.todo, "Tarea", f.ana)
	base := f.activityCount() // the creation

	attempts := []struct {
		name   string
		status int
		do     func() int
	}{
		{"stale edit", 409, func() int { return f.patch(task.ID, map[string]any{"title": "x", "version": 9}, f.ana).Code }},
		{"invalid edit", 422, func() int { return f.patch(task.ID, map[string]any{"title": "", "version": 1}, f.ana).Code }},
		{"stale move", 409, func() int { return f.move(task.ID, map[string]any{"columnId": f.doing, "version": 9}, f.ana).Code }},
		{"invalid move", 422, func() int {
			return f.move(task.ID, map[string]any{"columnId": f.doing, "afterTaskId": uuid.New(), "version": 1}, f.ana).Code
		}},
		{"viewer edit", 403, func() int { return f.patch(task.ID, map[string]any{"title": "x", "version": 1}, f.viewer).Code }},
		{"invalid create", 422, func() int {
			return f.do("POST", "/columns/"+f.todo.String()+"/tasks", `{"title":""}`, f.ana).Code
		}},
	}
	for _, a := range attempts {
		if got := a.do(); got != a.status {
			t.Fatalf("%s: status = %d, want %d", a.name, got, a.status)
		}
		if n := f.activityCount(); n != base {
			t.Fatalf("%s: activity rows = %d, want %d", a.name, n, base)
		}
	}
}

// The entry is written with the change's own transaction: if the insert into
// activity_log fails, the change must not be committed either. A temporary
// CHECK constraint makes the insert fail for one activity type.
func TestActivityIsWrittenInTheSameTransaction(t *testing.T) {
	f := newTaskFixture(t)
	task := f.createTask(f.todo, "Tarea", f.ana)

	rejectType := func(t *testing.T, typ string) {
		t.Helper()
		f.exec(`ALTER TABLE activity_log ADD CONSTRAINT test_reject_activity CHECK (type <> '` + typ + `') NOT VALID`)
		t.Cleanup(func() { f.exec(`ALTER TABLE activity_log DROP CONSTRAINT IF EXISTS test_reject_activity`) })
	}

	t.Run("create", func(t *testing.T) {
		rejectType(t, domain.ActivityTaskCreated)
		rec := f.do("POST", "/columns/"+f.todo.String()+"/tasks", `{"title":"Fantasma"}`, f.ana)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if got := f.titles(f.todo); got != "Tarea" {
			t.Errorf("task was created without its activity entry: %q", got)
		}
	})

	t.Run("update", func(t *testing.T) {
		rejectType(t, domain.ActivityTaskUpdated)
		rec := f.patch(task.ID, map[string]any{"title": "Cambiada", "version": 1}, f.ana)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if got := f.getTask(task.ID, f.ana); got.Title != "Tarea" || got.Version != 1 {
			t.Errorf("task was updated without its activity entry: %+v", got)
		}
	})

	t.Run("move", func(t *testing.T) {
		rejectType(t, domain.ActivityTaskMoved)
		rec := f.move(task.ID, map[string]any{"columnId": f.doing, "version": 1}, f.ana)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if got := f.getTask(task.ID, f.ana); got.ColumnID != f.todo || got.Version != 1 {
			t.Errorf("task was moved without its activity entry: %+v", got)
		}
	})

	if n := f.activityCount(); n != 1 {
		t.Errorf("activity rows = %d, want only the original creation", n)
	}
}

func TestActivityPagination(t *testing.T) {
	f := newTaskFixture(t)
	for _, title := range []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7"} {
		f.createTask(f.todo, title, f.ana)
	}
	titleOf := func(item activityItem) string {
		return decodePayload[domain.TaskCreatedPayload](t, item.Payload).Title
	}

	var got []string
	seen := map[int64]bool{}
	cursor := ""
	for pageNum := 1; ; pageNum++ {
		q := "limit=3"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		page := f.activity(f.board, q, f.ana)
		var titles []string
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("item %d returned twice", item.ID)
			}
			seen[item.ID] = true
			titles = append(titles, titleOf(item))
		}
		got = append(got, strings.Join(titles, ","))

		if pageNum == 1 {
			// New activity after the first page must not shift later pages.
			f.createTask(f.todo, "nueva", f.ana)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
		if pageNum > 5 {
			t.Fatal("pagination does not end")
		}
	}
	if strings.Join(got, " | ") != "t7,t6,t5 | t4,t3,t2 | t1" {
		t.Errorf("pages = %q", strings.Join(got, " | "))
	}

	// An exact multiple of the limit ends without an empty extra page.
	page := f.activity(f.board, "limit=8", f.ana)
	if len(page.Items) != 8 || page.NextCursor != nil {
		t.Errorf("limit=8: items = %d, next = %v; want 8 and null", len(page.Items), page.NextCursor)
	}
}

func TestActivityQueryValidation(t *testing.T) {
	f := newTaskFixture(t)
	for _, q := range []struct{ query, field string }{
		{"limit=0", "limit"},
		{"limit=101", "limit"},
		{"limit=abc", "limit"},
		{"cursor=%25%25%25", "cursor"},
		{"cursor=" + url.QueryEscape("MA"), "cursor"}, // base64url("0")
		{"cursor=bm8tbnVtZXJv", "cursor"},             // base64url("no-numero")
	} {
		rec := f.do("GET", "/boards/"+f.board.String()+"/activity?"+q.query, "", f.ana)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422", q.query, rec.Code)
			continue
		}
		if got := decodeError(t, rec); got.Fields[q.field] == "" {
			t.Errorf("%s: error = %+v", q.query, got)
		}
	}
}

func TestActivityIsScopedToTheBoard(t *testing.T) {
	f := newTaskFixture(t)
	other := f.dbEnv.board(f.ws, "Otro")
	otherCol := f.column(other, "X")
	f.createTask(f.todo, "aquí", f.ana)
	f.createTask(otherCol, "allá", f.ana)

	page := f.activity(f.board, "", f.ana)
	if len(page.Items) != 1 || decodePayload[domain.TaskCreatedPayload](t, page.Items[0].Payload).Title != "aquí" {
		t.Errorf("items = %+v", page.Items)
	}
}

func TestActivityAccess(t *testing.T) {
	f := newTaskFixture(t)
	f.createTask(f.todo, "Tarea", f.ana)

	for _, u := range []testUser{f.admin, f.ana, f.viewer} {
		if rec := f.do("GET", "/boards/"+f.board.String()+"/activity", "", u); rec.Code != http.StatusOK {
			t.Errorf("member: status = %d, want 200", rec.Code)
		}
	}
	var bodies []string
	for _, id := range []string{f.board.String(), uuid.NewString(), "no-es-un-uuid"} {
		rec := f.do("GET", "/boards/"+id+"/activity", "", f.outsider)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("outsider, board %s: status = %d, want 403", id, rec.Code)
		}
		bodies = append(bodies, rec.Body.String())
	}
	if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
		t.Errorf("responses differ:\n%s", strings.Join(bodies, "\n"))
	}
}

func TestActivityKeepsEntriesOfDeletedUsers(t *testing.T) {
	f := newTaskFixture(t)
	gone := f.user("Temporal")
	f.exec("INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'member')", f.ws, gone.ID)
	f.createTask(f.todo, "Huérfana", gone)
	f.exec("DELETE FROM users WHERE id = $1", gone.ID)

	page := f.activity(f.board, "", f.ana)
	if len(page.Items) != 1 || page.Items[0].Actor != nil {
		t.Errorf("items = %+v, want one entry with actor null", page.Items)
	}
}
