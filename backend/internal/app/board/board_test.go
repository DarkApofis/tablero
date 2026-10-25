package board_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/DarkApofis/tablero/backend/internal/app/board"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// --- fakes of the ports ---

// memBoards is an in-memory BoardRepository.
type memBoards struct {
	boards     map[domain.ID]domain.Board
	columns    map[domain.ID]domain.Column
	tasks      []domain.TaskDetail
	taskLabels map[domain.ID][]domain.Label
	locks      int
	rebalances int
}

func newBoards() *memBoards {
	return &memBoards{boards: map[domain.ID]domain.Board{}, columns: map[domain.ID]domain.Column{}, taskLabels: map[domain.ID][]domain.Label{}}
}

func (m *memBoards) WorkspaceOfBoard(_ context.Context, id domain.ID) (domain.ID, error) {
	if b, ok := m.boards[id]; ok {
		return b.WorkspaceID, nil
	}
	return domain.NilID, domain.ErrNotMember
}

func (m *memBoards) WorkspaceOfColumn(ctx context.Context, id domain.ID) (domain.ID, error) {
	if c, ok := m.columns[id]; ok {
		return m.WorkspaceOfBoard(ctx, c.BoardID)
	}
	return domain.NilID, domain.ErrNotMember
}

func (m *memBoards) CreateBoard(_ context.Context, ws domain.ID, name string) (domain.Board, error) {
	b := domain.Board{ID: domain.NewID(), WorkspaceID: ws, Name: name}
	m.boards[b.ID] = b
	return b, nil
}

func (m *memBoards) ListBoards(_ context.Context, ws domain.ID) ([]domain.Board, error) {
	var out []domain.Board
	for _, b := range m.boards {
		if b.WorkspaceID == ws {
			out = append(out, b)
		}
	}
	return out, nil
}

func (m *memBoards) GetBoard(_ context.Context, id domain.ID) (domain.Board, error) {
	if b, ok := m.boards[id]; ok {
		return b, nil
	}
	return domain.Board{}, domain.ErrBoardNotFound
}

func (m *memBoards) RenameBoard(ctx context.Context, id domain.ID, name string) (domain.Board, error) {
	b, err := m.GetBoard(ctx, id)
	if err != nil {
		return b, err
	}
	b.Name = name
	m.boards[id] = b
	return b, nil
}

func (m *memBoards) DeleteBoard(_ context.Context, id domain.ID) error {
	if _, ok := m.boards[id]; !ok {
		return domain.ErrBoardNotFound
	}
	delete(m.boards, id)
	return nil
}

func (m *memBoards) LockBoard(context.Context, domain.ID) error { m.locks++; return nil }

func (m *memBoards) LockColumn(context.Context, domain.ID) error { return nil }

func (m *memBoards) ColumnTaskIDs(_ context.Context, col domain.ID) ([]domain.ID, error) {
	var out []domain.ID
	for _, t := range m.tasks {
		if t.ColumnID == col {
			out = append(out, t.ID)
		}
	}
	return out, nil
}

func (m *memBoards) ListColumns(_ context.Context, boardID domain.ID) ([]domain.Column, error) {
	var out []domain.Column
	for _, c := range m.columns {
		if c.BoardID == boardID {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b domain.Column) int {
		if a.Position != b.Position {
			return map[bool]int{true: -1, false: 1}[a.Position < b.Position]
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	return out, nil
}

func (m *memBoards) GetColumn(_ context.Context, id domain.ID) (domain.Column, error) {
	if c, ok := m.columns[id]; ok {
		return c, nil
	}
	return domain.Column{}, domain.ErrColumnNotFound
}

func (m *memBoards) CreateColumn(_ context.Context, boardID domain.ID, name string, pos float64) (domain.Column, error) {
	c := domain.Column{ID: domain.NewID(), BoardID: boardID, Name: name, Position: pos}
	m.columns[c.ID] = c
	return c, nil
}

func (m *memBoards) RenameColumn(ctx context.Context, id domain.ID, name string) (domain.Column, error) {
	c, err := m.GetColumn(ctx, id)
	if err != nil {
		return c, err
	}
	c.Name = name
	m.columns[id] = c
	return c, nil
}

func (m *memBoards) SetColumnPosition(ctx context.Context, id domain.ID, pos float64) (domain.Column, error) {
	c, err := m.GetColumn(ctx, id)
	if err != nil {
		return c, err
	}
	c.Position = pos
	m.columns[id] = c
	return c, nil
}

func (m *memBoards) DeleteColumn(_ context.Context, id domain.ID) error {
	if _, ok := m.columns[id]; !ok {
		return domain.ErrColumnNotFound
	}
	delete(m.columns, id)
	return nil
}

func (m *memBoards) RebalanceColumns(ctx context.Context, boardID domain.ID) error {
	m.rebalances++
	cols, _ := m.ListColumns(ctx, boardID)
	for i, c := range cols {
		c.Position = float64(i+1) * domain.PositionStep
		m.columns[c.ID] = c
	}
	return nil
}

func (m *memBoards) ListBoardTasks(_ context.Context, _ domain.ID, includeArchived bool) ([]domain.TaskDetail, error) {
	var out []domain.TaskDetail
	for _, t := range m.tasks {
		if includeArchived || t.ArchivedAt == nil {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *memBoards) ListBoardTaskLabels(context.Context, domain.ID) (map[domain.ID][]domain.Label, error) {
	return m.taskLabels, nil
}

type memLabels struct{ labels map[domain.ID]domain.Label }

func (m *memLabels) WorkspaceOfLabel(context.Context, domain.ID) (domain.ID, error) {
	return domain.NilID, domain.ErrNotMember
}

func (m *memLabels) ListLabels(_ context.Context, boardID domain.ID) ([]domain.Label, error) {
	var out []domain.Label
	for _, l := range m.labels {
		if l.BoardID == boardID {
			out = append(out, l)
		}
	}
	return out, nil
}

func (m *memLabels) CreateLabel(_ context.Context, boardID domain.ID, name, color string) (domain.Label, error) {
	for _, l := range m.labels {
		if l.BoardID == boardID && l.Name == name {
			return domain.Label{}, domain.ErrLabelNameTaken
		}
	}
	l := domain.Label{ID: domain.NewID(), BoardID: boardID, Name: name, Color: color}
	m.labels[l.ID] = l
	return l, nil
}

func (m *memLabels) GetLabelForUpdate(_ context.Context, id domain.ID) (domain.Label, error) {
	l, ok := m.labels[id]
	if !ok {
		return l, domain.ErrLabelNotFound
	}
	return l, nil
}

func (m *memLabels) UpdateLabel(_ context.Context, id domain.ID, name, color *string) (domain.Label, error) {
	l, ok := m.labels[id]
	if !ok {
		return l, domain.ErrLabelNotFound
	}
	if name != nil {
		l.Name = *name
	}
	if color != nil {
		l.Color = *color
	}
	m.labels[id] = l
	return l, nil
}

func (m *memLabels) DeleteLabel(_ context.Context, id domain.ID) error {
	delete(m.labels, id)
	return nil
}

type fakeTx struct{ writes, reads, rollbacks int }

func (f *fakeTx) InTx(ctx context.Context, fn func(context.Context) error) error {
	f.writes++
	if err := fn(ctx); err != nil {
		f.rollbacks++
		return err
	}
	return nil
}

// memLog records appended entries; publisher records published events.
type memLog struct {
	entries []domain.ActivityEntry
	fail    error
}

func (m *memLog) Append(_ context.Context, a domain.NewActivity) (domain.ActivityEntry, error) {
	if m.fail != nil {
		return domain.ActivityEntry{}, m.fail
	}
	e := domain.ActivityEntry{ID: int64(len(m.entries) + 1), BoardID: a.BoardID, ActorID: a.ActorID, Type: a.Type, Payload: a.Payload}
	m.entries = append(m.entries, e)
	return e, nil
}

type publisher struct{ events []domain.Event }

func (p *publisher) Publish(_ context.Context, events ...domain.Event) {
	p.events = append(p.events, events...)
}

func (f *fakeTx) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	f.reads++
	return fn(ctx)
}

type env struct {
	boards *memBoards
	labels *memLabels
	log    *memLog
	pub    *publisher
	tx     *fakeTx
	svc    *board.Service
	lsvc   *board.LabelService
	ws     domain.ID
}

func newEnv() *env {
	e := &env{boards: newBoards(), labels: &memLabels{labels: map[domain.ID]domain.Label{}}, log: &memLog{}, pub: &publisher{}, tx: &fakeTx{}, ws: domain.NewID()}
	e.svc = board.NewService(e.boards, e.labels, e.log, e.tx, e.pub)
	e.lsvc = board.NewLabelService(e.labels, e.log, e.tx, e.pub)
	return e
}

func (e *env) as(role domain.Role) domain.Access {
	return domain.Access{UserID: domain.NewID(), WorkspaceID: e.ws, Role: role}
}

func (e *env) names(t *testing.T, boardID domain.ID) string {
	t.Helper()
	cols, _ := e.boards.ListColumns(context.Background(), boardID)
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return strings.Join(names, ",")
}

// --- authorization ---

// Every use case checks the action it needs against the Access it gets.
func TestUseCasesApplyTheRoleMatrix(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	b, _ := e.boards.CreateBoard(ctx, e.ws, "B")
	col, _ := e.boards.CreateColumn(ctx, b.ID, "C", 1024)
	label, _ := e.labels.CreateLabel(ctx, b.ID, "L", "#000000")
	name := "x"

	calls := []struct {
		name   string
		action domain.Action
		call   func(domain.Access) error
	}{
		{"ListBoards", domain.ActionRead, func(a domain.Access) error { _, err := e.svc.ListBoards(ctx, a); return err }},
		{"GetBoardDetail", domain.ActionRead, func(a domain.Access) error { _, err := e.svc.GetBoardDetail(ctx, a, b.ID, false); return err }},
		{"CreateBoard", domain.ActionManageBoards, func(a domain.Access) error { _, err := e.svc.CreateBoard(ctx, a, "N"); return err }},
		{"RenameBoard", domain.ActionManageBoards, func(a domain.Access) error { _, err := e.svc.RenameBoard(ctx, a, b.ID, "N"); return err }},
		{"CreateColumn", domain.ActionManageBoards, func(a domain.Access) error { _, err := e.svc.CreateColumn(ctx, a, b.ID, "N"); return err }},
		{"RenameColumn", domain.ActionManageBoards, func(a domain.Access) error { _, err := e.svc.RenameColumn(ctx, a, col.ID, "N"); return err }},
		{"MoveColumn", domain.ActionManageBoards, func(a domain.Access) error { _, err := e.svc.MoveColumn(ctx, a, col.ID, nil); return err }},
		{"ListLabels", domain.ActionRead, func(a domain.Access) error { _, err := e.lsvc.List(ctx, a, b.ID); return err }},
		{"CreateLabel", domain.ActionManageLabels, func(a domain.Access) error {
			_, err := e.lsvc.Create(ctx, a, b.ID, domain.NewID().String()[:8], "#111111")
			return err
		}},
		{"UpdateLabel", domain.ActionManageLabels, func(a domain.Access) error { _, err := e.lsvc.Update(ctx, a, label.ID, &name, nil); return err }},
	}
	for _, c := range calls {
		for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleMember, domain.RoleViewer, ""} {
			err := c.call(e.as(role))
			if allowed := domain.Can(role, c.action); allowed && err != nil {
				t.Errorf("%s as %q: err = %v", c.name, role, err)
			} else if !allowed && !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("%s as %q: err = %v, want ErrForbidden", c.name, role, err)
			}
		}
	}
	// Deletes last, as admin only would succeed.
	for _, role := range []domain.Role{domain.RoleViewer, domain.RoleMember} {
		if err := e.svc.DeleteColumn(ctx, e.as(role), col.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("DeleteColumn as %s: %v", role, err)
		}
		if err := e.svc.DeleteBoard(ctx, e.as(role), b.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("DeleteBoard as %s: %v", role, err)
		}
	}
	if err := e.lsvc.Delete(ctx, e.as(domain.RoleViewer), label.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("DeleteLabel as viewer: %v", err)
	}
	if _, ok := e.boards.boards[b.ID]; !ok {
		t.Error("a forbidden call deleted the board")
	}
}

// --- columns ---

func TestCreateColumnAppends(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	b, _ := e.boards.CreateBoard(ctx, e.ws, "B")
	var positions []float64
	for _, n := range []string{"A", "B", "C"} {
		c, err := e.svc.CreateColumn(ctx, e.as(domain.RoleAdmin), b.ID, "  "+n+" ")
		if err != nil {
			t.Fatal(err)
		}
		positions = append(positions, c.Position)
	}
	if !slices.Equal(positions, []float64{1024, 2048, 3072}) || e.names(t, b.ID) != "A,B,C" {
		t.Errorf("positions = %v, names = %s", positions, e.names(t, b.ID))
	}
	if e.boards.locks != 3 || e.tx.writes != 3 {
		t.Errorf("locks = %d, transactions = %d; each append locks the board in a transaction", e.boards.locks, e.tx.writes)
	}
}

func TestMoveColumn(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	admin := e.as(domain.RoleAdmin)
	b, _ := e.boards.CreateBoard(ctx, e.ws, "B")
	ids := map[string]domain.ID{}
	for _, n := range []string{"A", "B", "C", "D"} {
		c, _ := e.svc.CreateColumn(ctx, admin, b.ID, n)
		ids[n] = c.ID
	}
	ref := func(n string) *domain.ID {
		if n == "" {
			return nil
		}
		id := ids[n]
		return &id
	}
	for _, s := range []struct{ col, after, want string }{
		{"D", "", "D,A,B,C"},
		{"A", "C", "D,B,C,A"},
		{"B", "A", "D,C,A,B"},
		{"B", "D", "D,B,C,A"},
	} {
		if _, err := e.svc.MoveColumn(ctx, admin, ids[s.col], ref(s.after)); err != nil {
			t.Fatalf("move %s after %q: %v", s.col, s.after, err)
		}
		if got := e.names(t, b.ID); got != s.want {
			t.Fatalf("move %s after %q: %s, want %s", s.col, s.after, got, s.want)
		}
	}

	if _, err := e.svc.MoveColumn(ctx, admin, ids["A"], ref("A")); !errors.As(err, new(*domain.ValidationError)) {
		t.Errorf("after itself: err = %v", err)
	}
	other := domain.NewID()
	if _, err := e.svc.MoveColumn(ctx, admin, ids["A"], &other); !errors.As(err, new(*domain.ValidationError)) {
		t.Errorf("after an unknown column: err = %v", err)
	}
	if _, err := e.svc.MoveColumn(ctx, admin, domain.NewID(), nil); !errors.Is(err, domain.ErrColumnNotFound) {
		t.Errorf("unknown column: err = %v", err)
	}
}

func TestMoveColumnRebalancesWhenGapIsExhausted(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	b, _ := e.boards.CreateBoard(ctx, e.ws, "B")
	a, _ := e.boards.CreateColumn(ctx, b.ID, "A", 1)
	_, _ = e.boards.CreateColumn(ctx, b.ID, "B", math.Nextafter(1, 2)) // nothing fits between A and B
	c, _ := e.boards.CreateColumn(ctx, b.ID, "C", 10)

	moved, err := e.svc.MoveColumn(ctx, e.as(domain.RoleAdmin), c.ID, &a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.names(t, b.ID) != "A,C,B" || e.boards.rebalances != 1 || moved.Position != 1536 {
		t.Errorf("order = %s, rebalances = %d, position = %v", e.names(t, b.ID), e.boards.rebalances, moved.Position)
	}
}

// --- board detail ---

func TestGetBoardDetailGroupsTasksAndLabels(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	b, _ := e.boards.CreateBoard(ctx, e.ws, "B")
	todo, _ := e.boards.CreateColumn(ctx, b.ID, "Todo", 1)
	empty, _ := e.boards.CreateColumn(ctx, b.ID, "Vacía", 2)
	bug := domain.Label{ID: domain.NewID(), BoardID: b.ID, Name: "Bug"}
	e.labels.labels[bug.ID] = bug
	task := func(title string, archived bool) domain.TaskDetail {
		td := domain.TaskDetail{Task: domain.Task{ID: domain.NewID(), ColumnID: todo.ID, Title: title}}
		if archived {
			td.ArchivedAt = new(td.CreatedAt)
		}
		return td
	}
	first, second, old := task("1", false), task("2", false), task("vieja", true)
	e.boards.tasks = []domain.TaskDetail{first, second, old}
	e.boards.taskLabels[second.ID] = []domain.Label{bug}

	detail, err := e.svc.GetBoardDetail(ctx, e.as(domain.RoleViewer), b.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if e.tx.reads != 1 {
		t.Errorf("read transactions = %d, want one snapshot", e.tx.reads)
	}
	if len(detail.Columns) != 2 || detail.Columns[0].ID != todo.ID || detail.Columns[1].ID != empty.ID || len(detail.Labels) != 1 {
		t.Fatalf("detail = %+v", detail)
	}
	tasks := detail.Columns[0].Tasks
	if len(tasks) != 2 || tasks[0].Title != "1" || tasks[1].Title != "2" || len(tasks[0].Labels) != 0 || tasks[1].Labels[0].Name != "Bug" {
		t.Errorf("tasks = %+v", tasks)
	}
	if len(detail.Columns[1].Tasks) != 0 {
		t.Errorf("empty column = %+v", detail.Columns[1].Tasks)
	}

	withArchived, _ := e.svc.GetBoardDetail(ctx, e.as(domain.RoleViewer), b.ID, true)
	if len(withArchived.Columns[0].Tasks) != 3 {
		t.Errorf("with archived: %d tasks", len(withArchived.Columns[0].Tasks))
	}
	if _, err := e.svc.GetBoardDetail(ctx, e.as(domain.RoleViewer), domain.NewID(), false); !errors.Is(err, domain.ErrBoardNotFound) {
		t.Errorf("unknown board: err = %v", err)
	}
}

// --- labels ---

func TestLabelValidationAndNormalization(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	member := e.as(domain.RoleMember)
	boardID := domain.NewID()

	l, err := e.lsvc.Create(ctx, member, boardID, "  Urgente ", "#FF00AA")
	if err != nil || l.Name != "Urgente" || l.Color != "#ff00aa" {
		t.Fatalf("Create = %+v, %v", l, err)
	}
	if _, err := e.lsvc.Create(ctx, member, boardID, "Urgente", "#000000"); !errors.Is(err, domain.ErrLabelNameTaken) {
		t.Errorf("duplicate: err = %v", err)
	}
	for _, c := range []struct{ name, color, field string }{
		{"", "#ff0000", "name"}, {strings.Repeat("x", 51), "#ff0000", "name"}, {"x", "red", "color"}, {"x", "#fff", "color"},
	} {
		_, err := e.lsvc.Create(ctx, member, boardID, c.name, c.color)
		if v, ok := errors.AsType[*domain.ValidationError](err); !ok || v.Fields[c.field] == "" {
			t.Errorf("Create(%q, %q): err = %v, want field %s", c.name, c.color, err, c.field)
		}
	}
	if _, err := e.lsvc.Update(ctx, member, l.ID, nil, nil); err == nil {
		t.Error("Update with nothing to change succeeded")
	}
	color := "#00AAFF"
	if got, err := e.lsvc.Update(ctx, member, l.ID, nil, &color); err != nil || got.Color != "#00aaff" || got.Name != "Urgente" {
		t.Errorf("Update color = %+v, %v", got, err)
	}
}

// --- events ---

func payloadOf[T any](t *testing.T, e domain.Event) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(e.Payload, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// Column and label changes are logged in their transaction and published
// once it commits, one event each.
func TestColumnAndLabelChangesArePublishedAfterCommit(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	admin := e.as(domain.RoleAdmin)
	b, _ := e.boards.CreateBoard(ctx, e.ws, "B")

	a, _ := e.svc.CreateColumn(ctx, admin, b.ID, "A")
	c, _ := e.svc.CreateColumn(ctx, admin, b.ID, "C")
	if _, err := e.svc.RenameColumn(ctx, admin, c.ID, "Hecho"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MoveColumn(ctx, admin, c.ID, nil); err != nil {
		t.Fatal(err)
	}
	task := domain.TaskDetail{Task: domain.Task{ID: domain.NewID(), ColumnID: a.ID}}
	e.boards.tasks = append(e.boards.tasks, task)
	if err := e.svc.DeleteColumn(ctx, admin, a.ID); err != nil {
		t.Fatal(err)
	}
	l, _ := e.lsvc.Create(ctx, e.as(domain.RoleMember), b.ID, "Bug", "#ff0000")
	color := "#00ff00"
	if _, err := e.lsvc.Update(ctx, e.as(domain.RoleMember), l.ID, nil, &color); err != nil {
		t.Fatal(err)
	}
	if err := e.lsvc.Delete(ctx, e.as(domain.RoleMember), l.ID); err != nil {
		t.Fatal(err)
	}

	want := []domain.EventType{
		domain.EventColumnCreated, domain.EventColumnCreated, domain.EventColumnUpdated, domain.EventColumnReordered,
		domain.EventColumnDeleted, domain.EventLabelChanged, domain.EventLabelChanged, domain.EventLabelChanged,
	}
	if len(e.pub.events) != len(want) || len(e.log.entries) != len(want) {
		t.Fatalf("published %d, logged %d; want %d each", len(e.pub.events), len(e.log.entries), len(want))
	}
	for i, ev := range e.pub.events {
		if ev.Type != want[i] || ev.ID != e.log.entries[i].ID || ev.BoardID != b.ID || ev.ActorID == nil {
			t.Errorf("event %d = %+v, want %s", i, ev, want[i])
		}
		if ev.TaskVersion != nil {
			t.Errorf("event %d: a column or label event has a task version", i)
		}
	}
	if p := payloadOf[domain.ColumnUpdatedPayload](t, e.pub.events[2]); p.Name != "Hecho" || p.Changes["name"].From != "C" {
		t.Errorf("rename payload = %+v", p)
	}
	if p := payloadOf[domain.ColumnReorderedPayload](t, e.pub.events[3]); p.From != 2048 || p.To != 0 || p.Rebalanced != nil {
		t.Errorf("reorder payload = %+v", p)
	}
	if p := payloadOf[domain.ColumnDeletedPayload](t, e.pub.events[4]); p.ColumnID != a.ID || len(p.DeletedTaskIDs) != 1 || p.DeletedTaskIDs[0] != task.ID {
		t.Errorf("delete payload = %+v", p)
	}
	for i, action := range []string{domain.LabelCreated, domain.LabelUpdated, domain.LabelDeleted} {
		p := payloadOf[domain.LabelChangedPayload](t, e.pub.events[5+i])
		if p.Action != action || p.Label.ID != l.ID {
			t.Errorf("label event %d = %+v", i, p)
		}
	}
	if p := payloadOf[domain.LabelChangedPayload](t, e.pub.events[6]); p.Changes["color"].To != "#00ff00" || p.Label.Color != "#00ff00" {
		t.Errorf("label update payload = %+v", p)
	}

	// If the entry cannot be written the change fails, and nothing is published.
	e.log.fail = errors.New("boom")
	published := len(e.pub.events)
	if _, err := e.svc.CreateColumn(ctx, admin, b.ID, "X"); err == nil {
		t.Error("CreateColumn succeeded without its activity entry")
	}
	if _, err := e.lsvc.Create(ctx, e.as(domain.RoleMember), b.ID, "Y", "#000000"); err == nil {
		t.Error("label Create succeeded without its activity entry")
	}
	if len(e.pub.events) != published || e.tx.rollbacks != 2 {
		t.Errorf("published %d more, rollbacks %d", len(e.pub.events)-published, e.tx.rollbacks)
	}
}
