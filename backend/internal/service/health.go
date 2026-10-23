// Package service holds the application logic, independent of HTTP.
package service

import (
	"context"
	"time"
)

// Pinger is satisfied by *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

type HealthService struct {
	db Pinger
}

func NewHealthService(db Pinger) *HealthService {
	return &HealthService{db: db}
}

// Check reports whether the service's dependencies are reachable.
func (s *HealthService) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return s.db.Ping(ctx)
}
