package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	"github.com/pressly/goose/v3"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres/pgtest"
)

// Migration 00006 gives the workspaces that already exist an address from
// their name, and their creator as owner. It runs on a database of its own,
// migrated up to 00005 first.
func TestMigrationGivesExistingWorkspacesAnAddress(t *testing.T) {
	ctx := context.Background()
	admin := newPool(t)
	name := "slugs_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)") })

	u, err := url.Parse(pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(pgtest.MigrationsDir()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 5); err != nil {
		t.Fatal(err)
	}

	var userID uuid.UUID
	if err := db.QueryRowContext(ctx, "INSERT INTO users (email, password_hash, name) VALUES ('a@x.com', 'x', 'A') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	names := []string{"Diseño de Producto", "diseño de producto", "¿?", "Q4", "Equipo · Ñandú"}
	for _, n := range names {
		if _, err := db.ExecContext(ctx, "INSERT INTO workspaces (name, created_by) VALUES ($1, $2)", n, userID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := provider.UpTo(ctx, 6); err != nil {
		t.Fatal(err)
	}

	rows, err := db.QueryContext(ctx, "SELECT name, slug, owner_id = created_by FROM workspaces ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]string{}
	for rows.Next() {
		var n, slug string
		var owned bool
		if err := rows.Scan(&n, &slug, &owned); err != nil {
			t.Fatal(err)
		}
		if !owned {
			t.Errorf("%q: owner is not its creator", n)
		}
		got[n] = slug
	}
	want := map[string]string{
		"¿?": "workspace", "Q4": "workspace-1", "Equipo · Ñandú": "equipo-nandu",
	}
	for n, slug := range want {
		if got[n] != slug {
			t.Errorf("%q: slug = %q, want %q", n, got[n], slug)
		}
	}
	// The two "Diseño de producto" get the address and its numbered twin.
	pair := fmt.Sprint(got["Diseño de Producto"], " ", got["diseño de producto"])
	if pair != "diseno-de-producto diseno-de-producto-2" && pair != "diseno-de-producto-2 diseno-de-producto" {
		t.Errorf("same name: %s", pair)
	}
}
