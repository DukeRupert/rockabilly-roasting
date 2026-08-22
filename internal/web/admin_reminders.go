package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// handleAdminWholesaleReminders renders the weekly order-reminder dashboard: a
// dry-run list of exactly who the next scheduled send would email. This
// replaces the old rr service's unauthenticated GET
// /api/email/preview-reminders, which mailed the preview to a hardcoded address
// instead of showing it on screen.
//
// The one-off notice composer that used to live here moved to Announcements,
// which can reach retail as well as wholesale and can schedule the send. Two
// composers for the same job was one too many.
func (d *Deps) handleAdminWholesaleReminders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name, role := staffNameRole(r)

	props, err := d.reminderProps(r, name, role)
	if err != nil {
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		admin.WholesaleRemindersContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.WholesaleReminders(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminCustomerOrderReminders toggles the weekly reminder for one
// customer — the replacement for the old service's customer_notifications
// opt-out row.
func (d *Deps) handleAdminCustomerOrderReminders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	enabled := r.FormValue("enabled") == "true"

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.WholesaleService.SetOrderRemindersEnabled(ctx, tx, staffActor(r), id, enabled)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}

// reminderProps loads the shared page data for the reminders dashboard.
func (d *Deps) reminderProps(r *http.Request, name, role string) (admin.WholesaleRemindersProps, error) {
	ctx := r.Context()

	var recipients []domain.OrderReminderRecipient
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		recipients, txErr = d.WholesaleService.ListOrderReminderRecipients(ctx, tx, time.Now())
		return txErr
	}); err != nil {
		return admin.WholesaleRemindersProps{}, err
	}

	return admin.WholesaleRemindersProps{
		Recipients:   recipients,
		WindowDays:   int(app.OrderReminderWindow / (24 * time.Hour)),
		SuppressDays: int(app.OrderReminderSuppressWindow / (24 * time.Hour)),
		CutoffLabel:  app.OrderReminderCutoffLabel,
		ScheduleNote: d.ReminderScheduleNote,
		StaffName:    name,
		StaffRole:    role,
	}, nil
}
