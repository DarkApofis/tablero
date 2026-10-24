package board_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DarkApofis/tablero/backend/internal/app/board"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// memActivity holds entries with ids 1..n.
type memActivity struct{ n int64 }

func (m *memActivity) ListBoardActivity(_ context.Context, _ domain.ID, before int64, limit int) ([]domain.ActivityEntry, error) {
	var out []domain.ActivityEntry
	for id := min(before-1, m.n); id >= 1 && len(out) < limit; id-- {
		out = append(out, domain.ActivityEntry{ID: id})
	}
	return out, nil
}

func TestActivityPagination(t *testing.T) {
	svc := board.NewActivityService(&memActivity{n: 7})
	acc := domain.Access{Role: domain.RoleViewer}
	ctx := context.Background()

	var pages [][]int64
	cursor := ""
	for range 5 {
		page, err := svc.ListBoard(ctx, acc, domain.NewID(), cursor, 3)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, e := range page.Items {
			ids = append(ids, e.ID)
		}
		pages = append(pages, ids)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(pages) != 3 || len(pages[0]) != 3 || pages[0][0] != 7 || pages[1][0] != 4 || len(pages[2]) != 1 || pages[2][0] != 1 {
		t.Errorf("pages = %v, want [7 6 5] [4 3 2] [1]", pages)
	}

	// Exactly the limit: no next page.
	page, _ := svc.ListBoard(ctx, acc, domain.NewID(), "", 7)
	if len(page.Items) != 7 || page.NextCursor != "" {
		t.Errorf("limit 7: %d items, next %q", len(page.Items), page.NextCursor)
	}
}

func TestActivityRejectsForeignCursorsAndOutsiders(t *testing.T) {
	svc := board.NewActivityService(&memActivity{n: 3})
	ctx := context.Background()
	for _, c := range []string{"%%%", "MA", "bm8tbnVtZXJv"} { // invalid base64, "0", "no-numero"
		if _, err := svc.ListBoard(ctx, domain.Access{Role: domain.RoleViewer}, domain.NewID(), c, 10); !errors.Is(err, domain.ErrInvalidCursor) {
			t.Errorf("cursor %q: err = %v", c, err)
		}
	}
	if _, err := svc.ListBoard(ctx, domain.Access{}, domain.NewID(), "", 10); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("no role: err = %v", err)
	}
}
