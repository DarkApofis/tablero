package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/adapters/events"
	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres/db"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Channel is the LISTEN/NOTIFY channel every API instance shares.
const Channel = "tablero_events"

// maxPayload keeps notifications under Postgres's 8000-byte limit.
const maxPayload = 7000

// EventBusOptions tunes an EventBus; zero values take the defaults.
type EventBusOptions struct {
	// Instance names this process in notifications (random by default).
	Instance string
	// Beat is how often this instance announces its presence (10s); an
	// announcement older than TTL (3 beats) is forgotten.
	Beat, TTL time.Duration
	// MinBackoff and MaxBackoff bound the wait between listener
	// reconnection attempts (100ms, 10s).
	MinBackoff, MaxBackoff time.Duration
	// Dial opens the listener's dedicated connection; tests replace it.
	Dial   func(ctx context.Context) (*pgx.Conn, error)
	Logger *slog.Logger
}

// EventBus is the EventPublisher for several API instances sharing one
// database. Publish hands events to the local hub and sends a small
// NOTIFY (board and event id: entries can be far larger than a payload may
// be). Each instance LISTENs on a dedicated connection, outside the pool;
// for a notification from another instance it reads the entry from
// activity_log and hands it to its own hub.
//
// Presence travels the same channel: each instance announces its own
// connections (on change, and in a periodic beat) and its hub keeps the
// union with what the others announce, forgetting announcements older than
// the TTL (an instance that died).
type EventBus struct {
	hub      *events.Hub
	q        *db.Queries
	instance string
	opts     EventBusOptions
	log      *slog.Logger

	// outbox carries presence changes from the hub's observer (which must
	// not block) to the sender.
	outbox chan notification

	mu        sync.Mutex
	connected bool
}

// notification is the payload of every NOTIFY, kept short on purpose.
type notification struct {
	Kind     string          `json:"k"`           // e event, r board reset, p presence change, s presence beat, h hello
	Origin   string          `json:"o"`           // sending instance
	BoardID  *uuid.UUID      `json:"b,omitempty"` // e, p, s
	EventID  int64           `json:"id,omitempty"`
	User     *presenceEntry  `json:"u,omitempty"` // p
	Present  bool            `json:"on,omitempty"`
	Users    []presenceEntry `json:"us,omitempty"`   // s
	Continue bool            `json:"more,omitempty"` // s: another part of the same beat, merge instead of replace
}

type presenceEntry struct {
	ID   uuid.UUID `json:"i"`
	Name string    `json:"n"`
}

func NewEventBus(pool *pgxpool.Pool, databaseURL string, hub *events.Hub, opts EventBusOptions) *EventBus {
	if opts.Instance == "" {
		opts.Instance = domain.NewID().String()[:8]
	}
	if opts.Beat <= 0 {
		opts.Beat = 10 * time.Second
	}
	if opts.TTL <= 0 {
		opts.TTL = 3 * opts.Beat
	}
	if opts.MinBackoff <= 0 {
		opts.MinBackoff = 100 * time.Millisecond
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = 10 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	b := &EventBus{
		hub: hub, q: db.New(pool), instance: opts.Instance, opts: opts,
		log:    opts.Logger.With("component", "event_bus", "instance", opts.Instance),
		outbox: make(chan notification, 1024),
	}
	if b.opts.Dial == nil {
		b.opts.Dial = func(ctx context.Context) (*pgx.Conn, error) {
			cfg, err := pgx.ParseConfig(databaseURL)
			if err != nil {
				return nil, err
			}
			cfg.RuntimeParams["application_name"] = ListenerName(opts.Instance)
			return pgx.ConnectConfig(ctx, cfg)
		}
	}
	hub.ObservePresence(b.onLocalPresence)
	return b
}

// ListenerName is the application_name of an instance's listener connection.
func ListenerName(instance string) string { return "tablero-events-" + instance }

func (b *EventBus) Instance() string { return b.instance }

// Connected reports whether the listener is currently listening.
func (b *EventBus) Connected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.connected
}

// Publish delivers events to this instance's subscribers right away and
// notifies the other instances. Presence events are only local: presence
// travels on its own (onLocalPresence). A notification that cannot be sent
// is logged; the other instances' streams still catch up on their next
// heartbeat, which re-reads the log.
func (b *EventBus) Publish(ctx context.Context, evs ...domain.Event) {
	b.hub.Publish(ctx, evs...)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for _, ev := range evs {
		if ev.Type == domain.EventReset {
			board := uuid.UUID(ev.BoardID)
			b.send(ctx, notification{Kind: "r", BoardID: &board})
			continue
		}
		if ev.Type.IsPresence() || ev.ID == 0 {
			continue
		}
		board := uuid.UUID(ev.BoardID)
		b.send(ctx, notification{Kind: "e", BoardID: &board, EventID: ev.ID})
	}
}

// Run listens until ctx ends, reconnecting with backoff, and keeps the
// presence announcements going. It returns ctx's error.
func (b *EventBus) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Go(func() { b.sendLoop(ctx) })
	wg.Go(func() { b.beatLoop(ctx) })
	b.listenLoop(ctx)
	wg.Wait()
	return ctx.Err()
}

func (b *EventBus) listenLoop(ctx context.Context) {
	backoff := b.opts.MinBackoff
	for ctx.Err() == nil {
		wasConnected, err := b.listen(ctx)
		b.setConnected(false)
		if ctx.Err() != nil {
			return
		}
		if wasConnected { // a drop after a good connection starts the backoff over
			backoff = b.opts.MinBackoff
		}
		b.log.Warn("event listener disconnected", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter(backoff)):
		}
		backoff = min(2*backoff, b.opts.MaxBackoff)
	}
}

// listen connects, LISTENs and handles notifications until the connection
// fails. Every successful (re)connection resyncs: notifications sent while
// it was down are lost, so local streams re-read the log and presence is
// asked for again.
func (b *EventBus) listen(ctx context.Context) (connected bool, err error) {
	conn, err := b.opts.Dial(ctx)
	if err != nil {
		return false, fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return false, fmt.Errorf("listen: %w", err)
	}
	b.setConnected(true)
	b.log.Info("event listener connected")
	b.resync(ctx)

	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return true, err
		}
		b.handle(ctx, n.Payload)
	}
}

func (b *EventBus) resync(ctx context.Context) {
	b.hub.WakeAll()
	b.enqueue(notification{Kind: "h"}) // the others answer with their presence
	b.beat(ctx)
}

func (b *EventBus) handle(ctx context.Context, payload string) {
	var n notification
	if err := json.Unmarshal([]byte(payload), &n); err != nil {
		b.log.Warn("bad notification", "err", err)
		return
	}
	if n.Origin == b.instance {
		return // already delivered locally by Publish
	}
	now := time.Now()
	switch n.Kind {
	case "e":
		if n.BoardID == nil || b.hub.Subscribers(domain.ID(*n.BoardID)) == 0 {
			return // nobody here is watching that board
		}
		row, err := b.q.GetActivity(ctx, n.EventID)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) { // no row: the board was deleted since
				b.log.Warn("read notified event", "err", err, "id", n.EventID)
			}
			return
		}
		b.hub.Publish(ctx, toActivity(row, "").Event())
	case "r":
		if n.BoardID != nil {
			b.hub.Publish(ctx, domain.Event{Type: domain.EventReset, BoardID: domain.ID(*n.BoardID)})
		}
	case "p":
		if n.BoardID == nil || n.User == nil {
			return
		}
		who := domain.Presence{UserID: domain.ID(n.User.ID), Name: n.User.Name}
		if n.Present {
			b.hub.SetRemotePresence(n.Origin, domain.ID(*n.BoardID), []domain.Presence{who}, false, now)
		} else {
			b.hub.RemoveRemotePresence(n.Origin, domain.ID(*n.BoardID), who, now)
		}
	case "s":
		if n.BoardID == nil {
			return
		}
		users := make([]domain.Presence, len(n.Users))
		for i, u := range n.Users {
			users[i] = domain.Presence{UserID: domain.ID(u.ID), Name: u.Name}
		}
		b.hub.SetRemotePresence(n.Origin, domain.ID(*n.BoardID), users, !n.Continue, now)
	case "h":
		b.beat(ctx)
	}
}

// onLocalPresence is the hub's observer: it must not block.
func (b *EventBus) onLocalPresence(boardID domain.ID, who domain.Presence, present bool) {
	board := uuid.UUID(boardID)
	b.enqueue(notification{Kind: "p", BoardID: &board, User: &presenceEntry{ID: uuid.UUID(who.UserID), Name: who.Name}, Present: present})
}

func (b *EventBus) enqueue(n notification) {
	select {
	case b.outbox <- n:
	default:
		b.log.Warn("presence outbox full; the next beat will carry it")
	}
}

func (b *EventBus) sendLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-b.outbox:
			b.send(ctx, n)
		}
	}
}

func (b *EventBus) beatLoop(ctx context.Context) {
	t := time.NewTicker(b.opts.Beat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.beat(ctx)
			b.hub.ExpireRemotePresence(time.Now().Add(-b.opts.TTL))
		}
	}
}

// beat announces this instance's whole presence, per board, split in parts
// that fit a notification.
func (b *EventBus) beat(ctx context.Context) {
	for boardID, users := range b.hub.LocalPresence() {
		board := uuid.UUID(boardID)
		part := notification{Kind: "s", BoardID: &board}
		size := 0
		for _, u := range users {
			e := presenceEntry{ID: uuid.UUID(u.UserID), Name: u.Name}
			if entry := 60 + len(e.Name); size+entry > maxPayload && len(part.Users) > 0 {
				b.send(ctx, part)
				part = notification{Kind: "s", BoardID: &board, Continue: true}
				size = 0
			}
			part.Users = append(part.Users, e)
			size += 60 + len(e.Name)
		}
		b.send(ctx, part)
	}
}

func (b *EventBus) send(ctx context.Context, n notification) {
	n.Origin = b.instance
	data, err := json.Marshal(n)
	if err == nil && len(data) > maxPayload {
		err = fmt.Errorf("payload of %d bytes", len(data))
	}
	if err == nil {
		err = b.q.NotifyEvents(ctx, string(data))
	}
	if err != nil && ctx.Err() == nil {
		b.log.Warn("notify", "err", err, "kind", n.Kind)
	}
}

func (b *EventBus) setConnected(v bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.connected = v
}

// jitter spreads reconnection attempts of many instances: d ±25%.
func jitter(d time.Duration) time.Duration {
	return d*3/4 + rand.N(d/2+1)
}
