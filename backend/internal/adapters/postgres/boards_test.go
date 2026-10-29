package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

type boardFixture struct {
	t       *testing.T
	ctx     context.Context
	db      *pgxpool.Pool
	boards  *postgres.Boards
	labels  *postgres.Labels
	tx      *postgres.TxManager
	ws      domain.ID
	user    domain.User
	board   domain.Board
	columns []domain.Column
}

func newBoardFixture(t *testing.T) *boardFixture {
	t.Helper()
	pool := newPool(t)
	ctx := context.Background()
	f := &boardFixture{
		t: t, ctx: ctx, db: pool, boards: postgres.NewBoards(pool), labels: postgres.NewLabels(pool),
		tx: postgres.NewTxManager(pool),
	}
	f.user, _ = postgres.NewUsers(pool).Create(ctx, uniqueEmail("ana"), "Ana", "x")
	ws, err := postgres.NewWorkspaces(pool).Create(ctx, "Equipo", "", f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.ws = ws.ID
	if f.board, err = f.boards.CreateBoard(ctx, ws.ID, "Sprint", domain.VisibilityWorkspace, f.user.ID); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"A", "B", "C"} {
		c, err := f.boards.CreateColumn(ctx, f.board.ID, name, float64(i+1)*1024)
		if err != nil {
			t.Fatal(err)
		}
		f.columns = append(f.columns, c)
	}
	return f
}

func TestBoardsAndColumns(t *testing.T) {
	f := newBoardFixture(t)
	ctx := f.ctx

	want := domain.Location{WorkspaceID: f.ws, BoardID: f.board.ID}
	if loc, err := f.boards.LocationOfBoard(ctx, f.board.ID); err != nil || loc != want {
		t.Errorf("LocationOfBoard = %v, %v", loc, err)
	}
	if loc, err := f.boards.LocationOfColumn(ctx, f.columns[0].ID); err != nil || loc != want {
		t.Errorf("LocationOfColumn = %v, %v", loc, err)
	}
	for name, err := range map[string]error{
		"board":  second(f.boards.LocationOfBoard(ctx, domain.NewID())),
		"column": second(f.boards.LocationOfColumn(ctx, domain.NewID())),
	} {
		if !errors.Is(err, domain.ErrNotMember) {
			t.Errorf("unknown %s: err = %v, want ErrNotMember", name, err)
		}
	}

	if _, err := f.boards.SetColumnPosition(ctx, f.columns[2].ID, 1); err != nil {
		t.Fatal(err)
	}
	cols, _ := f.boards.ListColumns(ctx, f.board.ID)
	if cols[0].Name != "C" || cols[1].Name != "A" || cols[2].Name != "B" {
		t.Errorf("order = %s %s %s", cols[0].Name, cols[1].Name, cols[2].Name)
	}
	if err := f.boards.RebalanceColumns(ctx, f.board.ID); err != nil {
		t.Fatal(err)
	}
	cols, _ = f.boards.ListColumns(ctx, f.board.ID)
	if cols[0].Position != 1024 || cols[1].Position != 2048 || cols[2].Position != 3072 || cols[0].Name != "C" {
		t.Errorf("after rebalance = %+v", cols)
	}

	if _, err := f.boards.RenameColumn(ctx, domain.NewID(), "x"); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("rename unknown column: %v", err)
	}
	if err := f.boards.DeleteColumn(ctx, domain.NewID()); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("delete unknown column: %v", err)
	}
	if _, err := f.boards.UpdateBoard(ctx, domain.Board{ID: domain.NewID(), Name: "x", Visibility: domain.VisibilityWorkspace}); !errors.Is(err, domain.ErrBoardNotFound) {
		t.Errorf("update unknown board: %v", err)
	}
	if err := f.boards.DeleteBoard(ctx, f.board.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.boards.GetBoard(ctx, f.board.ID); !errors.Is(err, domain.ErrBoardNotFound) {
		t.Errorf("get deleted board: %v", err)
	}
	if left, _ := f.boards.ListColumns(ctx, f.board.ID); len(left) != 0 {
		t.Errorf("columns survived the board: %d", len(left))
	}
}

func TestBoardTaskReads(t *testing.T) {
	f := newBoardFixture(t)
	ctx := f.ctx
	bug, _ := f.labels.CreateLabel(ctx, f.board.ID, "Bug", "#ff0000")
	api, _ := f.labels.CreateLabel(ctx, f.board.ID, "API", "#00ff00")

	insert := func(title string, col domain.ID, pos float64, assignee *domain.ID, archived bool) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		var a any
		if assignee != nil {
			a = uuid.UUID(*assignee)
		}
		err := f.db.QueryRow(ctx, `INSERT INTO tasks (column_id, title, position, assignee_id, priority, due_date, archived_at)
			VALUES ($1, $2, $3, $4, 'high', '2026-11-15', CASE WHEN $5 THEN now() END) RETURNING id`,
			uuid.UUID(col), title, pos, a, archived).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	second := insert("segunda", f.columns[0].ID, 2, nil, false)
	first := insert("primera", f.columns[0].ID, 1, &f.user.ID, false)
	insert("archivada", f.columns[1].ID, 1, nil, true)
	for _, l := range []domain.ID{bug.ID, api.ID} {
		if _, err := f.db.Exec(ctx, "INSERT INTO task_labels (task_id, label_id) VALUES ($1, $2)", first, uuid.UUID(l)); err != nil {
			t.Fatal(err)
		}
	}

	tasks, err := f.boards.ListBoardTasks(ctx, f.board.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != domain.ID(first) || tasks[1].ID != domain.ID(second) {
		t.Fatalf("tasks = %+v", tasks)
	}
	got := tasks[0]
	if got.AssigneeName != "Ana" || got.AssigneeID == nil || *got.AssigneeID != f.user.ID || got.Priority != domain.PriorityHigh ||
		got.DueDate == nil || got.DueDate.Format(domain.DateLayout) != "2026-11-15" || got.Version != 1 || got.ArchivedAt != nil {
		t.Errorf("task = %+v", got)
	}
	if all, _ := f.boards.ListBoardTasks(ctx, f.board.ID, true); len(all) != 3 {
		t.Errorf("with archived: %d", len(all))
	}

	byTask, err := f.boards.ListBoardTaskLabels(ctx, f.board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ls := byTask[domain.ID(first)]; len(ls) != 2 || ls[0].Name != "API" || ls[1].Name != "Bug" || len(byTask) != 1 {
		t.Errorf("labels by task = %+v", byTask)
	}
}

func TestLabels(t *testing.T) {
	f := newBoardFixture(t)
	ctx := f.ctx
	l, err := f.labels.CreateLabel(ctx, f.board.ID, "Bug", "#ff0000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.labels.CreateLabel(ctx, f.board.ID, "Bug", "#000000"); !errors.Is(err, domain.ErrLabelNameTaken) {
		t.Errorf("duplicate: %v", err)
	}
	if _, err := f.labels.CreateLabel(ctx, domain.NewID(), "X", "#000000"); !errors.Is(err, domain.ErrBoardNotFound) {
		t.Errorf("unknown board: %v", err)
	}
	color := "#00ff00"
	if got, err := f.labels.UpdateLabel(ctx, l.ID, nil, &color); err != nil || got.Name != "Bug" || got.Color != color {
		t.Errorf("update color only = %+v, %v", got, err)
	}
	if _, err := f.labels.UpdateLabel(ctx, domain.NewID(), &color, nil); !errors.Is(err, domain.ErrLabelNotFound) {
		t.Errorf("update unknown: %v", err)
	}
	if loc, err := f.labels.LocationOfLabel(ctx, l.ID); err != nil || loc != (domain.Location{WorkspaceID: f.ws, BoardID: f.board.ID}) {
		t.Errorf("LocationOfLabel = %v, %v", loc, err)
	}
	if err := f.labels.DeleteLabel(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.labels.DeleteLabel(ctx, l.ID); !errors.Is(err, domain.ErrLabelNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}

// The transaction travels in the context: repositories called with it write
// inside it, so a failing use case leaves nothing behind.
func TestTxManagerRollsBackEverythingInTheContext(t *testing.T) {
	f := newBoardFixture(t)
	boom := errors.New("boom")
	err := f.tx.InTx(f.ctx, func(ctx context.Context) error {
		if _, err := f.boards.CreateColumn(ctx, f.board.ID, "D", 4096); err != nil {
			return err
		}
		if _, err := f.labels.CreateLabel(ctx, f.board.ID, "Fantasma", "#000000"); err != nil {
			return err
		}
		// Nested calls join the outer transaction.
		return f.tx.InTx(ctx, func(ctx context.Context) error {
			b := f.board
			b.Name = "Renombrado"
			_, err := f.boards.UpdateBoard(ctx, b)
			if err != nil {
				return err
			}
			return boom
		})
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	cols, _ := f.boards.ListColumns(f.ctx, f.board.ID)
	labels, _ := f.labels.ListLabels(f.ctx, f.board.ID)
	b, _ := f.boards.GetBoard(f.ctx, f.board.ID)
	if len(cols) != 3 || len(labels) != 0 || b.Name != "Sprint" {
		t.Errorf("rollback left columns=%d labels=%d name=%q", len(cols), len(labels), b.Name)
	}

	// Committed: visible afterwards.
	if err := f.tx.InTx(f.ctx, func(ctx context.Context) error {
		_, err := f.boards.CreateColumn(ctx, f.board.ID, "D", 4096)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if cols, _ := f.boards.ListColumns(f.ctx, f.board.ID); len(cols) != 4 {
		t.Errorf("columns after commit = %d", len(cols))
	}

	// Read transactions are read-only.
	err = f.tx.InReadTx(f.ctx, func(ctx context.Context) error {
		_, err := f.boards.CreateColumn(ctx, f.board.ID, "E", 5000)
		return err
	})
	if err == nil {
		t.Error("InReadTx allowed a write")
	}
}

func second[T any](_ T, err error) error { return err }
