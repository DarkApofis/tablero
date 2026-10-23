package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InTx runs fn with Queries bound to a transaction, committing if fn returns
// nil and rolling back otherwise.
func InTx(ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn func(q *Queries) error) error {
	return pgx.BeginTxFunc(ctx, pool, opts, func(tx pgx.Tx) error {
		return fn(New(tx))
	})
}
