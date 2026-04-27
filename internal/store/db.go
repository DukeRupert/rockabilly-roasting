package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryRecorder records duration and outcome of a named database query.
// The store layer defines this interface so it can stay decoupled from
// platform/metrics — pass nil for tests or one-off tools to disable.
type QueryRecorder interface {
	RecordQuery(name string, dur time.Duration, err error)
}

// trackQuery is the helper store methods use under defer to time a call.
// Safe to call with a nil recorder.
func trackQuery(rec QueryRecorder, name string, start time.Time, err *error) {
	if rec == nil {
		return
	}
	var e error
	if err != nil {
		e = *err
	}
	rec.RecordQuery(name, time.Since(start), e)
}

// Tx runs fn inside a database transaction. If fn returns an error, the
// transaction is rolled back; otherwise it is committed.
func Tx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
