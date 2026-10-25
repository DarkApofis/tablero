package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

const testLeaveDelay = 100 * time.Millisecond

func user(name string) domain.Presence { return domain.Presence{UserID: domain.NewID(), Name: name} }

// presenceEvents collects the presence events that reach ch within d.
func presenceEvents(t *testing.T, ch <-chan domain.Event, d time.Duration) []string {
	t.Helper()
	var out []string
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			if !ev.Type.IsPresence() {
				continue
			}
			var p domain.PresencePayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatal(err)
			}
			if ev.BoardID.IsZero() || ev.ActorID == nil || *ev.ActorID != p.User.ID {
				t.Errorf("event = %+v", ev)
			}
			out = append(out, string(ev.Type)+" "+p.User.Name)
		case <-deadline:
			return out
		}
	}
}

func names(ps []domain.Presence) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSnapshotAndJoined(t *testing.T) {
	h := NewHub(8, testLeaveDelay)
	defer h.Close()
	board := domain.NewID()
	ana, beto := user("Ana"), user("Beto")

	anaCh, snap, cancelAna := h.Subscribe(board, ana)
	defer cancelAna()
	if !equalStrings(names(snap), []string{"Ana"}) {
		t.Errorf("Ana's snapshot = %v, want herself", names(snap))
	}
	_, snap, cancelBeto := h.Subscribe(board, beto)
	defer cancelBeto()
	if !equalStrings(names(snap), []string{"Ana", "Beto"}) {
		t.Errorf("Beto's snapshot = %v", names(snap))
	}
	if got := presenceEvents(t, anaCh, 50*time.Millisecond); !equalStrings(got, []string{"presence.joined Beto"}) {
		t.Errorf("Ana saw %v", got)
	}
	// Presence is per board.
	if other := h.Present(domain.NewID()); len(other) != 0 {
		t.Errorf("another board = %v", names(other))
	}
}

// A user with several tabs is present once: joined on the first, left only
// after the last closes and the leave delay passes.
func TestSeveralTabsCountOnce(t *testing.T) {
	h := NewHub(8, testLeaveDelay)
	defer h.Close()
	board := domain.NewID()
	observer, cancelObserver := subscribe(h, board)
	defer cancelObserver()
	ana := user("Ana")

	_, _, tab1 := h.Subscribe(board, ana)
	_, snap, tab2 := h.Subscribe(board, ana)
	if !equalStrings(names(snap), []string{"Ana", "Tester"}) {
		t.Errorf("second tab's snapshot = %v, Ana once", names(snap))
	}
	if got := presenceEvents(t, observer, 50*time.Millisecond); !equalStrings(got, []string{"presence.joined Ana"}) {
		t.Errorf("after two tabs: %v, want one joined", got)
	}

	tab1()
	if got := presenceEvents(t, observer, 3*testLeaveDelay); len(got) != 0 {
		t.Errorf("closing one of two tabs sent %v", got)
	}
	start := time.Now()
	tab2()
	tab2() // idempotent: counted once
	if !equalStrings(names(h.Present(board)), []string{"Ana", "Tester"}) {
		t.Error("Ana disappeared before the leave delay")
	}
	got := presenceEvents(t, observer, 3*testLeaveDelay)
	if !equalStrings(got, []string{"presence.left Ana"}) {
		t.Fatalf("after the last tab: %v, want one left", got)
	}
	if waited := time.Since(start); waited < testLeaveDelay {
		t.Errorf("left after %v, before the %v delay", waited, testLeaveDelay)
	}
	if !equalStrings(names(h.Present(board)), []string{"Tester"}) {
		t.Errorf("present = %v", names(h.Present(board)))
	}
}

// Reconnecting within the leave delay (a reload) sends nothing at all.
func TestQuickReconnectDoesNotFlicker(t *testing.T) {
	h := NewHub(8, testLeaveDelay)
	defer h.Close()
	board := domain.NewID()
	observer, cancelObserver := subscribe(h, board)
	defer cancelObserver()
	ana := user("Ana")

	_, _, cancel := h.Subscribe(board, ana)
	if got := presenceEvents(t, observer, 50*time.Millisecond); len(got) != 1 {
		t.Fatalf("joined = %v", got)
	}
	for range 3 { // three reloads
		cancel()
		time.Sleep(testLeaveDelay / 4)
		_, _, cancel = h.Subscribe(board, ana)
	}
	if got := presenceEvents(t, observer, 3*testLeaveDelay); len(got) != 0 {
		t.Errorf("reloads sent %v", got)
	}
	cancel()
	if got := presenceEvents(t, observer, 3*testLeaveDelay); !equalStrings(got, []string{"presence.left Ana"}) {
		t.Errorf("final leave = %v", got)
	}
}

// Coming back after the delay is a new presence: left, then joined again.
func TestReconnectAfterTheDelayIsANewJoin(t *testing.T) {
	h := NewHub(8, testLeaveDelay)
	defer h.Close()
	board := domain.NewID()
	observer, cancelObserver := subscribe(h, board)
	defer cancelObserver()
	ana := user("Ana")

	_, _, cancel := h.Subscribe(board, ana)
	cancel()
	time.Sleep(2 * testLeaveDelay)
	_, _, cancel = h.Subscribe(board, ana)
	defer cancel()
	if got := presenceEvents(t, observer, 50*time.Millisecond); !equalStrings(got, []string{
		"presence.joined Ana", "presence.left Ana", "presence.joined Ana",
	}) {
		t.Errorf("events = %v", got)
	}
}

// A subscriber dropped for being slow still counts as connected until its
// stream ends and cancels; then it leaves like any other.
func TestDroppedSubscriberLeavesOnCancel(t *testing.T) {
	h := NewHub(1, testLeaveDelay)
	defer h.Close()
	board := domain.NewID()
	ana := user("Ana")
	slow, _, cancelSlow := h.Subscribe(board, ana)
	h.Publish(t.Context(), event(board, 1), event(board, 2)) // overflows Ana's buffer of 1
	<-slow
	if _, ok := <-slow; ok {
		t.Fatal("slow subscriber not dropped")
	}
	if !equalStrings(names(h.Present(board)), []string{"Ana"}) {
		t.Error("dropping the subscription removed the presence before cancel")
	}
	cancelSlow()
	time.Sleep(2 * testLeaveDelay)
	if len(h.Present(board)) != 0 {
		t.Errorf("present after cancel = %v", names(h.Present(board)))
	}
}

func TestCloseStopsPendingLeaves(t *testing.T) {
	h := NewHub(8, testLeaveDelay)
	board := domain.NewID()
	_, _, cancel := h.Subscribe(board, user("Ana"))
	cancel()
	h.Close()
	time.Sleep(2 * testLeaveDelay) // the stopped timer must not fire into a closed hub
	if len(h.Present(board)) != 0 {
		t.Error("presence survived Close")
	}
}
