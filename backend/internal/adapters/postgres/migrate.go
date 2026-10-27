package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// AppliedMigration is one migration Migrate applied.
type AppliedMigration struct {
	Version  int64
	File     string
	Duration time.Duration
}

// Migrate applies the pending migrations in dir to databaseURL. It runs while
// the previous version of the API may still be serving, so migrations must be
// backward compatible (docs/DEPLOY.md). Two safeguards: a session advisory
// lock, so two deploys never migrate at once (it waits up to 5 minutes), and
// lockTimeout (Postgres lock_timeout), so a statement that has to wait for a
// table lock fails instead of queuing every live query behind it.
func Migrate(ctx context.Context, databaseURL, dir string, lockTimeout time.Duration) ([]AppliedMigration, error) {
	// pgxpool's parser takes the pool_* settings DATABASE_URL may carry for the
	// API out of the connection's parameters (pgx would send them to Postgres).
	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("DATABASE_URL is not a valid Postgres URL") // not echoed: it has the password
	}
	conn := poolCfg.ConnConfig
	conn.RuntimeParams["lock_timeout"] = fmt.Sprintf("%dms", lockTimeout.Milliseconds())
	db := stdlib.OpenDB(*conn)
	defer func() { _ = db.Close() }()

	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(5, 60)) // 60 × 5s
	if err != nil {
		return nil, fmt.Errorf("goose locker: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(dir), goose.WithSessionLocker(locker))
	if err != nil {
		return nil, fmt.Errorf("goose provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	applied := make([]AppliedMigration, len(results))
	for i, r := range results {
		applied[i] = AppliedMigration{Version: r.Source.Version, File: r.Source.Path, Duration: r.Duration}
	}
	return applied, nil
}
