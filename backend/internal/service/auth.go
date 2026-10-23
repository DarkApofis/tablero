package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/DarkApofis/tablero/backend/internal/auth"
	"github.com/DarkApofis/tablero/backend/internal/db"
)

const (
	MinPasswordLen = 8
	MaxPasswordLen = 128 // caps hashing cost per request
	MaxNameLen     = 100
	MaxEmailLen    = 254

	pgUniqueViolation = "23505"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrEmailTaken         = errors.New("email already registered")
	ErrUserNotFound       = errors.New("user not found")
)

// ValidationError maps field names to a human-readable problem.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed: %v", e.Fields)
}

// UserStore is the persistence the auth service needs; *db.Queries satisfies it.
type UserStore interface {
	CreateUser(ctx context.Context, arg db.CreateUserParams) (db.User, error)
	GetUserByEmail(ctx context.Context, email string) (db.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (db.User, error)
	CreateRefreshToken(ctx context.Context, arg db.CreateRefreshTokenParams) error
}

type AuthService struct {
	store      UserStore
	tokens     *auth.TokenIssuer
	refreshTTL time.Duration
	now        func() time.Time

	// dummyHash is verified against when the email does not exist, so an
	// unknown email costs the same hashing work as a wrong password.
	dummyHash string
}

func NewAuthService(store UserStore, tokens *auth.TokenIssuer, refreshTTL time.Duration) *AuthService {
	dummy, err := auth.HashPassword(uuid.NewString())
	if err != nil {
		panic(fmt.Sprintf("auth: generate dummy password hash: %v", err))
	}
	return &AuthService{store: store, tokens: tokens, refreshTTL: refreshTTL, now: time.Now, dummyHash: dummy}
}

// Session is what a successful register or login hands back to the client.
type Session struct {
	User             db.User
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

type RegisterInput struct {
	Email    string
	Name     string
	Password string
}

func (s *AuthService) Register(ctx context.Context, in RegisterInput) (Session, error) {
	email := normalizeEmail(in.Email)
	name := strings.TrimSpace(in.Name)

	fields := map[string]string{}
	if !validEmail(email) {
		fields["email"] = "Introduce un correo válido"
	}
	if name == "" {
		fields["name"] = "El nombre es obligatorio"
	} else if utf8.RuneCountInString(name) > MaxNameLen {
		fields["name"] = fmt.Sprintf("El nombre no puede superar %d caracteres", MaxNameLen)
	}
	if n := utf8.RuneCountInString(in.Password); n < MinPasswordLen {
		fields["password"] = fmt.Sprintf("La contraseña debe tener al menos %d caracteres", MinPasswordLen)
	} else if n > MaxPasswordLen {
		fields["password"] = fmt.Sprintf("La contraseña no puede superar %d caracteres", MaxPasswordLen)
	}
	if len(fields) > 0 {
		return Session{}, &ValidationError{Fields: fields}
	}

	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return Session{}, fmt.Errorf("hash password: %w", err)
	}

	user, err := s.store.CreateUser(ctx, db.CreateUserParams{Email: email, PasswordHash: hash, Name: name})
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgUniqueViolation {
			return Session{}, ErrEmailTaken
		}
		return Session{}, fmt.Errorf("create user: %w", err)
	}
	return s.newSession(ctx, user)
}

// Login returns ErrInvalidCredentials for an unknown email and for a wrong
// password alike, and does the same hashing work in both cases so response
// time does not reveal which one it was.
func (s *AuthService) Login(ctx context.Context, email, password string) (Session, error) {
	email = normalizeEmail(email)

	if utf8.RuneCountInString(password) > MaxPasswordLen || len(email) > MaxEmailLen {
		return Session{}, ErrInvalidCredentials
	}

	user, err := s.store.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = auth.VerifyPassword(password, s.dummyHash)
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, fmt.Errorf("get user: %w", err)
	}

	ok, err := auth.VerifyPassword(password, user.PasswordHash)
	if err != nil {
		return Session{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return Session{}, ErrInvalidCredentials
	}
	return s.newSession(ctx, user)
}

// Me returns the user behind an authenticated request.
func (s *AuthService) Me(ctx context.Context, id uuid.UUID) (db.User, error) {
	user, err := s.store.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.User{}, ErrUserNotFound
	}
	return user, err
}

func (s *AuthService) newSession(ctx context.Context, user db.User) (Session, error) {
	access, accessExp, err := s.tokens.Issue(user.ID)
	if err != nil {
		return Session{}, fmt.Errorf("issue access token: %w", err)
	}

	refresh, refreshHash, err := auth.NewRefreshToken()
	if err != nil {
		return Session{}, fmt.Errorf("generate refresh token: %w", err)
	}
	refreshExp := s.now().Add(s.refreshTTL)
	if err := s.store.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: refreshExp,
	}); err != nil {
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

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// validEmail accepts a bare address (no display name) of a sane length.
func validEmail(email string) bool {
	if email == "" || len(email) > MaxEmailLen {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email && strings.Contains(email[strings.LastIndex(email, "@"):], ".")
}
