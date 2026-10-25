package events

import (
	"context"

	"github.com/DarkApofis/tablero/backend/internal/adapters/metrics"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Publisher is the EventPublisher port of the use cases.
type Publisher interface {
	Publish(ctx context.Context, events ...domain.Event)
}

// Counting wraps a Publisher and counts what this instance publishes, by
// event type: the events of the changes committed here, not the ones that
// arrive from other instances.
type Counting struct {
	Next      Publisher
	Published *metrics.CounterVec
}

func (c Counting) Publish(ctx context.Context, events ...domain.Event) {
	for _, ev := range events {
		c.Published.Add(string(ev.Type), 1)
	}
	c.Next.Publish(ctx, events...)
}
