package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

func TestUsers(t *testing.T) {
	pool := newPool(t)
	users := postgres.NewUsers(pool)
	ctx := context.Background()
	email := uniqueEmail("ana")

	created, err := users.Create(ctx, email, "Ana", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID.IsZero() || created.Email != email || created.Name != "Ana" || created.PasswordHash != "hash" || created.CreatedAt.IsZero() {
		t.Errorf("created = %+v", created)
	}

	if _, err := users.Create(ctx, email, "Otra", "hash"); !errors.Is(err, domain.ErrEmailTaken) {
		t.Errorf("duplicate email: err = %v, want ErrEmailTaken", err)
	}

	for name, get := range map[string]func() (domain.User, error){
		"by email": func() (domain.User, error) { return users.ByEmail(ctx, email) },
		"by id":    func() (domain.User, error) { return users.ByID(ctx, created.ID) },
	} {
		got, err := get()
		if err != nil || got.ID != created.ID || got.Email != email {
			t.Errorf("%s = %+v, %v", name, got, err)
		}
	}
	if _, err := users.ByEmail(ctx, uniqueEmail("nadie")); !errors.Is(err, domain.ErrUserNotFound) {
		t.Errorf("unknown email: err = %v", err)
	}
	if _, err := users.ByID(ctx, domain.NewID()); !errors.Is(err, domain.ErrUserNotFound) {
		t.Errorf("unknown id: err = %v", err)
	}

	hash := []byte("0123456789abcdef0123456789abcdef")
	if err := users.Save(ctx, created.ID, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := pool.QueryRow(ctx, "SELECT token_hash FROM refresh_tokens WHERE user_id = $1", uuid.UUID(created.ID)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(hash) {
		t.Errorf("stored hash = %x", stored)
	}
}

func TestRefreshTokenRotation(t *testing.T) {
	pool := newPool(t)
	users := postgres.NewUsers(pool)
	ctx := context.Background()
	now := time.Now()

	ana, err := users.Create(ctx, uniqueEmail("ana"), "Ana", "hash")
	if err != nil {
		t.Fatal(err)
	}
	h := func(s string) []byte { return []byte(s + "-" + ana.ID.String()) }
	liveTokens := func() int {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL",
			uuid.UUID(ana.ID)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, name := range []string{"a", "other-device"} {
		if err := users.Save(ctx, ana.ID, h(name), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}

	got, err := users.Rotate(ctx, h("a"), h("b"), now.Add(time.Hour), now)
	if err != nil || got != ana.ID {
		t.Fatalf("Rotate = %v, %v", got, err)
	}
	if n := liveTokens(); n != 2 {
		t.Errorf("live tokens after rotation = %d, want 2 (b and other-device)", n)
	}

	t.Run("a revoked, unknown or expired token does not rotate", func(t *testing.T) {
		if err := users.Save(ctx, ana.ID, h("expired"), now.Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
		for _, old := range []string{"a", "unknown", "expired"} {
			if _, err := users.Rotate(ctx, h(old), h("new-"+old), now.Add(time.Hour), now); !errors.Is(err, domain.ErrInvalidRefreshToken) {
				t.Errorf("Rotate(%s) err = %v, want ErrInvalidRefreshToken", old, err)
			}
		}
		if n := liveTokens(); n != 3 {
			t.Errorf("live tokens = %d, want 3 (failed rotations must not insert)", n)
		}
	})

	t.Run("concurrent rotations of one token: exactly one wins", func(t *testing.T) {
		if err := users.Save(ctx, ana.ID, h("race"), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, 2)
		for i := range 2 {
			go func() {
				_, err := users.Rotate(ctx, h("race"), h("race-"+string(rune('0'+i))), now.Add(time.Hour), now)
				errs <- err
			}()
		}
		wins := 0
		for range 2 {
			if err := <-errs; err == nil {
				wins++
			} else if !errors.Is(err, domain.ErrInvalidRefreshToken) {
				t.Errorf("err = %v", err)
			}
		}
		if wins != 1 {
			t.Errorf("successful rotations = %d, want 1", wins)
		}
	})

	t.Run("reuse within the grace period revokes nothing", func(t *testing.T) {
		reused, err := users.RevokeAllIfReused(ctx, h("a"), now.Add(-time.Minute), now)
		if err != nil || reused {
			t.Errorf("RevokeAllIfReused = %v, %v; want false", reused, err)
		}
	})

	t.Run("reuse after the grace period revokes every session", func(t *testing.T) {
		reused, err := users.RevokeAllIfReused(ctx, h("a"), now.Add(time.Minute), now)
		if err != nil || !reused {
			t.Fatalf("RevokeAllIfReused = %v, %v; want true", reused, err)
		}
		if n := liveTokens(); n != 0 {
			t.Errorf("live tokens = %d, want 0", n)
		}
	})

	t.Run("Revoke is idempotent", func(t *testing.T) {
		if err := users.Save(ctx, ana.ID, h("logout"), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := users.Revoke(ctx, h("logout"), now); err != nil {
				t.Fatal(err)
			}
		}
		if err := users.Revoke(ctx, h("unknown"), now); err != nil {
			t.Errorf("Revoke(unknown) = %v", err)
		}
		if n := liveTokens(); n != 0 {
			t.Errorf("live tokens = %d, want 0", n)
		}
	})
}
