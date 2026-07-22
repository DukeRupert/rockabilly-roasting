package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/platform/ratelimit"
	"github.com/dukerupert/hiri/internal/platform/sessions"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

const customerCookieName = "hiri_customer"

// requireCustomerSession validates a customer session from the cookie.
// On failure, redirects to the customer login page.
func (d *Deps) requireCustomerSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(customerCookieName)
		if err != nil || cookie.Value == "" {
			http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
			return
		}

		var sess *domain.Session
		var customer *domain.Customer

		err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
			var txErr error
			sess, txErr = d.AuthService.ValidateSession(r.Context(), tx, cookie.Value)
			if txErr != nil {
				return txErr
			}
			if sess.ActorType != domain.SessionActorTypeCustomer {
				return nil
			}
			customer, txErr = d.AuthService.GetCustomerByID(r.Context(), tx, sess.ActorID)
			return txErr
		})

		if err != nil || sess == nil || customer == nil {
			http.SetCookie(w, &http.Cookie{
				Name:     customerCookieName,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
			})
			http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
			return
		}

		ctx := auth.WithCustomer(r.Context(), customer)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// optionalCustomerSession loads the customer into context if a valid session
// cookie is present, but never blocks or redirects. Use this on public
// storefront routes so layouts can reflect logged-in state. Stale or invalid
// cookies are cleared so we don't keep re-validating them on every request.
func (d *Deps) optionalCustomerSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(customerCookieName)
		if err != nil || cookie.Value == "" {
			next.ServeHTTP(w, r)
			return
		}

		var sess *domain.Session
		var customer *domain.Customer

		err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
			var txErr error
			sess, txErr = d.AuthService.ValidateSession(r.Context(), tx, cookie.Value)
			if txErr != nil {
				return txErr
			}
			if sess.ActorType != domain.SessionActorTypeCustomer {
				return nil
			}
			customer, txErr = d.AuthService.GetCustomerByID(r.Context(), tx, sess.ActorID)
			return txErr
		})

		if err != nil || sess == nil || customer == nil {
			http.SetCookie(w, &http.Cookie{
				Name:     customerCookieName,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
			})
			next.ServeHTTP(w, r)
			return
		}

		ctx := auth.WithCustomer(r.Context(), customer)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireRetailCustomer validates that the request has an authenticated retail customer.
// Unauthenticated requests redirect to /account/login with a ?next= return URL.
// Wholesale customers are redirected to /wholesale/portal.
func (d *Deps) requireRetailCustomer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(customerCookieName)
		if err != nil || cookie.Value == "" {
			returnTo := url.QueryEscape(r.URL.Path)
			http.Redirect(w, r, "/account/login?next="+returnTo, http.StatusSeeOther)
			return
		}

		var sess *domain.Session
		var customer *domain.Customer

		err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
			var txErr error
			sess, txErr = d.AuthService.ValidateSession(r.Context(), tx, cookie.Value)
			if txErr != nil {
				return txErr
			}
			if sess.ActorType != domain.SessionActorTypeCustomer {
				return nil
			}
			customer, txErr = d.AuthService.GetCustomerByID(r.Context(), tx, sess.ActorID)
			return txErr
		})

		if err != nil || sess == nil || customer == nil {
			http.SetCookie(w, &http.Cookie{
				Name:     customerCookieName,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
			})
			returnTo := url.QueryEscape(r.URL.Path)
			http.Redirect(w, r, "/account/login?next="+returnTo, http.StatusSeeOther)
			return
		}

		if customer.AccountType == domain.AccountTypeWholesale {
			http.Redirect(w, r, "/wholesale/portal", http.StatusSeeOther)
			return
		}

		ctx := auth.WithCustomer(r.Context(), customer)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireApprovedWholesale wraps requireCustomerSession and additionally
// checks that the customer is an approved wholesale account.
func (d *Deps) requireApprovedWholesale(next http.Handler) http.Handler {
	return d.requireCustomerSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		customer, ok := auth.CustomerFromContext(r.Context())
		if !ok {
			http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
			return
		}

		if customer.AccountType != domain.AccountTypeWholesale {
			http.Error(w, "wholesale account required", http.StatusForbidden)
			return
		}

		if customer.WholesaleStatus == nil || *customer.WholesaleStatus != domain.WholesaleStatusApproved {
			// Show a specific page for pending/suspended wholesale customers.
			if customer.WholesaleStatus != nil && *customer.WholesaleStatus == domain.WholesaleStatusPending {
				storefront.WholesalePending().Render(r.Context(), w) //nolint:errcheck
				return
			}
			storefront.WholesaleSuspended().Render(r.Context(), w) //nolint:errcheck
			return
		}

		next.ServeHTTP(w, r)
	}))
}

// customerActor builds an Actor from the authenticated customer in context.
func customerActor(r *http.Request) app.Actor {
	customer, ok := auth.CustomerFromContext(r.Context())
	if !ok {
		return app.Actor{Type: domain.AuditActorTypeCustomer, Name: "unknown"}
	}
	return app.Actor{
		Type: domain.AuditActorTypeCustomer,
		ID:   &customer.ID,
		Name: customer.Email,
	}
}

// --- Account (retail magic link) handlers ---

func (d *Deps) handleAccountLoginPage(w http.ResponseWriter, r *http.Request) {
	// If already logged in, redirect.
	cookie, err := r.Cookie(customerCookieName)
	if err == nil && cookie.Value != "" {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	props := storefront.AccountLoginProps{
		Next: r.URL.Query().Get("next"),
	}
	if IsHTMX(r) {
		storefront.AccountLoginContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.AccountLoginPage(props).Render(r.Context(), w) //nolint:errcheck
}

// safeNextOr returns next if it is a safe local path (starts with "/" but not "//"),
// otherwise returns fallback. Prevents open-redirect attacks.
func safeNextOr(next, fallback string) string {
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") {
		return next
	}
	return fallback
}

// renderLogin renders the login page (htmx-aware).
func renderLogin(w http.ResponseWriter, r *http.Request, props storefront.AccountLoginProps) {
	if IsHTMX(r) {
		storefront.AccountLoginContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.AccountLoginPage(props).Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) handleAccountLoginRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	email := r.FormValue("email")
	next := r.FormValue("next")

	if email == "" {
		renderLogin(w, r, storefront.AccountLoginProps{Error: "Please enter your email address.", Next: next})
		return
	}

	password := r.FormValue("password")
	if password != "" {
		// Password login path.
		ip := ratelimit.ClientIP(r)
		ua := r.UserAgent()
		var rawToken string
		err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			var txErr error
			_, rawToken, txErr = d.AuthService.CustomerLogin(ctx, tx, email, password, false /*rememberMe*/, &ip, &ua)
			return txErr
		})
		if err != nil {
			// Generic error for all failures — no enumeration, no wholesale-aware branching.
			renderLogin(w, r, storefront.AccountLoginProps{Error: "Invalid email or password.", Email: email, Next: next})
			return
		}

		// Reset rate limit counters on successful login.
		_ = d.RateLimiter.Reset(ctx, ratelimit.AuthIPKey(ratelimit.ClientIP(r)))
		_ = d.RateLimiter.Reset(ctx, ratelimit.AuthIdentifierKey(ratelimit.HashIdentifier(email)))

		// SameSite=Strict for password logins (not a cross-site redirect from email).
		http.SetCookie(w, &http.Cookie{
			Name:     customerCookieName,
			Value:    rawToken,
			Path:     "/",
			MaxAge:   int(sessions.CustomerSessionDuration.Seconds()),
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			Secure:   d.SecureCookies,
		})

		redirectTo := safeNextOr(next, "/account")
		if IsHTMX(r) {
			w.Header().Set("HX-Redirect", redirectTo)
			return
		}
		http.Redirect(w, r, redirectTo, http.StatusSeeOther)
		return
	}

	// Magic-link path (no password supplied). Always return a generic success
	// message to prevent email enumeration.
	successMsg := "If you have an account, check your email for a sign-in link."

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		customer, txErr := d.CustomerService.GetCustomerByEmail(ctx, tx, email)
		if txErr != nil {
			if errors.Is(txErr, app.ErrCustomerNotFound) {
				return nil // Silently succeed — no enumeration
			}
			return txErr
		}

		rawToken, txErr := d.AuthService.CreateMagicLinkToken(ctx, tx, customer.ID)
		if txErr != nil {
			return txErr
		}

		// Enqueue email send job in the same transaction.
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.MagicLinkSendArgs{
			CustomerID: customer.ID,
			RawToken:   rawToken,
			Next:       next,
		}, nil)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	renderLogin(w, r, storefront.AccountLoginProps{Success: successMsg})
}

// renderForgotPassword renders the forgot-password page (htmx-aware).
func renderForgotPassword(w http.ResponseWriter, r *http.Request, props storefront.AccountForgotPasswordProps) {
	if IsHTMX(r) {
		storefront.AccountForgotPasswordContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.AccountForgotPasswordPage(props).Render(r.Context(), w) //nolint:errcheck
}

// handleAccountForgotPasswordPage renders the public forgot-password form.
func (d *Deps) handleAccountForgotPasswordPage(w http.ResponseWriter, r *http.Request) {
	renderForgotPassword(w, r, storefront.AccountForgotPasswordProps{})
}

// handleAccountForgotPassword mints a setup token for the matching customer and
// enqueues a reset email. Works for any customer (retail or wholesale) — the
// emailed link lands on the generic /account/password-setup page. Always returns
// a generic success message to prevent email enumeration; a non-matching email
// silently succeeds. Mirrors the magic-link branch of handleAccountLoginRequest.
func (d *Deps) handleAccountForgotPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	email := r.FormValue("email")

	if email == "" {
		renderForgotPassword(w, r, storefront.AccountForgotPasswordProps{Error: "Please enter your email address."})
		return
	}

	successMsg := "If you have an account, we've emailed you a link to set your password."

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		customer, txErr := d.CustomerService.GetCustomerByEmail(ctx, tx, email)
		if txErr != nil {
			if errors.Is(txErr, app.ErrCustomerNotFound) {
				return nil // Silently succeed — no enumeration
			}
			return txErr
		}

		rawToken, txErr := d.AuthService.CreateSetupToken(ctx, tx, customer.ID)
		if txErr != nil {
			return txErr
		}

		// Enqueue email send job in the same transaction.
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.PasswordResetSendArgs{
			CustomerID: customer.ID,
			RawToken:   rawToken,
		}, nil)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	renderForgotPassword(w, r, storefront.AccountForgotPasswordProps{Success: successMsg})
}

// handleAccountSecurity renders the security / password management page.
func (d *Deps) handleAccountSecurity(w http.ResponseWriter, r *http.Request) {
	customer, ok := auth.CustomerFromContext(r.Context())
	if !ok {
		// Should not happen — middleware guarantees this. Defensive.
		http.Redirect(w, r, "/account/login", http.StatusSeeOther)
		return
	}
	props := storefront.AccountSecurityProps{
		Customer:     customer,
		HasPassword:  customer.PasswordHash != nil,
		InitialSetup: r.URL.Query().Get("initial") == "1",
	}
	if r.URL.Query().Get("verify_sent") == "1" {
		props.Success = "Verification email sent — check your inbox."
	}
	if IsHTMX(r) {
		storefront.AccountSecurityContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.AccountSecurityPage(props).Render(r.Context(), w) //nolint:errcheck
}

// handleAccountVerifyEmailSend creates a magic-link token for the
// authenticated customer and enqueues an email-verification message. The
// underlying token is a magic link — redeeming it both verifies the email
// and creates a session — but the email copy is framed around verification.
func (d *Deps) handleAccountVerifyEmailSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, ok := auth.CustomerFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/account/login", http.StatusSeeOther)
		return
	}

	// Already verified — bounce back without enqueuing anything.
	if customer.EmailVerified {
		http.Redirect(w, r, "/account/security", http.StatusSeeOther)
		return
	}

	next := r.FormValue("next")

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		rawToken, txErr := d.AuthService.CreateMagicLinkToken(ctx, tx, customer.ID)
		if txErr != nil {
			return txErr
		}
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.EmailVerifySendArgs{
			CustomerID: customer.ID,
			RawToken:   rawToken,
			Next:       next,
		}, nil)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	redirectTo := "/account/security?verify_sent=1"
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", redirectTo)
		return
	}
	http.Redirect(w, r, redirectTo, http.StatusSeeOther)
}

// handleAccountPasswordSet sets a first-time password for an authenticated customer.
// No current-password check — the live session is the credential.
func (d *Deps) handleAccountPasswordSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, ok := auth.CustomerFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/account/login", http.StatusSeeOther)
		return
	}
	newPassword := r.FormValue("new_password")

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.AuthService.SetPassword(ctx, tx, customer.ID, newPassword, customerActor(r))
	})

	if err != nil {
		props := storefront.AccountSecurityProps{
			Customer:    customer,
			HasPassword: customer.PasswordHash != nil,
		}
		if errors.Is(err, app.ErrPasswordTooShort) {
			props.Error = "Password must be at least 10 characters."
		} else {
			Error(w, r, err)
			return
		}
		if IsHTMX(r) {
			storefront.AccountSecurityContent(props).Render(ctx, w) //nolint:errcheck
			return
		}
		storefront.AccountSecurityPage(props).Render(ctx, w) //nolint:errcheck
		return
	}

	// Success — re-render with HasPassword=true so the form switches to the change variant.
	props := storefront.AccountSecurityProps{
		Customer:    customer,
		HasPassword: true,
		Success:     "Password set. You can now sign in with your password.",
	}
	if IsHTMX(r) {
		storefront.AccountSecurityContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.AccountSecurityPage(props).Render(ctx, w) //nolint:errcheck
}

// handleAccountPasswordChange updates an existing password after verifying the current one.
func (d *Deps) handleAccountPasswordChange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, ok := auth.CustomerFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/account/login", http.StatusSeeOther)
		return
	}
	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.AuthService.ChangePassword(ctx, tx, customer.ID, currentPassword, newPassword, customerActor(r))
	})

	if err != nil {
		props := storefront.AccountSecurityProps{
			Customer:    customer,
			HasPassword: customer.PasswordHash != nil,
		}
		switch {
		case errors.Is(err, app.ErrInvalidCredentials):
			props.Error = "Current password is incorrect."
		case errors.Is(err, app.ErrPasswordTooShort):
			props.Error = "Password must be at least 10 characters."
		default:
			Error(w, r, err)
			return
		}
		if IsHTMX(r) {
			storefront.AccountSecurityContent(props).Render(ctx, w) //nolint:errcheck
			return
		}
		storefront.AccountSecurityPage(props).Render(ctx, w) //nolint:errcheck
		return
	}

	// Success — re-render with success flash.
	props := storefront.AccountSecurityProps{
		Customer:    customer,
		HasPassword: true,
		Success:     "Password updated.",
	}
	if IsHTMX(r) {
		storefront.AccountSecurityContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.AccountSecurityPage(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAccountMagicRedeem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rawToken := r.URL.Query().Get("token")

	if rawToken == "" {
		storefront.MagicLinkExpiredPage().Render(ctx, w) //nolint:errcheck
		return
	}

	ip := ratelimit.ClientIP(r)
	ua := r.UserAgent()

	var sessionToken string
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		_, sessionToken, txErr = d.AuthService.RedeemMagicLink(ctx, tx, rawToken, &ip, &ua)
		return txErr
	})
	if err != nil {
		if errors.Is(err, app.ErrMagicLinkExpired) {
			storefront.MagicLinkExpiredPage().Render(ctx, w) //nolint:errcheck
			return
		}
		Error(w, r, err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     customerCookieName,
		Value:    sessionToken,
		Path:     "/",
		MaxAge:   int(app.MagicLinkSessionDuration.Seconds()),
		HttpOnly: true,
		// Lax (not Strict) so the cookie attaches on the redirect that follows
		// the cross-site click from the email — Strict would drop the cookie on
		// the immediate hop to /account and bounce the user back to login.
		SameSite: http.SameSiteLaxMode,
		Secure:   d.SecureCookies,
	})

	redirectTo := safeNextOr(r.URL.Query().Get("next"), "/account")

	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", redirectTo)
		return
	}
	http.Redirect(w, r, redirectTo, http.StatusSeeOther)
}

func (d *Deps) handleAccountLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(customerCookieName)
	if err == nil && cookie.Value != "" {
		customer, ok := auth.CustomerFromContext(r.Context())
		if ok {
			_ = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
				sess, txErr := d.AuthService.ValidateSession(r.Context(), tx, cookie.Value)
				if txErr != nil {
					return txErr
				}
				return d.AuthService.Logout(r.Context(), tx, sess.ID, customer.ID, customer.Email, domain.AuditActorTypeCustomer)
			})
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     customerCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", "/")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleAccountPasswordSetupPage renders the public password-setup page using
// the token from the email link. Reuses the wholesale setup-token mechanism but
// presents a retail-branded page and redirects to /account/login on success.
func (d *Deps) handleAccountPasswordSetupPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Redirect(w, r, "/account/login", http.StatusSeeOther)
		return
	}
	props := storefront.AccountPasswordSetupProps{Token: token}
	if IsHTMX(r) {
		storefront.AccountPasswordSetupContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.AccountPasswordSetupPage(props).Render(r.Context(), w) //nolint:errcheck
}

// handleAccountPasswordSetup consumes the setup token and writes the new
// password. Token is single-use and expires after 72 hours.
func (d *Deps) handleAccountPasswordSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := r.FormValue("token")
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")

	renderErr := func(msg string) {
		props := storefront.AccountPasswordSetupProps{Token: token, Error: msg}
		if IsHTMX(r) {
			storefront.AccountPasswordSetupContent(props).Render(ctx, w) //nolint:errcheck
			return
		}
		storefront.AccountPasswordSetupPage(props).Render(ctx, w) //nolint:errcheck
	}

	if token == "" {
		http.Redirect(w, r, "/account/login", http.StatusSeeOther)
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
		_, txErr := d.AuthService.SetPasswordWithToken(ctx, tx, token, password)
		return txErr
	})
	if err != nil {
		if errors.Is(err, app.ErrSetupTokenExpired) {
			renderErr("This link has expired or has already been used. Ask us to send a fresh one.")
			return
		}
		if errors.Is(err, app.ErrPasswordTooShort) {
			renderErr("Password must be at least 10 characters.")
			return
		}
		Error(w, r, err)
		return
	}

	props := storefront.AccountPasswordSetupProps{Success: true}
	if IsHTMX(r) {
		storefront.AccountPasswordSetupContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.AccountPasswordSetupPage(props).Render(ctx, w) //nolint:errcheck
}

// --- Wholesale Login / Logout handlers ---

func (d *Deps) handleWholesaleLoginPage(w http.ResponseWriter, r *http.Request) {
	// If already logged in, redirect to portal.
	cookie, err := r.Cookie(customerCookieName)
	if err == nil && cookie.Value != "" {
		http.Redirect(w, r, "/wholesale/portal", http.StatusSeeOther)
		return
	}
	props := storefront.WholesaleLoginProps{}
	if IsHTMX(r) {
		storefront.WholesaleLoginContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.WholesaleLoginPage(props).Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) handleWholesaleLogin(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")
	rememberMe := r.FormValue("remember_me") == "on"

	if email == "" || password == "" {
		props := storefront.WholesaleLoginProps{Error: "Email and password are required.", Email: email}
		if IsHTMX(r) {
			storefront.WholesaleLoginContent(props).Render(r.Context(), w) //nolint:errcheck
			return
		}
		storefront.WholesaleLoginPage(props).Render(r.Context(), w) //nolint:errcheck
		return
	}

	ip := ratelimit.ClientIP(r)
	ua := r.UserAgent()

	var rawToken string

	err := store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		var txErr error
		_, rawToken, txErr = d.AuthService.CustomerLogin(r.Context(), tx, email, password, rememberMe, &ip, &ua)
		return txErr
	})
	if err != nil {
		props := storefront.WholesaleLoginProps{
			Error: "Invalid email or password.",
			Email: email,
		}
		if IsHTMX(r) {
			storefront.WholesaleLoginContent(props).Render(r.Context(), w) //nolint:errcheck
			return
		}
		storefront.WholesaleLoginPage(props).Render(r.Context(), w) //nolint:errcheck
		return
	}

	// Reset rate limit counters on successful login.
	_ = d.RateLimiter.Reset(r.Context(), ratelimit.AuthIPKey(ratelimit.ClientIP(r)))
	_ = d.RateLimiter.Reset(r.Context(), ratelimit.AuthIdentifierKey(ratelimit.HashIdentifier(email)))

	duration := sessions.CustomerSessionDuration
	if rememberMe {
		duration = sessions.CustomerRememberMeDuration
	}

	http.SetCookie(w, &http.Cookie{
		Name:     customerCookieName,
		Value:    rawToken,
		Path:     "/",
		MaxAge:   int(duration.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   d.SecureCookies,
	})

	redirect := safeNextOr(r.URL.Query().Get("redirect"), "/wholesale/portal")
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", redirect)
		return
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (d *Deps) handleWholesaleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(customerCookieName)
	if err == nil && cookie.Value != "" {
		customer, ok := auth.CustomerFromContext(r.Context())
		if ok {
			_ = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
				sess, txErr := d.AuthService.ValidateSession(r.Context(), tx, cookie.Value)
				if txErr != nil {
					return txErr
				}
				return d.AuthService.Logout(r.Context(), tx, sess.ID, customer.ID, customer.Email, domain.AuditActorTypeCustomer)
			})
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     customerCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", "/")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
