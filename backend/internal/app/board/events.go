package board

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// ActivityWriter appends to a board's activity log.
type ActivityWriter interface {
	// Append writes the entry; it must be the last write of the transaction
	// (it serializes the board's log until the transaction ends).
	Append(ctx context.Context, entry domain.NewActivity) (domain.ActivityEntry, error)
}

type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event)
}

// recorder logs a change in the transaction in ctx and, once the
// transaction has committed, publishes it.
type recorder struct {
	activity ActivityWriter
	events   EventPublisher
}

func (r recorder) log(ctx context.Context, acc domain.Access, boardID domain.ID, typ string, payload any) (domain.ActivityEntry, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return domain.ActivityEntry{}, fmt.Errorf("activity: encode payload: %w", err)
	}
	var actor *domain.ID
	if !acc.UserID.IsZero() {
		actor = &acc.UserID
	}
	entry, err := r.activity.Append(ctx, domain.NewActivity{BoardID: boardID, ActorID: actor, Type: typ, Payload: data})
	if err != nil {
		return domain.ActivityEntry{}, fmt.Errorf("activity: insert: %w", err)
	}
	return entry, nil
}

// publish announces a committed entry; call it only after the commit.
func (r recorder) publish(ctx context.Context, entry domain.ActivityEntry) {
	r.events.Publish(ctx, entry.Event())
}
