package httpapi

// Integration tests for priority, due date, assignee, archiving and labels:
// validation, permissions, the version guard, activity entries and the
// board query.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/db"
	"github.com/DarkApofis/tablero/backend/internal/service"
)

// label creates a label on the board through the API.
func (f *taskFixture) label(board uuid.UUID, name, color string) labelResponse {
	f.t.Helper()
	rec := f.do("POST", "/boards/"+board.String()+"/labels", jsonBody(map[string]string{"name": name, "color": color}), f.admin)
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("create label: %d %s", rec.Code, rec.Body)
	}
	var l labelResponse
	if err := json.NewDecoder(rec.Body).Decode(&l); err != nil {
		f.t.Fatal(err)
	}
	return l
}

func (f *taskFixture) createTaskWith(col uuid.UUID, body map[string]any, as testUser) taskResponse {
	f.t.Helper()
	rec := f.do("POST", "/columns/"+col.String()+"/tasks", jsonBody(body), as)
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("create task: %d %s", rec.Code, rec.Body)
	}
	return decodeTask(f.t, rec)
}

func (f *taskFixture) patchOK(id uuid.UUID, body map[string]any, as testUser) taskResponse {
	f.t.Helper()
	rec := f.patch(id, body, as)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("PATCH %v: %d %s", body, rec.Code, rec.Body)
	}
	return decodeTask(f.t, rec)
}

// lastChange returns the changes of the newest activity entry, which must be a task_updated.
func (f *taskFixture) lastChanges() map[string]service.FieldChange {
	f.t.Helper()
	page := f.activity(f.board, "limit=1", f.admin)
	if len(page.Items) != 1 || page.Items[0].Type != service.ActivityTaskUpdated {
		f.t.Fatalf("last activity = %+v", page.Items)
	}
	return decodePayload[service.TaskUpdatedPayload](f.t, page.Items[0].Payload).Changes
}

func labelNames(ls []labelResponse) string {
	names := make([]string, len(ls))
	for i, l := range ls {
		names[i] = l.Name
	}
	return strings.Join(names, ",")
}

func sortedIDs(ids ...uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	slices.Sort(out)
	return out
}

// asJSON normalizes a value decoded into `any` (or a Go value) for comparison.
func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// --- create ---

func TestCreateTaskWithFields(t *testing.T) {
	f := newTaskFixture(t)
	bug, front := f.label(f.board, "Bug", "#FF0000"), f.label(f.board, "Frontend", "#3ee6b0")

	task := f.createTaskWith(f.todo, map[string]any{
		"title": "Arreglar login", "priority": "urgent", "due_date": "2026-11-15",
		"assignee_id": f.beto.ID, "label_ids": []uuid.UUID{front.ID, bug.ID, front.ID},
	}, f.ana)

	if task.Priority != "urgent" || task.DueDate == nil || *task.DueDate != "2026-11-15" || task.ArchivedAt != nil {
		t.Errorf("task = %+v", task)
	}
	if task.Assignee == nil || task.Assignee.ID != f.beto.ID || task.Assignee.Name != "Beto" {
		t.Errorf("assignee = %+v", task.Assignee)
	}
	if got := labelNames(task.Labels); got != "Bug,Frontend" {
		t.Errorf("labels = %q, want Bug,Frontend (by name, deduped)", got)
	}
	if bug.Color != "#ff0000" {
		t.Errorf("label color = %q, want lowercase", bug.Color)
	}
	if got := f.getTask(task.ID, f.viewer); !sameTask(got, task) {
		t.Errorf("GET differs from create:\n got  %+v\n want %+v", got, task)
	}

	created := decodePayload[service.TaskCreatedPayload](t, f.activity(f.board, "limit=1", f.admin).Items[0].Payload)
	if created.Priority != "urgent" || created.DueDate == nil || *created.DueDate != "2026-11-15" ||
		created.AssigneeID == nil || *created.AssigneeID != f.beto.ID ||
		asJSON(created.LabelIDs) != asJSON(sortedIDs(bug.ID, front.ID)) {
		t.Errorf("created payload = %+v", created)
	}

	// Defaults.
	plain := f.createTask(f.todo, "Simple", f.ana)
	if plain.Priority != "medium" || plain.DueDate != nil || plain.Assignee != nil || plain.Labels == nil || len(plain.Labels) != 0 {
		t.Errorf("defaults = %+v", plain)
	}
}

// --- update ---

func TestUpdateTaskFields(t *testing.T) {
	f := newTaskFixture(t)
	bug, front, back := f.label(f.board, "Bug", "#ff0000"), f.label(f.board, "Frontend", "#00ff00"), f.label(f.board, "Backend", "#0000ff")
	task := f.createTask(f.todo, "Tarea", f.ana)

	steps := []struct {
		name  string
		body  map[string]any
		check func(taskResponse) bool
	}{
		{"priority", map[string]any{"priority": "high"},
			func(t taskResponse) bool { return t.Priority == "high" }},
		{"due date", map[string]any{"due_date": "2026-12-01"},
			func(t taskResponse) bool { return t.DueDate != nil && *t.DueDate == "2026-12-01" }},
		{"assignee", map[string]any{"assignee_id": f.ana.ID},
			func(t taskResponse) bool {
				return t.Assignee != nil && t.Assignee.ID == f.ana.ID && t.Assignee.Name == "Ana"
			}},
		{"viewer as assignee", map[string]any{"assignee_id": f.viewer.ID},
			func(t taskResponse) bool { return t.Assignee != nil && t.Assignee.ID == f.viewer.ID }},
		{"labels", map[string]any{"label_ids": []uuid.UUID{bug.ID, front.ID}},
			func(t taskResponse) bool { return labelNames(t.Labels) == "Bug,Frontend" }},
		{"labels replaced", map[string]any{"label_ids": []uuid.UUID{back.ID, front.ID}},
			func(t taskResponse) bool { return labelNames(t.Labels) == "Backend,Frontend" }},
		{"archive", map[string]any{"archived": true},
			func(t taskResponse) bool { return t.ArchivedAt != nil }},
		{"clear with null", map[string]any{"due_date": nil, "assignee_id": nil, "label_ids": []uuid.UUID{}, "archived": false},
			func(t taskResponse) bool {
				return t.DueDate == nil && t.Assignee == nil && len(t.Labels) == 0 && t.ArchivedAt == nil
			}},
	}
	version := task.Version
	for _, s := range steps {
		body := map[string]any{"version": version}
		maps.Copy(body, s.body)
		got := f.patchOK(task.ID, body, f.beto)
		if got.Version != version+1 || !s.check(got) {
			t.Fatalf("%s: got %+v", s.name, got)
		}
		if stored := f.getTask(task.ID, f.admin); !sameTask(stored, got) {
			t.Fatalf("%s: stored task differs from the response", s.name)
		}
		version = got.Version
	}
	// Untouched fields survived every partial edit.
	if final := f.getTask(task.ID, f.admin); final.Title != "Tarea" || final.Priority != "high" || final.Description != "desc Tarea" {
		t.Errorf("final = %+v", final)
	}
}

func TestArchivingTwiceKeepsTheOriginalTime(t *testing.T) {
	f := newTaskFixture(t)
	task := f.createTask(f.todo, "Tarea", f.ana)
	first := f.patchOK(task.ID, map[string]any{"archived": true, "version": 1}, f.ana)
	second := f.patchOK(task.ID, map[string]any{"archived": true, "version": 2}, f.ana)
	if first.ArchivedAt == nil || second.ArchivedAt == nil || !first.ArchivedAt.Equal(*second.ArchivedAt) {
		t.Errorf("archived_at = %v then %v", first.ArchivedAt, second.ArchivedAt)
	}
	if changes := f.lastChanges(); len(changes) != 0 {
		t.Errorf("re-archiving logged changes: %+v", changes)
	}
}

func TestTaskFieldValidation(t *testing.T) {
	f := newTaskFixture(t)
	otherBoard := f.dbEnv.board(f.ws, "Otro")
	foreignLabel := f.label(otherBoard, "Ajena", "#123456")
	own := f.label(f.board, "Propia", "#abcdef")
	task := f.createTask(f.todo, "Tarea", f.ana)
	base := f.activityCount()

	tests := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"unknown priority", map[string]any{"priority": "critical"}, "priority"},
		{"empty priority", map[string]any{"priority": ""}, "priority"},
		{"date with time", map[string]any{"due_date": "2026-11-15T10:00:00Z"}, "due_date"},
		{"date in another format", map[string]any{"due_date": "15/11/2026"}, "due_date"},
		{"impossible date", map[string]any{"due_date": "2026-02-30"}, "due_date"},
		{"assignee not in the workspace", map[string]any{"assignee_id": f.outsider.ID}, "assignee_id"},
		{"assignee does not exist", map[string]any{"assignee_id": uuid.New()}, "assignee_id"},
		{"label of another board", map[string]any{"label_ids": []uuid.UUID{own.ID, foreignLabel.ID}}, "label_ids"},
		{"label does not exist", map[string]any{"label_ids": []uuid.UUID{uuid.New()}}, "label_ids"},
	}
	for _, tt := range tests {
		t.Run("update/"+tt.name, func(t *testing.T) {
			body := map[string]any{"version": 1}
			maps.Copy(body, tt.body)
			rec := f.patch(task.ID, body, f.ana)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body)
			}
			if got := decodeError(t, rec); got.Fields[tt.field] == "" {
				t.Errorf("error = %+v, want field %q", got, tt.field)
			}
		})
		t.Run("create/"+tt.name, func(t *testing.T) {
			body := map[string]any{"title": "Nueva"}
			maps.Copy(body, tt.body)
			rec := f.do("POST", "/columns/"+f.todo.String()+"/tasks", jsonBody(body), f.ana)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body)
			}
			if got := decodeError(t, rec); got.Fields[tt.field] == "" {
				t.Errorf("error = %+v, want field %q", got, tt.field)
			}
		})
	}

	if got := f.getTask(task.ID, f.admin); got.Version != 1 || got.Priority != "medium" || got.Assignee != nil || len(got.Labels) != 0 {
		t.Errorf("invalid edits changed the task: %+v", got)
	}
	if f.titles(f.todo) != "Tarea" || f.activityCount() != base {
		t.Errorf("invalid requests left tasks %q or activity rows %d (want %d)", f.titles(f.todo), f.activityCount(), base)
	}
}

// --- permissions ---

func TestViewerCannotModifyTaskFields(t *testing.T) {
	f := newTaskFixture(t)
	l := f.label(f.board, "Bug", "#ff0000")
	task := f.createTask(f.todo, "Tarea", f.ana)
	before := f.getTask(task.ID, f.admin)

	for _, body := range []map[string]any{
		{"priority": "urgent"},
		{"due_date": "2026-11-15"},
		{"due_date": nil},
		{"assignee_id": f.viewer.ID},
		{"label_ids": []uuid.UUID{l.ID}},
		{"archived": true},
	} {
		body["version"] = 1
		rec := f.patch(task.ID, body, f.viewer)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%v: status = %d, want 403", body, rec.Code)
		}
		if got := decodeError(t, rec); got.Message != msgRoleNotAllowed {
			t.Errorf("%v: message = %q", body, got.Message)
		}
	}
	rec := f.do("POST", "/columns/"+f.todo.String()+"/tasks", jsonBody(map[string]any{
		"title": "Del viewer", "priority": "high", "label_ids": []uuid.UUID{l.ID},
	}), f.viewer)
	if rec.Code != http.StatusForbidden {
		t.Errorf("viewer create: status = %d, want 403", rec.Code)
	}

	if after := f.getTask(task.ID, f.admin); !sameTask(after, before) || f.titles(f.todo) != "Tarea" {
		t.Errorf("viewer requests changed something: %+v", after)
	}
}

// --- optimistic concurrency ---

func TestFieldEditsShareTheVersionGuard(t *testing.T) {
	f := newTaskFixture(t)
	bug := f.label(f.board, "Bug", "#ff0000")
	task := f.createTask(f.todo, "Tarea", f.admin)

	// Ana and Beto both hold version 1. Ana sets priority and a label.
	afterAna := f.patchOK(task.ID, map[string]any{"priority": "urgent", "label_ids": []uuid.UUID{bug.ID}, "version": 1}, f.ana)

	// Each of Beto's stale edits gets 409 with the full current task.
	for _, body := range []map[string]any{
		{"assignee_id": f.beto.ID},
		{"due_date": "2026-11-15"},
		{"label_ids": []uuid.UUID{}},
		{"archived": true},
		{"priority": "low"},
	} {
		body["version"] = 1
		c := decodeConflict(t, f.patch(task.ID, body, f.beto))
		if c.Error.Code != CodeVersionConflict || !sameTask(c.Current, afterAna) {
			t.Errorf("%v: conflict = %+v, want current = Ana's result", body, c)
		}
		if c.Current.Priority != "urgent" || labelNames(c.Current.Labels) != "Bug" {
			t.Errorf("%v: current lacks Ana's fields: %+v", body, c.Current)
		}
	}
	if stored := f.getTask(task.ID, f.admin); !sameTask(stored, afterAna) {
		t.Errorf("a rejected edit leaked: %+v", stored)
	}

	// Beto reconciles with the version from the 409; Ana's changes stay.
	final := f.patchOK(task.ID, map[string]any{"assignee_id": f.beto.ID, "version": afterAna.Version}, f.beto)
	if final.Priority != "urgent" || labelNames(final.Labels) != "Bug" || final.Assignee == nil || final.Assignee.ID != f.beto.ID {
		t.Errorf("final = %+v", final)
	}
}

func TestConcurrentLabelEditsOnlyOneWins(t *testing.T) {
	f := newTaskFixture(t)
	var labels []labelResponse
	for i := range 8 {
		labels = append(labels, f.label(f.board, fmt.Sprintf("L%d", i), "#000000"))
	}
	task := f.createTask(f.todo, "Tarea", f.admin)

	codes := make([]int, len(labels))
	var wg sync.WaitGroup
	for i, l := range labels {
		wg.Go(func() {
			codes[i] = f.patch(task.ID, map[string]any{"label_ids": []uuid.UUID{l.ID}, "version": 1}, f.ana).Code
		})
	}
	wg.Wait()

	winner := -1
	for i, code := range codes {
		switch code {
		case http.StatusOK:
			if winner >= 0 {
				t.Fatalf("two winners: %d and %d", winner, i)
			}
			winner = i
		case http.StatusConflict:
		default:
			t.Fatalf("edit %d: status %d", i, code)
		}
	}
	if winner < 0 {
		t.Fatal("no edit won")
	}
	if got := f.getTask(task.ID, f.admin); got.Version != 2 || labelNames(got.Labels) != labels[winner].Name {
		t.Errorf("stored = %+v, want only %s", got, labels[winner].Name)
	}
}

// --- activity ---

func TestTaskFieldChangesAreLogged(t *testing.T) {
	f := newTaskFixture(t)
	bug, front := f.label(f.board, "Bug", "#ff0000"), f.label(f.board, "Frontend", "#00ff00")
	task := f.createTaskWith(f.todo, map[string]any{"title": "Tarea", "label_ids": []uuid.UUID{bug.ID}}, f.ana)

	steps := []struct {
		body map[string]any
		want map[string][2]any // field -> {from, to}, as JSON values
	}{
		{map[string]any{"priority": "high"}, map[string][2]any{"priority": {"medium", "high"}}},
		{map[string]any{"due_date": "2026-11-15"}, map[string][2]any{"due_date": {nil, "2026-11-15"}}},
		{map[string]any{"due_date": "2026-12-01", "assignee_id": f.beto.ID}, map[string][2]any{
			"due_date": {"2026-11-15", "2026-12-01"}, "assignee_id": {nil, f.beto.ID},
		}},
		{map[string]any{"assignee_id": f.ana.ID}, map[string][2]any{"assignee_id": {f.beto.ID, f.ana.ID}}},
		{map[string]any{"label_ids": []uuid.UUID{front.ID, bug.ID}}, map[string][2]any{
			"label_ids": {sortedIDs(bug.ID), sortedIDs(bug.ID, front.ID)},
		}},
		{map[string]any{"due_date": nil, "assignee_id": nil, "label_ids": []uuid.UUID{}}, map[string][2]any{
			"due_date": {"2026-12-01", nil}, "assignee_id": {f.ana.ID, nil}, "label_ids": {sortedIDs(bug.ID, front.ID), []string{}},
		}},
		// Same values: a new version, but nothing to log as changed.
		{map[string]any{"priority": "high", "label_ids": []uuid.UUID{}}, map[string][2]any{}},
	}
	version := task.Version
	for i, s := range steps {
		s.body["version"] = version
		version = f.patchOK(task.ID, s.body, f.beto).Version

		changes := f.lastChanges()
		if len(changes) != len(s.want) {
			t.Errorf("step %d: changes = %+v, want keys of %v", i, changes, s.want)
		}
		for field, fromTo := range s.want {
			got, ok := changes[field]
			if !ok || asJSON(got.From) != asJSON(fromTo[0]) || asJSON(got.To) != asJSON(fromTo[1]) {
				t.Errorf("step %d: %s = %+v, want from %v to %v", i, field, got, fromTo[0], fromTo[1])
			}
		}
	}

	// Archiving logs the timestamp the task now has.
	archived := f.patchOK(task.ID, map[string]any{"archived": true, "version": version}, f.ana)
	ch := f.lastChanges()["archived_at"]
	if ch.From != nil || asJSON(ch.To) != asJSON(archived.ArchivedAt) {
		t.Errorf("archived_at change = %+v, want null -> %v", ch, archived.ArchivedAt)
	}
}

// --- GET /boards/{id} ---

type boardDetailForTest struct {
	Labels  []labelResponse `json:"labels"`
	Columns []struct {
		Name  string         `json:"name"`
		Tasks []taskResponse `json:"tasks"`
	} `json:"columns"`
}

func (f *taskFixture) boardDetail(query string) boardDetailForTest {
	f.t.Helper()
	rec := f.do("GET", "/boards/"+f.board.String()+query, "", f.viewer)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("GET board: %d %s", rec.Code, rec.Body)
	}
	var b boardDetailForTest
	if err := json.NewDecoder(rec.Body).Decode(&b); err != nil {
		f.t.Fatal(err)
	}
	return b
}

func TestGetBoardIncludesAssigneesAndLabels(t *testing.T) {
	f := newTaskFixture(t)
	bug, front := f.label(f.board, "Bug", "#ff0000"), f.label(f.board, "Frontend", "#00ff00")
	f.label(f.board, "Sin usar", "#0000ff")
	a := f.createTaskWith(f.todo, map[string]any{"title": "A", "assignee_id": f.ana.ID, "label_ids": []uuid.UUID{front.ID, bug.ID}}, f.ana)
	b := f.createTaskWith(f.todo, map[string]any{"title": "B", "priority": "low"}, f.ana)
	c := f.createTaskWith(f.doing, map[string]any{"title": "C", "assignee_id": f.beto.ID, "label_ids": []uuid.UUID{bug.ID}}, f.ana)
	archived := f.createTaskWith(f.todo, map[string]any{"title": "Vieja", "label_ids": []uuid.UUID{bug.ID}}, f.ana)
	f.patchOK(archived.ID, map[string]any{"archived": true, "version": 1}, f.ana)

	board := f.boardDetail("")
	if got := labelNames(board.Labels); got != "Bug,Frontend,Sin usar" {
		t.Errorf("board labels = %q", got)
	}
	todo, doing := board.Columns[0].Tasks, board.Columns[1].Tasks
	if len(todo) != 2 || len(doing) != 1 {
		t.Fatalf("tasks per column = %d, %d; want 2, 1 (archived left out)", len(todo), len(doing))
	}
	// Each task in the board is exactly what GET /tasks/{id} returns.
	for _, want := range []taskResponse{f.getTask(a.ID, f.admin), f.getTask(b.ID, f.admin)} {
		if i := slices.IndexFunc(todo, func(t taskResponse) bool { return t.ID == want.ID }); i < 0 || !sameTask(todo[i], want) {
			t.Errorf("board task differs from GET /tasks/%s", want.ID)
		}
	}
	if !sameTask(doing[0], f.getTask(c.ID, f.admin)) {
		t.Errorf("board task C = %+v", doing[0])
	}
	if todo[0].Assignee.Name != "Ana" || labelNames(todo[0].Labels) != "Bug,Frontend" || todo[1].Assignee != nil || todo[1].Labels == nil {
		t.Errorf("todo = %+v", todo)
	}

	withArchived := f.boardDetail("?include_archived=true")
	if n := len(withArchived.Columns[0].Tasks); n != 3 {
		t.Errorf("with archived: %d tasks in todo, want 3", n)
	}
	if rec := f.do("GET", "/boards/"+f.board.String()+"?include_archived=si", "", f.viewer); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad include_archived: status = %d, want 422", rec.Code)
	}
}

// queryCounter counts the statements a pool runs.
type queryCounter struct{ n atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}
func (c *queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// The board loads with a fixed number of queries, however many tasks,
// assignees and labels it has (no N+1).
func TestGetBoardQueryCountDoesNotGrowWithTasks(t *testing.T) {
	f := newTaskFixture(t)
	cfg, err := pgxpool.ParseConfig(testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	counter := &queryCounter{}
	cfg.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	boards := service.NewBoardService(pool)

	queries := func() int64 {
		t.Helper()
		before := counter.n.Load()
		if _, err := boards.GetBoardDetail(context.Background(), f.board, true); err != nil {
			t.Fatal(err)
		}
		return counter.n.Load() - before
	}

	l1, l2 := f.label(f.board, "Uno", "#111111"), f.label(f.board, "Dos", "#222222")
	f.createTaskWith(f.todo, map[string]any{"title": "t0", "assignee_id": f.ana.ID, "label_ids": []uuid.UUID{l1.ID}}, f.ana)
	small := queries()

	assignees := []uuid.UUID{f.ana.ID, f.beto.ID, f.admin.ID, f.viewer.ID}
	for i := range 30 {
		col := []uuid.UUID{f.todo, f.doing, f.done}[i%3]
		f.createTaskWith(col, map[string]any{
			"title": fmt.Sprintf("t%d", i+1), "assignee_id": assignees[i%4], "label_ids": []uuid.UUID{l1.ID, l2.ID},
		}, f.ana)
	}
	if big := queries(); big != small {
		t.Errorf("queries: %d with 1 task, %d with 31; want the same", small, big)
	}
}

// --- members ---

func TestRemovingMemberUnassignsTheirTasks(t *testing.T) {
	f := newTaskFixture(t)
	otherBoard := f.dbEnv.board(f.ws, "Otro")
	otherCol := f.column(otherBoard, "X")
	mine := f.createTaskWith(f.todo, map[string]any{"title": "De Beto", "assignee_id": f.beto.ID}, f.ana)
	alsoMine := f.createTaskWith(otherCol, map[string]any{"title": "También de Beto", "assignee_id": f.beto.ID}, f.ana)
	anas := f.createTaskWith(f.todo, map[string]any{"title": "De Ana", "assignee_id": f.ana.ID}, f.ana)

	// Beto's task in a workspace he stays in must not be touched.
	ws2 := f.workspace(map[testUser]db.WorkspaceRole{f.beto: db.WorkspaceRoleAdmin})
	elsewhere := f.createTaskWith(f.column(f.dbEnv.board(ws2, "Suyo"), "Y"), map[string]any{"title": "Fuera", "assignee_id": f.beto.ID}, f.beto)

	rec := f.do("DELETE", "/workspaces/"+f.ws.String()+"/members/"+f.beto.ID.String(), "", f.admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove member: %d %s", rec.Code, rec.Body)
	}

	for _, task := range []taskResponse{mine, alsoMine} {
		got := f.getTask(task.ID, f.admin)
		if got.Assignee != nil || got.Version != task.Version+1 {
			t.Errorf("%s after removal = %+v, want unassigned at version %d", task.Title, got, task.Version+1)
		}
	}
	if got := f.getTask(anas.ID, f.admin); got.Assignee == nil || got.Version != anas.Version {
		t.Errorf("Ana's task was touched: %+v", got)
	}
	if got := f.getTask(elsewhere.ID, f.beto); got.Assignee == nil || got.Assignee.ID != f.beto.ID || got.Version != 1 {
		t.Errorf("task in another workspace was touched: %+v", got)
	}

	// Logged as an edit, by the admin, in the task's board.
	ch := f.lastChanges()["assignee_id"]
	if asJSON(ch.From) != asJSON(f.beto.ID) || ch.To != nil {
		t.Errorf("logged change = %+v", ch)
	}
	page := f.activity(otherBoard, "limit=1", f.admin)
	if p := decodePayload[service.TaskUpdatedPayload](t, page.Items[0].Payload); p.TaskID != alsoMine.ID || p.Version != alsoMine.Version+1 ||
		page.Items[0].Actor == nil || page.Items[0].Actor.ID != f.admin.ID {
		t.Errorf("other board entry = %+v / %s", page.Items[0], page.Items[0].Payload)
	}

	// He can no longer be assigned.
	rec = f.patch(mine.ID, map[string]any{"assignee_id": f.beto.ID, "version": mine.Version + 1}, f.ana)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("assigning a removed member: status = %d, want 422", rec.Code)
	}
}

// Assignments racing with the removal of the assignee: each one either
// commits first (and is then unassigned) or sees the removal (and gets 422).
// None may leave a non-member assigned.
func TestAssigningWhileMemberIsRemovedLeavesNoStaleAssignee(t *testing.T) {
	for round := range 5 {
		f := newTaskFixture(t)
		var tasks []taskResponse
		for i := range 10 {
			tasks = append(tasks, f.createTask(f.todo, fmt.Sprintf("r%d-t%d", round, i), f.ana))
		}

		var wg sync.WaitGroup
		start := make(chan struct{})
		codes := make([]int, len(tasks))
		for i, task := range tasks {
			wg.Go(func() {
				<-start
				codes[i] = f.patch(task.ID, map[string]any{"assignee_id": f.beto.ID, "version": 1}, f.ana).Code
			})
		}
		var removeCode int
		wg.Go(func() {
			<-start
			removeCode = f.do("DELETE", "/workspaces/"+f.ws.String()+"/members/"+f.beto.ID.String(), "", f.admin).Code
		})
		close(start)
		wg.Wait()

		if removeCode != http.StatusNoContent {
			t.Fatalf("remove: status %d", removeCode)
		}
		for i, code := range codes {
			if code != http.StatusOK && code != http.StatusUnprocessableEntity {
				t.Fatalf("assign %d: status %d", i, code)
			}
		}
		var stale int
		if err := f.pool.QueryRow(context.Background(),
			"SELECT count(*) FROM tasks WHERE assignee_id = $1", f.beto.ID).Scan(&stale); err != nil {
			t.Fatal(err)
		}
		if stale != 0 {
			t.Fatalf("round %d: %d tasks still assigned to the removed member (codes %v)", round, stale, codes)
		}
	}
}
