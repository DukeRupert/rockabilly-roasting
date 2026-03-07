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
			http.Redirect(w, r, "/auth/staff/login", http.StatusSeeOther)
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
			http.Redirect(w, r, "/auth/staff/login", http.StatusSeeOther)
			return
		}

		// Attach staff to context.
		ctx := auth.WithStaff(r.Context(), staff)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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

// --- Login / Logout handlers ---

func (d *Deps) handleStaffLoginPage(w http.ResponseWriter, r *http.Request) {
	// If already logged in, redirect to admin.
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil && cookie.Value != "" {
		http.Redirect(w, r, "/admin/orders", http.StatusSeeOther)
		return
	}
	admin.StaffLoginPage("").Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) handleStaffLogin(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")

	if email == "" || password == "" {
		admin.StaffLoginPage("Email and password are required.").Render(r.Context(), w) //nolint:errcheck
		return
	}

	ip := r.RemoteAddr
	ua := r.UserAgent()

	var rawToken string

	err := store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		var txErr error
		_, rawToken, txErr = d.AuthService.StaffLogin(r.Context(), tx, email, password, &ip, &ua)
		return txErr
	})
	if err != nil {
		admin.StaffLoginPage("Invalid email or password.").Render(r.Context(), w) //nolint:errcheck
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
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})

	http.Redirect(w, r, "/admin/orders", http.StatusSeeOther)
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

	http.Redirect(w, r, "/auth/staff/login", http.StatusSeeOther)
}
