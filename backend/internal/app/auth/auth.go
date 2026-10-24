// Package auth holds the authentication use cases: register, login, the
// current user and access-token verification.
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
}

type Clock interface {
	Now() time.Time
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

func (s *Service) Register(ctx context.Context, email, name, password string) (Session, error) {
	reg, err := domain.NewRegistration(email, name, password)
	if err != nil {
		return Session{}, err
	}
	hash, err := s.hasher.Hash(reg.Password)
	if err != nil {
		return Session{}, fmt.Errorf("hash password: %w", err)
	}
	user, err := s.users.Create(ctx, reg.Email, reg.Name, hash)
	if err != nil {
		if errors.Is(err, domain.ErrEmailTaken) {
			return Session{}, err
		}
		return Session{}, fmt.Errorf("create user: %w", err)
	}
	return s.newSession(ctx, user)
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
