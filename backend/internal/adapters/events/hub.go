package events

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

const (
	// DefaultBuffer is how many events a subscriber may fall behind before
	// the hub drops it.
	DefaultBuffer = 64
	// DefaultLeaveDelay is how long a user stays present after their last
	// connection closes, so a page reload does not flash them out and in.
	DefaultLeaveDelay = 4 * time.Second
)

// Hub fans events out to in-memory subscribers, grouped by board, and
// derives presence from them. It implements the EventPublisher port of the
// use cases and the EventSubscriber port of board.StreamService. Safe for
// concurrent use.
//
// Publishing never blocks: every subscriber has a buffered channel, and one
// whose buffer is full (a slow client) is dropped and its channel closed, so
// it reconnects and resyncs instead of holding up everyone else.
//
// Presence counts connections per user and board: several tabs of one user
// are one presence. presence.joined goes out when the first connection
// opens; presence.left only once the last one has been closed for the leave
// delay, and not at all if the user reconnects within it.
type Hub struct {
	buffer     int
	leaveDelay time.Duration

	mu       sync.Mutex
	boards   map[domain.ID]map[*subscriber]struct{}
	presence map[domain.ID]map[domain.ID]*present // board -> user
	closed   bool
}

type subscriber struct {
	ch   chan domain.Event
	once sync.Once
}

func (s *subscriber) close() { s.once.Do(func() { close(s.ch) }) }

type present struct {
	who   domain.Presence
	conns int
	leave *time.Timer // pending presence.left, once conns reached 0
}

// NewHub returns a hub whose subscribers buffer up to buffer events and
// whose users leave leaveDelay after their last connection (the defaults
// when not positive).
func NewHub(buffer int, leaveDelay time.Duration) *Hub {
	if buffer < 1 {
		buffer = DefaultBuffer
	}
	if leaveDelay <= 0 {
		leaveDelay = DefaultLeaveDelay
	}
	return &Hub{
		buffer: buffer, leaveDelay: leaveDelay,
		boards:   map[domain.ID]map[*subscriber]struct{}{},
		presence: map[domain.ID]map[domain.ID]*present{},
	}
}

// Subscribe registers who as a subscriber of boardID's events and as
// present on the board, and returns who is present now (who included). In
// one step: the snapshot and the events that follow it are consistent, and
// the subscriber does not get its own presence.joined.
//
// The channel is closed when the subscriber falls too far behind or the hub
// is closed. cancel ends the subscription and the presence; it is
// idempotent and must always be called.
func (h *Hub) Subscribe(boardID domain.ID, who domain.Presence) (events <-chan domain.Event, snapshot []domain.Presence, cancel func()) {
	s := &subscriber{ch: make(chan domain.Event, h.buffer)}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		s.close()
		return s.ch, nil, func() {}
	}
	h.join(boardID, who) // before adding s: s must not get its own joined
	subs := h.boards[boardID]
	if subs == nil {
		subs = map[*subscriber]struct{}{}
		h.boards[boardID] = subs
	}
	subs[s] = struct{}{}

	var once sync.Once
	return s.ch, h.presentLocked(boardID), func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.remove(boardID, s)
			h.leave(boardID, who.UserID)
		})
	}
}

// Publish delivers each event to the subscribers of its board without
// blocking; subscribers with a full buffer are dropped.
func (h *Hub) Publish(_ context.Context, events ...domain.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ev := range events {
		h.publishLocked(ev)
	}
}

// Present returns who is present on boardID, by name. A user whose last
// connection closed less than the leave delay ago is still present.
func (h *Hub) Present(boardID domain.ID) []domain.Presence {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.presentLocked(boardID)
}

// Subscribers returns how many subscribers boardID has.
func (h *Hub) Subscribers(boardID domain.ID) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.boards[boardID])
}

// Close closes every subscription and refuses new ones; it lets open
// streams end when the server shuts down. No presence.left is sent.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for boardID, subs := range h.boards {
		for s := range subs {
			h.remove(boardID, s)
		}
	}
	for _, users := range h.presence {
		for _, p := range users {
			if p.leave != nil {
				p.leave.Stop()
			}
		}
	}
	h.presence = map[domain.ID]map[domain.ID]*present{}
}

// --- with h.mu held ---

func (h *Hub) publishLocked(ev domain.Event) {
	for s := range h.boards[ev.BoardID] {
		select {
		case s.ch <- ev:
		default:
			h.remove(ev.BoardID, s)
		}
	}
}

func (h *Hub) join(boardID domain.ID, who domain.Presence) {
	users := h.presence[boardID]
	if users == nil {
		users = map[domain.ID]*present{}
		h.presence[boardID] = users
	}
	p := users[who.UserID]
	switch {
	case p == nil:
		p = &present{who: who}
		users[who.UserID] = p
		h.publishLocked(domain.PresenceEvent(domain.EventPresenceJoined, boardID, who))
	case p.leave != nil:
		// Back within the leave delay (a reload): nobody saw them leave.
		p.leave.Stop()
		p.leave = nil
	}
	p.conns++
}

func (h *Hub) leave(boardID, userID domain.ID) {
	p := h.presence[boardID][userID]
	if p == nil {
		return // the hub was closed meanwhile
	}
	if p.conns--; p.conns > 0 {
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(h.leaveDelay, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		// Still the same pending leave: not rejoined, not closed.
		if cur := h.presence[boardID][userID]; cur != p || p.leave != timer {
			return
		}
		delete(h.presence[boardID], userID)
		if len(h.presence[boardID]) == 0 {
			delete(h.presence, boardID)
		}
		h.publishLocked(domain.PresenceEvent(domain.EventPresenceLeft, boardID, p.who))
	})
	p.leave = timer
}

func (h *Hub) presentLocked(boardID domain.ID) []domain.Presence {
	out := make([]domain.Presence, 0, len(h.presence[boardID]))
	for _, p := range h.presence[boardID] {
		out = append(out, p.who)
	}
	slices.SortFunc(out, func(a, b domain.Presence) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.UserID.String(), b.UserID.String())
	})
	return out
}

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
