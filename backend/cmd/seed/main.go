// Command seed loads the demo data into DATABASE_URL: three users (admin,
// member, viewer), the workspace "Estudio Nube" and a board with its
// history. It is idempotent: run again, it creates nothing twice and puts
// the board back to its initial state. The credentials are in the README.
//
//	DATABASE_URL=postgres://... go run ./cmd/seed
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
	"github.com/DarkApofis/tablero/backend/internal/demo"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("seed failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	pool, err := postgres.Open(ctx, url)
	if err != nil {
		return fmt.Errorf("cannot reach the database at DATABASE_URL: %w", err)
	}
	defer pool.Close()

	// No live streams to tell: a running API with DEMO_MODE would reset the
	// board on its own schedule; without it, clients reload on their own.
	res, err := demo.NewPostgres(pool, nil, nil, demo.Guests{}).Seed(ctx)
	if err != nil {
		return err
	}
	slog.Info("demo data ready", "workspace", demo.WorkspaceName, "workspace_id", res.WorkspaceID.String(),
		"board", demo.BoardName, "board_id", res.BoardID.String(), "tasks", res.Tasks)
	for _, p := range demo.People {
		slog.Info("demo user", "email", p.Email, "password", p.Password, "role", string(p.Role))
	}
	return nil
}
