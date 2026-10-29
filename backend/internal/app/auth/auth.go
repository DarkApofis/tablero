// Package auth holds the authentication use cases: register, login, session
// refresh and logout, the current user and access-token verification.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Ports.

type UserRepository interface {
	// Create stores a user; domain.ErrEmailTaken if the email is in use.
	Create(ctx context.Context, email, name, passwordHash string) (domain.User, error)
	// ByEmail and ByID return domain.ErrUserNotFound when there is no such user.
	ByEmail(ctx context.Context, email string) (domain.User, error)
	ByID(ctx context.Context, id domain.ID) (domain.User, error)
}

type RefreshTokenRepository interface {
	// Save stores the hash of a refresh token; the plain value is never stored.
	Save(ctx context.Context, userID domain.ID, hash []byte, expiresAt time.Time) error
	// Rotate atomically revokes the live (unrevoked, unexpired) token oldHash
	// and stores newHash for the same user, whom it returns. If oldHash is not
	// live it changes nothing and returns domain.ErrInvalidRefreshToken.
	Rotate(ctx context.Context, oldHash, newHash []byte, expiresAt, now time.Time) (domain.ID, error)
	// Revoke revokes the token if it is not already; unknown tokens are ignored.
	Revoke(ctx context.Context, hash []byte, now time.Time) error
	// RevokeAllIfReused revokes every live token of the token's user if the
	// token was revoked before revokedBefore, and reports whether it did.
	RevokeAllIfReused(ctx context.Context, hash []byte, revokedBefore, now time.Time) (bool, error)
}

type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, hash string) (bool, error)
}

type TokenIssuer interface {
	// Issue signs a short-lived access token for userID.
	Issue(userID domain.ID) (token string, expiresAt time.Time, err error)
	// Verify returns the user of a valid access token, or domain.ErrInvalidToken.
	Verify(token string) (domain.ID, error)
	// NewRefreshToken returns a random opaque token and the hash to store.
	NewRefreshToken() (plain string, hash []byte, err error)
	// HashRefreshToken is the hash NewRefreshToken returns for plain.
	HashRefreshToken(plain string) []byte
}

// ReuseGrace is how long a rotated refresh token may come back without being
// treated as stolen. It covers a client that lost the refresh response or two
// tabs refreshing at once; later, the reuse revokes all the user's sessions.
const ReuseGrace = 30 * time.Second

type Clock interface {
	Now() time.Time
}

// Onboarder sets up what a new account starts with (the default workspace);
// it runs in the registration's transaction.
type Onboarder interface {
	Onboard(ctx context.Context, userID domain.ID) error
}

type TxManager interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Service implements the use cases.
type Service struct {
	users      UserRepository
	refresh    RefreshTokenRepository
	hasher     PasswordHasher
	tokens     TokenIssuer
	clock      Clock
	refreshTTL time.Duration

	// dummyHash is verified against when the email does not exist, so an
	// unknown email costs the same hashing work as a wrong password.
	dummyHash string

	// onboard, if set, runs on every registration (see WithOnboarding).
	onboard Onboarder
	tx      TxManager
}

func NewService(users UserRepository, refresh RefreshTokenRepository, hasher PasswordHasher, tokens TokenIssuer, clock Clock, refreshTTL time.Duration) (*Service, error) {
	random := make([]byte, 16)
	_, _ = rand.Read(random)
	dummy, err := hasher.Hash(hex.EncodeToString(random))
	if err != nil {
		return nil, fmt.Errorf("auth: generate dummy password hash: %w", err)
	}
	return &Service{
		users: users, refresh: refresh, hasher: hasher, tokens: tokens, clock: clock,
		refreshTTL: refreshTTL, dummyHash: dummy,
	}, nil
}

// Session is what a successful register or login hands back to the client.
type Session struct {
	User             domain.User
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// WithOnboarding returns a copy of the service whose Register also runs
// onboard, in one transaction with creating the user and the session: an
// account is never left without what it should start with. Copies share
// everything else, so the original keeps registering without it (the demo's
// guest accounts).
func (s *Service) WithOnboarding(tx TxManager, onboard Onboarder) *Service {
	c := *s
	c.tx, c.onboard = tx, onboard
	return &c
}

func (s *Service) Register(ctx context.Context, email, name, password string) (Session, error) {
	reg, err := domain.NewRegistration(email, name, password)
	if err != nil {
		return Session{}, err
	}
	hash, err := s.hasher.Hash(reg.Password) // slow: outside the transaction
	if err != nil {
		return Session{}, fmt.Errorf("hash password: %w", err)
	}
	register := func(ctx context.Context) (Session, error) {
		user, err := s.users.Create(ctx, reg.Email, reg.Name, hash)
		if err != nil {
			if errors.Is(err, domain.ErrEmailTaken) {
				return Session{}, err
			}
			return Session{}, fmt.Errorf("create user: %w", err)
		}
		if s.onboard != nil {
			if err := s.onboard.Onboard(ctx, user.ID); err != nil {
				return Session{}, fmt.Errorf("onboard user: %w", err)
			}
		}
		return s.newSession(ctx, user)
	}
	if s.onboard == nil {
		return register(ctx)
	}
	var session Session
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		var err error
		session, err = register(ctx)
		return err
	})
	return session, err
}

// Login returns domain.ErrInvalidCredentials for an unknown email and for a
// wrong password alike, and does the same hashing work in both cases so
// response time does not reveal which one it was.
func (s *Service) Login(ctx context.Context, email, password string) (Session, error) {
	email = domain.NormalizeEmail(email)
	if utf8.RuneCountInString(password) > domain.MaxPasswordLen || len(email) > domain.MaxEmailLen {
		return Session{}, domain.ErrInvalidCredentials
	}

	user, err := s.users.ByEmail(ctx, email)
	if errors.Is(err, domain.ErrUserNotFound) {
		_, _ = s.hasher.Verify(password, s.dummyHash)
		return Session{}, domain.ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, fmt.Errorf("get user: %w", err)
	}

	ok, err := s.hasher.Verify(password, user.PasswordHash)
	if err != nil {
		return Session{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return Session{}, domain.ErrInvalidCredentials
	}
	return s.newSession(ctx, user)
}

// Refresh rotates a refresh token: the presented one is revoked and the new
// session carries its replacement. It returns domain.ErrInvalidRefreshToken
// for an unknown, expired or revoked token, and domain.ErrRefreshTokenReused
// (which wraps it) when a token rotated more than ReuseGrace ago comes back.
func (s *Service) Refresh(ctx context.Context, plain string) (Session, error) {
	if plain == "" {
		return Session{}, domain.ErrInvalidRefreshToken
	}
	oldHash := s.tokens.HashRefreshToken(plain)
	now := s.clock.Now()

	refresh, newHash, err := s.tokens.NewRefreshToken()
	if err != nil {
		return Session{}, fmt.Errorf("generate refresh token: %w", err)
	}
	refreshExp := now.Add(s.refreshTTL)
	userID, err := s.refresh.Rotate(ctx, oldHash, newHash, refreshExp, now)
	if errors.Is(err, domain.ErrInvalidRefreshToken) {
		reused, rerr := s.refresh.RevokeAllIfReused(ctx, oldHash, now.Add(-ReuseGrace), now)
		if rerr != nil {
			return Session{}, fmt.Errorf("revoke sessions on reuse: %w", rerr)
		}
		if reused {
			return Session{}, domain.ErrRefreshTokenReused
		}
		return Session{}, err
	}
	if err != nil {
		return Session{}, fmt.Errorf("rotate refresh token: %w", err)
	}

	user, err := s.users.ByID(ctx, userID)
	if errors.Is(err, domain.ErrUserNotFound) {
		return Session{}, domain.ErrInvalidRefreshToken
	}
	if err != nil {
		return Session{}, fmt.Errorf("get user: %w", err)
	}
	access, accessExp, err := s.tokens.Issue(user.ID)
	if err != nil {
		return Session{}, fmt.Errorf("issue access token: %w", err)
	}
	return Session{
		User:             user,
		AccessToken:      access,
		AccessExpiresAt:  accessExp,
		RefreshToken:     refresh,
		RefreshExpiresAt: refreshExp,
	}, nil
}

// Logout revokes the refresh token. An empty, unknown or already revoked
// token is not an error: the session is over either way.
func (s *Service) Logout(ctx context.Context, plain string) error {
	if plain == "" {
		return nil
	}
	if err := s.refresh.Revoke(ctx, s.tokens.HashRefreshToken(plain), s.clock.Now()); err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}

// Me returns the user behind an authenticated request, or
// domain.ErrUserNotFound if the token outlived the user.
func (s *Service) Me(ctx context.Context, userID domain.ID) (domain.User, error) {
	return s.users.ByID(ctx, userID)
}

// Authenticate returns the user of a valid access token, or domain.ErrInvalidToken.
func (s *Service) Authenticate(token string) (domain.ID, error) {
	id, err := s.tokens.Verify(token)
	if err != nil {
		return domain.NilID, domain.ErrInvalidToken
	}
	return id, nil
}

func (s *Service) newSession(ctx context.Context, user domain.User) (Session, error) {
	access, accessExp, err := s.tokens.Issue(user.ID)
	if err != nil {
		return Session{}, fmt.Errorf("issue access token: %w", err)
	}
	refresh, refreshHash, err := s.tokens.NewRefreshToken()
	if err != nil {
		return Session{}, fmt.Errorf("generate refresh token: %w", err)
	}
	refreshExp := s.clock.Now().Add(s.refreshTTL)
	if err := s.refresh.Save(ctx, user.ID, refreshHash, refreshExp); err != nil {
		return Session{}, fmt.Errorf("store refresh token: %w", err)
	}
	return Session{
		User:             user,
		AccessToken:      access,
		AccessExpiresAt:  accessExp,
		RefreshToken:     refresh,
		RefreshExpiresAt: refreshExp,
	}, nil
}
