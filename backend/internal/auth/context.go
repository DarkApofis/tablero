package auth

import (
	"context"

	"github.com/google/uuid"
)

type ctxKey struct{}

// WithUserID stores the authenticated user's id in ctx.
func WithUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// UserID returns the authenticated user's id, if any.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxKey{}).(uuid.UUID)
	return id, ok
}
