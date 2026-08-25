package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// postponementDateLayout is the value an <input type="date"> submits.
const postponementDateLayout = "2006-01-02"

// handleAdminDeliveryPostponementCreate moves one delivery run to a later day.
//
// Both dates are parsed in the merchant's zone, not UTC. They arrive as bare
// calendar days from a date input, and reading "2026-09-07" as UTC midnight
// puts it on the 6th for a merchant in the Americas — which would refuse the
// postponement as "not a delivery day" while showing the staffer a Monday.
func (d *Deps) handleAdminDeliveryPostponementCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	original, err := d.parsePostponementDate(r.FormValue("original_date"))
	if err != nil {
		redirectFlashError(w, r, "/admin/settings", "Pick the day the run is scheduled for.")
		return
	}
	moved, err := d.parsePostponementDate(r.FormValue("moved_to_date"))
	if err != nil {
		redirectFlashError(w, r, "/admin/settings", "Pick the day the run happens instead.")
		return
	}
	note := strings.TrimSpace(r.FormValue("note"))
	if len(note) > 200 {
		note = note[:200]
	}

	var result *app.PostponeDeliveryRunResult
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		result, txErr = d.CheckoutService.PostponeDeliveryRun(ctx, tx, time.Now(), original, moved, note, staffActor(r))
		return txErr
	})
	if err != nil {
		// The validation failures are the staffer's to fix and say so plainly;
		// anything else is ours and stays generic.
		switch {
		case errors.Is(err, app.ErrPostponeNotDeliveryDay):
			redirectFlashError(w, r, "/admin/settings",
				"That day has no delivery run to move — pick one of the days the van already runs.")
		case errors.Is(err, app.ErrPostponeNotForward):
			redirectFlashError(w, r, "/admin/settings", "A run can only be moved to a later day.")
		case errors.Is(err, app.ErrPostponeTooFar):
			redirectFlashError(w, r, "/admin/settings",
				"A run can only be moved up to two weeks. Further than that, change the delivery days instead.")
		case errors.Is(err, app.ErrPostponeIntoPast):
			redirectFlashError(w, r, "/admin/settings",
				"That day has already passed — pick a day still ahead.")
		case errors.Is(err, app.ErrPostponeAlreadyRun):
			redirectFlashError(w, r, "/admin/settings",
				"That run has already gone out — only a future run can be moved.")
		case errors.Is(err, app.ErrPostponeNoSchedule):
			redirectFlashError(w, r, "/admin/settings", "Set up a delivery schedule first.")
		default:
			slog.Error("admin settings: postpone delivery run", "error", err)
			redirectFlashError(w, r, "/admin/settings", "Failed to move that run")
		}
		return
	}

	redirectFlash(w, r, "/admin/settings", postponementFlash(result, d.MerchantTZ))
}

// handleAdminDeliveryPostponementDelete puts a moved run back on its scheduled
// day.
func (d *Deps) handleAdminDeliveryPostponementDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	original, err := d.parsePostponementDate(r.FormValue("original_date"))
	if err != nil {
		redirectFlashError(w, r, "/admin/settings", "Couldn't tell which run to restore.")
		return
	}

	var result *app.PostponeDeliveryRunResult
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		result, txErr = d.CheckoutService.RestoreDeliveryRun(ctx, tx, time.Now(), original, staffActor(r))
		return txErr
	})
	if err != nil {
		if errors.Is(err, app.ErrRestoreRunPassed) {
			redirectFlashError(w, r, "/admin/settings",
				"That run's scheduled day has already passed — it can't be put back.")
			return
		}
		slog.Error("admin settings: restore delivery run", "error", err)
		redirectFlashError(w, r, "/admin/settings", "Failed to restore that run")
		return
	}

	msg := fmt.Sprintf("%s is back on its scheduled day.", original.Format("Monday, January 2"))
	if result != nil && result.OrdersMoved > 0 {
		msg = fmt.Sprintf("%s %s moved back.", msg, pluralOrders(result.OrdersMoved))
	}
	redirectFlash(w, r, "/admin/settings", msg)
}

// parsePostponementDate reads a date input in the merchant's zone.
func (d *Deps) parsePostponementDate(value string) (time.Time, error) {
	loc := d.MerchantTZ
	if loc == nil {
		loc = time.UTC
	}
	return time.ParseInLocation(postponementDateLayout, value, loc)
}

// postponementFlash says what actually happened, including how many orders
// followed the run. Staff asked for the run to move; whether the orders already
// on the books came with it is the thing they cannot see from the settings page
// and would otherwise have to go check.
func postponementFlash(result *app.PostponeDeliveryRunResult, loc *time.Location) string {
	if result == nil {
		return "Run moved."
	}
	if loc == nil {
		loc = time.UTC
	}
	msg := fmt.Sprintf("%s now runs %s.",
		result.OriginalDate.In(loc).Format("Monday, January 2"),
		result.MovedTo.In(loc).Format("Monday, January 2"))
	if result.OrdersMoved > 0 {
		msg = fmt.Sprintf("%s %s moved with it.", msg, pluralOrders(result.OrdersMoved))
	}
	return msg
}

func pluralOrders(n int64) string {
	if n == 1 {
		return "1 order"
	}
	return fmt.Sprintf("%d orders", n)
}

// postponementRows formats the stored postponements for the settings page.
func postponementRows(ps []domain.DeliveryPostponement, loc *time.Location, now time.Time) []admin.PostponementRow {
	if loc == nil {
		loc = time.UTC
	}
	today := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, loc)

	rows := make([]admin.PostponementRow, 0, len(ps))
	for _, p := range ps {
		// Rebuilt in the merchant zone: these come off a `date` column in UTC,
		// and formatting them as they arrive would shift the printed day.
		original := time.Date(p.OriginalDate.Year(), p.OriginalDate.Month(), p.OriginalDate.Day(), 0, 0, 0, 0, loc)
		moved := time.Date(p.MovedTo.Year(), p.MovedTo.Month(), p.MovedTo.Day(), 0, 0, 0, 0, loc)
		row := admin.PostponementRow{
			OriginalValue: original.Format(postponementDateLayout),
			OriginalLabel: original.Format("Monday, January 2"),
			MovedToLabel:  moved.Format("Monday, January 2"),
			Note:          p.Note,
			// Restoring puts the run back on its scheduled day, so it is only
			// offered while that day is still ahead. Rendering the button on a
			// holiday that has been and gone would invite a click that rewrites
			// the promised date on orders delivered days ago.
			Restorable: !original.Before(today),
		}
		// A run passes through three states, not two, and the middle one is the
		// ordinary case for a Monday moved to Thursday viewed on the Wednesday.
		// Without a word for it the row simply loses its button and says nothing
		// about why.
		switch {
		case moved.Before(today):
			row.StatusNote = "Already run — kept for the record."
		case !row.Restorable:
			row.StatusNote = "Its scheduled day has passed; the run still goes out " +
				moved.Format("Monday, January 2") + "."
		}
		rows = append(rows, row)
	}
	return rows
}
