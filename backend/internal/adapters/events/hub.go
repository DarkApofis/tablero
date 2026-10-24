package events

import (
	"context"
	"sync"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// DefaultBuffer is how many events a subscriber may fall behind before the
// hub drops it.
const DefaultBuffer = 64

// Hub fans events out to in-memory subscribers, grouped by board. It
// implements the EventPublisher port of the use cases and the
// EventSubscriber port of board.StreamService. Safe for concurrent use.
//
// Publishing never blocks: every subscriber has a buffered channel, and one
// whose buffer is full (a slow client) is dropped and its channel closed, so
// it reconnects and resyncs instead of holding up everyone else.
type Hub struct {
	buffer int

	mu     sync.Mutex
	boards map[domain.ID]map[*subscriber]struct{}
	closed bool
}

type subscriber struct {
	ch   chan domain.Event
	once sync.Once
}

func (s *subscriber) close() { s.once.Do(func() { close(s.ch) }) }

// NewHub returns a hub whose subscribers buffer up to buffer events
// (DefaultBuffer if buffer < 1).
func NewHub(buffer int) *Hub {
	if buffer < 1 {
		buffer = DefaultBuffer
	}
	return &Hub{buffer: buffer, boards: map[domain.ID]map[*subscriber]struct{}{}}
}

// Subscribe registers a subscriber for boardID's events. The channel is
// closed when the subscriber falls too far behind, when cancel is called or
// when the hub is closed. cancel is idempotent and must always be called.
func (h *Hub) Subscribe(boardID domain.ID) (events <-chan domain.Event, cancel func()) {
	s := &subscriber{ch: make(chan domain.Event, h.buffer)}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		s.close()
		return s.ch, func() {}
	}
	subs := h.boards[boardID]
	if subs == nil {
		subs = map[*subscriber]struct{}{}
		h.boards[boardID] = subs
	}
	subs[s] = struct{}{}

	return s.ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.remove(boardID, s)
	}
}

// Publish delivers each event to the subscribers of its board without
// blocking; subscribers with a full buffer are dropped.
func (h *Hub) Publish(_ context.Context, events ...domain.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ev := range events {
		for s := range h.boards[ev.BoardID] {
			select {
			case s.ch <- ev:
			default:
				h.remove(ev.BoardID, s)
			}
		}
	}
}

// Subscribers returns how many subscribers boardID has.
func (h *Hub) Subscribers(boardID domain.ID) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.boards[boardID])
}

// Close closes every subscription and refuses new ones; it lets open
// streams end when the server shuts down.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for boardID, subs := range h.boards {
		for s := range subs {
			h.remove(boardID, s)
		}
	}
}

// remove must be called with h.mu held.
func (h *Hub) remove(boardID domain.ID, s *subscriber) {
	subs := h.boards[boardID]
	if _, ok := subs[s]; !ok {
		return
	}
	delete(subs, s)
	if len(subs) == 0 {
		delete(h.boards, boardID)
	}
	s.close()
}
