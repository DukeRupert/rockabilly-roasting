package store

import (
	"context"
	"encoding/json"
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

// CountDeadJobsOfKind returns the total for one kind, so a filtered list can
// paginate against a real total rather than the page it happens to show.
func (s *JobStore) CountDeadJobsOfKind(ctx context.Context, tx pgx.Tx, kind string) (_ int, err error) {
	if kind == "" {
		return s.CountDeadJobs(ctx, tx)
	}
	var count int
	query := `SELECT COUNT(*) FROM river_job` + deadJobWhere + ` AND kind = $1`
	if err := tx.QueryRow(ctx, query, kind).Scan(&count); err != nil {
		return 0, fmt.Errorf("count dead jobs of kind: %w", err)
	}
	return count, nil
}

// GetDeadJobKind returns a job's kind, or "" if the job is not discarded. The
// retry handler uses it to confirm the job is actually dead before touching
// it, and to name the kind in the audit record.
func (s *JobStore) GetDeadJobKind(ctx context.Context, tx pgx.Tx, id int64) (_ string, err error) {
	var kind string
	query := `SELECT kind FROM river_job` + deadJobWhere + ` AND id = $1`
	if err := tx.QueryRow(ctx, query, id).Scan(&kind); err != nil {
		if err == pgx.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("get dead job kind: %w", err)
	}
	return kind, nil
}
