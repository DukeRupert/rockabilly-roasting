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

func (d *Deps) handleAccountLoginRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	email := r.FormValue("email")
	next := r.FormValue("next")

	if email == "" {
		props := storefront.AccountLoginProps{Error: "Please enter your email address.", Next: next}
		if IsHTMX(r) {
			storefront.AccountLoginContent(props).Render(ctx, w) //nolint:errcheck
			return
		}
		storefront.AccountLoginPage(props).Render(ctx, w) //nolint:errcheck
		return
	}

	// Always show the same success message to prevent email enumeration.
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

	props := storefront.AccountLoginProps{Success: successMsg}
	if IsHTMX(r) {
		storefront.AccountLoginContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.AccountLoginPage(props).Render(ctx, w) //nolint:errcheck
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
		SameSite: http.SameSiteStrictMode,
		Secure:   d.SecureCookies,
	})

	redirectTo := "/account"
	if next := r.URL.Query().Get("next"); next != "" {
		// Only allow local paths to prevent open redirect.
		if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") {
			redirectTo = next
		}
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

	http.Redirect(w, r, "/", http.StatusSeeOther)
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

	// Redirect to the wholesale portal. Only allow local paths to prevent open redirect.
	redirect := "/wholesale/portal"
	if next := r.URL.Query().Get("redirect"); next != "" {
		if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") {
			redirect = next
		}
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

	http.Redirect(w, r, "/", http.StatusSeeOther)
}
