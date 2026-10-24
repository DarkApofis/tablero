package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx", for goose
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// The integration tests share one Postgres per test binary. By default it is
// a throwaway Testcontainers container started on first use; setting
// TEST_DATABASE_URL points them at an existing database instead (faster when
// iterating, see `make test-db`). Either way the migrations are applied.

const migrationsDir = "../../migrations"

var (
	testDBOnce      sync.Once
	testDBURL       string
	testDBErr       error
	testDBContainer *tcpostgres.PostgresContainer
)

func TestMain(m *testing.M) {
	code := m.Run()
	if testDBContainer != nil {
		if err := testcontainers.TerminateContainer(testDBContainer); err != nil {
			fmt.Fprintln(os.Stderr, "terminate postgres container:", err)
		}
	}
	os.Exit(code)
}

// testDatabaseURL returns a migrated database for integration tests, or skips
// the test when running with -short or without Docker.
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test skipped in -short mode")
	}
	if os.Getenv("TEST_DATABASE_URL") == "" {
		testcontainers.SkipIfProviderIsNotHealthy(t)
	}
	testDBOnce.Do(func() { testDBURL, testDBErr = setupTestDB() })
	if testDBErr != nil {
		t.Fatalf("set up test database: %v", testDBErr)
	}
	return testDBURL
}

func setupTestDB() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("tablero_test"),
			tcpostgres.WithUsername("tablero"),
			tcpostgres.WithPassword("tablero"),
			tcpostgres.BasicWaitStrategies(),
		)
		if ctr != nil {
			testDBContainer = ctr // terminate it in TestMain even if Run failed later
		}
		if err != nil {
			return "", fmt.Errorf("start postgres container: %w", err)
		}
		if url, err = ctr.ConnectionString(ctx, "sslmode=disable"); err != nil {
			return "", err
		}
	}

	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		return "", err
	}
	defer func() { _ = sqlDB.Close() }()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS(migrationsDir))
	if err != nil {
		return "", fmt.Errorf("goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return "", fmt.Errorf("migrate: %w", err)
	}
	return url, nil
}
