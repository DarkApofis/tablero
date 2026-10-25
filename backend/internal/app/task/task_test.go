package task_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/app/task"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

type env struct {
	t           *testing.T
	store       *memStore
	events      *recorder
	svc         *task.Service
	ws, board   domain.ID
	todo, doing domain.ID
	ana, beto   domain.ID
	now         time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, store: newStore(), events: &recorder{}, ws: domain.NewID(), board: domain.NewID(),
		ana: domain.NewID(), beto: domain.NewID(), now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	e.svc = task.NewService(e.store, e.store, e.store, e.store, e.store, e.events, fixedClock{e.now})
	e.store.boardWS[e.board] = e.ws
	e.todo, e.doing = e.column("Por hacer", 1024), e.column("En curso", 2048)
	e.store.members[e.ws] = map[domain.ID]bool{e.ana: true, e.beto: true}
	e.store.names[e.ana], e.store.names[e.beto] = "Ana", "Beto"
	return e
}

func (e *env) column(name string, pos float64) domain.ID {
	c := domain.Column{ID: domain.NewID(), BoardID: e.board, Name: name, Position: pos}
	e.store.columns[c.ID] = c
	return c.ID
}

func (e *env) as(user domain.ID, role domain.Role) domain.Access {
	return domain.Access{UserID: user, WorkspaceID: e.ws, Role: role}
}

func (e *env) create(title string) domain.TaskDetail {
	e.t.Helper()
	t, err := e.svc.Create(context.Background(), e.as(e.ana, domain.RoleMember), task.CreateInput{ColumnID: e.todo, Title: title})
	if err != nil {
		e.t.Fatal(err)
	}
	return t
}

func (e *env) titles(col domain.ID) string {
	var out []string
	for _, t := range e.store.column(col, domain.NilID) {
		out = append(out, t.Title)
	}
	return strings.Join(out, ",")
}

// --- optimistic concurrency ---

func TestTwoClientsEditingTheSameVersionGetAConflict(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	created := e.create("Diseñar login")
	ana, beto := e.as(e.ana, domain.RoleMember), e.as(e.beto, domain.RoleMember)
	events := len(e.events.events)

	// Both hold version 1; Ana saves first.
	won, err := e.svc.Update(ctx, ana, created.ID, 1, domain.TaskEdit{Title: new("Diseñar login (Ana)")})
	if err != nil || won.Version != 2 {
		t.Fatalf("Ana: %+v, %v", won, err)
	}

	// Beto's edit with version 1 is rejected with the task as it is now.
	_, err = e.svc.Update(ctx, beto, created.ID, 1, domain.TaskEdit{Description: new("Notas de Beto")})
	conflict, ok := errors.AsType[*domain.ConflictError](err)
	if !ok || !errors.Is(err, domain.ErrConflict) || conflict.Reason != domain.ConflictVersion {
		t.Fatalf("err = %v, want a version ConflictError", err)
	}
	if conflict.Current.Version != 2 || conflict.Current.Title != "Diseñar login (Ana)" || conflict.Current.Description != "" {
		t.Errorf("current = %+v, want Ana's result", conflict.Current)
	}
	// The rejected edit wrote nothing and announced nothing.
	if got := e.store.tasks[created.ID]; got.Version != 2 || got.Description != "" {
		t.Errorf("stored = %+v", got)
	}
	if len(e.store.activity) != 2 || len(e.events.events) != events+1 {
		t.Errorf("activity = %d, events = %d; the conflict must log nothing", len(e.store.activity), len(e.events.events)-events)
	}

	// Beto retries with the version from the conflict.
	final, err := e.svc.Update(ctx, beto, created.ID, conflict.Current.Version, domain.TaskEdit{Description: new("Notas de Beto")})
	if err != nil || final.Version != 3 || final.Title != "Diseñar login (Ana)" || final.Description != "Notas de Beto" {
		t.Errorf("retry = %+v, %v", final, err)
	}
}

// The version can also lose at the write: someone commits between our read
// and our update. The repository's guard reports domain.ErrConflict and the
// use case answers with the state read after the rollback.
func TestConflictDetectedByTheWriteGuard(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	created := e.create("Tarea")
	e.store.beforeUpdate = func(id domain.ID) {
		other := e.store.tasks[id]
		other.Title, other.Version = "Cambiada por otro", other.Version+1
		e.store.tasks[id] = other
		e.store.beforeUpdate = nil
	}

	_, err := e.svc.Update(ctx, e.as(e.beto, domain.RoleMember), created.ID, 1, domain.TaskEdit{Priority: new(domain.PriorityHigh)})
	conflict, ok := errors.AsType[*domain.ConflictError](err)
	if !ok {
		t.Fatalf("err = %v, want ConflictError", err)
	}
	// The concurrent change was itself rolled back by the fake transaction;
	// what matters is that the 409 carries a fresh read, not our attempt.
	if conflict.Current.Priority != domain.PriorityMedium || e.store.rollbacks != 1 {
		t.Errorf("current = %+v, rollbacks = %d", conflict.Current, e.store.rollbacks)
	}
}

func TestMoveSharesTheVersionGuard(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	created := e.create("Tarea")
	if _, err := e.svc.Move(ctx, e.as(e.ana, domain.RoleMember), created.ID, task.MoveInput{ColumnID: e.doing, Version: 1}); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Update(ctx, e.as(e.beto, domain.RoleMember), created.ID, 1, domain.TaskEdit{Title: new("x")})
	if c, ok := errors.AsType[*domain.ConflictError](err); !ok || c.Current.ColumnID != e.doing || c.Current.Version != 2 {
		t.Errorf("edit after move: %v", err)
	}
	_, err = e.svc.Move(ctx, e.as(e.beto, domain.RoleMember), created.ID, task.MoveInput{ColumnID: e.todo, Version: 1})
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("stale move: %v", err)
	}
}

// --- roles ---

func TestTaskUseCasesApplyTheRoleMatrix(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	created := e.create("Tarea")
	commits := e.store.commits

	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleMember, domain.RoleViewer, ""} {
		acc := e.as(e.beto, role)
		canRead, canEdit := domain.Can(role, domain.ActionRead), domain.Can(role, domain.ActionEditTasks)
		check := func(name string, allowed bool, err error) {
			t.Helper()
			if allowed == errors.Is(err, domain.ErrForbidden) {
				t.Errorf("%s as %q: err = %v (allowed = %v)", name, role, err, allowed)
			}
		}
		_, err := e.svc.Get(ctx, acc, created.ID)
		check("Get", canRead, err)
		if canEdit {
			continue // the writes are covered elsewhere; here only the denials
		}
		_, err = e.svc.Create(ctx, acc, task.CreateInput{ColumnID: e.todo, Title: "x"})
		check("Create", false, err)
		_, err = e.svc.Update(ctx, acc, created.ID, 1, domain.TaskEdit{Priority: new(domain.PriorityUrgent)})
		check("Update", false, err)
		_, err = e.svc.Move(ctx, acc, created.ID, task.MoveInput{ColumnID: e.doing, Version: 1})
		check("Move", false, err)
		check("Delete", false, e.svc.Delete(ctx, acc, created.ID))
	}
	if e.store.commits != commits || e.store.tasks[created.ID].Version != 1 {
		t.Error("a forbidden call changed something")
	}
	if err := e.svc.Delete(ctx, e.as(e.beto, domain.RoleMember), created.ID); err != nil {
		t.Errorf("member delete: %v", err)
	}
}

// --- create ---

func TestCreateLogsInTheTransactionAndPublishesAfter(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	bug := domain.Label{ID: domain.NewID(), BoardID: e.board, Name: "Bug"}
	e.store.labels[bug.ID] = bug

	created, err := e.svc.Create(ctx, e.as(e.ana, domain.RoleMember), task.CreateInput{
		ColumnID: e.todo, Title: "  Login ", Priority: new(domain.PriorityUrgent), AssigneeID: &e.beto,
		LabelIDs: []domain.ID{bug.ID, bug.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Title != "Login" || created.Position != 1024 || created.AssigneeName != "Beto" || len(created.Labels) != 1 {
		t.Errorf("created = %+v", created)
	}
	if len(e.store.activity) != 1 || len(e.events.events) != 1 {
		t.Fatalf("activity = %d, events = %d", len(e.store.activity), len(e.events.events))
	}
	entry, ev := e.store.activity[0], e.events.events[0]
	var p domain.TaskCreatedPayload
	_ = json.Unmarshal(entry.Payload, &p)
	if entry.Type != domain.ActivityTaskCreated || entry.WorkspaceID != e.ws || entry.BoardID != e.board || *entry.ActorID != e.ana ||
		p.TaskID != created.ID || p.Priority != domain.PriorityUrgent || *p.AssigneeID != e.beto || len(p.LabelIDs) != 1 {
		t.Errorf("entry = %+v, payload = %+v", entry, p)
	}
	if ev.ID != entry.ID || ev.Type != domain.EventTaskCreated || ev.BoardID != e.board || ev.TaskVersion == nil || *ev.TaskVersion != 1 {
		t.Errorf("event = %+v", ev)
	}
	if second := e.create("Otra"); second.Position != 2048 {
		t.Errorf("second position = %v, want appended", second.Position)
	}
}

func TestCreateValidatesAssigneeAndLabels(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	foreign := domain.Label{ID: domain.NewID(), BoardID: domain.NewID(), Name: "Ajena"}
	e.store.labels[foreign.ID] = foreign
	outsider := domain.NewID()

	for name, c := range map[string]struct {
		in    task.CreateInput
		field string
	}{
		"empty title":    {task.CreateInput{ColumnID: e.todo, Title: " "}, "title"},
		"bad priority":   {task.CreateInput{ColumnID: e.todo, Title: "x", Priority: new(domain.Priority("critical"))}, "priority"},
		"non-member":     {task.CreateInput{ColumnID: e.todo, Title: "x", AssigneeID: &outsider}, "assignee_id"},
		"foreign label":  {task.CreateInput{ColumnID: e.todo, Title: "x", LabelIDs: []domain.ID{foreign.ID}}, "label_ids"},
		"unknown label":  {task.CreateInput{ColumnID: e.todo, Title: "x", LabelIDs: []domain.ID{domain.NewID()}}, "label_ids"},
		"long title":     {task.CreateInput{ColumnID: e.todo, Title: strings.Repeat("a", 201)}, "title"},
		"long desc":      {task.CreateInput{ColumnID: e.todo, Title: "x", Description: strings.Repeat("a", 10_001)}, "description"},
		"deleted column": {task.CreateInput{ColumnID: domain.NewID(), Title: "x"}, ""},
	} {
		_, err := e.svc.Create(ctx, e.as(e.ana, domain.RoleMember), c.in)
		if c.field == "" {
			if !errors.Is(err, domain.ErrColumnNotFound) {
				t.Errorf("%s: err = %v", name, err)
			}
			continue
		}
		if v, ok := errors.AsType[*domain.ValidationError](err); !ok || v.Fields[c.field] == "" {
			t.Errorf("%s: err = %v, want field %s", name, err, c.field)
		}
	}
	if len(e.store.tasks) != 0 || len(e.store.activity) != 0 || len(e.events.events) != 0 {
		t.Errorf("invalid creates left tasks=%d activity=%d events=%d", len(e.store.tasks), len(e.store.activity), len(e.events.events))
	}
}

// --- update ---

func TestUpdateLogsTheDiff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	bug := domain.Label{ID: domain.NewID(), BoardID: e.board, Name: "Bug"}
	e.store.labels[bug.ID] = bug
	created := e.create("Tarea")

	due := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	updated, err := e.svc.Update(ctx, e.as(e.beto, domain.RoleMember), created.ID, 1, domain.TaskEdit{
		Priority: new(domain.PriorityHigh), DueDate: domain.Optional[time.Time]{Set: true, Value: &due},
		AssigneeID: domain.Optional[domain.ID]{Set: true, Value: &e.ana}, LabelIDs: &[]domain.ID{bug.ID}, Archived: new(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ArchivedAt == nil || !updated.ArchivedAt.Equal(e.now) || updated.AssigneeName != "Ana" || len(updated.Labels) != 1 {
		t.Errorf("updated = %+v", updated)
	}

	var p domain.TaskUpdatedPayload
	_ = json.Unmarshal(e.store.activity[len(e.store.activity)-1].Payload, &p)
	want := map[string][2]any{
		"priority":    {"medium", "high"},
		"due_date":    {nil, "2026-11-15"},
		"assignee_id": {nil, e.ana.String()},
		"label_ids":   {[]any{}, []any{bug.ID.String()}},
		"archived_at": {nil, e.now.Format(time.RFC3339)},
	}
	if p.Version != 2 || len(p.Changes) != len(want) {
		t.Fatalf("payload = %+v", p)
	}
	for field, fromTo := range want {
		got := p.Changes[field]
		gj, _ := json.Marshal([]any{got.From, got.To})
		wj, _ := json.Marshal([]any{fromTo[0], fromTo[1]})
		if string(gj) != string(wj) {
			t.Errorf("%s = %s, want %s", field, gj, wj)
		}
	}

	if _, err := e.svc.Update(ctx, e.as(e.beto, domain.RoleMember), created.ID, 2, domain.TaskEdit{}); !errors.As(err, new(*domain.ValidationError)) {
		t.Errorf("empty edit: %v", err)
	}
	outsider := domain.NewID()
	_, err = e.svc.Update(ctx, e.as(e.beto, domain.RoleMember), created.ID, 2, domain.TaskEdit{AssigneeID: domain.Optional[domain.ID]{Set: true, Value: &outsider}})
	if v, ok := errors.AsType[*domain.ValidationError](err); !ok || v.Fields["assignee_id"] == "" {
		t.Errorf("non-member assignee: %v", err)
	}
}

// If the activity entry cannot be written, the change does not happen
// either, and nothing is published.
func TestFailedActivityRollsBackTheChange(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	created := e.create("Tarea")
	events := len(e.events.events)
	e.store.failAppend = errBoom

	_, err := e.svc.Update(ctx, e.as(e.ana, domain.RoleMember), created.ID, 1, domain.TaskEdit{Title: new("Cambiada")})
	if !errors.Is(err, errBoom) {
		t.Fatalf("update: err = %v", err)
	}
	_, err = e.svc.Move(ctx, e.as(e.ana, domain.RoleMember), created.ID, task.MoveInput{ColumnID: e.doing, Version: 1})
	if !errors.Is(err, errBoom) {
		t.Fatalf("move: err = %v", err)
	}
	if got := e.store.tasks[created.ID]; got.Title != "Tarea" || got.Version != 1 || got.ColumnID != e.todo {
		t.Errorf("task changed without its entry: %+v", got)
	}
	if len(e.events.events) != events {
		t.Error("an event was published for a rolled-back change")
	}
}

// --- move ---

func TestMoveBetweenNeighbors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	member := e.as(e.ana, domain.RoleMember)
	ids, versions := map[string]domain.ID{}, map[string]int32{}
	for _, n := range []string{"A", "B", "C", "D"} {
		ids[n], versions[n] = e.create(n).ID, 1
	}
	ref := func(n string) *domain.ID {
		if n == "" {
			return nil
		}
		id := ids[n]
		return &id
	}
	for _, s := range []struct {
		task          string
		col           domain.ID
		after, before string
		todo, doing   string
	}{
		{"D", e.todo, "A", "", "A,D,B,C", ""},
		{"C", e.todo, "", "A", "C,A,D,B", ""},
		{"B", e.todo, "C", "A", "C,B,A,D", ""},
		{"C", e.todo, "", "", "B,A,D,C", ""},
		{"A", e.doing, "", "", "B,D,C", "A"},
		{"D", e.doing, "", "A", "B,C", "D,A"},
	} {
		moved, err := e.svc.Move(ctx, member, ids[s.task], task.MoveInput{
			ColumnID: s.col, AfterTaskID: ref(s.after), BeforeTaskID: ref(s.before), Version: versions[s.task],
		})
		if err != nil {
			t.Fatalf("move %s: %v", s.task, err)
		}
		versions[s.task] = moved.Version
		if e.titles(e.todo) != s.todo || e.titles(e.doing) != s.doing {
			t.Fatalf("move %s: todo=%s doing=%s, want %s / %s", s.task, e.titles(e.todo), e.titles(e.doing), s.todo, s.doing)
		}
	}

	// after and before no longer in that order: the client's view is stale.
	_, err := e.svc.Move(ctx, member, ids["B"], task.MoveInput{ColumnID: e.doing, AfterTaskID: ref("A"), BeforeTaskID: ref("D"), Version: versions["B"]})
	if c, ok := errors.AsType[*domain.ConflictError](err); !ok || c.Reason != domain.ConflictNeighbors || c.Current.ID != ids["B"] {
		t.Errorf("inverted neighbors: %v", err)
	}
	for name, in := range map[string]task.MoveInput{
		"itself":           {ColumnID: e.todo, AfterTaskID: ref("B"), Version: versions["B"]},
		"neighbor away":    {ColumnID: e.todo, AfterTaskID: ref("A"), Version: versions["B"]},
		"unknown column":   {ColumnID: domain.NewID(), Version: versions["B"]},
		"unknown neighbor": {ColumnID: e.todo, BeforeTaskID: new(domain.NewID()), Version: versions["B"]},
	} {
		if _, err := e.svc.Move(ctx, member, ids["B"], in); !errors.As(err, new(*domain.ValidationError)) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestMoveRebalancesWhenGapIsExhausted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b, c := e.create("A"), e.create("B"), e.create("C")
	ta := e.store.tasks[a.ID]
	ta.Position = 1
	e.store.tasks[a.ID] = ta
	tb := e.store.tasks[b.ID]
	tb.Position = math.Nextafter(1, 2)
	e.store.tasks[b.ID] = tb

	moved, err := e.svc.Move(ctx, e.as(e.ana, domain.RoleMember), c.ID, task.MoveInput{ColumnID: e.todo, AfterTaskID: &a.ID, BeforeTaskID: &b.ID, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if e.titles(e.todo) != "A,C,B" || moved.Position != 1536 {
		t.Errorf("order = %s, position = %v", e.titles(e.todo), moved.Position)
	}
}
