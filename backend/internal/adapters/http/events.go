package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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
)

// streamEvent is the data of each SSE message; it mirrors an activity item.
type streamEvent struct {
	ID        int64           `json:"id"` // activity id, also the SSE id
	Type      string          `json:"type"`
	BoardID   uuid.UUID       `json:"board_id"`
	ActorID   *uuid.UUID      `json:"actor_id"`
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
// changes. Every heartbeat it sends a ": ping" comment and re-checks that
// the user can still read the board (they may have been removed meanwhile).
// It returns when the client disconnects, when the hub drops a slow client
// (EventSource then reconnects) or when the server shuts down.
func (h *eventsHandler) stream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	acc := access(r)
	events, cancel, err := h.streams.Watch(ctx, acc, pathID(r))
	if err != nil {
		writeBoardError(w, r, "watch board", err)
		return
	}
	defer cancel()

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

	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("X-Accel-Buffering", "no") // nginx: do not buffer the stream
	w.WriteHeader(http.StatusOK)
	if err := write("retry: %d\n\n", streamRetryMillis); err != nil {
		return
	}

	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return // client went away

		case ev, ok := <-events:
			if !ok {
				return // dropped as too slow, or shutting down
			}
			data, err := json.Marshal(newStreamEvent(ev))
			if err != nil {
				slog.Error("encode stream event", "err", err, "request_id", RequestID(ctx))
				continue
			}
			if err := write("id: %d\nevent: %s\ndata: %s\n\n", ev.ActivityID, ev.Type, data); err != nil {
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

func newStreamEvent(ev domain.Event) streamEvent {
	out := streamEvent{
		ID: ev.ActivityID, Type: ev.Type, BoardID: uuid.UUID(ev.BoardID), Payload: ev.Payload, CreatedAt: ev.OccurredAt,
	}
	if ev.ActorID != nil {
		id := uuid.UUID(*ev.ActorID)
		out.ActorID = &id
	}
	return out
}
