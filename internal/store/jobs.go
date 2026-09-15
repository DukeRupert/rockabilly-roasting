package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
)

// JobStore reads River's job table for operator-facing health reporting.
//
// River owns river_job and manages its schema through river.Migrate(), so
// everything here is read-only — mutations go through the River client, which
// knows the state machine. This mirrors the label-attempt queries in
// shipping.go, which already read river_job directly for the same reason:
// River's Go client has no query shaped like "group the discards by kind".
type JobStore struct{}

// NewJobStore creates a new JobStore.
func NewJobStore() *JobStore {
	return &JobStore{}
}

// deadJobWhere is the definition of "dead" shared by every query here.
//
// Only 'discarded' counts. River discards a job when it exhausts max_attempts
// — nothing retries it again, so it is work that silently did not happen.
// 'cancelled' is deliberate (someone or something called JobCancel) and is not
// a fault, and 'retryable' jobs are still on their way.
//
// buy_label is excluded because failed labels already have a dedicated
// dashboard group that names the stuck orders; see domain.DeadJobKindBuyLabel.
const deadJobWhere = ` WHERE state = 'discarded' AND kind <> '` + domain.DeadJobKindBuyLabel + `'`

// CountDeadJobs returns how many background jobs have been discarded.
func (s *JobStore) CountDeadJobs(ctx context.Context, tx pgx.Tx) (_ int, err error) {
	var count int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM river_job`+deadJobWhere).Scan(&count); err != nil {
		return 0, fmt.Errorf("count dead jobs: %w", err)
	}
	return count, nil
}

// CountDeadJobsByKind returns the per-kind rollup, largest cluster first. Dead
// jobs are rarely independent — one expired token or bad deploy discards a
// whole kind at once — so this grouping is usually the diagnosis.
func (s *JobStore) CountDeadJobsByKind(ctx context.Context, tx pgx.Tx) (_ []domain.DeadJobKindCount, err error) {
	query := `SELECT kind, COUNT(*)::int, MIN(finalized_at), MAX(finalized_at)
	          FROM river_job` + deadJobWhere + `
	          GROUP BY kind
	          ORDER BY COUNT(*) DESC, kind`

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("count dead jobs by kind: %w", err)
	}
	defer rows.Close()

	var out []domain.DeadJobKindCount
	for rows.Next() {
		var k domain.DeadJobKindCount
		var oldest, newest *time.Time
		if err := rows.Scan(&k.Kind, &k.Count, &oldest, &newest); err != nil {
			return nil, fmt.Errorf("scan dead job kind: %w", err)
		}
		if oldest != nil {
			k.Oldest = *oldest
		}
		if newest != nil {
			k.Newest = *newest
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ListDeadJobs returns discarded jobs, most recently failed first — the newest
// failures are the ones still worth chasing, and an operator working the list
// clears it from the top. Pass kind to narrow to a single job type; empty
// returns every kind.
func (s *JobStore) ListDeadJobs(ctx context.Context, tx pgx.Tx, kind string, limit, offset int) (_ []domain.DeadJob, err error) {
	if limit <= 0 {
		limit = 50
	}

	// river_job.errors is jsonb[] — a Postgres array of jsonb, not a jsonb
	// array — so the last attempt's message is pulled out in SQL rather than
	// scanned as JSON. Scanning the column whole yields Postgres array literal
	// text, which no JSON decoder will parse.
	query := `SELECT id, kind, queue, attempt, max_attempts,
	                 COALESCE(errors[array_upper(errors, 1)]->>'error', '') AS last_error,
	                 args, created_at, finalized_at
	          FROM river_job` + deadJobWhere
	args := []any{limit, offset}
	if kind != "" {
		query += ` AND kind = $3`
		args = append(args, kind)
	}
	query += ` ORDER BY finalized_at DESC, id DESC LIMIT $1 OFFSET $2`

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list dead jobs: %w", err)
	}
	defer rows.Close()

	var out []domain.DeadJob
	for rows.Next() {
		var j domain.DeadJob
		var argsJSON json.RawMessage
		var finalizedAt *time.Time
		if err := rows.Scan(&j.ID, &j.Kind, &j.Queue, &j.Attempt, &j.MaxAttempts,
			&j.LastError, &argsJSON, &j.CreatedAt, &finalizedAt); err != nil {
			return nil, fmt.Errorf("scan dead job: %w", err)
		}
		j.Args = string(argsJSON)
		if finalizedAt != nil {
			j.FinalizedAt = *finalizedAt
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// GetDeadJob returns one discarded job, or ok=false if the id names a job that
// is not discarded (or no job at all). The retry and dismiss handlers use it to
// confirm the job is actually dead before touching it, and to describe it in
// the audit record — which, for a dismissal, outlives the River row itself.
func (s *JobStore) GetDeadJob(ctx context.Context, tx pgx.Tx, id int64) (_ domain.DeadJob, _ bool, err error) {
	query := `SELECT id, kind, queue, attempt, max_attempts,
	                 COALESCE(errors[array_upper(errors, 1)]->>'error', '') AS last_error,
	                 args, created_at, finalized_at
	          FROM river_job` + deadJobWhere + ` AND id = $1`

	var j domain.DeadJob
	var argsJSON json.RawMessage
	var finalizedAt *time.Time
	if err := tx.QueryRow(ctx, query, id).Scan(&j.ID, &j.Kind, &j.Queue, &j.Attempt, &j.MaxAttempts,
		&j.LastError, &argsJSON, &j.CreatedAt, &finalizedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.DeadJob{}, false, nil
		}
		return domain.DeadJob{}, false, fmt.Errorf("get dead job: %w", err)
	}
	j.Args = string(argsJSON)
	if finalizedAt != nil {
		j.FinalizedAt = *finalizedAt
	}
	return j, true, nil
}
