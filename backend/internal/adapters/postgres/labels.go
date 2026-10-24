package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/db"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Labels implements board.LabelRepository.
type Labels struct{ conn }

func NewLabels(pool *pgxpool.Pool) *Labels { return &Labels{conn{pool}} }

func (r *Labels) WorkspaceOfLabel(ctx context.Context, labelID domain.ID) (domain.ID, error) {
	id, err := r.q(ctx).GetLabelWorkspaceID(ctx, uuid.UUID(labelID))
	return domain.ID(id), notFound(err, domain.ErrNotMember)
}

func (r *Labels) ListLabels(ctx context.Context, boardID domain.ID) ([]domain.Label, error) {
	rows, err := r.q(ctx).ListLabelsByBoard(ctx, uuid.UUID(boardID))
	return mapAll(rows, toLabel), err
}

func (r *Labels) CreateLabel(ctx context.Context, boardID domain.ID, name, color string) (domain.Label, error) {
	l, err := r.q(ctx).CreateLabel(ctx, db.CreateLabelParams{BoardID: uuid.UUID(boardID), Name: name, Color: color})
	return toLabel(l), labelError(err, domain.ErrBoardNotFound)
}

func (r *Labels) UpdateLabel(ctx context.Context, labelID domain.ID, name, color *string) (domain.Label, error) {
	l, err := r.q(ctx).UpdateLabel(ctx, db.UpdateLabelParams{ID: uuid.UUID(labelID), Name: text(name), Color: text(color)})
	return toLabel(l), labelError(notFound(err, domain.ErrLabelNotFound), domain.ErrLabelNotFound)
}

// DeleteLabel removes it from its tasks too (ON DELETE CASCADE).
func (r *Labels) DeleteLabel(ctx context.Context, labelID domain.ID) error {
	n, err := r.q(ctx).DeleteLabel(ctx, uuid.UUID(labelID))
	return deleted(n, err, domain.ErrLabelNotFound)
}

func labelError(err, missing error) error {
	switch pgCode(err) {
	case pgUniqueViolation:
		return domain.ErrLabelNameTaken
	case pgForeignKeyViolation: // the board was deleted after authorization
		return missing
	}
	return err
}

func toLabel(l db.Label) domain.Label {
	return domain.Label{ID: domain.ID(l.ID), BoardID: domain.ID(l.BoardID), Name: l.Name, Color: l.Color, CreatedAt: l.CreatedAt}
}

// text maps an optional string to SQL NULL when nil.
func text(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}
