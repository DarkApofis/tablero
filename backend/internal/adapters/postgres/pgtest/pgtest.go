// Package pgtest gives integration tests a migrated PostgreSQL database. It
// is imported only from _test.go files.
//
// Each test binary gets its own database, so packages can run in parallel
// (go test ./... does) without seeing each other's data: by default a
// throwaway Testcontainers container started on first use; with
// TEST_DATABASE_URL set, a fresh database created on that server and dropped
// at the end (faster when iterating, and what CI uses).
package pgtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx", for goose
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var (
	once      sync.Once
	dbURL     string
	setupErr  error
	container *tcpostgres.PostgresContainer
	cleanup   func()
)

// Main runs the tests and then removes the database (or container). Call it
// from TestMain.
func Main(m *testing.M) {
	code := m.Run()
	if cleanup != nil {
		cleanup()
	}
	os.Exit(code)
}

// URL returns the connection string of the test database, or skips the test
// in -short mode or when Docker is not available.
func URL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test skipped in -short mode")
	}
	if os.Getenv("TEST_DATABASE_URL") == "" {
		testcontainers.SkipIfProviderIsNotHealthy(t)
	}
	once.Do(func() { dbURL, setupErr = setup() })
	if setupErr != nil {
		t.Fatalf("set up test database: %v", setupErr)
	}
	return dbURL
}

func setup() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var target string
	if server := os.Getenv("TEST_DATABASE_URL"); server != "" {
		var err error
		if target, err = createDatabase(ctx, server); err != nil {
			return "", err
		}
	} else {
		ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("tablero_test"),
			tcpostgres.WithUsername("tablero"),
			tcpostgres.WithPassword("tablero"),
			tcpostgres.BasicWaitStrategies(),
		)
		if ctr != nil {
			container = ctr // terminate it even if Run failed later
			cleanup = func() {
				if err := testcontainers.TerminateContainer(container); err != nil {
					fmt.Fprintln(os.Stderr, "terminate postgres container:", err)
				}
			}
		}
		if err != nil {
			return "", fmt.Errorf("start postgres container: %w", err)
		}
		if target, err = ctr.ConnectionString(ctx, "sslmode=disable"); err != nil {
			return "", err
		}
	}
	return target, migrate(ctx, target)
}

// createDatabase creates a uniquely named database on the server of
// serverURL and returns its URL; cleanup drops it.
func createDatabase(ctx context.Context, serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("parse TEST_DATABASE_URL: %w", err)
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	name := "tablero_test_" + hex.EncodeToString(suffix)

	admin, err := pgx.Connect(ctx, serverURL)
	if err != nil {
		return "", fmt.Errorf("connect to TEST_DATABASE_URL: %w", err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return "", fmt.Errorf("create database %s: %w", name, err)
	}
	cleanup = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := pgx.Connect(ctx, serverURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, "drop test database:", err)
			return
		}
		defer func() { _ = c.Close(context.Background()) }()
		if _, err := c.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			fmt.Fprintln(os.Stderr, "drop test database:", err)
		}
	}
	u.Path = "/" + name
	return u.String(), nil
}

func migrate(ctx context.Context, target string) error {
	sqlDB, err := sql.Open("pgx", target)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS(MigrationsDir()))
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// MigrationsDir is backend/migrations, found from this file's location so it
// works from any test package.
func MigrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "migrations")
}
