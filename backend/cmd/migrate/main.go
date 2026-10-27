// Command migrate applies the pending migrations in MIGRATIONS_DIR (default
// "migrations") to DATABASE_URL and exits: Compose's migrate service runs it
// before the API, and so would a pre-deploy step. Where there is none (Render's
// free instances), the API does the same at startup with MIGRATE_ON_START.
// Locally, `make migrate-up` does it with the goose CLI.
//
// Migrations must be backward compatible (docs/DEPLOY.md); this test package
// checks it. MIGRATE_LOCK_TIMEOUT (default 5s) is how long a statement may
// wait for a table lock (see postgres.Migrate).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("migrate failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}
	dir := os.Getenv("MIGRATIONS_DIR")
	if dir == "" {
		dir = "migrations"
	}
	lockTimeout := 5 * time.Second
	if v := os.Getenv("MIGRATE_LOCK_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("MIGRATE_LOCK_TIMEOUT must be a duration like 5s, got %q", v)
		}
		lockTimeout = d
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	applied, err := postgres.Migrate(ctx, url, dir, lockTimeout)
	if err != nil {
		return err
	}
	for _, m := range applied {
		slog.Info("migration applied", "version", m.Version, "file", m.File, "duration", m.Duration.String())
	}
	slog.Info("migrations up to date", "applied", len(applied))
	return nil
}
