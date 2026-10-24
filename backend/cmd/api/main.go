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

	"github.com/DarkApofis/tablero/backend/internal/auth"
	"github.com/DarkApofis/tablero/backend/internal/config"
	"github.com/DarkApofis/tablero/backend/internal/db"
	httpapi "github.com/DarkApofis/tablero/backend/internal/http"
	"github.com/DarkApofis/tablero/backend/internal/service"
)

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

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	slog.Info("database connected")

	tokens := auth.NewTokenIssuer(cfg.JWTSecret, cfg.AccessTokenTTL)

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: httpapi.NewRouter(httpapi.Deps{
			CORSOrigins:  cfg.CORSOrigins,
			CookieSecure: cfg.CookieSecure,
			Health:       service.NewHealthService(pool),
			Auth:         service.NewAuthService(db.New(pool), tokens, cfg.RefreshTokenTTL),
			Tokens:       tokens,
			Workspaces:   service.NewWorkspaceService(service.NewPostgresWorkspaceStore(pool)),
			Boards:       service.NewBoardService(pool),
			Tasks:        service.NewTaskService(pool),
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
