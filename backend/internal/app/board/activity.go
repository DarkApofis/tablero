package board

import (
	"context"
	"encoding/base64"
	"math"
	"strconv"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

const (
	DefaultActivityLimit = 50
	MaxActivityLimit     = 100
)

// ActivityService reads a board's activity log.
type ActivityService struct {
	activity ActivityRepository
}

func NewActivityService(activity ActivityRepository) *ActivityService {
	return &ActivityService{activity: activity}
}

type ActivityPage struct {
	Items []domain.ActivityEntry
	// NextCursor fetches the next (older) page; empty when this is the last one.
	NextCursor string
}

// ListBoard returns a page of a board's activity, newest first. cursor is
// empty for the first page or a NextCursor from a previous page.
func (s *ActivityService) ListBoard(ctx context.Context, acc domain.Access, boardID domain.ID, cursor string, limit int) (ActivityPage, error) {
	if err := acc.Require(domain.ActionRead); err != nil {
		return ActivityPage{}, err
	}
	before := int64(math.MaxInt64)
	if cursor != "" {
		id, err := decodeCursor(cursor)
		if err != nil {
			return ActivityPage{}, err
		}
		before = id
	}

	// One extra row tells us whether there is a next page without a COUNT.
	rows, err := s.activity.ListBoardActivity(ctx, boardID, before, limit+1)
	if err != nil {
		return ActivityPage{}, err
	}

	page := ActivityPage{Items: rows}
	if len(rows) > limit {
		page.Items = rows[:limit]
		page.NextCursor = encodeCursor(page.Items[limit-1].ID)
	}
	return page, nil
}

// Cursors are opaque to clients: base64url of the last id they saw.
func encodeCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

func decodeCursor(c string) (int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, domain.ErrInvalidCursor
	}
	id, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, domain.ErrInvalidCursor
	}
	return id, nil
}
