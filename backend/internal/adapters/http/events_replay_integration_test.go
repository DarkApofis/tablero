package httpapi

// Event stream: domain events, replay on reconnect (Last-Event-ID), resync,
// exclude_self and ordering under concurrent writes.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
	"github.com/DarkApofis/tablero/backend/internal/app/board"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// streamServerWithReplayLimit is streamServer with a small replay limit, to
// reach the resync path.
func (f *taskFixture) streamServerWithReplayLimit(limit int) *httptest.Server {
	f.t.Helper()
	deps := f.deps
	deps.StreamHeartbeat = time.Minute
	deps.Streams = board.NewStreamService(f.hub, postgres.NewActivity(f.pool), postgres.NewUsers(f.pool), limit)
	srv := httptest.NewServer(NewRouter(deps))
	f.t.Cleanup(srv.Close)
	return srv
}

func withLastEventID(id string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Last-Event-ID", id) }
}

func withQuery(q string) func(*http.Request) {
	return func(r *http.Request) { r.URL.RawQuery = q }
}

// ready reads the retry hint and waits until the stream is subscribed.
func (f *taskFixture) ready(s *sseStream, subscribers int) {
	f.t.Helper()
	if fr := s.next(f.t, 2*time.Second); fr != "retry: 3000" {
		f.t.Fatalf("first frame = %q", fr)
	}
	waitFor(f.t, "the subscription", func() bool { return f.hub.Subscribers(domain.ID(f.board)) >= subscribers })
}

type gotEvent struct {
	id   int64
	typ  domain.EventType
	data streamEvent
}

func (f *taskFixture) nextEvent(s *sseStream) gotEvent {
	f.t.Helper()
	id, typ, data := parseEvent(f.t, s.next(f.t, 3*time.Second))
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		f.t.Fatalf("event id %q", id)
	}
	return gotEvent{n, typ, data}
}

// noMoreEvents fails if an event (not a ping) arrives within d.
func noMoreEvents(t *testing.T, s *sseStream, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case fr, ok := <-s.frames:
			if ok && fr != ": ping" {
				t.Fatalf("unexpected frame %q", fr)
			}
			if !ok {
				return
			}
		case <-deadline:
			return
		}
	}
}

func payloadAs[T any](t *testing.T, e gotEvent) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(e.data.Payload, &v); err != nil {
		t.Fatalf("payload %s: %v", e.data.Payload, err)
	}
	return v
}

// A test SSE client sees a task moved through the API, with the resulting
// version, in the same order as the board's log.
func TestStreamClientSeesATaskMovedThroughTheAPI(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(time.Minute, 0)
	task := f.createTask(f.todo, "Mover", f.ana)
	other := f.createTask(f.doing, "Ancla", f.ana)

	s := f.openStream(srv, f.board, f.viewer)
	f.ready(s, 1)
	if rec := f.move(task.ID, map[string]any{"columnId": f.doing, "beforeTaskId": other.ID, "version": 1}, f.beto); rec.Code != http.StatusOK {
		t.Fatalf("move: %d %s", rec.Code, rec.Body)
	}

	ev := f.nextEvent(s)
	p := payloadAs[domain.TaskMovedPayload](t, ev)
	if ev.typ != domain.EventTaskMoved || ev.data.Type != domain.EventTaskMoved || ev.id != ev.data.ID ||
		ev.data.Version == nil || *ev.data.Version != 2 || ev.data.ActorID == nil || *ev.data.ActorID != f.beto.ID {
		t.Errorf("event = %+v", ev)
	}
	if p.TaskID != domain.ID(task.ID) || p.From.ColumnID != domain.ID(f.todo) || p.To.ColumnID != domain.ID(f.doing) ||
		p.To.Position >= other.Position || p.Version != 2 {
		t.Errorf("payload = %+v", p)
	}
	var newest int64
	_ = f.pool.QueryRow(context.Background(), "SELECT max(id) FROM activity_log WHERE board_id = $1", f.board).Scan(&newest)
	if ev.id != newest {
		t.Errorf("event id %d, want the log entry's id %d", ev.id, newest)
	}
}

// A client that disconnects and comes back with Last-Event-ID receives
// exactly what it missed, in order, then the live events: no gaps, no
// duplicates.
func TestReconnectWithLastEventIDReplaysWhatWasMissed(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(time.Minute, 0)
	task := f.createTask(f.todo, "Tarea", f.ana)

	first := f.openStream(srv, f.board, f.viewer)
	f.ready(first, 1)
	f.patchOK(task.ID, map[string]any{"priority": "high", "version": 1}, f.ana)
	seen := f.nextEvent(first)
	first.cancel() // the client drops
	waitFor(t, "the first stream to go away", func() bool { return f.hub.Subscribers(domain.ID(f.board)) == 0 })

	// While it is away: a move, an edit and a new task.
	if rec := f.move(task.ID, map[string]any{"columnId": f.doing, "version": 2}, f.beto); rec.Code != http.StatusOK {
		t.Fatalf("move: %d", rec.Code)
	}
	f.patchOK(task.ID, map[string]any{"title": "Renombrada", "version": 3}, f.beto)
	f.createTask(f.done, "Nueva", f.ana)

	again := f.openStream(srv, f.board, f.viewer, withLastEventID(strconv.FormatInt(seen.id, 10)))
	f.ready(again, 1)
	var got []gotEvent
	for range 3 {
		got = append(got, f.nextEvent(again))
	}
	want := []domain.EventType{domain.EventTaskMoved, domain.EventTaskUpdated, domain.EventTaskCreated}
	for i, e := range got {
		if e.typ != want[i] || e.id <= seen.id || (i > 0 && e.id <= got[i-1].id) {
			t.Errorf("replayed %d = %s #%d, want %s after #%d, increasing", i, e.typ, e.id, want[i], seen.id)
		}
	}
	if v := got[1].data.Version; v == nil || *v != 4 {
		t.Errorf("replayed edit version = %v, want 4", v)
	}

	// Then live, with nothing repeated.
	f.patchOK(task.ID, map[string]any{"archived": true, "version": 4}, f.ana)
	live := f.nextEvent(again)
	if live.typ != domain.EventTaskArchived || live.id <= got[2].id {
		t.Errorf("live after replay = %s #%d", live.typ, live.id)
	}
	noMoreEvents(t, again, 300*time.Millisecond)
}

// The same works for a new EventSource, which cannot set headers: ?last_event_id=.
func TestLastEventIDQueryParam(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(time.Minute, 0)
	f.createTask(f.todo, "uno", f.ana)
	f.createTask(f.todo, "dos", f.ana)
	var firstID int64
	_ = f.pool.QueryRow(context.Background(), "SELECT min(id) FROM activity_log WHERE board_id = $1", f.board).Scan(&firstID)

	s := f.openStream(srv, f.board, f.viewer, withQuery("last_event_id="+strconv.FormatInt(firstID, 10)))
	f.ready(s, 1)
	if e := f.nextEvent(s); payloadAs[domain.TaskCreatedPayload](t, e).Title != "dos" {
		t.Errorf("replayed %+v, want only the second task", e)
	}
}

func TestTooLargeAGapSendsResync(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServerWithReplayLimit(2)
	task := f.createTask(f.todo, "Tarea", f.ana)
	var since int64
	_ = f.pool.QueryRow(context.Background(), "SELECT max(id) FROM activity_log WHERE board_id = $1", f.board).Scan(&since)
	for v := int32(1); v <= 3; v++ { // 3 missed > limit 2
		f.patchOK(task.ID, map[string]any{"description": fmt.Sprint(v), "version": v}, f.ana)
	}
	var newest int64
	_ = f.pool.QueryRow(context.Background(), "SELECT max(id) FROM activity_log WHERE board_id = $1", f.board).Scan(&newest)

	for name, lastID := range map[string]string{
		"gap too large":          strconv.FormatInt(since, 10),
		"id from the future":     strconv.FormatInt(newest+1000, 10),
		"not an id (from start)": "abc",
	} {
		t.Run(name, func(t *testing.T) {
			s := f.openStream(srv, f.board, f.viewer, withLastEventID(lastID))
			if fr := s.next(t, 2*time.Second); fr != "retry: 3000" {
				t.Fatalf("first frame = %q", fr)
			}
			fr := s.next(t, 2*time.Second)
			want := fmt.Sprintf("id: %d\nevent: resync\ndata: {\"last_event_id\":%d}", newest, newest)
			if fr != want {
				t.Fatalf("frame = %q, want %q", fr, want)
			}
			// After the resync the stream continues live from there.
			f.createTask(f.todo, "después de "+name, f.ana)
			if e := f.nextEvent(s); e.typ != domain.EventTaskCreated || e.id <= newest {
				t.Errorf("after resync = %s #%d", e.typ, e.id)
			}
			newest++ // roughly; only used as a lower bound below
			_ = f.pool.QueryRow(context.Background(), "SELECT max(id) FROM activity_log WHERE board_id = $1", f.board).Scan(&newest)
			s.cancel()
		})
	}
}

// exclude_self (query param or header) leaves out the caller's own changes
// and only those.
func TestExcludeSelf(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(time.Minute, 0)
	viaQuery := f.openStream(srv, f.board, f.ana, withQuery("exclude_self=true"))
	viaHeader := f.openStream(srv, f.board, f.ana, func(r *http.Request) { r.Header.Set(excludeSelfHeader, "true") })
	everyone := f.openStream(srv, f.board, f.ana)
	f.ready(viaQuery, 3)
	f.ready(viaHeader, 3)
	f.ready(everyone, 3)

	f.createTask(f.todo, "de Ana", f.ana)
	f.createTask(f.todo, "de Beto", f.beto)

	for name, s := range map[string]*sseStream{"query": viaQuery, "header": viaHeader} {
		e := f.nextEvent(s)
		if payloadAs[domain.TaskCreatedPayload](t, e).Title != "de Beto" {
			t.Errorf("%s: got %+v, want only Beto's task", name, e)
		}
	}
	if a, b := f.nextEvent(everyone), f.nextEvent(everyone); payloadAs[domain.TaskCreatedPayload](t, a).Title != "de Ana" ||
		payloadAs[domain.TaskCreatedPayload](t, b).Title != "de Beto" {
		t.Error("without exclude_self Ana must see both")
	}
}

// Every kind of change of a board reaches the stream as its domain event.
func TestEveryDomainEventIsStreamed(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(time.Minute, 0)
	s := f.openStream(srv, f.board, f.viewer)
	f.ready(s, 1)
	expect := func(want domain.EventType) gotEvent {
		t.Helper()
		e := f.nextEvent(s)
		if e.typ != want {
			t.Fatalf("got %s, want %s", e.typ, want)
		}
		return e
	}
	do := func(method, p, body string, as testUser) *httptest.ResponseRecorder {
		t.Helper()
		rec := f.do(method, p, body, as)
		if rec.Code >= 300 {
			t.Fatalf("%s %s: %d %s", method, p, rec.Code, rec.Body)
		}
		return rec
	}

	rec := do("POST", "/boards/"+f.board.String()+"/columns", `{"name":"Revisión"}`, f.admin)
	var col columnResponse
	_ = json.NewDecoder(rec.Body).Decode(&col)
	if p := payloadAs[domain.ColumnCreatedPayload](t, expect(domain.EventColumnCreated)); p.Name != "Revisión" || p.ColumnID != domain.ID(col.ID) {
		t.Errorf("column.created = %+v", p)
	}
	do("PATCH", "/columns/"+col.ID.String(), `{"name":"QA"}`, f.admin)
	if p := payloadAs[domain.ColumnUpdatedPayload](t, expect(domain.EventColumnUpdated)); p.Changes["name"].To != "QA" {
		t.Errorf("column.updated = %+v", p)
	}
	do("POST", "/columns/"+col.ID.String()+"/move", `{"after_id":null}`, f.admin)
	if p := payloadAs[domain.ColumnReorderedPayload](t, expect(domain.EventColumnReordered)); p.To >= 1024 {
		t.Errorf("column.reordered = %+v", p)
	}

	rec = do("POST", "/boards/"+f.board.String()+"/labels", `{"name":"Bug","color":"#ff0000"}`, f.ana)
	var label labelResponse
	_ = json.NewDecoder(rec.Body).Decode(&label)
	if p := payloadAs[domain.LabelChangedPayload](t, expect(domain.EventLabelChanged)); p.Action != domain.LabelCreated || p.Label.Name != "Bug" {
		t.Errorf("label.changed created = %+v", p)
	}
	do("PATCH", "/labels/"+label.ID.String(), `{"color":"#00ff00"}`, f.ana)
	if p := payloadAs[domain.LabelChangedPayload](t, expect(domain.EventLabelChanged)); p.Action != domain.LabelUpdated || p.Changes["color"].To != "#00ff00" {
		t.Errorf("label.changed updated = %+v", p)
	}

	task := f.createTask(col.ID, "En QA", f.ana)
	if e := expect(domain.EventTaskCreated); *e.data.Version != 1 || payloadAs[domain.TaskCreatedPayload](t, e).Description != "desc En QA" {
		t.Errorf("task.created = %+v", e)
	}
	f.patchOK(task.ID, map[string]any{"archived": true, "version": 1}, f.ana)
	if e := expect(domain.EventTaskArchived); *e.data.Version != 2 {
		t.Errorf("task.archived version = %d", *e.data.Version)
	}
	f.patchOK(task.ID, map[string]any{"archived": false, "version": 2}, f.ana)
	expect(domain.EventTaskUpdated) // unarchiving is an ordinary update
	do("DELETE", "/labels/"+label.ID.String(), "", f.ana)
	if p := payloadAs[domain.LabelChangedPayload](t, expect(domain.EventLabelChanged)); p.Action != domain.LabelDeleted {
		t.Errorf("label.changed deleted = %+v", p)
	}
	do("DELETE", "/tasks/"+task.ID.String(), "", f.ana)
	if e := expect(domain.EventTaskDeleted); *e.data.Version != 3 || payloadAs[domain.TaskDeletedPayload](t, e).TaskID != domain.ID(task.ID) {
		t.Errorf("task.deleted = %+v", e)
	}

	inColumn := f.createTask(col.ID, "Se va con la columna", f.ana)
	expect(domain.EventTaskCreated)
	do("DELETE", "/columns/"+col.ID.String(), "", f.admin)
	p := payloadAs[domain.ColumnDeletedPayload](t, expect(domain.EventColumnDeleted))
	if p.ColumnID != domain.ID(col.ID) || len(p.DeletedTaskIDs) != 1 || p.DeletedTaskIDs[0] != domain.ID(inColumn.ID) {
		t.Errorf("column.deleted = %+v", p)
	}
	noMoreEvents(t, s, 200*time.Millisecond)
}

// Many concurrent writes to one board (different tasks, so nothing else
// serializes them) reach a live client exactly once each, in the log's id
// order. The log's per-board append lock is what keeps commit order equal to
// id order; without it a client can skip an entry that commits late.
func TestConcurrentWritesReachTheStreamInOrderWithoutGaps(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(time.Minute, 0)
	var tasks []taskResponse
	for i := range 8 {
		tasks = append(tasks, f.createTask(f.todo, fmt.Sprintf("t%d", i), f.ana))
	}
	s := f.openStream(srv, f.board, f.viewer)
	f.ready(s, 1)

	const rounds = 5
	var wg sync.WaitGroup
	for _, task := range tasks {
		wg.Go(func() {
			for v := int32(1); v <= rounds; v++ {
				rec := f.patch(task.ID, map[string]any{"description": fmt.Sprint(v), "version": v}, f.ana)
				if rec.Code != http.StatusOK {
					t.Errorf("patch: %d", rec.Code)
					return
				}
			}
		})
	}
	wg.Wait()

	var got []int64
	for range len(tasks) * rounds {
		got = append(got, f.nextEvent(s).id)
	}
	noMoreEvents(t, s, 300*time.Millisecond)

	rows, err := f.pool.Query(context.Background(), "SELECT id FROM activity_log WHERE board_id = $1 AND type = 'task_updated' ORDER BY id", f.board)
	if err != nil {
		t.Fatal(err)
	}
	var want []int64
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		want = append(want, id)
	}
	if !slices.Equal(got, want) {
		t.Errorf("stream ids %v\nlog ids    %v", got, want)
	}
}
