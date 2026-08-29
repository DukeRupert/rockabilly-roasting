package web

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/platform/ratelimit"
	"github.com/dukerupert/hiri/internal/platform/sessions"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

const sessionCookieName = "hiri_session"

// requireStaffSession is middleware that validates a staff session from the
// session cookie. If no valid session exists, it redirects to the login page.
func (d *Deps) requireStaffSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			redirectToStaffLogin(w, r)
			return
		}

		var sess *domain.Session
		var staff *domain.Staff

		err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
			var txErr error
			sess, txErr = d.AuthService.ValidateSession(r.Context(), tx, cookie.Value)
			if txErr != nil {
				return txErr
			}
			if sess.ActorType != domain.SessionActorTypeStaff {
				return nil
			}
			staff, txErr = d.AuthService.GetStaffByID(r.Context(), tx, sess.ActorID)
			return txErr
		})

		if err != nil || sess == nil || staff == nil || !staff.IsActive {
			// Clear invalid cookie.
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookieName,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
			})
			redirectToStaffLogin(w, r)
			return
		}

		// Attach staff to context.
		ctx := auth.WithStaff(r.Context(), staff)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requirePermission wraps a handler so it only runs for staff whose role grants
// the given permission. Must be mounted inside requireStaffSession, which puts
// the authenticated staff on the context. This is the middleware home for
// authorization checks — handlers and services stay permission-agnostic.
func (d *Deps) requirePermission(perm string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		staff, ok := auth.StaffFromContext(r.Context())
		if !ok || !auth.HasPermission(staff.Role, perm) {
			Error(w, r, app.ErrPermissionDenied)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// redirectToStaffLogin sends the client to the staff login page. For htmx
// requests (the admin panel is hx-boosted), a plain 303 would be followed by
// XHR and the login page swapped into #main-content; HX-Redirect forces a full
// page navigation instead.
func redirectToStaffLogin(w http.ResponseWriter, r *http.Request) {
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", "/auth/staff/login")
		return
	}
	http.Redirect(w, r, "/auth/staff/login", http.StatusSeeOther)
}

// staffActor builds an Actor from the authenticated staff in context.
func staffActor(r *http.Request) app.Actor {
	staff, ok := auth.StaffFromContext(r.Context())
	if !ok {
		return app.Actor{Type: domain.AuditActorTypeStaff, Name: "unknown"}
	}
	return app.Actor{
		Type: domain.AuditActorTypeStaff,
		ID:   &staff.ID,
		Name: staff.Name,
	}
}

// staffNameRole returns the staff name and role from context for template rendering.
func staffNameRole(r *http.Request) (string, string) {
	staff, ok := auth.StaffFromContext(r.Context())
	if !ok {
		return "Staff", ""
	}
	return staff.Name, string(staff.Role)
}

// staffCan reports whether the staff on the request's context holds perm. It is
// for deciding whether to *render* a control, never for deciding whether to
// allow the action — that stays in requirePermission. Hiding a button the
// server would refuse anyway is a courtesy, not a check.
func staffCan(r *http.Request, perm string) bool {
	staff, ok := auth.StaffFromContext(r.Context())
	return ok && auth.HasPermission(staff.Role, perm)
}

// --- Login / Logout handlers ---

func (d *Deps) handleStaffLoginPage(w http.ResponseWriter, r *http.Request) {
	// If already logged in, redirect to admin.
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil && cookie.Value != "" {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		return
	}
	admin.StaffLoginPage("").Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) handleStaffLogin(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")

	if email == "" || password == "" {
		d.renderStaffLoginError(w, r, "Email and password are required.", email)
		return
	}

	ip := ratelimit.ClientIP(r)
	ua := r.UserAgent()

	var rawToken string

	err := store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		var txErr error
		_, rawToken, txErr = d.AuthService.StaffLogin(r.Context(), tx, email, password, &ip, &ua)
		return txErr
	})
	if err != nil {
		d.renderStaffLoginError(w, r, "Invalid email or password.", email)
		return
	}

	// Reset rate limit counters on successful login.
	_ = d.RateLimiter.Reset(r.Context(), ratelimit.AuthIPKey(ratelimit.ClientIP(r)))
	_ = d.RateLimiter.Reset(r.Context(), ratelimit.AuthIdentifierKey(ratelimit.HashIdentifier(email)))

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    rawToken,
		Path:     "/",
		MaxAge:   int(sessions.StaffSessionDuration.Seconds()),
		HttpOnly: true,
		// Lax, not Strict. Strict withholds the cookie on cross-site top-level
		// navigations, which silently broke the one flow that depends on one:
		// returning from Intuit's consent screen to the QuickBooks OAuth
		// callback bounced to the login page before the handler ran, so the
		// integration could never be connected (verified 2026-08-29).
		// Lax still withholds the cookie on cross-site POST, so form CSRF
		// protection is unchanged. Admin's mutations are all POSTs bar one:
		// the QuickBooks OAuth callback is a GET that writes credentials, and
		// it is the very request this change exists to let through. That one
		// is defended by its own signed state cookie, checked before the code
		// is exchanged, so a cross-site GET cannot forge it. The other GETs
		// that read as verbs were each checked and none writes local state —
		// shipment label download and box-preset list only read, order rates
		// only calls Shippo, and the connect route mints state and redirects.
		SameSite: http.SameSiteLaxMode,
		Secure:   d.SecureCookies,
	})

	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", "/admin/")
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (d *Deps) renderStaffLoginError(w http.ResponseWriter, r *http.Request, errMsg, email string) {
	if IsHTMX(r) {
		admin.StaffLoginForm(errMsg, email).Render(r.Context(), w) //nolint:errcheck
		return
	}
	admin.StaffLoginPage(errMsg).Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) handleStaffLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil && cookie.Value != "" {
		staff, ok := auth.StaffFromContext(r.Context())
		if ok {
			_ = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
				sess, txErr := d.AuthService.ValidateSession(r.Context(), tx, cookie.Value)
				if txErr != nil {
					return txErr
				}
				return d.AuthService.Logout(r.Context(), tx, sess.ID, staff.ID, staff.Name, domain.AuditActorTypeStaff)
			})
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", "/auth/staff/login")
		return
	}
	http.Redirect(w, r, "/auth/staff/login", http.StatusSeeOther)
}
