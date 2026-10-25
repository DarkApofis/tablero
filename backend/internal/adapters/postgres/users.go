package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres/db"
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

func (r *Users) Rotate(ctx context.Context, oldHash, newHash []byte, expiresAt, now time.Time) (domain.ID, error) {
	id, err := r.q(ctx).RotateRefreshToken(ctx, db.RotateRefreshTokenParams{
		OldHash: oldHash, NewHash: newHash, ExpiresAt: expiresAt, Now: &now,
	})
	return domain.ID(id), notFound(err, domain.ErrInvalidRefreshToken)
}

func (r *Users) Revoke(ctx context.Context, hash []byte, now time.Time) error {
	return r.q(ctx).RevokeRefreshToken(ctx, db.RevokeRefreshTokenParams{TokenHash: hash, Now: &now})
}

func (r *Users) RevokeAllIfReused(ctx context.Context, hash []byte, revokedBefore, now time.Time) (bool, error) {
	n, err := r.q(ctx).RevokeUserRefreshTokensOnReuse(ctx, db.RevokeUserRefreshTokensOnReuseParams{
		TokenHash: hash, RevokedBefore: &revokedBefore, Now: &now,
	})
	return n > 0, err
}

func toUser(u db.User) domain.User {
	return domain.User{ID: domain.ID(u.ID), Email: u.Email, Name: u.Name, PasswordHash: u.PasswordHash, CreatedAt: u.CreatedAt}
}
