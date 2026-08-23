package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// jobListPerPage keeps the failed-jobs table to one screenful. A healthy store
// has zero rows here; a broken one usually has hundreds of the same kind, and
// paging through them is not how anyone diagnoses that — the kind rollup at the
// top is.
const jobListPerPage = 50

// handleAdminJobList renders background jobs River has discarded: what failed,
// why, and a per-row retry.
func (d *Deps) handleAdminJobList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}
	// The kind is echoed straight back into links and used as a SQL parameter,
	// never concatenated, so an unknown kind is harmless — it simply matches
	// nothing.
	kind := q.Get("kind")

	var props admin.JobListProps
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error

		props.Kinds, txErr = d.JobHealthService.CountDeadJobsByKind(ctx, tx)
		if txErr != nil {
			return txErr
		}
		props.Total, txErr = d.JobHealthService.CountDeadJobsOfKind(ctx, tx, kind)
		if txErr != nil {
			return txErr
		}
		// One extra row tells us whether a next page exists without a second
		// count query.
		props.Jobs, txErr = d.JobHealthService.ListDeadJobs(ctx, tx, kind, jobListPerPage+1, (page-1)*jobListPerPage)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props.HasMore = len(props.Jobs) > jobListPerPage
	if props.HasMore {
		props.Jobs = props.Jobs[:jobListPerPage]
	}

	props.KindFilter = kind
	props.Page = page
	props.PerPage = jobListPerPage
	props.Flash = flashMessage(q.Get("flash"))
	props.MerchantTZ = d.MerchantTZ
	props.StaffName, props.StaffRole = staffNameRole(r)

	if IsHTMX(r) {
		admin.JobListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.JobList(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminJobRetry hands one discarded job back to River.
func (d *Deps) handleAdminJobRetry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	jobID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	actor := staffActor(r)
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.JobHealthService.RetryDeadJob(ctx, tx, jobID, actor)
	})
	switch {
	case errors.Is(err, app.ErrJobNotDead):
		// The job is not in the dead list any more — someone else retried it,
		// a worker picked it back up, or the id was never there. The store
		// cannot tell those apart and it does not matter: nothing to do.
		// Saying so beats a 404 on a row that was on screen a second ago.
		http.Redirect(w, r, "/admin/jobs?flash=job_not_pending", http.StatusSeeOther)
		return
	case err != nil:
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/jobs?flash=job_retried", http.StatusSeeOther)
}

// flashMessage maps the redirect's flash key to operator-facing copy. Keys
// rather than free text so a crafted URL cannot put words on the page.
func flashMessage(key string) string {
	switch key {
	case "job_retried":
		return "Job re-queued. It runs as soon as a worker picks it up."
	case "job_not_pending":
		return "That job is no longer waiting to be retried — nothing to do."
	default:
		return ""
	}
}
