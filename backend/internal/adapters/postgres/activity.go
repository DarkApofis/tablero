package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres/db"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Activity implements the activity_log ports: appending entries (in the
// transaction of the change) and reading a board's log.
type Activity struct{ conn }

func NewActivity(pool *pgxpool.Pool) *Activity { return &Activity{conn{pool}} }

func (r *Activity) ListBoardActivity(ctx context.Context, boardID domain.ID, beforeID int64, limit int) ([]domain.ActivityEntry, error) {
	rows, err := r.q(ctx).ListBoardActivity(ctx, db.ListBoardActivityParams{
		BoardID: uuid.UUID(boardID), BeforeID: beforeID, RowLimit: rowLimit(limit),
	})
	return mapAll(rows, func(row db.ListBoardActivityRow) domain.ActivityEntry {
		return domain.ActivityEntry{
			ID: row.ID, BoardID: boardID, ActorID: optionalID(row.ActorID), ActorName: row.ActorName.String,
			Type: row.Type, Payload: row.Payload, CreatedAt: row.CreatedAt,
		}
	}), err
}
