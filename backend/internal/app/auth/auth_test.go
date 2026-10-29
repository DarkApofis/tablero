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
	revokedAt *time.Time
}

// fakeRefresh mimics the refresh_tokens table.
type fakeRefresh struct{ saved []*refreshRecord }

func (f *fakeRefresh) Save(_ context.Context, userID domain.ID, hash []byte, expiresAt time.Time) error {
	f.saved = append(f.saved, &refreshRecord{userID: userID, hash: string(hash), expiresAt: expiresAt})
	return nil
}

func (f *fakeRefresh) find(hash []byte) *refreshRecord {
	for _, r := range f.saved {
		if r.hash == string(hash) {
			return r
		}
	}
	return nil
}

func (f *fakeRefresh) Rotate(ctx context.Context, oldHash, newHash []byte, expiresAt, now time.Time) (domain.ID, error) {
	old := f.find(oldHash)
	if old == nil || old.revokedAt != nil || !old.expiresAt.After(now) {
		return domain.NilID, domain.ErrInvalidRefreshToken
	}
	old.revokedAt = &now
	return old.userID, f.Save(ctx, old.userID, newHash, expiresAt)
}

func (f *fakeRefresh) Revoke(_ context.Context, hash []byte, now time.Time) error {
	if r := f.find(hash); r != nil && r.revokedAt == nil {
		r.revokedAt = &now
	}
	return nil
}

func (f *fakeRefresh) RevokeAllIfReused(_ context.Context, hash []byte, revokedBefore, now time.Time) (bool, error) {
	r := f.find(hash)
	if r == nil || r.revokedAt == nil || !r.revokedAt.Before(revokedBefore) {
		return false, nil
	}
	revoked := false
	for _, other := range f.saved {
		if other.userID == r.userID && other.revokedAt == nil {
			other.revokedAt = &now
			revoked = true
		}
	}
	return revoked, nil
}

func (f *fakeRefresh) live(userID domain.ID) int {
	n := 0
	for _, r := range f.saved {
		if r.userID == userID && r.revokedAt == nil {
			n++
		}
	}
	return n
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

func (f *fakeTokens) HashRefreshToken(plain string) []byte { return []byte("sha:" + plain) }

// fixedClock reads the time through a pointer so tests can move it.
type fixedClock struct{ t *time.Time }

func (c fixedClock) Now() time.Time { return *c.t }

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
	svc, err := auth.NewService(e.users, e.refresh, e.hasher, &fakeTokens{}, fixedClock{&e.now}, refreshTTL)
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

func TestRefreshRotatesToken(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	reg, _ := e.svc.Register(ctx, "ana@example.com", "Ana", "supersecreta")

	e.now = e.now.Add(time.Hour)
	s, err := e.svc.Refresh(ctx, reg.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if s.User.ID != reg.User.ID || s.AccessToken != "access-"+reg.User.ID.String() {
		t.Errorf("session = %+v", s)
	}
	if s.RefreshToken == "" || s.RefreshToken == reg.RefreshToken {
		t.Errorf("refresh token not rotated: %q", s.RefreshToken)
	}
	if !s.RefreshExpiresAt.Equal(e.now.Add(refreshTTL)) {
		t.Errorf("refresh expiry = %v, want now+TTL", s.RefreshExpiresAt)
	}
	if old := e.refresh.find([]byte("sha:" + reg.RefreshToken)); old.revokedAt == nil {
		t.Error("the used token was not revoked")
	}
	if n := e.refresh.live(reg.User.ID); n != 1 {
		t.Errorf("live tokens = %d, want 1", n)
	}
}

func TestRefreshRejectsInvalidTokens(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	reg, _ := e.svc.Register(ctx, "ana@example.com", "Ana", "supersecreta")

	for name, token := range map[string]string{"empty": "", "unknown": "refresh-zzz"} {
		if _, err := e.svc.Refresh(ctx, token); !errors.Is(err, domain.ErrInvalidRefreshToken) {
			t.Errorf("%s: err = %v, want ErrInvalidRefreshToken", name, err)
		}
	}

	e.now = e.now.Add(refreshTTL)
	if _, err := e.svc.Refresh(ctx, reg.RefreshToken); !errors.Is(err, domain.ErrInvalidRefreshToken) {
		t.Errorf("expired: err = %v, want ErrInvalidRefreshToken", err)
	}
}

func TestRefreshReuse(t *testing.T) {
	t.Run("within the grace period only fails", func(t *testing.T) {
		e := newEnv(t)
		ctx := context.Background()
		reg, _ := e.svc.Register(ctx, "ana@example.com", "Ana", "supersecreta")
		if _, err := e.svc.Refresh(ctx, reg.RefreshToken); err != nil {
			t.Fatal(err)
		}

		e.now = e.now.Add(auth.ReuseGrace - time.Second)
		_, err := e.svc.Refresh(ctx, reg.RefreshToken)
		if !errors.Is(err, domain.ErrInvalidRefreshToken) || errors.Is(err, domain.ErrRefreshTokenReused) {
			t.Errorf("err = %v, want plain ErrInvalidRefreshToken", err)
		}
		if n := e.refresh.live(reg.User.ID); n != 1 {
			t.Errorf("live tokens = %d; the rotated session must survive", n)
		}
	})

	t.Run("after the grace period revokes every session", func(t *testing.T) {
		e := newEnv(t)
		ctx := context.Background()
		reg, _ := e.svc.Register(ctx, "ana@example.com", "Ana", "supersecreta")
		if _, err := e.svc.Login(ctx, "ana@example.com", "supersecreta"); err != nil { // another device
			t.Fatal(err)
		}
		rotated, err := e.svc.Refresh(ctx, reg.RefreshToken)
		if err != nil {
			t.Fatal(err)
		}

		e.now = e.now.Add(auth.ReuseGrace + time.Second)
		if _, err := e.svc.Refresh(ctx, reg.RefreshToken); !errors.Is(err, domain.ErrRefreshTokenReused) {
			t.Fatalf("err = %v, want ErrRefreshTokenReused", err)
		}
		if n := e.refresh.live(reg.User.ID); n != 0 {
			t.Errorf("live tokens = %d, want 0", n)
		}
		if _, err := e.svc.Refresh(ctx, rotated.RefreshToken); !errors.Is(err, domain.ErrInvalidRefreshToken) {
			t.Errorf("the token issued by the rotation still works: err = %v", err)
		}
	})
}

func TestLogoutRevokesToken(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	reg, _ := e.svc.Register(ctx, "ana@example.com", "Ana", "supersecreta")

	if err := e.svc.Logout(ctx, reg.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Refresh(ctx, reg.RefreshToken); !errors.Is(err, domain.ErrInvalidRefreshToken) {
		t.Errorf("refresh after logout: err = %v", err)
	}
	for _, token := range []string{"", "refresh-zzz", reg.RefreshToken} {
		if err := e.svc.Logout(ctx, token); err != nil {
			t.Errorf("Logout(%q) = %v, want nil", token, err)
		}
	}
}

type fakeOnboarder struct {
	onboarded []domain.ID
	err       error
}

func (f *fakeOnboarder) Onboard(_ context.Context, user domain.User) error {
	f.onboarded = append(f.onboarded, user.ID)
	return f.err
}

// fakeTx runs fn and counts the transactions.
type fakeTx struct{ n int }

func (f *fakeTx) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	f.n++
	return fn(ctx)
}

func TestRegisterOnboardsInTheSameTransaction(t *testing.T) {
	e := newEnv(t)
	onboard, tx := &fakeOnboarder{}, &fakeTx{}
	svc := e.svc.WithOnboarding(tx, onboard)

	s, err := svc.Register(context.Background(), "nueva@example.com", "Nueva", "supersecreta")
	if err != nil {
		t.Fatal(err)
	}
	if len(onboard.onboarded) != 1 || onboard.onboarded[0] != s.User.ID || tx.n != 1 {
		t.Errorf("onboarded %v in %d transactions, want [%s] in 1", onboard.onboarded, tx.n, s.User.ID)
	}

	// The original service still registers without it (the demo's guests).
	if _, err := e.svc.Register(context.Background(), "invitada@example.com", "Invitada", "supersecreta"); err != nil {
		t.Fatal(err)
	}
	if len(onboard.onboarded) != 1 {
		t.Errorf("the service without onboarding onboarded someone: %v", onboard.onboarded)
	}
}

func TestRegisterFailsIfOnboardingFails(t *testing.T) {
	e := newEnv(t)
	svc := e.svc.WithOnboarding(&fakeTx{}, &fakeOnboarder{err: errors.New("no such workspace")})

	if _, err := svc.Register(context.Background(), "nueva@example.com", "Nueva", "supersecreta"); err == nil {
		t.Fatal("registered although onboarding failed")
	}
	// The real transaction rolls the user back; here, no session was issued.
	if len(e.refresh.saved) != 0 {
		t.Errorf("a session was issued: %d refresh tokens", len(e.refresh.saved))
	}
}

// Onboarders runs every step in order and stops at the first that fails.
func TestOnboardersRunInOrderAndStopOnError(t *testing.T) {
	first, second, third := &fakeOnboarder{}, &fakeOnboarder{err: errors.New("boom")}, &fakeOnboarder{}
	user := domain.User{ID: domain.NewID()}
	err := auth.Onboarders{first, second, third}.Onboard(context.Background(), user)
	if err == nil || len(first.onboarded) != 1 || len(second.onboarded) != 1 || len(third.onboarded) != 0 {
		t.Errorf("err = %v, ran %d/%d/%d", err, len(first.onboarded), len(second.onboarded), len(third.onboarded))
	}
}
