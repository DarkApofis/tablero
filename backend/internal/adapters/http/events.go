package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/adapters/metrics"
	"github.com/DarkApofis/tablero/backend/internal/app/board"
	"github.com/DarkApofis/tablero/backend/internal/app/workspace"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

const (
	defaultHeartbeat = 25 * time.Second
	// defaultStreamWriteTimeout bounds each write of a stream, so a dead
	// connection is noticed, without capping how long the stream lives.
	defaultStreamWriteTimeout = 10 * time.Second
	// streamRetryMillis tells EventSource how soon to reconnect.
	streamRetryMillis = 3000

	excludeSelfHeader = "X-Exclude-Self"
)

// streamEvent is the data of each SSE message.
type streamEvent struct {
	ID      int64            `json:"id"` // activity id, also the SSE id
	Type    domain.EventType `json:"type"`
	BoardID uuid.UUID        `json:"board_id"`
	ActorID *uuid.UUID       `json:"actor_id"`
	// Version is the task's version after the change; null for column and
	// label events.
	Version   *int32          `json:"version"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// presenceUser is how presence shows a user, in the stream and in GET /presence.
type presenceUser struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

func newPresenceUsers(ps []domain.Presence) []presenceUser {
	out := make([]presenceUser, 0, len(ps))
	for _, p := range ps {
		out = append(out, presenceUser{ID: uuid.UUID(p.UserID), Name: p.Name})
	}
	return out
}

// presenceMessage is the data of presence.snapshot (Users) and of
// presence.joined / presence.left (User).
type presenceMessage struct {
	Type    domain.EventType `json:"type"`
	BoardID uuid.UUID        `json:"board_id"`
	User    *presenceUser    `json:"user,omitempty"`
	Users   []presenceUser   `json:"users,omitempty"`
}

type eventsHandler struct {
	streams      *board.StreamService
	authz        *workspace.Authorizer
	heartbeat    time.Duration
	writeTimeout time.Duration
	// delivery observes, for each live event, the time from its publication
	// to the flush of its message.
	delivery *metrics.Histogram
}

// stream: GET /boards/{id}/events, a text/event-stream of the board's
// committed changes. With Last-Event-ID (header, or ?last_event_id= when
// opening a new EventSource) it first replays what the client missed, or
// sends a resync if that is too much. ?exclude_self=true (or the
// X-Exclude-Self: true header) leaves out the caller's own changes.
//
// Right after connecting it sends presence.snapshot, who is connected (the
// caller included), and then presence.joined / presence.left as people come
// and go. Presence messages carry no SSE id, so they never move the
// client's Last-Event-ID.
//
// Every heartbeat it sends ": ping" and re-checks that the user can still
// read the board. It returns when the client disconnects, when the hub drops
// a slow client (it reconnects and catches up) or when the server shuts down.
func (h *eventsHandler) stream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	acc := access(r)
	opts := board.StreamOptions{LastEventID: lastEventID(r), ExcludeSelf: excludeSelf(r)}
	st, err := h.streams.Open(ctx, acc, pathID(r), opts)
	if err != nil {
		writeBoardError(w, r, "open board stream", err)
		return
	}
	defer st.Close()

	rc := http.NewResponseController(w)
	// write sets a fresh deadline before each write: it replaces the server's
	// WriteTimeout for this connection, which would otherwise cut the stream.
	write := func(format string, args ...any) error {
		if err := rc.SetWriteDeadline(time.Now().Add(h.writeTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return err
		}
		return rc.Flush()
	}
	// send writes what is new in the log since the last message sent;
	// published has the publication time of the live events that woke the
	// stream, to measure their delivery (nil: none).
	send := func(published map[int64]time.Time) error {
		msgs, err := st.Pull(ctx)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			if m.Resync {
				err = write("id: %d\nevent: resync\ndata: {\"last_event_id\":%d}\n\n", m.ID, m.ID)
			} else {
				data, encErr := json.Marshal(newStreamEvent(m.Event))
				if encErr != nil {
					return encErr
				}
				err = write("id: %d\nevent: %s\ndata: %s\n\n", m.ID, m.Event.Type, data)
			}
			if err != nil {
				return err
			}
			if t, ok := published[m.ID]; ok && !m.Resync {
				h.delivery.ObserveSince(t)
			}
		}
		return nil
	}

	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("X-Accel-Buffering", "no") // nginx: do not buffer the stream
	w.WriteHeader(http.StatusOK)
	if err := write("retry: %d\n\n", streamRetryMillis); err != nil {
		return
	}
	// sendPresence writes a presence message, without an id.
	sendPresence := func(m presenceMessage) error {
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}
		return write("event: %s\ndata: %s\n\n", m.Type, data)
	}
	boardID := uuid.UUID(pathID(r))
	snapshot := presenceMessage{Type: domain.EventPresenceSnapshot, BoardID: boardID, Users: newPresenceUsers(st.Present())}
	if err := sendPresence(snapshot); err != nil {
		return
	}
	if err := send(nil); err != nil { // the replay, if reconnecting
		return
	}

	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return // client went away

		case ev, ok := <-st.Wake():
			if !ok {
				return // dropped as too slow, or shutting down
			}
			pending, closed := drain(st.Wake())
			var published map[int64]time.Time
			pullLog := false
			for _, ev := range append([]domain.Event{ev}, pending...) {
				if ev.Type == domain.EventReset {
					st.Reset()
				}
				if !ev.Type.IsPresence() {
					pullLog = true // one read of the log covers every change
					if ev.ID != 0 && !ev.PublishedAt.IsZero() {
						if published == nil {
							published = map[int64]time.Time{}
						}
						published[ev.ID] = ev.PublishedAt
					}
					continue
				}
				var p domain.PresencePayload
				if err := json.Unmarshal(ev.Payload, &p); err != nil {
					continue
				}
				u := presenceUser{ID: uuid.UUID(p.User.ID), Name: p.User.Name}
				if err := sendPresence(presenceMessage{Type: ev.Type, BoardID: boardID, User: &u}); err != nil {
					return
				}
			}
			if pullLog {
				if err := send(published); err != nil {
					return
				}
			}
			if closed {
				return
			}

		case <-ticker.C:
			if _, err := h.authz.Authorize(ctx, acc.UserID, acc.WorkspaceID, domain.ActionRead); err != nil {
				return
			}
			// Re-read the log too: if a wake-up was lost (a notification
			// between instances), the delay is one heartbeat at most.
			if err := send(nil); err != nil {
				return
			}
			if err := write(": ping\n\n"); err != nil {
				return
			}
		}
	}
}

// drain takes whatever is already waiting in ch, and reports whether ch
// was closed.
func drain(ch <-chan domain.Event) (events []domain.Event, closed bool) {
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events, true
			}
			events = append(events, ev)
		default:
			return events, false
		}
	}
}

// presence: GET /boards/{id}/presence, who is connected to the board's event
// stream (a user whose last connection closed a moment ago still counts).
func (h *eventsHandler) presence(w http.ResponseWriter, r *http.Request) {
	ps, err := h.streams.Presence(r.Context(), access(r), pathID(r))
	if err != nil {
		writeBoardError(w, r, "board presence", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"users": newPresenceUsers(ps)})
}

// lastEventID reads Last-Event-ID (sent by EventSource when it reconnects)
// or ?last_event_id=. Anything that is not an id this API handed out still
// means the client has some state, so it becomes 0: replay from the start,
// or resync if that is too much.
func lastEventID(r *http.Request) *int64 {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("last_event_id")
	}
	if v == "" {
		return nil
	}
	id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || id < 0 {
		id = 0
	}
	return &id
}

func excludeSelf(r *http.Request) bool {
	v := r.URL.Query().Get("exclude_self")
	if v == "" {
		v = r.Header.Get(excludeSelfHeader)
	}
	b, _ := strconv.ParseBool(v)
	return b
}

func newStreamEvent(ev domain.Event) streamEvent {
	out := streamEvent{
		ID: ev.ID, Type: ev.Type, BoardID: uuid.UUID(ev.BoardID), Version: ev.TaskVersion,
		Payload: ev.Payload, CreatedAt: ev.OccurredAt,
	}
	if ev.ActorID != nil {
		id := uuid.UUID(*ev.ActorID)
		out.ActorID = &id
	}
	return out
}
