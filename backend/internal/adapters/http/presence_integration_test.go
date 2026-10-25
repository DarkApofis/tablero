package httpapi

// Presence on the event stream and GET /boards/{id}/presence: several tabs,
// leaving after the delay and quick reconnects.

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// testLeaveDelay is the leave delay of the test hub (see newDBEnv).
const testLeaveDelay = 200 * time.Millisecond

// nextPresence returns the next presence message as "type: name,name…",
// failing after timeout.
func (s *sseStream) nextPresence(t *testing.T, timeout time.Duration) string {
	t.Helper()
	select {
	case msg, ok := <-s.presence:
		if !ok {
			t.Fatal("stream ended")
		}
		return describePresence(t, msg)
	case <-time.After(timeout):
		t.Fatal("no presence message in time")
		return ""
	}
}

// noPresence fails if a presence message arrives within d.
func (s *sseStream) noPresence(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case msg := <-s.presence:
		t.Fatalf("unexpected %s", describePresence(t, msg))
	case <-time.After(d):
	}
}

func describePresence(t *testing.T, msg string) string {
	t.Helper()
	if strings.Contains(msg, "\nid: ") || strings.HasPrefix(msg, "id: ") {
		t.Errorf("presence message with an SSE id: %q", msg)
	}
	_, data, _ := strings.Cut(msg, "\ndata: ")
	var m presenceMessage
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("presence data %q: %v", data, err)
	}
	users := m.Users
	if m.User != nil {
		users = []presenceUser{*m.User}
	}
	var names []string
	for _, u := range users {
		names = append(names, u.Name)
	}
	return string(m.Type) + ": " + strings.Join(names, ",")
}

func (f *taskFixture) presenceNow(as testUser) []string {
	f.t.Helper()
	rec := f.do("GET", "/boards/"+f.board.String()+"/presence", "", as)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("GET presence: %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Users []presenceUser `json:"users"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&body)
	names := []string{}
	for _, u := range body.Users {
		names = append(names, u.Name)
	}
	return names
}

func TestPresenceSnapshotAndJoined(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(time.Minute, 0)

	ana := f.openStream(srv, f.board, f.ana)
	if fr := ana.next(t, 2*time.Second); fr != "retry: 3000" {
		t.Fatalf("first frame = %q", fr)
	}
	if got := ana.nextPresence(t, 2*time.Second); got != "presence.snapshot: Ana" {
		t.Errorf("Ana's snapshot = %q", got)
	}
	beto := f.openStream(srv, f.board, f.beto)
	if got := beto.nextPresence(t, 2*time.Second); got != "presence.snapshot: Ana,Beto" {
		t.Errorf("Beto's snapshot = %q", got)
	}
	if got := ana.nextPresence(t, 2*time.Second); got != "presence.joined: Beto" {
		t.Errorf("Ana got %q", got)
	}
	beto.noPresence(t, 100*time.Millisecond) // not his own joined

	if got := f.presenceNow(f.viewer); !slices.Equal(got, []string{"Ana", "Beto"}) {
		t.Errorf("GET presence = %v", got)
	}
	// Presence does not get in the way of the log events or their ids.
	f.createTask(f.todo, "Tarea", f.ana)
	if _, typ, data := parseEvent(t, ana.next(t, 2*time.Second)); typ != domain.EventTaskCreated || data.ID == 0 {
		t.Errorf("after presence: %s #%d", typ, data.ID)
	}
}

// Two tabs of one user are one presence: one joined, no left while a tab
// remains, and a single left once the last closes and the delay passes.
func TestPresenceWithSeveralTabs(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(time.Minute, 0)
	observer := f.openStream(srv, f.board, f.admin)
	observer.nextPresence(t, 2*time.Second) // snapshot

	tab1 := f.openStream(srv, f.board, f.ana)
	tab2 := f.openStream(srv, f.board, f.ana)
	if got := tab2.nextPresence(t, 2*time.Second); got != "presence.snapshot: Admin,Ana" {
		t.Errorf("second tab's snapshot = %q, Ana once", got)
	}
	if got := observer.nextPresence(t, 2*time.Second); got != "presence.joined: Ana" {
		t.Fatalf("observer got %q", got)
	}
	observer.noPresence(t, 150*time.Millisecond) // the second tab joins nothing

	tab1.cancel()
	observer.noPresence(t, 3*testLeaveDelay) // a tab is still open
	if got := f.presenceNow(f.viewer); !slices.Equal(got, []string{"Admin", "Ana"}) {
		t.Errorf("with one tab left: %v", got)
	}

	closedAt := time.Now()
	tab2.cancel()
	if got := f.presenceNow(f.viewer); !slices.Equal(got, []string{"Admin", "Ana"}) {
		t.Errorf("right after the last tab: %v (still within the delay)", got)
	}
	if got := observer.nextPresence(t, 5*testLeaveDelay); got != "presence.left: Ana" {
		t.Fatalf("observer got %q", got)
	}
	if waited := time.Since(closedAt); waited < testLeaveDelay {
		t.Errorf("left after %v, before the %v delay", waited, testLeaveDelay)
	}
	if got := f.presenceNow(f.viewer); !slices.Equal(got, []string{"Admin"}) {
		t.Errorf("after leaving: %v", got)
	}
}

// A reload (disconnect and reconnect within the delay) is invisible to the
// others: no left, no joined. The reloaded tab gets a full snapshot again.
func TestPresenceSurvivesAQuickReconnect(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(time.Minute, 0)
	observer := f.openStream(srv, f.board, f.admin)
	observer.nextPresence(t, 2*time.Second)
	tab := f.openStream(srv, f.board, f.ana)
	tab.nextPresence(t, 2*time.Second)
	if got := observer.nextPresence(t, 2*time.Second); got != "presence.joined: Ana" {
		t.Fatalf("observer got %q", got)
	}

	for range 3 {
		tab.cancel()
		waitFor(t, "the tab to close", func() bool { return f.hub.Subscribers(domain.ID(f.board)) == 1 })
		tab = f.openStream(srv, f.board, f.ana)
		if got := tab.nextPresence(t, 2*time.Second); got != "presence.snapshot: Admin,Ana" {
			t.Errorf("reloaded tab's snapshot = %q", got)
		}
	}
	observer.noPresence(t, 3*testLeaveDelay)

	tab.cancel()
	if got := observer.nextPresence(t, 5*testLeaveDelay); got != "presence.left: Ana" {
		t.Errorf("final leave = %q", got)
	}
}

// Presence messages carry no SSE id: a client reconnecting after them still
// replays from the last log event it saw.
func TestPresenceDoesNotMoveLastEventID(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(time.Minute, 0)
	s := f.openStream(srv, f.board, f.viewer)
	f.ready(s, 1)
	if got := s.nextPresence(t, 2*time.Second); got != "presence.snapshot: Viewer" {
		t.Fatalf("snapshot = %q", got)
	}
	f.createTask(f.todo, "vista", f.ana)
	seen := f.nextEvent(s)

	other := f.openStream(srv, f.board, f.beto) // presence.joined reaches s, without an id
	if got := s.nextPresence(t, 2*time.Second); got != "presence.joined: Beto" {
		t.Fatalf("got %q", got)
	}
	other.cancel()
	s.cancel()
	f.createTask(f.todo, "perdida", f.ana)

	again := f.openStream(srv, f.board, f.viewer, withLastEventID(strconv.FormatInt(seen.id, 10)))
	f.ready(again, 1)
	if e := f.nextEvent(again); payloadAs[domain.TaskCreatedPayload](t, e).Title != "perdida" {
		t.Errorf("replayed %+v", e)
	}
}

func TestPresenceRouteRequiresMembership(t *testing.T) {
	f := newTaskFixture(t)
	if rec := f.do("GET", "/boards/"+f.board.String()+"/presence", "", f.outsider); rec.Code != http.StatusForbidden {
		t.Errorf("outsider: %d", rec.Code)
	}
	if got := f.presenceNow(f.viewer); len(got) != 0 {
		t.Errorf("nobody connected: %v", got)
	}
}
