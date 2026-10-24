package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/adapters/events"
	"github.com/DarkApofis/tablero/backend/internal/adapters/hasher"
	httpapi "github.com/DarkApofis/tablero/backend/internal/adapters/http"
	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
	"github.com/DarkApofis/tablero/backend/internal/adapters/token"
	"github.com/DarkApofis/tablero/backend/internal/app/auth"
	"github.com/DarkApofis/tablero/backend/internal/app/board"
	"github.com/DarkApofis/tablero/backend/internal/app/task"
	"github.com/DarkApofis/tablero/backend/internal/app/workspace"
	"github.com/DarkApofis/tablero/backend/internal/config"
)

// main.go is the composition root: it builds the adapters, hands them to the
// use cases as ports, and the use cases to the HTTP adapter. Wiring is manual
// on purpose; there is no DI framework.

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	slog.Info("database connected")

	tx := postgres.NewTxManager(pool)
	// Phase 2 replaces it with the SSE broadcaster.
	publisher := events.Noop{}

	users := postgres.NewUsers(pool)
	workspaces := postgres.NewWorkspaces(pool)
	boards := postgres.NewBoards(pool)
	labels := postgres.NewLabels(pool)
	activity := postgres.NewActivity(pool)
	authService, err := auth.NewService(users, users, hasher.Argon2id{},
		token.NewIssuer(cfg.JWTSecret, cfg.AccessTokenTTL), systemClock{}, cfg.RefreshTokenTTL)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: httpapi.NewRouter(httpapi.Deps{
			CORSOrigins:  cfg.CORSOrigins,
			CookieSecure: cfg.CookieSecure,
			Health:       pool,
			Auth:         authService,
			Authorizer:   workspace.NewAuthorizer(workspaces),
			Workspaces:   workspace.NewService(workspaces, tx, publisher),
			Boards:       board.NewService(boards, labels, tx),
			Tasks:        task.NewService(postgres.NewTasks(pool), boards, workspaces, activity, tx, publisher, systemClock{}),
			Labels:       board.NewLabelService(labels),
			Activity:     board.NewActivityService(activity),
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server starting", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	slog.Info("server stopped")
	return nil
}
