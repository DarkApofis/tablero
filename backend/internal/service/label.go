package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/db"
)

const MaxLabelNameLen = 50

var (
	ErrLabelNotFound = errors.New("label not found")
	// ErrLabelNameTaken: the board already has a label with that name.
	ErrLabelNameTaken = errors.New("label name taken")
)

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type LabelService struct {
	q *db.Queries
}

func NewLabelService(pool *pgxpool.Pool) *LabelService {
	return &LabelService{q: db.New(pool)}
}

// WorkspaceOfLabel resolves a label to its workspace for authorization. A
// missing label is ErrNotMember, like a forbidden one.
func (s *LabelService) WorkspaceOfLabel(ctx context.Context, labelID uuid.UUID) (uuid.UUID, error) {
	id, err := s.q.GetLabelWorkspaceID(ctx, labelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotMember
	}
	return id, err
}

func (s *LabelService) List(ctx context.Context, boardID uuid.UUID) ([]db.Label, error) {
	return s.q.ListLabelsByBoard(ctx, boardID)
}

func (s *LabelService) Create(ctx context.Context, boardID uuid.UUID, name, color string) (db.Label, error) {
	n, c, err := validLabel(&name, &color)
	if err != nil {
		return db.Label{}, err
	}
	label, err := s.q.CreateLabel(ctx, db.CreateLabelParams{BoardID: boardID, Name: n.String, Color: c.String})
	return label, labelError(err, ErrBoardNotFound)
}

// Update changes the name and/or color; nil keeps the current value.
func (s *LabelService) Update(ctx context.Context, labelID uuid.UUID, name, color *string) (db.Label, error) {
	if name == nil && color == nil {
		return db.Label{}, &ValidationError{Fields: map[string]string{"body": "Indica name y/o color"}}
	}
	n, c, err := validLabel(name, color)
	if err != nil {
		return db.Label{}, err
	}
	label, err := s.q.UpdateLabel(ctx, db.UpdateLabelParams{ID: labelID, Name: n, Color: c})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Label{}, ErrLabelNotFound
	}
	return label, labelError(err, ErrLabelNotFound)
}

// Delete removes the label from the board and from every task that had it.
func (s *LabelService) Delete(ctx context.Context, labelID uuid.UUID) error {
	n, err := s.q.DeleteLabel(ctx, labelID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLabelNotFound
	}
	return nil
}

func labelError(err, missing error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case pgUniqueViolation:
			return ErrLabelNameTaken
		case pgForeignKeyViolation: // the board was deleted after authorization
			return missing
		}
	}
	return err
}

// validLabel validates the fields that are given and normalizes them: the
// name trimmed, the color lowercase. Nil fields come back as SQL NULL.
func validLabel(name, color *string) (pgtype.Text, pgtype.Text, error) {
	var n, c pgtype.Text
	fields := map[string]string{}
	if name != nil {
		v := strings.TrimSpace(*name)
		switch {
		case v == "":
			fields["name"] = "El nombre es obligatorio"
		case utf8.RuneCountInString(v) > MaxLabelNameLen:
			fields["name"] = fmt.Sprintf("El nombre no puede superar %d caracteres", MaxLabelNameLen)
		}
		n = pgtype.Text{String: v, Valid: true}
	}
	if color != nil {
		if !hexColor.MatchString(*color) {
			fields["color"] = "El color debe ser hexadecimal, como #3ee6b0"
		}
		c = pgtype.Text{String: strings.ToLower(*color), Valid: true}
	}
	if len(fields) > 0 {
		return n, c, &ValidationError{Fields: fields}
	}
	return n, c, nil
}
