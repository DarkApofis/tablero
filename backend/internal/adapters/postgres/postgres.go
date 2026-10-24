// Package postgres implements the application's repositories on PostgreSQL
// with pgx and the code sqlc generates, and maps generated rows to domain
// entities. Nothing outside this package sees pgx or sqlc types.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/db"
)

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

// Open creates a connection pool for databaseURL and verifies it with a ping.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// --- transactions ---

type txKey struct{}

// TxManager runs a use case in a transaction. The transaction travels in the
// context; every repository of this package uses it when present, so all the
// writes of the use case commit or roll back together.
type TxManager struct {
	pool *pgxpool.Pool
}

func NewTxManager(pool *pgxpool.Pool) *TxManager { return &TxManager{pool: pool} }

// InTx runs fn in a read-write transaction (READ COMMITTED), committing if
// fn returns nil. Called inside another transaction, fn joins it.
func (m *TxManager) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return m.run(ctx, pgx.TxOptions{}, fn)
}

// InReadTx runs fn in a read-only REPEATABLE READ transaction, so all its
// reads see one snapshot.
func (m *TxManager) InReadTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return m.run(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, fn)
}

func (m *TxManager) run(ctx context.Context, opts pgx.TxOptions, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	return pgx.BeginTxFunc(ctx, m.pool, opts, func(tx pgx.Tx) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

// conn gives repositories the transaction in ctx, or the pool outside one.
type conn struct {
	pool *pgxpool.Pool
}

func (c conn) q(ctx context.Context) *db.Queries {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return db.New(tx)
	}
	return db.New(c.pool)
}

func pgCode(err error) string {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code
	}
	return ""
}

// notFound maps "no rows" to the given domain error.
func notFound(err, domainErr error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domainErr
	}
	return err
}
