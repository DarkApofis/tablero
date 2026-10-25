package events

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

func event(board domain.ID, id int64) domain.Event {
	return domain.Event{Type: domain.ActivityTaskUpdated, BoardID: board, ID: id}
}

// receive reads n events or fails after a second.
func receive(t *testing.T, ch <-chan domain.Event, n int) []int64 {
	t.Helper()
	var ids []int64
	for range n {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed after %d events", len(ids))
			}
			ids = append(ids, ev.ID)
		case <-time.After(time.Second):
			t.Fatalf("timed out after %d events", len(ids))
		}
	}
	return ids
}

func closed(ch <-chan domain.Event) bool {
	select {
	case _, ok := <-ch:
		return !ok
	case <-time.After(time.Second):
		return false
	}
}

func TestSubscribersGetTheirBoardsEvents(t *testing.T) {
	h := NewHub(8)
	boardA, boardB := domain.NewID(), domain.NewID()
	a1, cancelA1 := h.Subscribe(boardA)
	defer cancelA1()
	a2, cancelA2 := h.Subscribe(boardA)
	defer cancelA2()
	b, cancelB := h.Subscribe(boardB)
	defer cancelB()

	h.Publish(context.Background(), event(boardA, 1), event(boardB, 2), event(boardA, 3))

	for name, ch := range map[string]<-chan domain.Event{"a1": a1, "a2": a2} {
		if got := receive(t, ch, 2); got[0] != 1 || got[1] != 3 {
			t.Errorf("%s got %v, want [1 3] in order", name, got)
		}
	}
	if got := receive(t, b, 1); got[0] != 2 {
		t.Errorf("b got %v", got)
	}
	select {
	case ev := <-b:
		t.Errorf("b got another board's event: %+v", ev)
	default:
	}
}

func TestPublishWithoutSubscribersIsANoop(t *testing.T) {
	h := NewHub(1)
	h.Publish(context.Background(), event(domain.NewID(), 1))
}

// A subscriber that stops reading is dropped once its buffer is full; the
// publisher is not blocked and the others keep receiving.
func TestSlowSubscriberIsDroppedWithoutBlocking(t *testing.T) {
	h := NewHub(2)
	board := domain.NewID()
	slow, cancelSlow := h.Subscribe(board)
	defer cancelSlow()
	fast, cancelFast := h.Subscribe(board)
	defer cancelFast()

	// fast reads each event right after it is published (in the same
	// goroutine, so it always keeps up); slow never reads.
	done := make(chan struct{})
	var fastGot []int64
	go func() {
		defer close(done)
		for i := range int64(10) {
			h.Publish(context.Background(), event(board, i+1))
			select {
			case ev, ok := <-fast:
				if ok {
					fastGot = append(fastGot, ev.ID)
				}
			case <-time.After(time.Second):
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
	if len(fastGot) != 10 {
		t.Errorf("fast got %v, want all 10", fastGot)
	}
	// The slow one got the first two (its buffer) and then its channel closed.
	if got := receive(t, slow, 2); got[0] != 1 || got[1] != 2 {
		t.Errorf("slow got %v before being dropped", got)
	}
	if !closed(slow) {
		t.Error("slow subscriber's channel was not closed")
	}
	if n := h.Subscribers(board); n != 1 {
		t.Errorf("subscribers = %d, want only the fast one", n)
	}
	cancelSlow() // cancelling after being dropped is harmless
}

func TestCancelUnsubscribesAndCleansUp(t *testing.T) {
	h := NewHub(4)
	board := domain.NewID()
	ch, cancel := h.Subscribe(board)
	if h.Subscribers(board) != 1 {
		t.Fatal("not subscribed")
	}

	cancel()
	cancel() // idempotent
	if !closed(ch) {
		t.Error("channel not closed on cancel")
	}
	if n := h.Subscribers(board); n != 0 {
		t.Errorf("subscribers = %d after cancel", n)
	}
	if len(h.boards) != 0 {
		t.Errorf("empty board entry left behind: %v", h.boards)
	}
	h.Publish(context.Background(), event(board, 1)) // no panic on a closed channel
}

func TestCloseEndsEverySubscription(t *testing.T) {
	h := NewHub(4)
	a, cancelA := h.Subscribe(domain.NewID())
	defer cancelA()
	b, cancelB := h.Subscribe(domain.NewID())
	defer cancelB()

	h.Close()
	if !closed(a) || !closed(b) {
		t.Error("Close left subscriptions open")
	}
	late, cancelLate := h.Subscribe(domain.NewID())
	defer cancelLate()
	if !closed(late) {
		t.Error("a subscription after Close stayed open")
	}
}

// Publishing, subscribing and cancelling from many goroutines at once must
// not deadlock, panic (send on closed channel) or leak subscribers.
func TestConcurrentUse(t *testing.T) {
	h := NewHub(4)
	board := domain.NewID()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			ch, cancel := h.Subscribe(board)
			defer cancel()
			for range i % 5 {
				select {
				case <-ch:
				case <-time.After(10 * time.Millisecond):
				}
			}
		})
		wg.Go(func() { h.Publish(context.Background(), event(board, int64(i))) })
	}
	wg.Wait()
	if n := h.Subscribers(board); n != 0 {
		t.Errorf("subscribers left = %d", n)
	}
}
