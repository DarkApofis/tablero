package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEventsBackend(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")

	for env, want := range map[string]string{"": EventsMemory, "memory": EventsMemory, "postgres": EventsPostgres} {
		t.Setenv("EVENTS_BACKEND", env)
		cfg, err := Load()
		if err != nil || cfg.EventsBackend != want {
			t.Errorf("EVENTS_BACKEND=%q: %q, %v; want %q", env, cfg.EventsBackend, err, want)
		}
	}
	t.Setenv("EVENTS_BACKEND", "redis")
	if _, err := Load(); err == nil {
		t.Error("EVENTS_BACKEND=redis was accepted")
	}
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	for _, k := range []string{"DATABASE_URL", "JWT_SECRET", "PORT", "CORS_ORIGINS", "SHUTDOWN_TIMEOUT", "SHUTDOWN_DELAY"} {
		t.Setenv(k, "")
	}
	t.Setenv("PORT", "http")
	t.Setenv("CORS_ORIGINS", "localhost:3000")
	t.Setenv("SHUTDOWN_TIMEOUT", "-1s")
	t.Setenv("SHUTDOWN_DELAY", "pronto")

	_, err := Load()
	var cfgErr *Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("err = %v, want *config.Error", err)
	}
	for _, want := range []string{"PORT", "DATABASE_URL is required", "CORS_ORIGINS", "JWT_SECRET", "SHUTDOWN_DELAY", "SHUTDOWN_TIMEOUT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%s", want, err)
		}
	}
	if len(cfgErr.Problems) != 6 {
		t.Errorf("%d problems, want 6:\n%s", len(cfgErr.Problems), err)
	}
}

func TestDatabaseURLIsNotEchoed(t *testing.T) {
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("DATABASE_URL", "mysql://root:s3cr3t@db/tablero")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL must be a postgres://") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("the password leaked into the error: %s", err)
	}
}

func TestDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://tablero:tablero@db:5432/tablero")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DemoMode || cfg.DemoResetInterval != time.Hour || cfg.DemoGuestTTL != 2*time.Hour || cfg.DemoMaxGuests != 100 {
		t.Errorf("demo defaults: %+v", cfg)
	}
	if cfg.Port != "8080" || cfg.ShutdownDelay != 0 || cfg.ShutdownTimeout != 15*time.Second || len(cfg.CORSOrigins) != 1 {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestDemoSettings(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://tablero:tablero@db:5432/tablero")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("DEMO_RESET_INTERVAL", "30m")
	t.Setenv("DEMO_MAX_GUESTS", "5")

	cfg, err := Load()
	if err != nil || !cfg.DemoMode || cfg.DemoResetInterval != 30*time.Minute || cfg.DemoMaxGuests != 5 {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}

	t.Setenv("DEMO_RESET_INTERVAL", "10s")
	t.Setenv("DEMO_MAX_GUESTS", "0")
	_, err = Load()
	var cfgErr *Error
	if !errors.As(err, &cfgErr) || len(cfgErr.Problems) != 2 {
		t.Errorf("err = %v, want DEMO_RESET_INTERVAL and DEMO_MAX_GUESTS rejected", err)
	}
}

func TestSecuritySettings(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://tablero:tablero@db:5432/tablero")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CookieSameSite != "lax" || cfg.CookieDomain != "" || cfg.TrustProxy || !cfg.RateLimit || cfg.SSEMaxPerUser != 10 || cfg.SSEMaxPerBoard != 1000 {
		t.Errorf("defaults: %+v", cfg)
	}

	t.Setenv("COOKIE_SAMESITE", "None")
	t.Setenv("COOKIE_DOMAIN", "tablero.example.com")
	t.Setenv("TRUST_PROXY", "true")
	t.Setenv("SSE_MAX_PER_USER", "3")
	if cfg, err = Load(); err != nil || cfg.CookieSameSite != "none" || cfg.CookieDomain != "tablero.example.com" || !cfg.TrustProxy || cfg.SSEMaxPerUser != 3 {
		t.Errorf("cfg = %+v, %v", cfg, err)
	}

	for env, val := range map[string]string{"COOKIE_SECURE": "false", "COOKIE_DOMAIN": "https://tablero.example.com"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, val)
			if _, err := Load(); err == nil {
				t.Errorf("%s=%s with COOKIE_SAMESITE=none was accepted", env, val)
			}
		})
	}
	t.Setenv("COOKIE_SAMESITE", "loose")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "COOKIE_SAMESITE") {
		t.Errorf("COOKIE_SAMESITE=loose: %v", err)
	}
}
