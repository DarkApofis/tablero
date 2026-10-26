// Command migrate applies the pending migrations in MIGRATIONS_DIR (default
// "migrations") to DATABASE_URL and exits. The container image runs it before
// the API (Compose's migrate service, Render's preDeployCommand); locally,
// `make migrate-up` does the same with the goose CLI.
//
// It runs while the previous version of the API is still serving, so the
// migrations must be backward compatible (docs/DEPLOY.md). Two safeguards:
// an advisory lock, so two deploys never migrate at once, and a lock_timeout
// (MIGRATE_LOCK_TIMEOUT, default 5s), so a statement that has to wait for a
// table lock fails the deploy instead of queuing every live query behind it.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
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
	lockTimeout := os.Getenv("MIGRATE_LOCK_TIMEOUT")
	if lockTimeout == "" {
		lockTimeout = "5s"
	}
	if _, err := time.ParseDuration(lockTimeout); err != nil {
		return fmt.Errorf("MIGRATE_LOCK_TIMEOUT must be a duration like 5s: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return errors.New("DATABASE_URL is not a valid Postgres URL") // not echoed: it has the password
	}
	cfg.RuntimeParams["lock_timeout"] = lockTimeout
	db := stdlib.OpenDB(*cfg)
	defer func() { _ = db.Close() }()

	// Waits up to 5 min (60 × 5s) if another deploy holds it.
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(5, 60))
	if err != nil {
		return fmt.Errorf("goose locker: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(dir), goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	for _, r := range results {
		slog.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration.String())
	}
	slog.Info("migrations up to date", "applied", len(results))
	return nil
}
