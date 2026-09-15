package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
)

// JobRetrier is the pair of things an operator can do to a discarded job: put
// it back on the queue, or throw it away. River's client implements both; the
// interface keeps app/ off the worker package, the same way JobEnqueuer does
// for inserts.
//
// Both ride on the caller's transaction so they commit with their own audit
// record — a job that was re-queued but not logged, or logged but not
// re-queued, leaves an operator unable to tell what was already tried.
type JobRetrier interface {
	RetryJob(ctx context.Context, tx pgx.Tx, jobID int64) error
	DeleteJob(ctx context.Context, tx pgx.Tx, jobID int64) error
}

// JobHealthService reports on background jobs River has given up on, puts them
// back when an operator decides the underlying cause is fixed, and throws them
// away when it cannot be.
//
// This exists because a dead job is invisible everywhere else. Every customer
// email, label, invoice, and renewal in this system is a job; when the worker
// for one of them starts failing, the symptom is an absence — a receipt that
// never arrived — and absences don't show up in queues or counts.
type JobHealthService struct {
	jobs    *store.JobStore
	retrier JobRetrier
	audit   *audit.AuditWriter
}

// NewJobHealthService creates a JobHealthService. retrier may be nil in
// contexts with no River client wired (tests, one-off commands); retrying then
// fails cleanly with ErrJobRetryUnavailable rather than panicking.
func NewJobHealthService(jobs *store.JobStore, retrier JobRetrier, auditWriter *audit.AuditWriter) *JobHealthService {
	return &JobHealthService{jobs: jobs, retrier: retrier, audit: auditWriter}
}

// CountDeadJobs returns how many jobs River has discarded.
func (s *JobHealthService) CountDeadJobs(ctx context.Context, tx pgx.Tx) (int, error) {
	n, err := s.jobs.CountDeadJobs(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("count dead jobs: %w", err)
	}
	return n, nil
}

// CountDeadJobsByKind returns the per-kind rollup, largest first.
func (s *JobHealthService) CountDeadJobsByKind(ctx context.Context, tx pgx.Tx) ([]domain.DeadJobKindCount, error) {
	kinds, err := s.jobs.CountDeadJobsByKind(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("count dead jobs by kind: %w", err)
	}
	return kinds, nil
}

// ListDeadJobs returns discarded jobs, newest failure first, optionally
// narrowed to one kind.
func (s *JobHealthService) ListDeadJobs(ctx context.Context, tx pgx.Tx, kind string, limit, offset int) ([]domain.DeadJob, error) {
	list, err := s.jobs.ListDeadJobs(ctx, tx, kind, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list dead jobs: %w", err)
	}
	return list, nil
}

// RetryDeadJob puts one discarded job back on the queue and records who did it.
//
// Jobs must be idempotent by contract, so re-running one is safe — but it is
// still an operator action with customer-visible consequences (a retried email
// job sends an email), which is why it is audited rather than silent.
//
// Only discarded jobs are eligible. Retrying a job that is queued or running
// would shove it back in line for no reason, and River's JobRetry is permissive
// enough to allow it, so the guard lives here.
func (s *JobHealthService) RetryDeadJob(ctx context.Context, tx pgx.Tx, jobID int64, actor Actor) error {
	if s.retrier == nil {
		return ErrJobRetryUnavailable
	}

	job, ok, err := s.jobs.GetDeadJob(ctx, tx, jobID)
	if err != nil {
		return fmt.Errorf("load dead job: %w", err)
	}
	if !ok {
		return ErrJobNotDead
	}
	kind := job.Kind

	if err := s.retrier.RetryJob(ctx, tx, jobID); err != nil {
		return fmt.Errorf("retry job %d: %w", jobID, err)
	}

	// River job ids are int64, not UUIDs, so the id travels in metadata and
	// resource_id stays nil — the same shape other id-less audit entries use.
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditJobRetried,
		ResourceType: "river_job",
		ResourceID:   uuid.Nil,
		Metadata:     map[string]any{"job_id": jobID, "kind": kind},
	}); err != nil {
		return fmt.Errorf("audit job retried: %w", err)
	}
	return nil
}

// DismissDeadJob throws a discarded job away: River's row is deleted and the
// job is gone from the failed-jobs list for good.
//
// It exists because retrying is not always on the table. Plenty of dead jobs
// are unresolvable by the time anyone reads them — an email for a customer who
// has since been deleted, a job from a worker that no longer exists, a burst
// from an outage that has already been handled another way. Without a way to
// clear those, the list only ever grows, and a list that never reaches zero
// stops being read at all: the next real failure lands among a hundred rows
// nobody can do anything about.
//
// The deletion is permanent — River has no "undiscard" — so the audit record
// carries the job's kind, args, attempts and final error. After this commits
// that entry is the only surviving evidence the work existed, which is the
// point: dropping a customer's email is a decision somebody made, not an
// absence that quietly happened.
func (s *JobHealthService) DismissDeadJob(ctx context.Context, tx pgx.Tx, jobID int64, actor Actor) error {
	if s.retrier == nil {
		return ErrJobRetryUnavailable
	}

	job, ok, err := s.jobs.GetDeadJob(ctx, tx, jobID)
	if err != nil {
		return fmt.Errorf("load dead job: %w", err)
	}
	if !ok {
		return ErrJobNotDead
	}

	if err := s.retrier.DeleteJob(ctx, tx, jobID); err != nil {
		return fmt.Errorf("dismiss job %d: %w", jobID, err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditJobDismissed,
		ResourceType: "river_job",
		ResourceID:   uuid.Nil,
		Metadata: map[string]any{
			"job_id":     jobID,
			"kind":       job.Kind,
			"args":       job.Args,
			"attempts":   job.Attempt,
			"last_error": job.LastError,
		},
	}); err != nil {
		return fmt.Errorf("audit job dismissed: %w", err)
	}
	return nil
}
