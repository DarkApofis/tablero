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

type eventsHandler struct {
	streams      *board.StreamService
	authz        *workspace.Authorizer
	heartbeat    time.Duration
	writeTimeout time.Duration
}

// stream: GET /boards/{id}/events, a text/event-stream of the board's
// committed changes. With Last-Event-ID (header, or ?last_event_id= when
// opening a new EventSource) it first replays what the client missed, or
// sends a resync if that is too much. ?exclude_self=true (or the
// X-Exclude-Self: true header) leaves out the caller's own changes.
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
	// send writes what is new in the log since the last message sent.
	send := func() error {
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
	if err := send(); err != nil { // the replay, if reconnecting
		return
	}

	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return // client went away

		case _, ok := <-st.Wake():
			if !ok {
				return // dropped as too slow, or shutting down
			}
			drain(st.Wake()) // one read of the log covers every pending wake-up
			if err := send(); err != nil {
				return
			}

		case <-ticker.C:
			if _, err := h.authz.Authorize(ctx, acc.UserID, acc.WorkspaceID, domain.ActionRead); err != nil {
				return
			}
			if err := write(": ping\n\n"); err != nil {
				return
			}
		}
	}
}

func drain(ch <-chan domain.Event) {
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		default:
			return
		}
	}
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
