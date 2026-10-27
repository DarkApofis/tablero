// Package config loads the application settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const minJWTSecretLen = 32

type Config struct {
	Port            string        // PORT, default 8080
	DatabaseURL     string        // DATABASE_URL, required
	CORSOrigins     []string      // CORS_ORIGINS, comma-separated, default http://localhost:3000
	JWTSecret       []byte        // JWT_SECRET, required, at least 32 bytes
	AccessTokenTTL  time.Duration // ACCESS_TOKEN_TTL, default 15m
	RefreshTokenTTL time.Duration // REFRESH_TOKEN_TTL, default 720h (30 days)
	// Session cookies (refresh and access): always HttpOnly. CookieSecure is
	// COOKIE_SECURE (default true); CookieSameSite is COOKIE_SAMESITE: lax
	// (default), strict or none (needs COOKIE_SECURE=true; for a frontend on
	// another site); CookieDomain is COOKIE_DOMAIN (default: none, the API's
	// host only).
	CookieSecure   bool
	CookieSameSite string
	CookieDomain   string
	// TrustProxy (TRUST_PROXY, default false): the API is behind a reverse
	// proxy, so the client's IP is the last X-Forwarded-For entry and the
	// scheme is X-Forwarded-Proto. Leave it off when clients reach the API
	// directly: they could forge those headers.
	TrustProxy bool
	// RateLimit (RATE_LIMIT, default true) limits login, register and guest
	// attempts per IP and per email (see httpapi.DefaultAuthLimits).
	RateLimit bool
	// Open event streams: at most SSEMaxPerUser per user (SSE_MAX_PER_USER,
	// default 10) and SSEMaxPerBoard per board (SSE_MAX_PER_BOARD, default
	// 1000), per API instance.
	SSEMaxPerUser  int
	SSEMaxPerBoard int

	// MigrateOnStart (MIGRATE_ON_START, default false): apply the pending
	// migrations of MigrationsDir (MIGRATIONS_DIR, default "migrations")
	// before listening, for platforms with no pre-deploy step. A statement
	// waits at most MigrateLockTimeout (MIGRATE_LOCK_TIMEOUT, default 5s) for
	// a table lock.
	MigrateOnStart     bool
	MigrationsDir      string
	MigrateLockTimeout time.Duration
	// On SIGTERM the API first answers GET /ready with 503 and keeps serving
	// for ShutdownDelay (SHUTDOWN_DELAY, default 0s), so a load balancer stops
	// sending it traffic and connections already queued get served. Then it
	// stops accepting and gives the requests in flight ShutdownTimeout
	// (SHUTDOWN_TIMEOUT, default 15s). Keep both together under the
	// orchestrator's grace period (compose: stop_grace_period).
	ShutdownDelay   time.Duration
	ShutdownTimeout time.Duration

	// Demo mode (DEMO_MODE, default false): POST /auth/guest creates guest
	// accounts in the demo workspace, and the demo's data is reset every
	// DemoResetInterval (DEMO_RESET_INTERVAL, default 1h, at least 1m), which
	// also deletes the guests older than DemoGuestTTL (DEMO_GUEST_TTL,
	// default 2h). At most DemoMaxGuests (DEMO_MAX_GUESTS, default 100)
	// guests younger than the TTL exist at once.
	DemoMode          bool
	DemoResetInterval time.Duration
	DemoGuestTTL      time.Duration
	DemoMaxGuests     int
	// EventsBackend is EVENTS_BACKEND: "memory" (default, one instance) or
	// "postgres" (LISTEN/NOTIFY, for several instances on one database).
	EventsBackend string
}

const (
	EventsMemory   = "memory"
	EventsPostgres = "postgres"
)

func Load() (Config, error) {
	var errs []error

	cfg := Config{
		Port:          getenv("PORT", "8080"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		CORSOrigins:   splitList(getenv("CORS_ORIGINS", "http://localhost:3000")),
		JWTSecret:     []byte(os.Getenv("JWT_SECRET")),
		EventsBackend: getenv("EVENTS_BACKEND", EventsMemory),
	}
	if cfg.EventsBackend != EventsMemory && cfg.EventsBackend != EventsPostgres {
		errs = append(errs, fmt.Errorf("EVENTS_BACKEND must be %s or %s", EventsMemory, EventsPostgres))
	}
	if n, err := strconv.Atoi(cfg.Port); err != nil || n < 1 || n > 65535 {
		errs = append(errs, fmt.Errorf("PORT must be a port number (1-65535), got %q", cfg.Port))
	}
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required, e.g. postgres://user:pass@host:5432/tablero?sslmode=disable"))
	} else if u, err := url.Parse(cfg.DatabaseURL); err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		// The value is not echoed: it carries the password.
		errs = append(errs, errors.New("DATABASE_URL must be a postgres://user:pass@host:port/db URL"))
	}
	if len(cfg.CORSOrigins) == 0 {
		errs = append(errs, errors.New("CORS_ORIGINS must name at least one origin"))
	}
	for _, o := range cfg.CORSOrigins {
		if u, err := url.Parse(o); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.TrimSuffix(u.Path, "/") != "" {
			errs = append(errs, fmt.Errorf("CORS_ORIGINS: %q is not an origin like https://tablero.example.com", o))
		}
	}
	if len(cfg.JWTSecret) < minJWTSecretLen {
		errs = append(errs, fmt.Errorf("JWT_SECRET is required and must be at least %d bytes (e.g. openssl rand -hex 32)", minJWTSecretLen))
	}

	var err error
	if cfg.AccessTokenTTL, err = getDuration("ACCESS_TOKEN_TTL", 15*time.Minute); err != nil {
		errs = append(errs, err)
	}
	if cfg.RefreshTokenTTL, err = getDuration("REFRESH_TOKEN_TTL", 30*24*time.Hour); err != nil {
		errs = append(errs, err)
	}
	if cfg.CookieSecure, err = getBool("COOKIE_SECURE", true); err != nil {
		errs = append(errs, err)
	}
	switch cfg.CookieSameSite = strings.ToLower(getenv("COOKIE_SAMESITE", "lax")); cfg.CookieSameSite {
	case "lax", "strict":
	case "none":
		if !cfg.CookieSecure {
			errs = append(errs, errors.New("COOKIE_SAMESITE=none needs COOKIE_SECURE=true (browsers reject it otherwise)"))
		}
	default:
		errs = append(errs, errors.New("COOKIE_SAMESITE must be lax, strict or none"))
	}
	cfg.CookieDomain = os.Getenv("COOKIE_DOMAIN")
	if d := cfg.CookieDomain; d != "" && (strings.ContainsAny(d, ":/ ") || !strings.Contains(strings.TrimPrefix(d, "."), ".")) {
		errs = append(errs, fmt.Errorf("COOKIE_DOMAIN must be a domain like tablero.example.com, got %q", d))
	}
	if cfg.TrustProxy, err = getBool("TRUST_PROXY", false); err != nil {
		errs = append(errs, err)
	}
	if cfg.RateLimit, err = getBool("RATE_LIMIT", true); err != nil {
		errs = append(errs, err)
	}
	if cfg.SSEMaxPerUser, err = getPositiveInt("SSE_MAX_PER_USER", 10); err != nil {
		errs = append(errs, err)
	}
	if cfg.SSEMaxPerBoard, err = getPositiveInt("SSE_MAX_PER_BOARD", 1000); err != nil {
		errs = append(errs, err)
	}
	if cfg.MigrateOnStart, err = getBool("MIGRATE_ON_START", false); err != nil {
		errs = append(errs, err)
	}
	cfg.MigrationsDir = getenv("MIGRATIONS_DIR", "migrations")
	if cfg.MigrateLockTimeout, err = getDuration("MIGRATE_LOCK_TIMEOUT", 5*time.Second); err != nil {
		errs = append(errs, err)
	}
	if cfg.ShutdownDelay, err = getNonNegativeDuration("SHUTDOWN_DELAY", 0); err != nil {
		errs = append(errs, err)
	}
	if cfg.ShutdownTimeout, err = getDuration("SHUTDOWN_TIMEOUT", 15*time.Second); err != nil {
		errs = append(errs, err)
	}
	if cfg.DemoMode, err = getBool("DEMO_MODE", false); err != nil {
		errs = append(errs, err)
	}
	if cfg.DemoResetInterval, err = getDuration("DEMO_RESET_INTERVAL", time.Hour); err != nil {
		errs = append(errs, err)
	} else if cfg.DemoResetInterval < time.Minute {
		errs = append(errs, errors.New("DEMO_RESET_INTERVAL must be at least 1m"))
	}
	if cfg.DemoGuestTTL, err = getDuration("DEMO_GUEST_TTL", 2*time.Hour); err != nil {
		errs = append(errs, err)
	}
	if cfg.DemoMaxGuests, err = getPositiveInt("DEMO_MAX_GUESTS", 100); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return Config{}, &Error{Problems: errs}
	}
	return cfg, nil
}

// Error lists every problem found in the environment at once, so a deploy is
// fixed in one go rather than one variable per restart.
type Error struct {
	Problems []error
}

func (e *Error) Error() string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		lines[i] = "  - " + p.Error()
	}
	return "invalid configuration:\n" + strings.Join(lines, "\n")
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration like 15m or 720h", key)
	}
	return d, nil
}

func getNonNegativeDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%s must be a duration like 0s or 5s", key)
	}
	return d, nil
}

func getPositiveInt(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a positive number", key)
	}
	return n, nil
}

func getBool(key string, fallback bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return b, nil
}

func splitList(v string) []string {
	var items []string
	for item := range strings.SplitSeq(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}
