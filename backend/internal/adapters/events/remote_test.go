package events

import (
	"sync"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Presence announced by other instances joins this hub's own: one union,
// with joined/left only when a user comes or goes overall.

func TestRemotePresenceIsPartOfTheUnion(t *testing.T) {
	h := NewHub(8, testLeaveDelay)
	defer h.Close()
	board := domain.NewID()
	observer, cancel := subscribe(h, board)
	defer cancel()
	ana := user("Ana")
	now := time.Now()

	h.SetRemotePresence("B", board, []domain.Presence{ana}, false, now)
	if got := presenceEvents(t, observer, 50*time.Millisecond); !equalStrings(got, []string{"presence.joined Ana"}) {
		t.Errorf("remote join: %v", got)
	}
	if !equalStrings(names(h.Present(board)), []string{"Ana", "Tester"}) {
		t.Errorf("present = %v", names(h.Present(board)))
	}
	// Seen again (a beat) or through a third instance: already present.
	h.SetRemotePresence("B", board, []domain.Presence{ana}, true, now)
	h.SetRemotePresence("C", board, []domain.Presence{ana}, false, now)
	h.RemoveRemotePresence("B", board, ana, now)
	if got := presenceEvents(t, observer, 50*time.Millisecond); len(got) != 0 {
		t.Errorf("still present through C, but got %v", got)
	}
	h.RemoveRemotePresence("C", board, ana, now)
	if got := presenceEvents(t, observer, 50*time.Millisecond); !equalStrings(got, []string{"presence.left Ana"}) {
		t.Errorf("gone everywhere: %v", got)
	}
}

// Connected here and elsewhere: leaving one side is not leaving.
func TestLocalAndRemoteTogether(t *testing.T) {
	h := NewHub(8, testLeaveDelay)
	defer h.Close()
	board := domain.NewID()
	observer, cancel := subscribe(h, board)
	defer cancel()
	ana := user("Ana")

	h.SetRemotePresence("B", board, []domain.Presence{ana}, false, time.Now())
	_, _, local := h.Subscribe(board, ana)
	if got := presenceEvents(t, observer, 50*time.Millisecond); !equalStrings(got, []string{"presence.joined Ana"}) {
		t.Errorf("joined once: %v", got)
	}
	local()
	if got := presenceEvents(t, observer, 3*testLeaveDelay); len(got) != 0 {
		t.Errorf("left locally but still on B: %v", got)
	}
	h.RemoveRemotePresence("B", board, ana, time.Now())
	if got := presenceEvents(t, observer, 50*time.Millisecond); !equalStrings(got, []string{"presence.left Ana"}) {
		t.Errorf("finally: %v", got)
	}
}

func TestBeatReplacesAndStaleSetsExpire(t *testing.T) {
	h := NewHub(8, testLeaveDelay)
	defer h.Close()
	board := domain.NewID()
	observer, cancel := subscribe(h, board)
	defer cancel()
	ana, beto := user("Ana"), user("Beto")
	t0 := time.Now()

	h.SetRemotePresence("B", board, []domain.Presence{ana, beto}, true, t0)
	presenceEvents(t, observer, 50*time.Millisecond)
	h.SetRemotePresence("B", board, []domain.Presence{beto}, true, t0.Add(time.Second)) // Ana no longer in B's beat
	if got := presenceEvents(t, observer, 50*time.Millisecond); !equalStrings(got, []string{"presence.left Ana"}) {
		t.Errorf("replace: %v", got)
	}

	h.ExpireRemotePresence(t0) // B was seen after t0: kept
	if !equalStrings(names(h.Present(board)), []string{"Beto", "Tester"}) {
		t.Errorf("present = %v", names(h.Present(board)))
	}
	h.ExpireRemotePresence(t0.Add(2 * time.Second)) // B went quiet: it died
	if got := presenceEvents(t, observer, 50*time.Millisecond); !equalStrings(got, []string{"presence.left Beto"}) {
		t.Errorf("expire: %v", got)
	}
}

func TestObserverHearsLocalChangesAndWakeAll(t *testing.T) {
	h := NewHub(8, testLeaveDelay)
	defer h.Close()
	var mu sync.Mutex
	var heard []string
	h.ObservePresence(func(_ domain.ID, who domain.Presence, present bool) {
		mu.Lock()
		defer mu.Unlock()
		heard = append(heard, map[bool]string{true: "+", false: "-"}[present]+who.Name)
	})
	board := domain.NewID()
	ana := user("Ana")
	ch, _, tab1 := h.Subscribe(board, ana)
	_, _, tab2 := h.Subscribe(board, ana)
	tab1()
	tab2()
	time.Sleep(3 * testLeaveDelay)
	mu.Lock()
	if !equalStrings(heard, []string{"+Ana", "-Ana"}) {
		t.Errorf("observer heard %v, want one join and one leave", heard)
	}
	mu.Unlock()

	if _, ok := <-ch; ok {
		t.Fatal("cancelled subscription still open")
	}
	live, cancel := subscribe(h, board)
	defer cancel()
	h.WakeAll()
	select {
	case ev := <-live:
		if ev.Type != domain.EventWake || ev.BoardID != board {
			t.Errorf("got %+v, want a wake for the board", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("WakeAll did not reach the stream")
	}
}
