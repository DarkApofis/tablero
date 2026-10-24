package postgres_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

func TestTasksVersionGuard(t *testing.T) {
	f := newBoardFixture(t)
	tasks := postgres.NewTasks(f.db)
	ctx := f.ctx
	fields := domain.TaskFields{Title: "Tarea", Priority: domain.PriorityMedium, AssigneeID: &f.user.ID}

	created, err := tasks.Create(ctx, f.columns[0].ID, 1024, fields, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if created.Version != 1 || created.CreatedBy == nil || *created.CreatedBy != f.user.ID || *created.AssigneeID != f.user.ID {
		t.Errorf("created = %+v", created)
	}

	fields.Title = "Editada"
	updated, err := tasks.Update(ctx, created.ID, 1, fields)
	if err != nil || updated.Version != 2 || updated.Title != "Editada" {
		t.Fatalf("Update(v1) = %+v, %v", updated, err)
	}
	// The same version again: the guard rejects it and nothing changes.
	fields.Title = "Perdida"
	if _, err := tasks.Update(ctx, created.ID, 1, fields); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("Update(stale) err = %v, want ErrConflict", err)
	}
	if _, err := tasks.Move(ctx, created.ID, 1, f.columns[1].ID, 5); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("Move(stale) err = %v, want ErrConflict", err)
	}
	if got, _ := tasks.Get(ctx, created.ID); got.Title != "Editada" || got.Version != 2 || got.ColumnID != f.columns[0].ID {
		t.Errorf("stored = %+v", got)
	}

	if _, err := tasks.Create(ctx, domain.NewID(), 1, fields, f.user.ID); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("Create in unknown column: %v", err)
	}
	if err := tasks.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Get(ctx, created.ID); !errors.Is(err, domain.ErrTaskNotFound) {
		t.Errorf("Get(deleted) = %v", err)
	}
	if _, err := tasks.WorkspaceOfTask(ctx, created.ID); !errors.Is(err, domain.ErrNotMember) {
		t.Errorf("WorkspaceOfTask(deleted) = %v", err)
	}
}

func TestTaskNeighborsAndRebalance(t *testing.T) {
	f := newBoardFixture(t)
	tasks := postgres.NewTasks(f.db)
	ctx := f.ctx
	col := f.columns[0].ID
	mk := func(title string, pos float64) domain.Task {
		t.Helper()
		task, err := tasks.Create(ctx, col, pos, domain.TaskFields{Title: title, Priority: domain.PriorityMedium}, f.user.ID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	a, b, c := mk("a", 100), mk("b", 200), mk("c", 300)

	pos := func(p *float64, err error) float64 {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if p == nil {
			return -1
		}
		return *p
	}
	if got := pos(tasks.LastPosition(ctx, col, domain.NilID)); got != 300 {
		t.Errorf("last = %v", got)
	}
	if got := pos(tasks.LastPosition(ctx, col, c.ID)); got != 200 {
		t.Errorf("last without c = %v", got)
	}
	if got := pos(tasks.PositionAfter(ctx, col, b.ID, a)); got != 300 {
		t.Errorf("after a, skipping b = %v", got)
	}
	if got := pos(tasks.PositionBefore(ctx, col, domain.NilID, a)); got != -1 {
		t.Errorf("before a = %v, want none", got)
	}
	if got := pos(tasks.LastPosition(ctx, f.columns[2].ID, domain.NilID)); got != -1 {
		t.Errorf("empty column = %v", got)
	}

	if err := tasks.Rebalance(ctx, col); err != nil {
		t.Fatal(err)
	}
	for want, task := range map[float64]domain.Task{1024: a, 2048: b, 3072: c} {
		if got, _ := tasks.Get(ctx, task.ID); got.Position != want || got.Version != 1 {
			t.Errorf("%s after rebalance = %v (v%d), want %v; rebalancing is not an edit", got.Title, got.Position, got.Version, want)
		}
	}
}

func TestTaskLabelsAndDetail(t *testing.T) {
	f := newBoardFixture(t)
	tasks := postgres.NewTasks(f.db)
	ctx := f.ctx
	bug, _ := f.labels.CreateLabel(ctx, f.board.ID, "Bug", "#ff0000")
	api, _ := f.labels.CreateLabel(ctx, f.board.ID, "API", "#00ff00")
	other, _ := f.boards.CreateBoard(ctx, f.ws, "Otro")
	foreign, _ := f.labels.CreateLabel(ctx, other.ID, "Ajena", "#0000ff")
	created, _ := tasks.Create(ctx, f.columns[0].ID, 1, domain.TaskFields{Title: "T", Priority: domain.PriorityLow, AssigneeID: &f.user.ID}, f.user.ID)

	if n, err := f.boards.LockBoardLabels(ctx, f.board.ID, []domain.ID{bug.ID, api.ID, foreign.ID, domain.NewID()}); err != nil || n != 2 {
		t.Errorf("LockBoardLabels = %d, %v; want only the board's 2", n, err)
	}
	if err := tasks.SetLabels(ctx, created.ID, []domain.ID{bug.ID, api.ID}); err != nil {
		t.Fatal(err)
	}
	d, err := tasks.Detail(ctx, created.ID)
	if err != nil || d.AssigneeName != "Ana" || len(d.Labels) != 2 || d.Labels[0].Name != "API" || d.Priority != domain.PriorityLow {
		t.Errorf("Detail = %+v, %v", d, err)
	}
	if err := tasks.SetLabels(ctx, created.ID, nil); err != nil {
		t.Fatal(err)
	}
	if ls, _ := tasks.Labels(ctx, created.ID); len(ls) != 0 {
		t.Errorf("labels after clearing = %+v", ls)
	}
	if _, err := tasks.Detail(ctx, domain.NewID()); !errors.Is(err, domain.ErrTaskNotFound) {
		t.Errorf("Detail(unknown) = %v", err)
	}

	loc, err := f.boards.ColumnLocation(ctx, f.columns[0].ID)
	if err != nil || loc.BoardID != f.board.ID || loc.WorkspaceID != f.ws {
		t.Errorf("ColumnLocation = %+v, %v", loc, err)
	}
	if _, err := f.boards.ColumnLocation(ctx, domain.NewID()); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("ColumnLocation(unknown) = %v", err)
	}
}

func TestActivityAppendAndList(t *testing.T) {
	f := newBoardFixture(t)
	activity := postgres.NewActivity(f.db)
	ctx := f.ctx
	payload, _ := json.Marshal(map[string]string{"title": "x"})

	var ids []int64
	for _, actor := range []*domain.ID{&f.user.ID, nil} {
		e, err := activity.Append(ctx, domain.NewActivity{WorkspaceID: f.ws, BoardID: f.board.ID, ActorID: actor, Type: domain.ActivityTaskCreated, Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		if e.ID == 0 || e.BoardID != f.board.ID || e.CreatedAt.IsZero() || (actor == nil) != (e.ActorID == nil) {
			t.Errorf("appended = %+v", e)
		}
		ids = append(ids, e.ID)
	}

	page, err := activity.ListBoardActivity(ctx, f.board.ID, ids[1]+1, 10)
	if err != nil || len(page) != 2 || page[0].ID != ids[1] || page[1].ActorName != "Ana" || page[0].ActorID != nil {
		t.Errorf("list = %+v, %v", page, err)
	}
	if older, _ := activity.ListBoardActivity(ctx, f.board.ID, ids[1], 10); len(older) != 1 || older[0].ID != ids[0] {
		t.Errorf("before the newest = %+v", older)
	}
}
