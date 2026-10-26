package board

import (
	"context"
	"fmt"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// EventSubscriber delivers the events published for a board as they happen,
// and keeps track of who is connected.
type EventSubscriber interface {
	// Subscribe returns a channel of boardID's events and marks who as
	// present on the board until cancel; snapshot is who is present right
	// then (who included). The channel is closed if the subscriber falls too
	// far behind or the subscriber shuts down; cancel must always be called
	// and is idempotent.
	Subscribe(boardID domain.ID, who domain.Presence) (events <-chan domain.Event, snapshot []domain.Presence, cancel func())
	// Present returns who is present on boardID.
	Present(boardID domain.ID) []domain.Presence
}

// UserDirectory gives the name presence shows for a user.
type UserDirectory interface {
	ByID(ctx context.Context, id domain.ID) (domain.User, error)
}

// ActivityFeed reads a board's log in the order its entries became visible.
type ActivityFeed interface {
	// ActivitySince returns up to limit entries after afterID, oldest first.
	ActivitySince(ctx context.Context, boardID domain.ID, afterID int64, limit int) ([]domain.ActivityEntry, error)
	// LatestActivityID is the id of the newest entry, or 0.
	LatestActivityID(ctx context.Context, boardID domain.ID) (int64, error)
	// HasActivity reports whether entry id is in boardID's log.
	HasActivity(ctx context.Context, boardID domain.ID, id int64) (bool, error)
}

// DefaultReplayLimit is how many missed events a reconnecting client may
// catch up on; past that it is told to reload the board instead.
const DefaultReplayLimit = 500

// StreamService lets members follow a board's changes live.
//
// The activity log is the source of truth: a stream remembers the id of
// the last entry it sent and always reads "entries after it" from the log.
// Live events only wake it up. So a reconnect with Last-Event-ID replays
// exactly what was missed, and live delivery has no gaps or duplicates even
// if two events are published out of order (their ids are not: the log
// makes a board's entries visible in id order).
type StreamService struct {
	events      EventSubscriber
	feed        ActivityFeed
	users       UserDirectory
	replayLimit int
}

func NewStreamService(events EventSubscriber, feed ActivityFeed, users UserDirectory, replayLimit int) *StreamService {
	if replayLimit < 1 {
		replayLimit = DefaultReplayLimit
	}
	return &StreamService{events: events, feed: feed, users: users, replayLimit: replayLimit}
}

// Presence returns who is connected to boardID's event stream, by name.
func (s *StreamService) Presence(_ context.Context, acc domain.Access, boardID domain.ID) ([]domain.Presence, error) {
	if err := acc.Require(domain.ActionRead); err != nil {
		return nil, err
	}
	return s.events.Present(boardID), nil
}

type StreamOptions struct {
	// LastEventID is the id of the last event the client got (from the
	// Last-Event-ID header); nil for a fresh client, which starts from now.
	LastEventID *int64
	// ExcludeSelf leaves out the caller's own changes.
	ExcludeSelf bool
}

// Message is one thing to send: an event, or (Resync) the instruction to
// reload the whole board, after which the stream continues from ID.
type Message struct {
	Event  domain.Event
	Resync bool
	ID     int64
}

// Stream is one client's view of a board's events.
type Stream struct {
	svc     *StreamService
	acc     domain.Access
	boardID domain.ID
	exclude bool
	wake    <-chan domain.Event
	cancel  func()
	present []domain.Presence // who was connected when the stream opened

	cursor int64 // id of the last entry handled (sent or excluded)
	resync bool  // the next Pull must start with a resync
}

// Open starts a stream; any role that can read the board may. The caller
// counts as present on the board until Close. It subscribes before reading
// the log, so nothing committed from then on can fall between the replay
// and the live events. Close it when done.
func (s *StreamService) Open(ctx context.Context, acc domain.Access, boardID domain.ID, opts StreamOptions) (*Stream, error) {
	if err := acc.Require(domain.ActionRead); err != nil {
		return nil, err
	}
	user, err := s.users.ByID(ctx, acc.UserID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	wake, present, cancel := s.events.Subscribe(boardID, domain.Presence{UserID: user.ID, Name: user.Name})
	latest, err := s.feed.LatestActivityID(ctx, boardID)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("latest activity: %w", err)
	}
	st := &Stream{svc: s, acc: acc, boardID: boardID, exclude: opts.ExcludeSelf, wake: wake, cancel: cancel, present: present, cursor: latest}
	if id := opts.LastEventID; id != nil {
		st.cursor = *id
		// An id past the newest entry did not come from this board's log
		// (another board, a reset database): the client's state is unknown.
		st.resync = *id > latest || *id < 0
		if !st.resync && *id > 0 {
			// Nor did an id whose entry is gone: the board was reset since
			// (EventReset), and replaying the new log would not undo the old.
			found, err := s.feed.HasActivity(ctx, boardID, *id)
			if err != nil {
				cancel()
				return nil, fmt.Errorf("find activity %d: %w", *id, err)
			}
			st.resync = !found
		}
	}
	return st, nil
}

// Wake delivers live events. Presence events are complete as they come
// (send them as they are); any other event only signals that there may be
// new entries in the log: Pull them. It is closed when the subscription ends
// (the client was too slow, or shutdown): close the stream then, the client
// will reconnect and catch up.
func (st *Stream) Wake() <-chan domain.Event { return st.wake }

// Present is who was connected to the board when the stream opened, the
// caller included: the presence.snapshot to send first.
func (st *Stream) Present() []domain.Presence { return st.present }

func (st *Stream) Close() { st.cancel() }

// Reset makes the next Pull a resync: call it when Wake delivers EventReset.
func (st *Stream) Reset() { st.resync = true }

// Pull returns, in order, the events after the last one handled: the
// replay on the first call after a reconnect, new events afterwards. If
// there are more than the replay limit, it returns a single resync instead.
func (st *Stream) Pull(ctx context.Context) ([]Message, error) {
	limit := st.svc.replayLimit
	var rows []domain.ActivityEntry
	if !st.resync {
		var err error
		if rows, err = st.svc.feed.ActivitySince(ctx, st.boardID, st.cursor, limit+1); err != nil {
			return nil, fmt.Errorf("activity since %d: %w", st.cursor, err)
		}
	}
	if st.resync || len(rows) > limit {
		latest, err := st.svc.feed.LatestActivityID(ctx, st.boardID)
		if err != nil {
			return nil, fmt.Errorf("latest activity: %w", err)
		}
		st.cursor, st.resync = latest, false
		return []Message{{Resync: true, ID: latest}}, nil
	}

	msgs := make([]Message, 0, len(rows))
	for _, row := range rows {
		st.cursor = row.ID
		if st.exclude && row.ActorID != nil && *row.ActorID == st.acc.UserID {
			continue
		}
		msgs = append(msgs, Message{Event: row.Event(), ID: row.ID})
	}
	return msgs, nil
}
