package config

import "testing"

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
