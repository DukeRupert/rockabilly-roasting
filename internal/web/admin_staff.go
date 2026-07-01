package web

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// staffListLimit caps the team list. The staff table is small (a single-merchant
// crew), so a generous single-page limit avoids pagination UI entirely.
const staffListLimit = 200

// handleAdminStaffList renders the Team management page.
func (d *Deps) handleAdminStaffList(w http.ResponseWriter, r *http.Request) {
	d.renderAdminStaffList(w, r, "")
}

// renderAdminStaffList loads the staff list and renders the Team page, optionally
// with a form-level error banner. Shared by the list handler and the POST
// handlers when an action fails a guard (so the error survives without a flash).
func (d *Deps) renderAdminStaffList(w http.ResponseWriter, r *http.Request, formError string) {
	ctx := r.Context()

	var staff []domain.Staff
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		staff, txErr = d.StaffService.List(ctx, tx, staffListLimit, 0)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	var currentID uuid.UUID
	if s, ok := auth.StaffFromContext(ctx); ok {
		currentID = s.ID
	}

	props := admin.StaffListProps{
		Staff:          staff,
		CurrentStaffID: currentID,
		MerchantTZ:     d.MerchantTZ,
		FormError:      formError,
		StaffName:      name,
		StaffRole:      role,
	}

	if IsHTMX(r) {
		admin.StaffListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.StaffList(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminStaffInvite creates a new staff member and enqueues their invite
// email. The email send is a background job (mirrors the white-label invite), so
// a transient mail failure never blocks account creation — an admin can resend.
func (d *Deps) handleAdminStaffInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	params := app.InviteStaffParams{
		Name:  r.FormValue("name"),
		Email: r.FormValue("email"),
		Role:  domain.StaffRole(r.FormValue("role")),
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		created, txErr := d.StaffService.Invite(ctx, tx, params, staffActor(r))
		if txErr != nil {
			return txErr
		}
		// Enqueue the invite email in the same tx so account-created and
		// email-queued commit atomically — River is Postgres-backed, so this is
		// an in-transaction insert, not an external call.
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.StaffInviteArgs{StaffID: created.ID}, nil)
		return txErr
	})
	if err != nil {
		switch {
		case errors.Is(err, app.ErrStaffEmailExists):
			d.renderAdminStaffList(w, r, "A staff member with that email already exists.")
		case errors.Is(err, app.ErrStaffNameRequired):
			d.renderAdminStaffList(w, r, "Name is required.")
		case errors.Is(err, app.ErrStaffEmailRequired):
			d.renderAdminStaffList(w, r, "Email is required.")
		case errors.Is(err, app.ErrInvalidStaffRole):
			d.renderAdminStaffList(w, r, "Pick a valid role.")
		default:
			Error(w, r, err)
		}
		return
	}

	http.Redirect(w, r, "/admin/staff", http.StatusSeeOther)
}

// handleAdminStaffRole changes a staff member's role.
func (d *Deps) handleAdminStaffRole(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrStaffNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}
	newRole := domain.StaffRole(r.FormValue("role"))

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.StaffService.ChangeRole(ctx, tx, id, newRole, staffActor(r))
	})
	if err != nil {
		if d.renderStaffGuardError(w, r, err) {
			return
		}
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/staff", http.StatusSeeOther)
}

// handleAdminStaffActivate reactivates a deactivated staff member.
func (d *Deps) handleAdminStaffActivate(w http.ResponseWriter, r *http.Request) {
	d.setStaffActive(w, r, true)
}

// handleAdminStaffDeactivate deactivates a staff member.
func (d *Deps) handleAdminStaffDeactivate(w http.ResponseWriter, r *http.Request) {
	d.setStaffActive(w, r, false)
}

func (d *Deps) setStaffActive(w http.ResponseWriter, r *http.Request, active bool) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrStaffNotFound)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.StaffService.SetActive(ctx, tx, id, active, staffActor(r))
	})
	if err != nil {
		if d.renderStaffGuardError(w, r, err) {
			return
		}
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/staff", http.StatusSeeOther)
}

// handleAdminStaffResendInvite re-issues an invite / password-reset link.
func (d *Deps) handleAdminStaffResendInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrStaffNotFound)
		return
	}

	// Confirm the staff member exists before enqueuing (the job would otherwise
	// dead-letter after retries on a bogus ID).
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.StaffService.Get(ctx, tx, id)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	if _, err := d.RiverClient.Insert(ctx, jobs.StaffInviteArgs{StaffID: id}, nil); err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/staff", http.StatusSeeOther)
}

// renderStaffGuardError re-renders the Team page with a banner for the two
// user-actionable guard errors (self-modification, last-admin). Returns true if
// it handled the error, false if the caller should fall through to Error().
func (d *Deps) renderStaffGuardError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case errors.Is(err, app.ErrCannotModifySelf):
		d.renderAdminStaffList(w, r, "You can't change your own role or account status.")
		return true
	case errors.Is(err, app.ErrLastActiveAdmin):
		d.renderAdminStaffList(w, r, "You can't remove the last active admin — promote another admin first.")
		return true
	default:
		return false
	}
}

// --- Public staff password-setup (no session) ---

// handleStaffSetupPage renders the public set-password page from the invite
// token. Mirrors handleAccountPasswordSetupPage but for staff.
func (d *Deps) handleStaffSetupPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Redirect(w, r, "/auth/staff/login", http.StatusSeeOther)
		return
	}
	admin.StaffSetupPage(admin.StaffSetupProps{Token: token}).Render(r.Context(), w) //nolint:errcheck
}

// handleStaffSetup consumes the invite token and writes the new password. The
// token is single-use and expires after 72 hours.
func (d *Deps) handleStaffSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := r.FormValue("token")
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")

	renderErr := func(msg string) {
		admin.StaffSetupPage(admin.StaffSetupProps{Token: token, Error: msg}).Render(ctx, w) //nolint:errcheck
	}

	if token == "" {
		http.Redirect(w, r, "/auth/staff/login", http.StatusSeeOther)
		return
	}
	if password != passwordConfirm {
		renderErr("Passwords do not match.")
		return
	}
	if len(password) < 10 {
		renderErr("Password must be at least 10 characters.")
		return
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.AuthService.SetStaffPasswordWithToken(ctx, tx, token, password)
		return txErr
	})
	if err != nil {
		switch {
		case errors.Is(err, app.ErrStaffInviteInvalid):
			renderErr("This link has expired or has already been used. Ask an admin to send a fresh one.")
		case errors.Is(err, app.ErrPasswordTooShort):
			renderErr("Password must be at least 10 characters.")
		default:
			Error(w, r, err)
		}
		return
	}

	admin.StaffSetupPage(admin.StaffSetupProps{Success: true}).Render(ctx, w) //nolint:errcheck
}
