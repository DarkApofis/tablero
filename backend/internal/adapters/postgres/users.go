package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/db"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Users implements auth.UserRepository and auth.RefreshTokenRepository.
type Users struct{ conn }

func NewUsers(pool *pgxpool.Pool) *Users { return &Users{conn{pool}} }

func (r *Users) Create(ctx context.Context, email, name, passwordHash string) (domain.User, error) {
	u, err := r.q(ctx).CreateUser(ctx, db.CreateUserParams{Email: email, Name: name, PasswordHash: passwordHash})
	if pgCode(err) == pgUniqueViolation {
		return domain.User{}, domain.ErrEmailTaken
	}
	return toUser(u), err
}

func (r *Users) ByEmail(ctx context.Context, email string) (domain.User, error) {
	u, err := r.q(ctx).GetUserByEmail(ctx, email)
	return toUser(u), notFound(err, domain.ErrUserNotFound)
}

func (r *Users) ByID(ctx context.Context, id domain.ID) (domain.User, error) {
	u, err := r.q(ctx).GetUserByID(ctx, uuid.UUID(id))
	return toUser(u), notFound(err, domain.ErrUserNotFound)
}

func (r *Users) Save(ctx context.Context, userID domain.ID, hash []byte, expiresAt time.Time) error {
	return r.q(ctx).CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID: uuid.UUID(userID), TokenHash: hash, ExpiresAt: expiresAt,
	})
}

func toUser(u db.User) domain.User {
	return domain.User{ID: domain.ID(u.ID), Email: u.Email, Name: u.Name, PasswordHash: u.PasswordHash, CreatedAt: u.CreatedAt}
}
