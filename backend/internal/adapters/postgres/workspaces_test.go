package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

func TestWorkspaces(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	users := postgres.NewUsers(pool)
	repo := postgres.NewWorkspaces(pool)

	ana, _ := users.Create(ctx, uniqueEmail("ana"), "Ana", "x")
	beto, _ := users.Create(ctx, uniqueEmail("beto"), "Beto", "x")
	ws, err := repo.Create(ctx, "Equipo")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AddMember(ctx, ws.ID, ana.ID, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddMember(ctx, ws.ID, beto.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}

	if role, err := repo.Role(ctx, ws.ID, beto.ID); err != nil || role != domain.RoleViewer {
		t.Errorf("Role = %q, %v", role, err)
	}
	if _, err := repo.Role(ctx, ws.ID, domain.NewID()); !errors.Is(err, domain.ErrNotMember) {
		t.Errorf("Role(outsider) err = %v", err)
	}
	if _, err := repo.Role(ctx, domain.NewID(), ana.ID); !errors.Is(err, domain.ErrNotMember) {
		t.Errorf("Role(unknown workspace) err = %v", err)
	}
	if m, err := repo.Member(ctx, ws.ID, beto.ID); err != nil || m.Name != "Beto" || m.Role != domain.RoleViewer {
		t.Errorf("Member = %+v, %v", m, err)
	}
	if _, err := repo.Member(ctx, ws.ID, domain.NewID()); !errors.Is(err, domain.ErrMemberNotFound) {
		t.Errorf("Member(unknown) err = %v", err)
	}

	list, err := repo.ListForUser(ctx, beto.ID)
	if err != nil || len(list) != 1 || list[0].ID != ws.ID || list[0].Role != domain.RoleViewer || list[0].MemberCount != 2 {
		t.Errorf("ListForUser = %+v, %v", list, err)
	}
	if err := repo.UpdateRole(ctx, ws.ID, beto.ID, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.CountAdmins(ctx, ws.ID); n != 2 {
		t.Errorf("admins = %d", n)
	}
	if err := repo.LockMembership(ctx, ws.ID, beto.ID); err != nil {
		t.Errorf("LockMembership(member) = %v", err)
	}
	if err := repo.LockMembership(ctx, ws.ID, domain.NewID()); !errors.Is(err, domain.ErrNotMember) {
		t.Errorf("LockMembership(outsider) = %v", err)
	}
}

func TestUnassignTasksLogsEachTask(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	users := postgres.NewUsers(pool)
	repo := postgres.NewWorkspaces(pool)
	admin, _ := users.Create(ctx, uniqueEmail("admin"), "Admin", "x")
	beto, _ := users.Create(ctx, uniqueEmail("beto"), "Beto", "x")
	ws, _ := repo.Create(ctx, "Equipo")

	var board, col uuid.UUID
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	if err := pool.QueryRow(ctx, "INSERT INTO boards (workspace_id, name) VALUES ($1, 'B') RETURNING id", uuid.UUID(ws.ID)).Scan(&board); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO columns (board_id, name, position) VALUES ($1, 'C', 1) RETURNING id", board).Scan(&col); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"a", "b"} {
		exec("INSERT INTO tasks (column_id, title, position, assignee_id) VALUES ($1, $2, 1, $3)", col, title, uuid.UUID(beto.ID))
	}
	exec("INSERT INTO tasks (column_id, title, position) VALUES ($1, 'sin asignar', 2)", col)

	entries, err := repo.UnassignTasks(ctx, ws.ID, beto.ID, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	for _, e := range entries {
		var p struct {
			Version int32 `json:"version"`
			Changes map[string]struct {
				From any `json:"from"`
				To   any `json:"to"`
			} `json:"changes"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		ch := p.Changes["assignee_id"]
		if e.ID == 0 || e.Type != domain.ActivityTaskUpdated || e.WorkspaceID != ws.ID || e.BoardID != domain.ID(board) ||
			e.ActorID == nil || *e.ActorID != admin.ID || p.Version != 2 || ch.From != beto.ID.String() || ch.To != nil {
			t.Errorf("entry = %+v, payload = %s", e, e.Payload)
		}
	}
	var left int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM tasks WHERE assignee_id = $1", uuid.UUID(beto.ID)).Scan(&left)
	if left != 0 {
		t.Errorf("%d tasks still assigned", left)
	}
}
