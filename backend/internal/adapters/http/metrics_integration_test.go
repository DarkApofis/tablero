package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/adapters/metrics"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// A live event is measured from its publication to its flush; the replay
// of a reconnecting client is not.
func TestMetricsMeasureLiveDelivery(t *testing.T) {
	f := newTaskFixture(t)
	deps := f.deps
	deps.Metrics = metrics.NewRegistry()
	srv := httptest.NewServer(NewRouter(deps))
	t.Cleanup(srv.Close)

	s := f.openStream(srv, f.board, f.viewer)
	if fr := s.next(t, time.Second); fr != "retry: 3000" {
		t.Fatalf("first frame = %q", fr)
	}
	waitFor(t, "the subscription", func() bool { return f.hub.Subscribers(domain.ID(f.board)) == 1 })
	f.createTask(f.todo, "medida", f.ana)
	id, _, _ := parseEvent(t, s.next(t, 2*time.Second))

	// Reconnecting from before that event replays it, unmeasured.
	replay := f.openStream(srv, f.board, f.viewer, func(r *http.Request) { r.Header.Set("Last-Event-ID", "0") })
	_ = replay.next(t, time.Second) // retry
	if got, _, _ := parseEvent(t, replay.next(t, 2*time.Second)); got != id {
		t.Fatalf("replayed id %s, want %s", got, id)
	}

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "\ntablero_sse_delivery_seconds_count 1\n") {
		t.Errorf("GET /metrics = %d:\n%s", resp.StatusCode, body)
	}
}
