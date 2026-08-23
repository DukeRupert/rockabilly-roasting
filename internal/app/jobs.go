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

// JobRetrier puts a discarded background job back on the queue. River's client
// implements it; the interface keeps app/ off the worker package, the same way
// JobEnqueuer does for inserts.
//
// The retry rides on the caller's transaction so it commits with its own audit
// record — a job that was re-queued but not logged, or logged but not
// re-queued, leaves an operator unable to tell what was already tried.
type JobRetrier interface {
	RetryJob(ctx context.Context, tx pgx.Tx, jobID int64) error
}

// JobHealthService reports on background jobs River has given up on, and puts
// them back when an operator decides the underlying cause is fixed.
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

// CountDeadJobsOfKind returns the total for one kind (or all kinds when kind is
// empty), so a filtered list paginates against a real total.
func (s *JobHealthService) CountDeadJobsOfKind(ctx context.Context, tx pgx.Tx, kind string) (int, error) {
	n, err := s.jobs.CountDeadJobsOfKind(ctx, tx, kind)
	if err != nil {
		return 0, fmt.Errorf("count dead jobs of kind: %w", err)
	}
	return n, nil
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

	kind, err := s.jobs.GetDeadJobKind(ctx, tx, jobID)
	if err != nil {
		return fmt.Errorf("load dead job: %w", err)
	}
	if kind == "" {
		return ErrJobNotDead
	}

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
