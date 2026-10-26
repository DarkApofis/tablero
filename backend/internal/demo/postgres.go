package demo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/adapters/hasher"
	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
	"github.com/DarkApofis/tablero/backend/internal/app/board"
	"github.com/DarkApofis/tablero/backend/internal/app/task"
	"github.com/DarkApofis/tablero/backend/internal/app/workspace"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Guests is the guest policy of demo mode (see Deps).
type Guests struct {
	Max int
	TTL time.Duration
}

// NewPostgres wires the service to Postgres and to use cases of its own,
// which discard their events (see New). registrar is only needed for guests
// and events only to reach live streams; either may be nil.
func NewPostgres(pool *pgxpool.Pool, registrar Registrar, events EventPublisher, guests Guests) *Service {
	tx := postgres.NewTxManager(pool)
	workspaces := postgres.NewWorkspaces(pool)
	boards := postgres.NewBoards(pool)
	labels := postgres.NewLabels(pool)
	activity := postgres.NewActivity(pool)
	return New(Deps{
		Store:      postgres.NewDemo(pool),
		Tx:         tx,
		Hasher:     hasher.Argon2id{},
		Workspaces: workspace.NewService(workspaces, tx, discard{}),
		Boards:     board.NewService(boards, labels, activity, tx, discard{}),
		Labels:     board.NewLabelService(labels, activity, tx, discard{}),
		Tasks:      task.NewService(postgres.NewTasks(pool), boards, workspaces, activity, tx, discard{}, systemClock{}),
		Registrar:  registrar,
		Events:     events,
		Clock:      systemClock{},
		MaxGuests:  guests.Max,
		GuestTTL:   guests.TTL,
	})
}

type discard struct{}

func (discard) Publish(context.Context, ...domain.Event) {}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
