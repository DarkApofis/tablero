// Package service holds the application logic that has not moved to
// internal/app yet.
package service

import "github.com/DarkApofis/tablero/backend/internal/domain"

// ValidationError is the domain's, so the HTTP adapter handles both layers alike.
type ValidationError = domain.ValidationError

const pgUniqueViolation = "23505"
