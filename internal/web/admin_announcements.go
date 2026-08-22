package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// scheduleInputLayout is the format an <input type="datetime-local"> submits.
// Parsed in the merchant's timezone, never UTC: staff type "9am" meaning 9am at
// the shop, and interpreting that as UTC would send a holiday notice in the
// middle of the night.
const scheduleInputLayout = "2006-01-02T15:04"

// handleAdminAnnouncements lists notices, newest first.
func (d *Deps) handleAdminAnnouncements(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name, role := staffNameRole(r)

	var announcements []domain.Announcement
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		announcements, txErr = d.AnnouncementService.ListAnnouncements(ctx, tx, 50)
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	props := admin.AnnouncementsProps{
		Announcements: announcements,
		MerchantTZ:    d.MerchantTZ,
		StaffName:     name,
		StaffRole:     role,
	}

	if IsHTMX(r) {
		admin.AnnouncementsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.Announcements(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminAnnouncementNew renders the composer.
//
// It carries a live headcount for every audience, not just the selected one, so
// the choice between "all customers" and "wholesale only" is made against real
// numbers instead of a guess about how big the retail list is.
func (d *Deps) handleAdminAnnouncementNew(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name, role := staffNameRole(r)

	counts := map[domain.AnnouncementAudience]int{}
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		for _, a := range []domain.AnnouncementAudience{
			domain.AnnouncementAudienceAll,
			domain.AnnouncementAudienceRetail,
			domain.AnnouncementAudienceWholesale,
		} {
			recipients, txErr := d.AnnouncementService.PreviewRecipients(ctx, tx, a)
			if txErr != nil {
				return txErr
			}
			counts[a] = len(recipients)
		}
		return nil
	}); err != nil {
		Error(w, r, err)
		return
	}

	// Prefill honoured only when it names a real audience — the Order Reminders
	// page links in with ?audience=wholesale, and a junk value must fall back
	// to the safe default rather than selecting nothing.
	selected := domain.AnnouncementAudienceAll
	if a := domain.AnnouncementAudience(r.URL.Query().Get("audience")); a.Valid() {
		selected = a
	}

	props := admin.AnnouncementNewProps{
		Counts:   counts,
		Selected: selected,
		// Prefilled with the merchant-local "now", so staff scheduling a send
		// are editing a real timestamp in their own timezone rather than an
		// empty field.
		DefaultSendAt: time.Now().In(d.MerchantTZ).Format(scheduleInputLayout),
		TZAbbrev:      time.Now().In(d.MerchantTZ).Format("MST"),
		TestAddress:   staffEmail(r),
		StaffName:     name,
		StaffRole:     role,
	}

	if IsHTMX(r) {
		admin.AnnouncementNewContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.AnnouncementNew(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminAnnouncementShow renders one notice.
func (d *Deps) handleAdminAnnouncementShow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name, role := staffNameRole(r)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var announcement *domain.Announcement
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		announcement, txErr = d.AnnouncementService.GetAnnouncement(ctx, tx, id)
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	props := admin.AnnouncementShowProps{
		Announcement: *announcement,
		MerchantTZ:   d.MerchantTZ,
		StaffName:    name,
		StaffRole:    role,
	}

	if IsHTMX(r) {
		admin.AnnouncementShowContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.AnnouncementShow(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminAnnouncementCreate schedules a notice.
func (d *Deps) handleAdminAnnouncementCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	params, err := d.announcementParams(r)
	if err != nil {
		Error(w, r, err)
		return
	}

	var announcement *domain.Announcement
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		announcement, txErr = d.AnnouncementService.ScheduleAnnouncement(ctx, tx, staffActor(r), params)
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/announcements/%s", announcement.ID), http.StatusSeeOther)
}

// handleAdminAnnouncementTest sends the draft to the staff member's own
// address and answers with an inline result, so the composer keeps everything
// they have typed. A full page reload here would lose the draft, which is
// exactly when people stop bothering to preview.
func (d *Deps) handleAdminAnnouncementTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	to := staffEmail(r)
	if to == "" {
		admin.AnnouncementTestResult("Your staff account has no email address on file, so there's nowhere to send the test.", false).Render(ctx, w) //nolint:errcheck
		return
	}

	params, err := d.announcementParams(r)
	if err != nil && !isScheduleError(err) {
		// A test does not care about the send time — only the subject and body
		// are rendered — so a bad or empty schedule must not block previewing.
		admin.AnnouncementTestResult(err.Error(), false).Render(ctx, w) //nolint:errcheck
		return
	}

	if err := d.AnnouncementService.SendAnnouncementTest(ctx, d.Pool, staffActor(r), params, to); err != nil {
		admin.AnnouncementTestResult(err.Error(), false).Render(ctx, w) //nolint:errcheck
		return
	}

	admin.AnnouncementTestResult("Test sent to "+to+". Check how it reads before scheduling the real thing.", true).Render(ctx, w) //nolint:errcheck
}

// handleAdminAnnouncementCancel pulls a scheduled notice.
func (d *Deps) handleAdminAnnouncementCancel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.AnnouncementService.CancelAnnouncement(ctx, tx, staffActor(r), id)
	}); err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/announcements/%s", id), http.StatusSeeOther)
}

// handleAdminCustomerAnnouncements toggles announcements for one customer —
// the staff-side equivalent of the unsubscribe link, for when somebody asks to
// be taken off the list over the phone.
func (d *Deps) handleAdminCustomerAnnouncements(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	enabled := r.FormValue("enabled") == "true"

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.AnnouncementService.SetAnnouncementsEnabled(ctx, tx, staffActor(r), id, enabled)
	}); err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}

// announcementParams reads the composer form.
func (d *Deps) announcementParams(r *http.Request) (app.ScheduleAnnouncementParams, error) {
	p := app.ScheduleAnnouncementParams{
		Subject:  r.FormValue("subject"),
		Body:     r.FormValue("body"),
		Audience: domain.AnnouncementAudience(r.FormValue("audience")),
	}

	// "now" is the default so an omitted radio (or a test post, which carries
	// no schedule at all) sends immediately rather than failing.
	if r.FormValue("when") != "schedule" {
		return p, nil
	}

	raw := strings.TrimSpace(r.FormValue("send_at"))
	if raw == "" {
		return p, app.ErrScheduleInPast
	}
	sendAt, err := time.ParseInLocation(scheduleInputLayout, raw, d.MerchantTZ)
	if err != nil {
		return p, app.ErrScheduleInPast
	}
	p.SendAt = sendAt
	return p, nil
}

// isScheduleError reports whether err is only about the send time, which the
// test-send path can ignore.
func isScheduleError(err error) bool {
	return err == app.ErrScheduleInPast
}

// staffEmail returns the signed-in staff member's address, or "" when there is
// no staff in context.
func staffEmail(r *http.Request) string {
	staff, ok := auth.StaffFromContext(r.Context())
	if !ok {
		return ""
	}
	return staff.Email
}
