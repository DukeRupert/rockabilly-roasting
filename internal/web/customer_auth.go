package web

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
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
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
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
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
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
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
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

// --- Customer Login / Logout handlers ---

func (d *Deps) handleCustomerLoginPage(w http.ResponseWriter, r *http.Request) {
	// If already logged in, redirect.
	cookie, err := r.Cookie(customerCookieName)
	if err == nil && cookie.Value != "" {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	storefront.CustomerLoginPage(storefront.CustomerLoginProps{}).Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) handleCustomerLogin(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")
	rememberMe := r.FormValue("remember_me") == "on"

	if email == "" || password == "" {
		props := storefront.CustomerLoginProps{Error: "Email and password are required."}
		storefront.CustomerLoginPage(props).Render(r.Context(), w) //nolint:errcheck
		return
	}

	ip := r.RemoteAddr
	ua := r.UserAgent()

	var rawToken string

	err := store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		var txErr error
		_, rawToken, txErr = d.AuthService.CustomerLogin(r.Context(), tx, email, password, rememberMe, &ip, &ua)
		return txErr
	})
	if err != nil {
		props := storefront.CustomerLoginProps{
			Error: "Invalid email or password.",
			Email: email,
		}
		storefront.CustomerLoginPage(props).Render(r.Context(), w) //nolint:errcheck
		return
	}

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
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})

	// Redirect to the page they were trying to visit, or the account page.
	redirect := r.URL.Query().Get("redirect")
	if redirect == "" {
		redirect = "/account"
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (d *Deps) handleCustomerLogout(w http.ResponseWriter, r *http.Request) {
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
