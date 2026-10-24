package board_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DarkApofis/tablero/backend/internal/app/board"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

type fakeSubscriber struct{ subscribed []domain.ID }

func (f *fakeSubscriber) Subscribe(boardID domain.ID) (<-chan domain.Event, func()) {
	f.subscribed = append(f.subscribed, boardID)
	return make(chan domain.Event), func() {}
}

func TestWatchRequiresRead(t *testing.T) {
	subs := &fakeSubscriber{}
	svc := board.NewStreamService(subs)
	boardID := domain.NewID()

	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleMember, domain.RoleViewer} {
		ch, cancel, err := svc.Watch(context.Background(), domain.Access{Role: role}, boardID)
		if err != nil || ch == nil || cancel == nil {
			t.Errorf("%s: Watch = %v", role, err)
		}
	}
	if _, _, err := svc.Watch(context.Background(), domain.Access{}, boardID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("no role: err = %v, want ErrForbidden", err)
	}
	if len(subs.subscribed) != 3 || subs.subscribed[0] != boardID {
		t.Errorf("subscriptions = %v; a forbidden Watch must not subscribe", subs.subscribed)
	}
}
