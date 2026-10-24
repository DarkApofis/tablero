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
