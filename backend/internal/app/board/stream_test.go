package board_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DarkApofis/tablero/backend/internal/app/board"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

type fakeSubscriber struct {
	subscribed int
	cancelled  int
	who        []domain.Presence
}

func (f *fakeSubscriber) Subscribe(_ domain.ID, who domain.Presence) (<-chan domain.Event, []domain.Presence, func()) {
	f.subscribed++
	f.who = append(f.who, who)
	return make(chan domain.Event), f.who, func() { f.cancelled++ }
}

func (f *fakeSubscriber) Present(domain.ID) []domain.Presence { return f.who }

// directory knows every user: their name is "user-" + the start of their id.
type directory struct{}

func (directory) ByID(_ context.Context, id domain.ID) (domain.User, error) {
	return domain.User{ID: id, Name: "user-" + id.String()[:4]}, nil
}

// memFeed is a board log with entries 1..n (ids may skip, like the real
// log's, which is shared by every board).
type memFeed struct{ entries []domain.ActivityEntry }

func (m *memFeed) add(id int64, actor domain.ID) {
	m.entries = append(m.entries, domain.ActivityEntry{ID: id, ActorID: &actor, Type: domain.ActivityTaskMoved, Payload: []byte(`{"version":3}`)})
}

func (m *memFeed) ActivitySince(_ context.Context, _ domain.ID, after int64, limit int) ([]domain.ActivityEntry, error) {
	var out []domain.ActivityEntry
	for _, e := range m.entries {
		if e.ID > after && len(out) < limit {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *memFeed) LatestActivityID(context.Context, domain.ID) (int64, error) {
	if len(m.entries) == 0 {
		return 0, nil
	}
	return m.entries[len(m.entries)-1].ID, nil
}

func ids(msgs []board.Message) []int64 {
	var out []int64
	for _, m := range msgs {
		if m.Resync {
			out = append(out, -m.ID) // negative marks a resync
			continue
		}
		out = append(out, m.ID)
	}
	return out
}

func pull(t *testing.T, st *board.Stream) []int64 {
	t.Helper()
	msgs, err := st.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ids(msgs)
}

func equal(a, b []int64) bool {
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

func TestOpenRequiresReadAndSubscribesFirst(t *testing.T) {
	subs, feed := &fakeSubscriber{}, &memFeed{}
	svc := board.NewStreamService(subs, feed, directory{}, 10)
	ctx := context.Background()
	if _, err := svc.Open(ctx, domain.Access{}, domain.NewID(), board.StreamOptions{}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("no role: err = %v", err)
	}
	if subs.subscribed != 0 {
		t.Error("a forbidden Open subscribed")
	}
	st, err := svc.Open(ctx, domain.Access{Role: domain.RoleViewer}, domain.NewID(), board.StreamOptions{})
	if err != nil || subs.subscribed != 1 {
		t.Fatalf("viewer Open = %v, subscribed %d", err, subs.subscribed)
	}
	st.Close()
	if subs.cancelled != 1 {
		t.Error("Close did not cancel the subscription")
	}
}

func TestFreshStreamStartsFromNow(t *testing.T) {
	feed := &memFeed{}
	actor := domain.NewID()
	feed.add(1, actor)
	feed.add(4, actor)
	st, _ := board.NewStreamService(&fakeSubscriber{}, feed, directory{}, 10).Open(context.Background(), domain.Access{Role: domain.RoleViewer}, domain.NewID(), board.StreamOptions{})

	if got := pull(t, st); len(got) != 0 {
		t.Errorf("fresh stream replayed %v", got)
	}
	feed.add(9, actor)
	feed.add(12, actor)
	if got := pull(t, st); !equal(got, []int64{9, 12}) {
		t.Errorf("live = %v, want [9 12]", got)
	}
	// Woken again with nothing new (or for an event already sent): no duplicates.
	if got := pull(t, st); len(got) != 0 {
		t.Errorf("second pull = %v", got)
	}
}

func TestReconnectReplaysWhatWasMissed(t *testing.T) {
	feed := &memFeed{}
	actor := domain.NewID()
	for _, id := range []int64{3, 5, 8, 13} {
		feed.add(id, actor)
	}
	last := int64(5)
	st, _ := board.NewStreamService(&fakeSubscriber{}, feed, directory{}, 10).Open(context.Background(), domain.Access{Role: domain.RoleViewer}, domain.NewID(), board.StreamOptions{LastEventID: &last})

	if got := pull(t, st); !equal(got, []int64{8, 13}) {
		t.Errorf("replay = %v, want [8 13]", got)
	}
	feed.add(21, actor)
	if got := pull(t, st); !equal(got, []int64{21}) {
		t.Errorf("then live = %v", got)
	}
}

func TestTooLargeAGapAsksForAResync(t *testing.T) {
	feed := &memFeed{}
	actor := domain.NewID()
	for id := int64(1); id <= 8; id++ {
		feed.add(id, actor)
	}
	svc := board.NewStreamService(&fakeSubscriber{}, feed, directory{}, 5)
	viewer := domain.Access{Role: domain.RoleViewer}
	ctx := context.Background()

	last := int64(2) // 6 missed > limit 5
	st, _ := svc.Open(ctx, viewer, domain.NewID(), board.StreamOptions{LastEventID: &last})
	if got := pull(t, st); !equal(got, []int64{-8}) {
		t.Errorf("got %v, want a resync at 8", got)
	}
	feed.add(9, actor)
	if got := pull(t, st); !equal(got, []int64{9}) {
		t.Errorf("after the resync = %v, want [9]", got)
	}

	// Exactly the limit (5 missed: 5..9) still replays.
	last = 4
	st, _ = svc.Open(ctx, viewer, domain.NewID(), board.StreamOptions{LastEventID: &last})
	if got := pull(t, st); !equal(got, []int64{5, 6, 7, 8, 9}) {
		t.Errorf("at the limit = %v, want [5 6 7 8 9]", got)
	}

	// An id from the future (another board, a reset database) cannot be trusted.
	last = 1000
	st, _ = svc.Open(ctx, viewer, domain.NewID(), board.StreamOptions{LastEventID: &last})
	if got := pull(t, st); !equal(got, []int64{-9}) {
		t.Errorf("future id = %v, want a resync at 9", got)
	}
}

func TestExcludeSelfSkipsOnlyTheCallersEvents(t *testing.T) {
	feed := &memFeed{}
	me, other := domain.NewID(), domain.NewID()
	feed.add(1, me)
	feed.add(2, other)
	feed.add(3, me)
	last := int64(0)
	svc := board.NewStreamService(&fakeSubscriber{}, feed, directory{}, 10)
	ctx := context.Background()
	acc := domain.Access{UserID: me, Role: domain.RoleMember}

	st, _ := svc.Open(ctx, acc, domain.NewID(), board.StreamOptions{LastEventID: &last, ExcludeSelf: true})
	if got := pull(t, st); !equal(got, []int64{2}) {
		t.Errorf("exclude self = %v, want [2]", got)
	}
	feed.add(4, me)
	if got := pull(t, st); len(got) != 0 {
		t.Errorf("own live event sent: %v", got) // and not re-read on the next pull either
	}

	st, _ = svc.Open(ctx, acc, domain.NewID(), board.StreamOptions{LastEventID: &last})
	if got := pull(t, st); !equal(got, []int64{1, 2, 3, 4}) {
		t.Errorf("without the option = %v", got)
	}
}

func TestEventsCarryTheTaskVersion(t *testing.T) {
	feed := &memFeed{}
	feed.add(1, domain.NewID())
	last := int64(0)
	st, _ := board.NewStreamService(&fakeSubscriber{}, feed, directory{}, 10).Open(context.Background(), domain.Access{Role: domain.RoleViewer}, domain.NewID(), board.StreamOptions{LastEventID: &last})
	msgs, _ := st.Pull(context.Background())
	if len(msgs) != 1 || msgs[0].Event.Type != domain.EventTaskMoved || msgs[0].Event.TaskVersion == nil || *msgs[0].Event.TaskVersion != 3 {
		t.Errorf("event = %+v", msgs)
	}
}

func TestOpenMarksTheCallerPresentByName(t *testing.T) {
	subs := &fakeSubscriber{}
	svc := board.NewStreamService(subs, &memFeed{}, directory{}, 10)
	me := domain.NewID()
	st, err := svc.Open(context.Background(), domain.Access{UserID: me, Role: domain.RoleViewer}, domain.NewID(), board.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := domain.Presence{UserID: me, Name: "user-" + me.String()[:4]}
	if len(subs.who) != 1 || subs.who[0] != want || len(st.Present()) != 1 || st.Present()[0] != want {
		t.Errorf("subscribed as %+v, snapshot %+v; want %+v", subs.who, st.Present(), want)
	}

	if p, err := svc.Presence(context.Background(), domain.Access{Role: domain.RoleViewer}, domain.NewID()); err != nil || len(p) != 1 {
		t.Errorf("Presence = %v, %v", p, err)
	}
	if _, err := svc.Presence(context.Background(), domain.Access{}, domain.NewID()); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("Presence without a role: %v", err)
	}
}
