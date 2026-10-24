package httpapi

import (
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/adapters/hasher"
	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres/pgtest"
	"github.com/DarkApofis/tablero/backend/internal/adapters/token"
	"github.com/DarkApofis/tablero/backend/internal/app/auth"
)

// The integration tests (*_integration_test.go) share one migrated database
// per test binary; see pgtest.
func TestMain(m *testing.M) { pgtest.Main(m) }

func testDatabaseURL(t *testing.T) string { return pgtest.URL(t) }

var testSecret = []byte("test-secret-test-secret-test-secret!")

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// newAuthService wires the auth use cases with the real hasher and token
// adapters over the given user storage.
func newAuthService(users interface {
	auth.UserRepository
	auth.RefreshTokenRepository
}, tokens *token.Issuer, refreshTTL time.Duration) *auth.Service {
	svc, err := auth.NewService(users, users, hasher.Argon2id{}, tokens, systemClock{}, refreshTTL)
	if err != nil {
		panic(err)
	}
	return svc
}
