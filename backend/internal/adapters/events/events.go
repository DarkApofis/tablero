// Package events implements the EventPublisher port. Phase 2 will add an
// implementation that fans events out to SSE clients.
package events

import (
	"context"
	"sync"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Noop drops every event.
type Noop struct{}

func (Noop) Publish(context.Context, ...domain.Event) {}

// Memory keeps the events it receives, in order. Safe for concurrent use.
type Memory struct {
	mu     sync.Mutex
	events []domain.Event
}

func (m *Memory) Publish(_ context.Context, events ...domain.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, events...)
}

// Events returns a copy of what has been published so far.
func (m *Memory) Events() []domain.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]domain.Event(nil), m.events...)
}
