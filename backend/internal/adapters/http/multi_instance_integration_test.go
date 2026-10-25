package httpapi

// Two API instances in one process, sharing the Testcontainers database and
// talking through Postgres LISTEN/NOTIFY (postgres.EventBus), as two
// processes behind a load balancer would.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/DarkApofis/tablero/backend/internal/adapters/events"
	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

type apiInstance struct {
	name string
	hub  *events.Hub
	bus  *postgres.EventBus
	srv  *httptest.Server
	stop context.CancelFunc // stops the bus, as if the process died
	// gate, while set and not closed, keeps the listener from reconnecting.
	gate atomic.Pointer[chan struct{}]
}

const (
	testBeat = 100 * time.Millisecond
	testTTL  = 400 * time.Millisecond
)

// startInstance starts an API instance with the postgres event backend.
func (f *taskFixture) startInstance(name string) *apiInstance {
	f.t.Helper()
	return f.startInstanceWithBeat(name, testBeat)
}

func (f *taskFixture) startInstanceWithBeat(name string, beat time.Duration) *apiInstance {
	f.t.Helper()
	url := testDatabaseURL(f.t)
	in := &apiInstance{name: name + "-" + domain.NewID().String()[:6], hub: events.NewHub(events.DefaultBuffer, testLeaveDelay)}
	dial := func(ctx context.Context) (*pgx.Conn, error) {
		if g := in.gate.Load(); g != nil {
			select {
			case <-*g:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		cfg, err := pgx.ParseConfig(url)
		if err != nil {
			return nil, err
		}
		cfg.RuntimeParams["application_name"] = postgres.ListenerName(in.name)
		return pgx.ConnectConfig(ctx, cfg)
	}
	in.bus = postgres.NewEventBus(f.pool, url, in.hub, postgres.EventBusOptions{
		Instance: in.name, Beat: beat, TTL: testTTL,
		MinBackoff: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond, Dial: dial,
	})
	ctx, cancel := context.WithCancel(context.Background())
	in.stop = cancel
	done := make(chan struct{})
	go func() { defer close(done); _ = in.bus.Run(ctx) }()
	waitFor(f.t, in.name+"'s listener", in.bus.Connected)

	deps := instanceDeps(f.pool, f.tokens, in.hub, in.bus)
	deps.StreamHeartbeat = time.Minute // so only notifications (or a resync) deliver
	in.srv = httptest.NewServer(NewRouter(deps))
	f.t.Cleanup(func() {
		in.srv.CloseClientConnections()
		in.srv.Close()
		cancel()
		<-done
		in.hub.Close()
	})
	return in
}

// watch opens a stream on the instance and waits until it is subscribed
// there; it returns the stream with its snapshot already read.
func (f *taskFixture) watch(in *apiInstance, as testUser) (*sseStream, string) {
	f.t.Helper()
	before := in.hub.Subscribers(domain.ID(f.board))
	s := f.openStream(in.srv, f.board, as)
	if fr := s.next(f.t, 2*time.Second); fr != "retry: 3000" {
		f.t.Fatalf("first frame = %q", fr)
	}
	snapshot := s.nextPresence(f.t, 2*time.Second)
	waitFor(f.t, "the subscription on "+in.name, func() bool { return in.hub.Subscribers(domain.ID(f.board)) > before })
	return s, snapshot
}

// presence is GET /boards/{id}/presence on the instance.
func (in *apiInstance) presence(t *testing.T, f *taskFixture) []string {
	t.Helper()
	req, _ := http.NewRequest("GET", in.srv.URL+"/boards/"+f.board.String()+"/presence", nil)
	req.Header.Set("Authorization", "Bearer "+f.viewer.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Users []presenceUser `json:"users"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&body) != nil {
		t.Fatalf("GET presence on %s: %d", in.name, resp.StatusCode)
	}
	out := []string{}
	for _, u := range body.Users {
		out = append(out, u.Name)
	}
	return out
}

func (f *taskFixture) createTaskOn(in *apiInstance, title string, as testUser) {
	f.t.Helper()
	req, _ := http.NewRequest("POST", in.srv.URL+"/columns/"+f.todo.String()+"/tasks", strings.NewReader(`{"title":"`+title+`"}`))
	req.Header.Set("Authorization", "Bearer "+as.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		f.t.Fatalf("create task on %s: %d", in.name, resp.StatusCode)
	}
}

func TestAnEventPublishedOnOneInstanceReachesAClientOnTheOther(t *testing.T) {
	f := newTaskFixture(t)
	a, b := f.startInstance("a"), f.startInstance("b")
	s, _ := f.watch(b, f.viewer)

	f.createTaskOn(a, "desde A", f.ana)
	e := f.nextEvent(s)
	if e.typ != domain.EventTaskCreated || payloadAs[domain.TaskCreatedPayload](t, e).Title != "desde A" || e.data.Version == nil || *e.data.Version != 1 {
		t.Fatalf("client on B got %+v", e)
	}
	// And the other way round, plus nothing twice.
	sa, _ := f.watch(a, f.viewer)
	f.createTaskOn(b, "desde B", f.beto)
	for name, st := range map[string]*sseStream{"A": sa, "B": s} {
		if e := f.nextEvent(st); payloadAs[domain.TaskCreatedPayload](t, e).Title != "desde B" {
			t.Errorf("client on %s got %+v", name, e)
		}
	}
	noMoreEvents(t, s, 300*time.Millisecond)
}

// Changes travel as soon as they happen: the beat is a minute here, so it
// cannot be what carries them.
func TestPresenceAcrossInstances(t *testing.T) {
	f := newTaskFixture(t)
	a, b := f.startInstanceWithBeat("a", time.Minute), f.startInstanceWithBeat("b", time.Minute)
	viewer, snapshot := f.watch(b, f.viewer)
	if snapshot != "presence.snapshot: Viewer" {
		t.Fatalf("snapshot on B = %q", snapshot)
	}

	ana, _ := f.watch(a, f.ana) // Ana connects to the other instance
	if got := viewer.nextPresence(t, 2*time.Second); got != "presence.joined: Ana" {
		t.Fatalf("viewer on B got %q", got)
	}
	if got := b.presence(t, f); !slices.Equal(got, []string{"Ana", "Viewer"}) {
		t.Errorf("presence on B = %v", got)
	}
	_, snap := f.watch(b, f.beto)
	if snap != "presence.snapshot: Ana,Beto,Viewer" {
		t.Errorf("a new client on B sees %q", snap)
	}
	if got := viewer.nextPresence(t, 2*time.Second); got != "presence.joined: Beto" {
		t.Errorf("viewer got %q", got)
	}

	// Ana leaves A: after A's leave delay, B hears it.
	ana.cancel()
	if got := viewer.nextPresence(t, 10*testLeaveDelay); got != "presence.left: Ana" {
		t.Errorf("viewer got %q, want Ana's left", got)
	}
}

// An instance that starts later learns who is already connected elsewhere
// at once: it says hello and the others answer with their presence. A's
// beat is a minute, so only the answer to the hello can explain it.
func TestALateInstanceLearnsThePresence(t *testing.T) {
	f := newTaskFixture(t)
	a := f.startInstanceWithBeat("a", time.Minute)
	f.watch(a, f.ana)
	c := f.startInstance("c")
	waitFor(t, "Ana to show up on the late instance", func() bool {
		return slices.Equal(c.presence(t, f), []string{"Ana"})
	})
}

// An instance that dies cannot say its users left: the others forget its
// announcements once they are older than the TTL.
func TestPresenceOfADeadInstanceExpires(t *testing.T) {
	f := newTaskFixture(t)
	a, b := f.startInstance("a"), f.startInstance("b")
	viewer, _ := f.watch(b, f.viewer)
	f.watch(a, f.ana)
	if got := viewer.nextPresence(t, 2*time.Second); got != "presence.joined: Ana" {
		t.Fatalf("got %q", got)
	}

	a.stop() // A's bus stops: no beats, no goodbyes (its client is still connected)
	start := time.Now()
	if got := viewer.nextPresence(t, 10*testTTL); got != "presence.left: Ana" {
		t.Fatalf("got %q", got)
	}
	if waited := time.Since(start); waited < testTTL/2 {
		t.Errorf("expired after %v, TTL %v", waited, testTTL)
	}
}

// The listener's connection dies; while it is down, an event is published
// on the other instance and its notification is lost. When the listener
// reconnects it resyncs: the client still gets the event, from the log.
func TestListenerReconnectsAndResyncs(t *testing.T) {
	f := newTaskFixture(t)
	a, b := f.startInstance("a"), f.startInstance("b")
	s, _ := f.watch(b, f.viewer)

	gate := make(chan struct{})
	b.gate.Store(&gate) // hold B's reconnection
	var killed int
	if err := f.pool.QueryRow(context.Background(),
		"SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE application_name = $1",
		postgres.ListenerName(b.name)).Scan(&killed); err != nil || killed != 1 {
		t.Fatalf("terminate B's listener: %d, %v", killed, err)
	}
	waitFor(t, "B to notice", func() bool { return !b.bus.Connected() })

	f.createTaskOn(a, "durante el corte", f.ana)
	noMoreEvents(t, s, 300*time.Millisecond) // B did not hear about it

	close(gate) // B reconnects (after its backoff) and resyncs
	waitFor(t, "B to reconnect", b.bus.Connected)
	if e := f.nextEvent(s); payloadAs[domain.TaskCreatedPayload](t, e).Title != "durante el corte" {
		t.Errorf("after the reconnect the client got %+v", e)
	}
	// Live again.
	f.createTaskOn(a, "después", f.ana)
	if e := f.nextEvent(s); payloadAs[domain.TaskCreatedPayload](t, e).Title != "después" {
		t.Errorf("then %+v", e)
	}
}
