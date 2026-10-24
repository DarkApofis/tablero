package postgres_test

// Integration tests of the repositories against a real PostgreSQL (see pgtest).

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres/pgtest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func uniqueEmail(name string) string {
	return strings.ToLower(name) + "-" + uuid.NewString()[:8] + "@test.local"
}
