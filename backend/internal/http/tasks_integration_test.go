package httpapi

// Integration tests for tasks against a real Postgres (Testcontainers, see
// testdb_test.go): optimistic concurrency with two or more clients editing
// the same task, fractional positions, rebalancing and permissions.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/db"
)

// taskFixture is a workspace with an admin, two members (the "two clients"),
// a viewer, an outsider, and one board with three columns.
type taskFixture struct {
	*dbEnv
	admin, ana, beto, viewer, outsider testUser
	ws, board                          uuid.UUID
	todo, doing, done                  uuid.UUID
}

func newTaskFixture(t *testing.T) *taskFixture {
	e := newDBEnv(t)
	f := &taskFixture{
		dbEnv: e,
		admin: e.user("Admin"), ana: e.user("Ana"), beto: e.user("Beto"),
		viewer: e.user("Viewer"), outsider: e.user("Outsider"),
	}
	f.ws = e.workspace(map[testUser]db.WorkspaceRole{
		f.admin: db.WorkspaceRoleAdmin, f.ana: db.WorkspaceRoleMember,
		f.beto: db.WorkspaceRoleMember, f.viewer: db.WorkspaceRoleViewer,
	})
	f.board = e.board(f.ws, "Sprint")
	f.todo, f.doing, f.done = e.column(f.board, "Por hacer"), e.column(f.board, "En curso"), e.column(f.board, "Hecho")
	return f
}

// createTask goes through the API, like a client would.
func (f *taskFixture) createTask(col uuid.UUID, title string, as testUser) taskResponse {
	f.t.Helper()
	rec := f.do("POST", "/columns/"+col.String()+"/tasks", jsonBody(map[string]string{"title": title, "description": "desc " + title}), as)
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("create task: %d %s", rec.Code, rec.Body)
	}
	return decodeTask(f.t, rec)
}

func (f *taskFixture) getTask(id uuid.UUID, as testUser) taskResponse {
	f.t.Helper()
	rec := f.do("GET", "/tasks/"+id.String(), "", as)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("get task: %d %s", rec.Code, rec.Body)
	}
	return decodeTask(f.t, rec)
}

func (f *taskFixture) patch(id uuid.UUID, body map[string]any, as testUser) *httptest.ResponseRecorder {
	return f.do("PATCH", "/tasks/"+id.String(), jsonBody(body), as)
}

func (f *taskFixture) move(id uuid.UUID, body map[string]any, as testUser) *httptest.ResponseRecorder {
	return f.do("PATCH", "/tasks/"+id.String()+"/move", jsonBody(body), as)
}

// titles lists a column's task titles in order, straight from the database.
func (f *taskFixture) titles(col uuid.UUID) string {
	f.t.Helper()
	tasks, err := f.q.ListTasksByColumn(context.Background(), col)
	if err != nil {
		f.t.Fatal(err)
	}
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.Title
	}
	return strings.Join(out, ",")
}

func decodeTask(t *testing.T, rec *httptest.ResponseRecorder) taskResponse {
	t.Helper()
	var task taskResponse
	if err := json.NewDecoder(rec.Body).Decode(&task); err != nil {
		t.Fatalf("decode task: %v (body %s)", err, rec.Body)
	}
	return task
}

// sameTask compares every field as the API serializes it. (== would compare
// time.Time location pointers, which differ between two decoded responses.)
func sameTask(a, b taskResponse) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func decodeConflict(t *testing.T, rec *httptest.ResponseRecorder) conflictResponse {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rec.Code, rec.Body)
	}
	var c conflictResponse
	if err := json.NewDecoder(rec.Body).Decode(&c); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	return c
}

// --- optimistic concurrency: two clients, one task ---

func TestTwoClientsEditingSameTaskGetConflict(t *testing.T) {
	f := newTaskFixture(t)
	created := f.createTask(f.todo, "Diseñar login", f.admin)

	// Both clients load the task: each holds version 1.
	anaView := f.getTask(created.ID, f.ana)
	betoView := f.getTask(created.ID, f.beto)
	if anaView.Version != 1 || betoView.Version != 1 {
		t.Fatalf("versions = %d, %d; want 1, 1", anaView.Version, betoView.Version)
	}

	// Ana saves first and wins.
	rec := f.patch(created.ID, map[string]any{"title": "Diseñar login (Ana)", "version": anaView.Version}, f.ana)
	if rec.Code != http.StatusOK {
		t.Fatalf("Ana: %d %s", rec.Code, rec.Body)
	}
	afterAna := decodeTask(t, rec)
	if afterAna.Version != 2 || afterAna.Title != "Diseñar login (Ana)" {
		t.Fatalf("after Ana = %+v", afterAna)
	}

	// Beto saves with the version he loaded: 409 with the full current task.
	rec = f.patch(created.ID, map[string]any{"description": "Notas de Beto", "version": betoView.Version}, f.beto)
	conflict := decodeConflict(t, rec)
	if conflict.Error.Code != CodeVersionConflict || conflict.Error.Message == "" {
		t.Errorf("error = %+v", conflict.Error)
	}
	if now := f.getTask(created.ID, f.admin); !sameTask(conflict.Current, now) {
		t.Errorf("409 current state differs from the stored task:\n got  %+v\n want %+v", conflict.Current, now)
	}
	if !sameTask(conflict.Current, afterAna) {
		t.Errorf("409 current = %+v, want Ana's result %+v", conflict.Current, afterAna)
	}

	// Beto's rejected edit changed nothing.
	if stored := f.getTask(created.ID, f.admin); stored.Description != "desc Diseñar login" || stored.Version != 2 {
		t.Errorf("rejected edit leaked: %+v", stored)
	}

	// Beto reconciles using the version from the 409 and succeeds; Ana's title stays.
	rec = f.patch(created.ID, map[string]any{"description": "Notas de Beto", "version": conflict.Current.Version}, f.beto)
	if rec.Code != http.StatusOK {
		t.Fatalf("Beto retry: %d %s", rec.Code, rec.Body)
	}
	final := decodeTask(t, rec)
	if final.Version != 3 || final.Title != "Diseñar login (Ana)" || final.Description != "Notas de Beto" {
		t.Errorf("final = %+v", final)
	}
}

func TestConcurrentEditsOfSameVersionOnlyOneWins(t *testing.T) {
	f := newTaskFixture(t)
	task := f.createTask(f.todo, "Original", f.admin)

	const clients = 10
	type result struct {
		code   int
		title  string
		answer conflictResponse
	}
	results := make([]result, clients)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range clients {
		wg.Go(func() {
			<-start
			title := fmt.Sprintf("Cliente %d", i)
			who := f.ana
			if i%2 == 1 {
				who = f.beto
			}
			rec := f.patch(task.ID, map[string]any{"title": title, "version": 1}, who)
			results[i] = result{code: rec.Code, title: title}
			if rec.Code == http.StatusConflict {
				_ = json.NewDecoder(rec.Body).Decode(&results[i].answer)
			}
		})
	}
	close(start)
	wg.Wait()

	var winners []string
	for _, r := range results {
		switch r.code {
		case http.StatusOK:
			winners = append(winners, r.title)
		case http.StatusConflict:
		default:
			t.Fatalf("unexpected status %d", r.code)
		}
	}
	if len(winners) != 1 {
		t.Fatalf("winners = %v, want exactly one", winners)
	}
	for _, r := range results {
		if r.code == http.StatusConflict && (r.answer.Current.Version != 2 || r.answer.Current.Title != winners[0]) {
			t.Errorf("loser saw current = %+v, want version 2 with title %q", r.answer.Current, winners[0])
		}
	}
	if stored := f.getTask(task.ID, f.admin); stored.Version != 2 || stored.Title != winners[0] {
		t.Errorf("stored = %+v", stored)
	}
}

func TestEditAndMoveShareTheVersionGuard(t *testing.T) {
	f := newTaskFixture(t)
	task := f.createTask(f.todo, "Tarea", f.admin)

	// Ana moves it (v1 -> v2) while Beto still has v1 on screen.
	if rec := f.move(task.ID, map[string]any{"columnId": f.doing, "version": 1}, f.ana); rec.Code != http.StatusOK {
		t.Fatalf("move: %d %s", rec.Code, rec.Body)
	}

	// Beto's edit is rejected, and the 409 tells him where the task is now.
	c := decodeConflict(t, f.patch(task.ID, map[string]any{"title": "Editada", "version": 1}, f.beto))
	if c.Current.ColumnID != f.doing || c.Current.Version != 2 || c.Current.Title != "Tarea" {
		t.Errorf("current = %+v", c.Current)
	}

	// Beto's stale move is rejected too.
	c = decodeConflict(t, f.move(task.ID, map[string]any{"columnId": f.done, "version": 1}, f.beto))
	if c.Error.Code != CodeVersionConflict || c.Current.ColumnID != f.doing {
		t.Errorf("conflict = %+v", c)
	}
	if got := f.titles(f.doing); got != "Tarea" {
		t.Errorf("doing = %q", got)
	}
}

func TestUpdateTaskValidation(t *testing.T) {
	f := newTaskFixture(t)
	task := f.createTask(f.todo, "Tarea", f.admin)

	for _, tt := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"missing version", map[string]any{"title": "x"}, "version"},
		{"version zero", map[string]any{"title": "x", "version": 0}, "version"},
		{"nothing to change", map[string]any{"version": 1}, "title"},
		{"empty title", map[string]any{"title": "  ", "version": 1}, "title"},
		{"title too long", map[string]any{"title": strings.Repeat("a", 201), "version": 1}, "title"},
		{"description too long", map[string]any{"description": strings.Repeat("a", 10_001), "version": 1}, "description"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := f.patch(task.ID, tt.body, f.ana)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body)
			}
			if got := decodeError(t, rec); got.Fields[tt.field] == "" {
				t.Errorf("error = %+v, want field %q", got, tt.field)
			}
		})
	}
	if stored := f.getTask(task.ID, f.admin); stored.Version != 1 {
		t.Errorf("invalid edits changed the task: %+v", stored)
	}
}

// --- create ---

func TestCreateTaskAppendsToColumn(t *testing.T) {
	f := newTaskFixture(t)
	a := f.createTask(f.todo, "A", f.ana)
	b := f.createTask(f.todo, "B", f.beto)

	if a.Position != 1024 || b.Position != 2048 || a.Version != 1 {
		t.Errorf("a = %+v, b = %+v", a, b)
	}
	if a.CreatedBy.UUID != f.ana.ID || b.CreatedBy.UUID != f.beto.ID || a.ColumnID != f.todo {
		t.Errorf("created_by/column wrong: %+v %+v", a, b)
	}

	rec := f.do("POST", "/columns/"+f.todo.String()+"/tasks", `{"title":""}`, f.ana)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty title: status = %d, want 422", rec.Code)
	}
}

// --- move ---

func TestMoveTask(t *testing.T) {
	f := newTaskFixture(t)
	ids := map[string]uuid.UUID{}
	versions := map[string]int32{}
	for _, n := range []string{"A", "B", "C", "D"} {
		task := f.createTask(f.todo, n, f.admin)
		ids[n], versions[n] = task.ID, task.Version
	}
	ref := func(name string) any {
		if name == "" {
			return nil
		}
		return ids[name]
	}

	steps := []struct {
		name          string
		task          string
		column        uuid.UUID
		after, before string
		wantTodo      string
		wantDoing     string
	}{
		{"after only", "D", f.todo, "A", "", "A,D,B,C", ""},
		{"before only, to the start", "C", f.todo, "", "A", "C,A,D,B", ""},
		{"after and before", "B", f.todo, "C", "A", "C,B,A,D", ""},
		{"neither: to the end", "C", f.todo, "", "", "B,A,D,C", ""},
		{"to an empty column", "A", f.doing, "", "", "B,D,C", "A"},
		{"to another column, before a task", "D", f.doing, "", "A", "B,C", "D,A"},
		{"to another column, after a task", "B", f.doing, "D", "", "C", "D,B,A"},
	}
	for _, s := range steps {
		rec := f.move(ids[s.task], map[string]any{
			"columnId": s.column, "afterTaskId": ref(s.after), "beforeTaskId": ref(s.before), "version": versions[s.task],
		}, f.ana)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", s.name, rec.Code, rec.Body)
		}
		moved := decodeTask(t, rec)
		if moved.Version != versions[s.task]+1 || moved.ColumnID != s.column {
			t.Errorf("%s: moved = %+v", s.name, moved)
		}
		versions[s.task] = moved.Version

		if got := f.titles(f.todo); got != s.wantTodo {
			t.Fatalf("%s: todo = %q, want %q", s.name, got, s.wantTodo)
		}
		if got := f.titles(f.doing); got != s.wantDoing {
			t.Fatalf("%s: doing = %q, want %q", s.name, got, s.wantDoing)
		}
	}
}

func TestMoveTaskErrors(t *testing.T) {
	f := newTaskFixture(t)
	a := f.createTask(f.todo, "A", f.admin)
	b := f.createTask(f.todo, "B", f.admin)
	other := f.createTask(f.doing, "Otra", f.admin)
	otherBoardCol := f.column(f.dbEnv.board(f.ws, "Otro tablero"), "X")

	tests := []struct {
		name   string
		body   map[string]any
		status int
		code   string
	}{
		{"missing columnId", map[string]any{"version": 1}, 422, CodeValidation},
		{"missing version", map[string]any{"columnId": f.todo}, 422, CodeValidation},
		{"column does not exist", map[string]any{"columnId": uuid.New(), "version": 1}, 422, CodeValidation},
		{"column of another board", map[string]any{"columnId": otherBoardCol, "version": 1}, 422, CodeValidation},
		{"neighbor in another column", map[string]any{"columnId": f.todo, "afterTaskId": other.ID, "version": 1}, 422, CodeValidation},
		{"neighbor does not exist", map[string]any{"columnId": f.todo, "beforeTaskId": uuid.New(), "version": 1}, 422, CodeValidation},
		{"neighbor is itself", map[string]any{"columnId": f.todo, "afterTaskId": a.ID, "version": 1}, 422, CodeValidation},
		{"after and before inverted", map[string]any{"columnId": f.doing, "afterTaskId": other.ID, "beforeTaskId": other.ID, "version": 1}, 409, CodeNeighborsChanged},
		{"stale version", map[string]any{"columnId": f.todo, "version": 7}, 409, CodeVersionConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := f.move(a.ID, tt.body, f.ana)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tt.status, rec.Body)
			}
			var body struct {
				Error   errorDetail   `json:"error"`
				Current *taskResponse `json:"current"`
			}
			_ = json.NewDecoder(rec.Body).Decode(&body)
			if body.Error.Code != tt.code {
				t.Errorf("code = %q, want %q", body.Error.Code, tt.code)
			}
			if tt.status == http.StatusConflict && (body.Current == nil || body.Current.ID != a.ID) {
				t.Errorf("409 without the current task: %+v", body)
			}
		})
	}

	// Truly inverted neighbors in the target column.
	c := decodeConflict(t, f.move(other.ID, map[string]any{
		"columnId": f.todo, "afterTaskId": b.ID, "beforeTaskId": a.ID, "version": 1,
	}, f.ana))
	if c.Error.Code != CodeNeighborsChanged {
		t.Errorf("inverted neighbors: %+v", c.Error)
	}

	if f.titles(f.todo) != "A,B" || f.titles(f.doing) != "Otra" {
		t.Errorf("failed moves changed the board: todo=%q doing=%q", f.titles(f.todo), f.titles(f.doing))
	}
	if got := f.getTask(a.ID, f.admin); got.Version != 1 {
		t.Errorf("failed moves bumped the version: %+v", got)
	}
}

func TestMoveTaskRebalancesWhenGapIsExhausted(t *testing.T) {
	f := newTaskFixture(t)
	a := f.task(f.todo, "A", 1, f.admin)
	b := f.task(f.todo, "B", math.Nextafter(1, 2), f.admin) // no float fits between A and B
	c := f.task(f.todo, "C", 10, f.admin)

	rec := f.move(c, map[string]any{"columnId": f.todo, "afterTaskId": a, "beforeTaskId": b, "version": 1}, f.ana)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body)
	}
	if got := f.titles(f.todo); got != "A,C,B" {
		t.Errorf("order = %s, want A,C,B", got)
	}
	tasks, _ := f.q.ListTasksByColumn(context.Background(), f.todo)
	if tasks[0].Position != 1024 || tasks[1].Position != 1536 || tasks[2].Position != 2048 {
		t.Errorf("positions = %v, %v, %v; want 1024, 1536, 2048", tasks[0].Position, tasks[1].Position, tasks[2].Position)
	}
	// Rebalancing reorders nothing, so it does not count as an edit of A or B.
	if got := f.getTask(a, f.admin); got.Version != 1 {
		t.Errorf("rebalance bumped A's version: %+v", got)
	}
}

func TestConcurrentMovesIntoSameGapGetDistinctPositions(t *testing.T) {
	f := newTaskFixture(t)
	anchor := f.createTask(f.todo, "anchor", f.admin)
	f.createTask(f.todo, "tail", f.admin)
	var movers []taskResponse
	for i := range 12 {
		movers = append(movers, f.createTask(f.doing, fmt.Sprintf("m%02d", i), f.admin))
	}

	var wg sync.WaitGroup
	codes := make([]int, len(movers))
	for i, m := range movers {
		wg.Go(func() {
			rec := f.move(m.ID, map[string]any{"columnId": f.todo, "afterTaskId": anchor.ID, "version": m.Version}, f.ana)
			codes[i] = rec.Code
		})
	}
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("move %d: status %d", i, code)
		}
	}

	tasks, _ := f.q.ListTasksByColumn(context.Background(), f.todo)
	seen := map[float64]string{}
	for _, tk := range tasks {
		if prev, dup := seen[tk.Position]; dup {
			t.Errorf("%s and %s share position %v", prev, tk.Title, tk.Position)
		}
		seen[tk.Position] = tk.Title
	}
	if len(tasks) != 14 || tasks[0].Title != "anchor" || tasks[len(tasks)-1].Title != "tail" {
		t.Errorf("order = %s", f.titles(f.todo))
	}
}

// --- delete ---

func TestDeleteTask(t *testing.T) {
	f := newTaskFixture(t)
	task := f.createTask(f.todo, "Borrar", f.admin)

	if rec := f.do("DELETE", "/tasks/"+task.ID.String(), "", f.beto); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if f.titles(f.todo) != "" {
		t.Error("task still there")
	}
	// Gone: indistinguishable from a task you cannot see.
	if rec := f.do("GET", "/tasks/"+task.ID.String(), "", f.beto); rec.Code != http.StatusForbidden {
		t.Errorf("get deleted task: %d, want 403", rec.Code)
	}
}

// --- authorization ---

func TestTaskRoutesPermissionMatrix(t *testing.T) {
	type route struct {
		name, method string
		path         func(f *taskFixture, task uuid.UUID) string
		body         func(f *taskFixture) string
		write        bool
		success      int
	}
	routes := []route{
		{"create", "POST", func(f *taskFixture, _ uuid.UUID) string { return "/columns/" + f.todo.String() + "/tasks" },
			func(*taskFixture) string { return `{"title":"Nueva"}` }, true, 201},
		{"get", "GET", func(_ *taskFixture, id uuid.UUID) string { return "/tasks/" + id.String() },
			func(*taskFixture) string { return "" }, false, 200},
		{"edit", "PATCH", func(_ *taskFixture, id uuid.UUID) string { return "/tasks/" + id.String() },
			func(*taskFixture) string { return `{"title":"Editada","version":1}` }, true, 200},
		{"move", "PATCH", func(_ *taskFixture, id uuid.UUID) string { return "/tasks/" + id.String() + "/move" },
			func(f *taskFixture) string { return `{"columnId":"` + f.doing.String() + `","version":1}` }, true, 200},
		{"delete", "DELETE", func(_ *taskFixture, id uuid.UUID) string { return "/tasks/" + id.String() },
			func(*taskFixture) string { return "" }, true, 204},
	}
	actors := []string{"admin", "member", "viewer", "outsider"}

	for _, rt := range routes {
		for _, actor := range actors {
			t.Run(rt.name+"/"+actor, func(t *testing.T) {
				f := newTaskFixture(t)
				task := f.task(f.todo, "Tarea", 1024, f.admin)
				as := map[string]testUser{"admin": f.admin, "member": f.ana, "viewer": f.viewer, "outsider": f.outsider}[actor]

				rec := f.do(rt.method, rt.path(f, task), rt.body(f), as)

				wantStatus, wantMsg := rt.success, ""
				switch {
				case actor == "outsider":
					wantStatus, wantMsg = http.StatusForbidden, msgNoWorkspaceAccess
				case rt.write && actor == "viewer":
					wantStatus, wantMsg = http.StatusForbidden, msgRoleNotAllowed
				}
				if rec.Code != wantStatus {
					t.Fatalf("status = %d, want %d; body = %s", rec.Code, wantStatus, rec.Body)
				}
				if wantStatus == http.StatusForbidden {
					if got := decodeError(t, rec); got.Message != wantMsg {
						t.Errorf("message = %q, want %q", got.Message, wantMsg)
					}
					if f.titles(f.todo) != "Tarea" || f.titles(f.doing) != "" {
						t.Errorf("forbidden request changed tasks: todo=%q doing=%q", f.titles(f.todo), f.titles(f.doing))
					}
				}
			})
		}
	}
}

func TestTaskRoutesDoNotRevealExistence(t *testing.T) {
	f := newTaskFixture(t)
	task := f.createTask(f.todo, "Privada", f.admin)

	for _, rt := range []struct{ method, suffix, body string }{
		{"GET", "", ""},
		{"PATCH", "", `{"title":"x","version":1}`},
		{"PATCH", "/move", `{"columnId":"` + f.doing.String() + `","version":1}`},
		{"DELETE", "", ""},
	} {
		t.Run(rt.method+rt.suffix, func(t *testing.T) {
			var bodies []string
			for _, id := range []string{task.ID.String(), uuid.NewString(), "no-es-un-uuid"} {
				rec := f.do(rt.method, "/tasks/"+id+rt.suffix, rt.body, f.outsider)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("id %s: status = %d, want 403", id, rec.Code)
				}
				bodies = append(bodies, rec.Body.String())
			}
			if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
				t.Errorf("responses differ:\n%s", strings.Join(bodies, "\n"))
			}
		})
	}
}
