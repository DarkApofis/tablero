package task_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// memStore is an in-memory implementation of every port of the task use
// cases. InTx snapshots the state and restores it if fn fails, so tests can
// check that a failed change leaves nothing behind (task, labels, activity).
type memStore struct {
	tasks      map[domain.ID]domain.Task
	taskLabels map[domain.ID][]domain.ID
	labels     map[domain.ID]domain.Label
	columns    map[domain.ID]domain.Column
	boardWS    map[domain.ID]domain.ID
	members    map[domain.ID]map[domain.ID]bool // workspace -> user
	names      map[domain.ID]string
	activity   []domain.ActivityEntry

	// Test hooks.
	failAppend   error
	beforeUpdate func(id domain.ID) // runs inside Update, before the version guard
	commits      int
	rollbacks    int
}

func newStore() *memStore {
	return &memStore{
		tasks: map[domain.ID]domain.Task{}, taskLabels: map[domain.ID][]domain.ID{}, labels: map[domain.ID]domain.Label{},
		columns: map[domain.ID]domain.Column{}, boardWS: map[domain.ID]domain.ID{}, members: map[domain.ID]map[domain.ID]bool{},
		names: map[domain.ID]string{},
	}
}

type snapshot struct {
	tasks      map[domain.ID]domain.Task
	taskLabels map[domain.ID][]domain.ID
	activity   []domain.ActivityEntry
}

func (m *memStore) InTx(ctx context.Context, fn func(context.Context) error) error {
	snap := snapshot{maps.Clone(m.tasks), maps.Clone(m.taskLabels), slices.Clone(m.activity)}
	if err := fn(ctx); err != nil {
		m.tasks, m.taskLabels, m.activity = snap.tasks, snap.taskLabels, snap.activity
		m.rollbacks++
		return err
	}
	m.commits++
	return nil
}

func (m *memStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

// --- TaskRepository ---

func (m *memStore) WorkspaceOfTask(_ context.Context, id domain.ID) (domain.ID, error) {
	t, ok := m.tasks[id]
	if !ok {
		return domain.NilID, domain.ErrNotMember
	}
	return m.boardWS[m.columns[t.ColumnID].BoardID], nil
}

func (m *memStore) Get(_ context.Context, id domain.ID) (domain.Task, error) {
	t, ok := m.tasks[id]
	if !ok {
		return domain.Task{}, domain.ErrTaskNotFound
	}
	return t, nil
}

func (m *memStore) GetForUpdate(ctx context.Context, id domain.ID) (domain.Task, error) {
	return m.Get(ctx, id)
}

func (m *memStore) Detail(ctx context.Context, id domain.ID) (domain.TaskDetail, error) {
	t, err := m.Get(ctx, id)
	if err != nil {
		return domain.TaskDetail{}, err
	}
	labels, _ := m.Labels(ctx, id)
	d := domain.TaskDetail{Task: t, Labels: labels}
	if t.AssigneeID != nil {
		d.AssigneeName = m.names[*t.AssigneeID]
	}
	return d, nil
}

func (m *memStore) Labels(_ context.Context, id domain.ID) ([]domain.Label, error) {
	var out []domain.Label
	for _, l := range m.taskLabels[id] {
		out = append(out, m.labels[l])
	}
	slices.SortFunc(out, func(a, b domain.Label) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (m *memStore) Create(_ context.Context, col domain.ID, pos float64, f domain.TaskFields, by domain.ID) (domain.Task, error) {
	if _, ok := m.columns[col]; !ok {
		return domain.Task{}, domain.ErrColumnNotFound
	}
	now := time.Now()
	t := domain.Task{
		ID: domain.NewID(), ColumnID: col, Title: f.Title, Description: f.Description, Position: pos, Version: 1,
		Priority: f.Priority, DueDate: f.DueDate, AssigneeID: f.AssigneeID, CreatedBy: &by, CreatedAt: now, UpdatedAt: now,
	}
	m.tasks[t.ID] = t
	return t, nil
}

func (m *memStore) Update(_ context.Context, id domain.ID, version int32, f domain.TaskFields) (domain.Task, error) {
	if m.beforeUpdate != nil {
		m.beforeUpdate(id)
	}
	t := m.tasks[id]
	if t.Version != version {
		return domain.Task{}, domain.ErrConflict
	}
	t.Title, t.Description, t.Priority, t.DueDate, t.AssigneeID, t.ArchivedAt = f.Title, f.Description, f.Priority, f.DueDate, f.AssigneeID, f.ArchivedAt
	t.Version++
	m.tasks[id] = t
	return t, nil
}

func (m *memStore) Move(_ context.Context, id domain.ID, version int32, col domain.ID, pos float64) (domain.Task, error) {
	t := m.tasks[id]
	if t.Version != version {
		return domain.Task{}, domain.ErrConflict
	}
	t.ColumnID, t.Position = col, pos
	t.Version++
	m.tasks[id] = t
	return t, nil
}

func (m *memStore) SetLabels(_ context.Context, id domain.ID, ids []domain.ID) error {
	m.taskLabels[id] = slices.Clone(ids)
	return nil
}

func (m *memStore) Delete(_ context.Context, id domain.ID) error {
	if _, ok := m.tasks[id]; !ok {
		return domain.ErrTaskNotFound
	}
	delete(m.tasks, id)
	return nil
}

// column returns a column's tasks ordered by (position, id), without exclude.
func (m *memStore) column(col, exclude domain.ID) []domain.Task {
	var out []domain.Task
	for _, t := range m.tasks {
		if t.ColumnID == col && t.ID != exclude {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b domain.Task) int {
		if domain.TaskBefore(a, b) {
			return -1
		}
		return 1
	})
	return out
}

func (m *memStore) LastPosition(_ context.Context, col, exclude domain.ID) (*float64, error) {
	ts := m.column(col, exclude)
	if len(ts) == 0 {
		return nil, nil
	}
	return &ts[len(ts)-1].Position, nil
}

func (m *memStore) PositionAfter(_ context.Context, col, exclude domain.ID, n domain.Task) (*float64, error) {
	for _, t := range m.column(col, exclude) {
		if domain.TaskBefore(n, t) {
			return &t.Position, nil
		}
	}
	return nil, nil
}

func (m *memStore) PositionBefore(_ context.Context, col, exclude domain.ID, n domain.Task) (*float64, error) {
	ts := m.column(col, exclude)
	for i := len(ts) - 1; i >= 0; i-- {
		if domain.TaskBefore(ts[i], n) {
			return &ts[i].Position, nil
		}
	}
	return nil, nil
}

func (m *memStore) Rebalance(_ context.Context, col domain.ID) error {
	for i, t := range m.column(col, domain.NilID) {
		t.Position = float64(i+1) * domain.PositionStep
		m.tasks[t.ID] = t
	}
	return nil
}

// --- BoardRepository, WorkspaceRepository, ActivityRepository ---

func (m *memStore) GetColumn(_ context.Context, id domain.ID) (domain.Column, error) {
	c, ok := m.columns[id]
	if !ok {
		return domain.Column{}, domain.ErrColumnNotFound
	}
	return c, nil
}

func (m *memStore) ColumnLocation(ctx context.Context, id domain.ID) (domain.Location, error) {
	c, err := m.GetColumn(ctx, id)
	if err != nil {
		return domain.Location{}, err
	}
	return domain.Location{WorkspaceID: m.boardWS[c.BoardID], BoardID: c.BoardID}, nil
}

func (m *memStore) LockColumn(context.Context, domain.ID) error { return nil }

func (m *memStore) LockBoardLabels(_ context.Context, board domain.ID, ids []domain.ID) (int, error) {
	n := 0
	for _, id := range ids {
		if l, ok := m.labels[id]; ok && l.BoardID == board {
			n++
		}
	}
	return n, nil
}

func (m *memStore) LockMembership(_ context.Context, ws, user domain.ID) error {
	if !m.members[ws][user] {
		return domain.ErrNotMember
	}
	return nil
}

func (m *memStore) Append(_ context.Context, a domain.NewActivity) (domain.ActivityEntry, error) {
	if m.failAppend != nil {
		return domain.ActivityEntry{}, m.failAppend
	}
	e := domain.ActivityEntry{
		ID: int64(len(m.activity) + 1), WorkspaceID: a.WorkspaceID, BoardID: a.BoardID, ActorID: a.ActorID,
		Type: a.Type, Payload: a.Payload, CreatedAt: time.Now(),
	}
	m.activity = append(m.activity, e)
	return e, nil
}

type recorder struct{ events []domain.Event }

func (r *recorder) Publish(_ context.Context, events ...domain.Event) {
	r.events = append(r.events, events...)
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

var errBoom = errors.New("boom")
