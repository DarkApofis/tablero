package events

import (
	"context"
	"testing"

	"github.com/DarkApofis/tablero/backend/internal/adapters/metrics"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

func TestCountingCountsByType(t *testing.T) {
	var next Memory
	published := metrics.NewRegistry().CounterVec("published_total", "Published.", "type")
	c := Counting{Next: &next, Published: published}
	board := domain.NewID()
	c.Publish(context.Background(),
		domain.Event{Type: domain.EventTaskMoved, BoardID: board, ID: 1},
		domain.Event{Type: domain.EventTaskMoved, BoardID: board, ID: 2},
		domain.Event{Type: domain.EventColumnCreated, BoardID: board, ID: 3})
	if got := published.Value(string(domain.EventTaskMoved)); got != 2 {
		t.Errorf("task.moved = %d, want 2", got)
	}
	if got := published.Value(string(domain.EventColumnCreated)); got != 1 {
		t.Errorf("column.created = %d, want 1", got)
	}
	if got := len(next.Events()); got != 3 {
		t.Errorf("passed on %d events, want 3", got)
	}
}
