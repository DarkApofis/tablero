package demo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/adapters/hasher"
	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres/pgtest"
	"github.com/DarkApofis/tablero/backend/internal/adapters/token"
	"github.com/DarkApofis/tablero/backend/internal/app/auth"
	"github.com/DarkApofis/tablero/backend/internal/demo"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

// One database for the package: the demo's users and workspace are global.
func setup(t *testing.T, guests demo.Guests) (*pgxpool.Pool, *demo.Service, *recorder) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	users := postgres.NewUsers(pool)
	authService, err := auth.NewService(users, users, hasher.Argon2id{},
		token.NewIssuer([]byte("0123456789abcdef0123456789abcdef"), 15*time.Minute), clock{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	events := &recorder{}
	return pool, demo.NewPostgres(pool, authService, events, guests), events
}

type clock struct{}

func (clock) Now() time.Time { return time.Now() }

type recorder struct{ events []domain.Event }

func (r *recorder) Publish(_ context.Context, evs ...domain.Event) {
	r.events = append(r.events, evs...)
}

type counts struct {
	Users, Workspaces, Members, Boards, Columns, Tasks, Archived, Labels, TaskLabels, Assigned, Dated, Activity int
}

func count(t *testing.T, pool *pgxpool.Pool, ws domain.ID) counts {
	t.Helper()
	var c counts
	q := func(dst *int, sql string) {
		if err := pool.QueryRow(context.Background(), sql, ws.String()).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	q(&c.Users, `SELECT count(*) FROM users WHERE email LIKE '%@estudionube.example' AND $1::uuid IS NOT NULL`)
	q(&c.Workspaces, `SELECT count(*) FROM workspaces WHERE name = 'Estudio Nube' AND $1::uuid IS NOT NULL`)
	q(&c.Members, `SELECT count(*) FROM workspace_members WHERE workspace_id = $1`)
	q(&c.Boards, `SELECT count(*) FROM boards WHERE workspace_id = $1`)
	q(&c.Columns, `SELECT count(*) FROM columns c JOIN boards b ON b.id = c.board_id WHERE b.workspace_id = $1`)
	q(&c.Tasks, `SELECT count(*) FROM tasks t JOIN columns c ON c.id = t.column_id JOIN boards b ON b.id = c.board_id WHERE b.workspace_id = $1`)
	q(&c.Archived, `SELECT count(*) FROM tasks t JOIN columns c ON c.id = t.column_id JOIN boards b ON b.id = c.board_id WHERE b.workspace_id = $1 AND t.archived_at IS NOT NULL`)
	q(&c.Labels, `SELECT count(*) FROM labels l JOIN boards b ON b.id = l.board_id WHERE b.workspace_id = $1`)
	q(&c.TaskLabels, `SELECT count(*) FROM task_labels tl JOIN labels l ON l.id = tl.label_id JOIN boards b ON b.id = l.board_id WHERE b.workspace_id = $1`)
	q(&c.Assigned, `SELECT count(*) FROM tasks t JOIN columns c ON c.id = t.column_id JOIN boards b ON b.id = c.board_id WHERE b.workspace_id = $1 AND t.assignee_id IS NOT NULL`)
	q(&c.Dated, `SELECT count(*) FROM tasks t JOIN columns c ON c.id = t.column_id JOIN boards b ON b.id = c.board_id WHERE b.workspace_id = $1 AND t.due_date IS NOT NULL`)
	q(&c.Activity, `SELECT count(*) FROM activity_log WHERE workspace_id = $1`)
	return c
}

func TestSeedIsIdempotent(t *testing.T) {
	pool, svc, events := setup(t, demo.Guests{})
	ctx := context.Background()

	first, err := svc.Seed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := count(t, pool, first.WorkspaceID)
	want := counts{
		Users: 3, Workspaces: 1, Members: 3, Boards: 1, Columns: 4, Tasks: len(demo.BoardTasks), Archived: 1, Labels: 6,
		TaskLabels: before.TaskLabels, Assigned: before.Assigned, Dated: before.Dated, Activity: before.Activity,
	}
	if before != want || before.TaskLabels < 20 || before.Assigned < 15 || before.Dated < 15 || before.Activity < 50 {
		t.Fatalf("after the first seed: %+v", before)
	}

	// A guest-made mess the next seed must undo: an extra board, a changed role.
	if _, err := pool.Exec(ctx, `INSERT INTO boards (workspace_id, name) VALUES ($1, 'Tablero de un invitado')`, first.WorkspaceID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workspace_members SET role = 'admin' WHERE user_id = $1`, first.Users["vera"].ID.String()); err != nil {
		t.Fatal(err)
	}

	second, err := svc.Seed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.WorkspaceID != first.WorkspaceID || second.BoardID != first.BoardID {
		t.Errorf("ids changed: %+v then %+v", first, second)
	}
	if after := count(t, pool, second.WorkspaceID); after != before {
		t.Errorf("second seed: %+v, want %+v", after, before)
	}
	var role string
	if err := pool.QueryRow(ctx, `SELECT role FROM workspace_members WHERE user_id = $1`, first.Users["vera"].ID.String()).Scan(&role); err != nil || role != "viewer" {
		t.Errorf("vera's role = %q, %v", role, err)
	}

	// The history happened over the past ten days, in order.
	var oldest, newest time.Time
	var outOfOrder int
	if err := pool.QueryRow(ctx, `SELECT min(created_at), max(created_at),
		count(*) FILTER (WHERE created_at < prev) FROM (
			SELECT created_at, lag(created_at) OVER (ORDER BY id) AS prev FROM activity_log WHERE board_id = $1) a`,
		second.BoardID.String()).Scan(&oldest, &newest, &outOfOrder); err != nil {
		t.Fatal(err)
	}
	if time.Since(oldest) < 9*24*time.Hour || time.Since(newest) < 10*time.Minute || outOfOrder != 0 {
		t.Errorf("activity from %s to %s, %d out of order", oldest, newest, outOfOrder)
	}

	// The documented passwords work, and live streams were told to reload.
	for _, p := range demo.People {
		var hash string
		if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE email = $1`, p.Email).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if ok, err := (hasher.Argon2id{}).Verify(p.Password, hash); !ok || err != nil {
			t.Errorf("%s: documented password rejected (%v)", p.Email, err)
		}
	}
	if len(events.events) != 2 || events.events[1].Type != domain.EventReset || events.events[1].BoardID != first.BoardID {
		t.Errorf("events = %+v", events.events)
	}
}

func TestGuests(t *testing.T) {
	pool, svc, _ := setup(t, demo.Guests{Max: 2, TTL: time.Hour})
	ctx := context.Background()
	res, err := svc.Seed(ctx)
	if err != nil {
		t.Fatal(err)
	}

	guest, err := svc.StartGuest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if guest.BoardID != res.BoardID || guest.AccessToken == "" || guest.RefreshToken == "" {
		t.Errorf("guest = %+v", guest)
	}
	var role string
	if err := pool.QueryRow(ctx, `SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
		res.WorkspaceID.String(), guest.User.ID.String()).Scan(&role); err != nil || role != "member" {
		t.Errorf("guest role = %q, %v", role, err)
	}

	if _, err := svc.StartGuest(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartGuest(ctx); !errors.Is(err, demo.ErrGuestLimit) {
		t.Errorf("third guest with a limit of 2: %v", err)
	}

	// The reset deletes guests older than the TTL (all of them, with 0) and
	// keeps the demo users.
	_, resetter, _ := setup(t, demo.Guests{TTL: 0})
	if err := resetter.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	var guests, members int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email LIKE '%'||$1`, demo.GuestEmailSuffix).Scan(&guests); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workspace_members WHERE workspace_id = $1`, res.WorkspaceID.String()).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if guests != 0 || members != 3 {
		t.Errorf("after reset: %d guests, %d members", guests, members)
	}
}
