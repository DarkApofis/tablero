package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/adapters/events"
	"github.com/DarkApofis/tablero/backend/internal/adapters/hasher"
	httpapi "github.com/DarkApofis/tablero/backend/internal/adapters/http"
	"github.com/DarkApofis/tablero/backend/internal/adapters/metrics"
	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
	"github.com/DarkApofis/tablero/backend/internal/adapters/token"
	"github.com/DarkApofis/tablero/backend/internal/app/auth"
	"github.com/DarkApofis/tablero/backend/internal/app/board"
	"github.com/DarkApofis/tablero/backend/internal/app/task"
	"github.com/DarkApofis/tablero/backend/internal/app/workspace"
	"github.com/DarkApofis/tablero/backend/internal/config"
	"github.com/DarkApofis/tablero/backend/internal/demo"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// main.go is the composition root: it builds the adapters, hands them to the
// use cases as ports, and the use cases to the HTTP adapter. Wiring is manual
// on purpose; there is no DI framework.

var sameSite = map[string]http.SameSite{
	"lax": http.SameSiteLaxMode, "strict": http.SameSiteStrictMode, "none": http.SameSiteNoneMode,
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		// A bad environment is not a crash: say what to fix, all of it, and stop
		// before touching the database.
		var cfgErr *config.Error
		if errors.As(err, &cfgErr) {
			problems := make([]string, len(cfgErr.Problems))
			for i, p := range cfgErr.Problems {
				problems[i] = p.Error()
			}
			slog.Error("invalid configuration, not starting", "problems", problems)
			os.Exit(2)
		}
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

	// Without a pre-deploy step (Render's free instances), migrate here,
	// before listening: the health check only passes once it is done, and the
	// previous version keeps serving meanwhile, hence backward compatibility.
	if cfg.MigrateOnStart {
		applied, err := postgres.Migrate(ctx, cfg.DatabaseURL, cfg.MigrationsDir, cfg.MigrateLockTimeout)
		if err != nil {
			return fmt.Errorf("migrations failed, not starting: %w", err)
		}
		for _, m := range applied {
			slog.Info("migration applied", "version", m.Version, "file", m.File, "duration", m.Duration.String())
		}
		slog.Info("migrations up to date", "applied", len(applied))
	}

	pool, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("cannot reach the database at DATABASE_URL, not starting: %w", err)
	}
	defer pool.Close()
	slog.Info("database connected")

	tx := postgres.NewTxManager(pool)
	// The hub feeds this instance's event streams. With one instance it is
	// also the use cases' EventPublisher; with several, the Postgres bus is:
	// it hands events to the hub and NOTIFYs the other instances.
	hub := events.NewHub(events.DefaultBuffer, events.DefaultLeaveDelay)
	var publisher events.Publisher = hub
	if cfg.EventsBackend == config.EventsPostgres {
		bus := postgres.NewEventBus(pool, cfg.DatabaseURL, hub, postgres.EventBusOptions{})
		publisher = bus
		// Not on ctx: the requests still served after SIGTERM (the delay, then
		// those in flight) must reach the other instances. Ends once they are done.
		busCtx, stopBus := context.WithCancel(context.Background())
		defer stopBus()
		go func() { _ = bus.Run(busCtx) }()
		slog.Info("events over postgres LISTEN/NOTIFY", "instance", bus.Instance())
	}

	// GET /metrics. The router adds the stream's delivery histogram.
	reg := metrics.NewRegistry()
	metrics.RegisterRuntime(reg)
	reg.GaugeFunc("tablero_sse_connections", "Open SSE event streams on this instance.",
		func() float64 { return float64(hub.Connections()) })
	reg.CounterFunc("tablero_sse_slow_clients_dropped_total", "SSE clients dropped for falling behind (full buffer).",
		func() float64 { return float64(hub.Dropped()) })
	publisher = events.Counting{Next: publisher, Published: reg.CounterVec("tablero_events_published_total",
		"Board events published by this instance after their commit, by type.", "type")}

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

	// Demo mode: guest accounts, and the demo's data reset now and then
	// (which also loads it the first time).
	var guests httpapi.GuestStarter
	if cfg.DemoMode {
		demoService := demo.NewPostgres(pool, authService, publisher, demo.Guests{Max: cfg.DemoMaxGuests, TTL: cfg.DemoGuestTTL})
		guests = demoService
		go demoService.RunResets(ctx, cfg.DemoResetInterval)
		slog.Info("demo mode on", "reset_every", cfg.DemoResetInterval.String(), "guest_ttl", cfg.DemoGuestTTL.String())
	}

	// New accounts join the default workspace as members. Guests of the demo
	// (above) register through authService itself, without it.
	httpAuth := authService
	if cfg.DefaultWorkspaceID != "" {
		wsID, _ := domain.ParseID(cfg.DefaultWorkspaceID) // validated by config.Load
		exists, err := workspaces.Exists(ctx, wsID)
		if err != nil {
			return fmt.Errorf("check DEFAULT_WORKSPACE_ID: %w", err)
		}
		if !exists {
			return fmt.Errorf("DEFAULT_WORKSPACE_ID %s: no such workspace, not starting", wsID)
		}
		httpAuth = authService.WithOnboarding(tx, workspace.NewDefaultMembership(workspaces, wsID))
		slog.Info("new accounts join the default workspace as members", "workspace", wsID.String())
	}

	var authLimits *httpapi.AuthLimits
	if cfg.RateLimit {
		authLimits = &httpapi.DefaultAuthLimits
	}

	var shuttingDown atomic.Bool
	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: httpapi.NewRouter(httpapi.Deps{
			CORSOrigins: cfg.CORSOrigins,
			Cookies: httpapi.CookieOptions{
				Secure: cfg.CookieSecure, SameSite: sameSite[cfg.CookieSameSite], Domain: cfg.CookieDomain, PathPrefix: cfg.CookiePathPrefix,
			},
			TrustProxy:   cfg.TrustProxy,
			ProxyHops:    cfg.TrustProxyHops,
			AuthLimits:   authLimits,
			StreamLimits: httpapi.StreamLimits{PerUser: cfg.SSEMaxPerUser, PerBoard: cfg.SSEMaxPerBoard},
			Ready:        pool,
			ShuttingDown: &shuttingDown,
			Auth:         httpAuth,
			Authorizer:   workspace.NewAuthorizer(workspaces),
			Workspaces:   workspace.NewService(workspaces, tx, publisher),
			Boards:       board.NewService(boards, labels, activity, tx, publisher),
			Tasks:        task.NewService(postgres.NewTasks(pool), boards, workspaces, activity, tx, publisher, systemClock{}),
			Labels:       board.NewLabelService(labels, activity, tx, publisher),
			Activity:     board.NewActivityService(activity),
			Streams:      board.NewStreamService(hub, activity, users, board.DefaultReplayLimit),
			Guests:       guests,
			Metrics:      reg,
		}),
		// Slow clients cannot hold connections open: headers within 5s, the
		// whole request (bodies are small, see httpapi.maxRequestBytes) within
		// 15s. It does not cut event streams, which outlive the request read.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		MaxHeaderBytes:    32 << 10,
		// Caps ordinary responses; the event stream replaces it per write on its
		// own connection (http.ResponseController), so streams are not cut.
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  2 * time.Minute,
	}
	// Shutdown waits for open requests; closing the hub ends the streams.
	srv.RegisterOnShutdown(hub.Close)

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
	stop() // a second signal now kills the process at once

	shuttingDown.Store(true)
	if cfg.ShutdownDelay > 0 {
		slog.Info("shutting down: /ready answers 503, still serving", "delay", cfg.ShutdownDelay.String())
		time.Sleep(cfg.ShutdownDelay)
	}
	// Shutdown closes the listener, so no new requests come in, and waits for
	// the ones in flight; the event streams end through hub.Close.
	slog.Info("shutting down, finishing requests in flight", "timeout", cfg.ShutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("requests still open after %s, closed: %w", cfg.ShutdownTimeout, err)
	}
	slog.Info("server stopped")
	return nil
}
