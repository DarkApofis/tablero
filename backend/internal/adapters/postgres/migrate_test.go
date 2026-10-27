package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres/pgtest"
)

// The test database is already migrated: Migrate finds nothing to do. The URL
// carries a pool setting for the API, which must not reach Postgres as a
// connection parameter (it would reject it as unknown).
func TestMigrateIsIdempotentAndIgnoresPoolSettings(t *testing.T) {
	url := pgtest.URL(t)
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	applied, err := postgres.Migrate(context.Background(), url+sep+"pool_max_conn_idle_time=4m", pgtest.MigrationsDir(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 {
		t.Errorf("applied %d migrations to an up-to-date database", len(applied))
	}
}
