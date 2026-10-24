package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/app/auth"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// --- fakes of the ports ---

type fakeUsers struct {
	byID    map[domain.ID]domain.User
	lookups int
}

func (f *fakeUsers) Create(_ context.Context, email, name, hash string) (domain.User, error) {
	for _, u := range f.byID {
		if u.Email == email {
			return domain.User{}, domain.ErrEmailTaken
		}
	}
	u := domain.User{ID: domain.NewID(), Email: email, Name: name, PasswordHash: hash, CreatedAt: time.Now()}
	f.byID[u.ID] = u
	return u, nil
}

func (f *fakeUsers) ByEmail(_ context.Context, email string) (domain.User, error) {
	f.lookups++
	for _, u := range f.byID {
		if u.Email == email {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrUserNotFound
}

func (f *fakeUsers) ByID(_ context.Context, id domain.ID) (domain.User, error) {
	if u, ok := f.byID[id]; ok {
		return u, nil
	}
	return domain.User{}, domain.ErrUserNotFound
}

type refreshRecord struct {
	userID    domain.ID
	hash      string
	expiresAt time.Time
}

type fakeRefresh struct{ saved []refreshRecord }

func (f *fakeRefresh) Save(_ context.Context, userID domain.ID, hash []byte, expiresAt time.Time) error {
	f.saved = append(f.saved, refreshRecord{userID, string(hash), expiresAt})
	return nil
}

// fakeHasher "hashes" by prefixing, and counts verifications.
type fakeHasher struct {
	hashed   []string
	verified []string // hashes checked against
}

func (f *fakeHasher) Hash(pw string) (string, error) {
	f.hashed = append(f.hashed, pw)
	return "hash:" + pw, nil
}

func (f *fakeHasher) Verify(pw, hash string) (bool, error) {
	f.verified = append(f.verified, hash)
	return hash == "hash:"+pw, nil
}

type fakeTokens struct{ n int }

func (f *fakeTokens) Issue(userID domain.ID) (string, time.Time, error) {
	return "access-" + userID.String(), time.Now().Add(15 * time.Minute), nil
}

func (f *fakeTokens) Verify(token string) (domain.ID, error) {
	id, err := domain.ParseID(strings.TrimPrefix(token, "access-"))
	if err != nil || !strings.HasPrefix(token, "access-") {
		return domain.NilID, errors.New("bad token")
	}
	return id, nil
}

func (f *fakeTokens) NewRefreshToken() (string, []byte, error) {
	f.n++
	plain := "refresh-" + string(rune('a'+f.n))
	return plain, []byte("sha:" + plain), nil
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

type env struct {
	svc     *auth.Service
	users   *fakeUsers
	refresh *fakeRefresh
	hasher  *fakeHasher
	now     time.Time
}

const refreshTTL = 30 * 24 * time.Hour

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		users: &fakeUsers{byID: map[domain.ID]domain.User{}}, refresh: &fakeRefresh{}, hasher: &fakeHasher{},
		now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
	}
	svc, err := auth.NewService(e.users, e.refresh, e.hasher, &fakeTokens{}, fixedClock{e.now}, refreshTTL)
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc
	e.hasher.hashed = nil // drop the dummy hash made by NewService
	return e
}

// --- tests ---

func TestRegisterNormalizesHashesAndStartsSession(t *testing.T) {
	e := newEnv(t)
	s, err := e.svc.Register(context.Background(), "  Ana@Example.COM ", " Ana ", "supersecreta")
	if err != nil {
		t.Fatal(err)
	}
	if s.User.Email != "ana@example.com" || s.User.Name != "Ana" || s.User.PasswordHash != "hash:supersecreta" {
		t.Errorf("user = %+v", s.User)
	}
	if s.AccessToken != "access-"+s.User.ID.String() || s.RefreshToken == "" {
		t.Errorf("tokens = %q, %q", s.AccessToken, s.RefreshToken)
	}
	if len(e.refresh.saved) != 1 {
		t.Fatalf("saved refresh tokens = %d", len(e.refresh.saved))
	}
	saved := e.refresh.saved[0]
	if saved.userID != s.User.ID || saved.hash != "sha:"+s.RefreshToken || !saved.expiresAt.Equal(e.now.Add(refreshTTL)) {
		t.Errorf("saved = %+v; only the hash, expiring at now+TTL", saved)
	}
	if !s.RefreshExpiresAt.Equal(e.now.Add(refreshTTL)) {
		t.Errorf("refresh expiry = %v", s.RefreshExpiresAt)
	}
}

func TestRegisterValidationTouchesNothing(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Register(context.Background(), "no-es-correo", " ", "corta")
	valErr, ok := errors.AsType[*domain.ValidationError](err)
	if !ok || len(valErr.Fields) != 3 {
		t.Fatalf("err = %v, want 3 field errors", err)
	}
	if len(e.users.byID) != 0 || len(e.hasher.hashed) != 0 || len(e.refresh.saved) != 0 {
		t.Error("invalid registration created, hashed or stored something")
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.Register(ctx, "ana@example.com", "Ana", "supersecreta"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Register(ctx, "ANA@example.com", "Otra", "otraclave1"); !errors.Is(err, domain.ErrEmailTaken) {
		t.Errorf("err = %v, want ErrEmailTaken", err)
	}
}

func TestLogin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	reg, _ := e.svc.Register(ctx, "ana@example.com", "Ana", "supersecreta")

	s, err := e.svc.Login(ctx, " ANA@example.com", "supersecreta")
	if err != nil || s.User.ID != reg.User.ID {
		t.Fatalf("login = %+v, %v", s.User, err)
	}
	if len(e.refresh.saved) != 2 {
		t.Errorf("each login stores a new refresh token; saved = %d", len(e.refresh.saved))
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.Register(ctx, "ana@example.com", "Ana", "supersecreta"); err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]struct{ email, password string }{
		"wrong password": {"ana@example.com", "incorrecta"},
		"unknown email":  {"nadie@example.com", "supersecreta"},
		"empty":          {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			e.hasher.verified = nil
			if _, err := e.svc.Login(ctx, c.email, c.password); !errors.Is(err, domain.ErrInvalidCredentials) {
				t.Fatalf("err = %v, want ErrInvalidCredentials", err)
			}
			// Unknown emails still pay for one password verification (against
			// the dummy hash), so timing does not reveal which accounts exist.
			if len(e.hasher.verified) != 1 {
				t.Errorf("verifications = %d, want 1", len(e.hasher.verified))
			}
		})
	}
}

func TestLoginRejectsOversizedInputBeforeLookingUp(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Login(context.Background(), "ana@example.com", strings.Repeat("x", domain.MaxPasswordLen+1))
	if !errors.Is(err, domain.ErrInvalidCredentials) || e.users.lookups != 0 || len(e.hasher.verified) != 0 {
		t.Errorf("err = %v, lookups = %d, verifications = %d", err, e.users.lookups, len(e.hasher.verified))
	}
}

func TestMeAndAuthenticate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	s, _ := e.svc.Register(ctx, "ana@example.com", "Ana", "supersecreta")

	id, err := e.svc.Authenticate(s.AccessToken)
	if err != nil || id != s.User.ID {
		t.Fatalf("Authenticate = %v, %v", id, err)
	}
	if u, err := e.svc.Me(ctx, id); err != nil || u.Email != "ana@example.com" {
		t.Errorf("Me = %+v, %v", u, err)
	}
	if _, err := e.svc.Me(ctx, domain.NewID()); !errors.Is(err, domain.ErrUserNotFound) {
		t.Errorf("Me(unknown) err = %v", err)
	}
	if _, err := e.svc.Authenticate("forged"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Errorf("Authenticate(forged) err = %v, want ErrInvalidToken", err)
	}
}
