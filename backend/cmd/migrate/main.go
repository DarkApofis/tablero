// Command migrate applies the pending migrations in MIGRATIONS_DIR (default
// "migrations") to DATABASE_URL and exits. The container image runs it before
// the API; locally, `make migrate-up` does the same with the goose CLI.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx", for goose
	"github.com/pressly/goose/v3"
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	db, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(dir))
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	for _, r := range results {
		slog.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path)
	}
	slog.Info("migrations up to date", "applied", len(results))
	return nil
}
