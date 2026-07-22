package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Beginner is implemented by pgxpool.Pool and allows transaction behaviour to
// be tested without coupling services to a concrete pool.
type Beginner interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// WithinTx executes fn in a transaction. It rolls back on callback errors and
// panics, and commits only after fn returns nil.
func WithinTx(ctx context.Context, beginner Beginner, options pgx.TxOptions, fn func(pgx.Tx) error) (err error) {
	tx, err := beginner.BeginTx(ctx, options)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(recovered)
		}
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
