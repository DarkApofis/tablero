package board

import (
	"context"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// EventSubscriber delivers the events published for a board as they happen.
type EventSubscriber interface {
	// Subscribe returns a channel of boardID's events. The channel is closed
	// if the subscriber falls too far behind or the subscriber shuts down;
	// cancel must always be called and is idempotent.
	Subscribe(boardID domain.ID) (events <-chan domain.Event, cancel func())
}

// StreamService lets members follow a board's changes live.
type StreamService struct {
	events EventSubscriber
}

func NewStreamService(events EventSubscriber) *StreamService {
	return &StreamService{events: events}
}

// Watch subscribes to boardID's events; any role that can read the board may.
func (s *StreamService) Watch(_ context.Context, acc domain.Access, boardID domain.ID) (<-chan domain.Event, func(), error) {
	if err := acc.Require(domain.ActionRead); err != nil {
		return nil, nil, err
	}
	ch, cancel := s.events.Subscribe(boardID)
	return ch, cancel, nil
}
