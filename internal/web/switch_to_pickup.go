package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// Switching an order to pickup is split across GET and POST on one URL for the
// same reason unsubscribe is (see unsubscribe.go): corporate mail gateways and
// inbox scanners fetch every link in an incoming message. A GET that acted
// would let a customer's IT department silently cancel their delivery. GET
// renders a confirmation page and changes nothing; the button on it POSTs.
//
// There is no one-click equivalent here — no mail client POSTs this URL on its
// own — so the POST always renders a page.

// handleSwitchToPickupPage renders the confirmation. Read-only.
func (d *Deps) handleSwitchToPickupPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	orderID, err := d.verifySwitchToPickupToken(token)
	if err != nil {
		d.renderSwitchToPickupProblem(w, r, err)
		return
	}

	var order *domain.Order
	var pickupInstructions string
	err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		o, txErr := d.OrderService.GetOrderAsStaff(r.Context(), tx, orderID)
		if txErr != nil {
			return txErr
		}
		order = o
		if cfg, cfgErr := d.CheckoutService.GetShippingConfig(r.Context(), tx); cfgErr == nil {
			pickupInstructions = cfg.LocalPickupInstructions
			if !cfg.LocalPickupEnabled {
				return app.ErrPickupUnavailable
			}
		}
		return nil
	})
	if err != nil {
		d.renderSwitchToPickupProblem(w, r, err)
		return
	}

	// Already on pickup — show the "you're all set" page rather than inviting a
	// switch that would do nothing.
	if order.ShippingMethod != nil && *order.ShippingMethod == domain.ShippingMethodPickup {
		storefront.SwitchToPickupDonePage(storefront.SwitchToPickupProps{
			OrderNumber:        order.Number,
			PickupInstructions: pickupInstructions,
		}).Render(r.Context(), w) //nolint:errcheck
		return
	}
	if order.ShippingMethod == nil ||
		*order.ShippingMethod != domain.ShippingMethodLocalDelivery ||
		order.FulfillmentStatus != domain.FulfillmentStatusUnfulfilled {
		d.renderSwitchToPickupProblem(w, r, app.ErrOrderNotSwitchable)
		return
	}

	storefront.SwitchToPickupConfirmPage(storefront.SwitchToPickupProps{
		Token:              token,
		OrderNumber:        order.Number,
		DeliveryDate:       formatDeliveryDate(order.ScheduledDeliveryDate),
		PickupInstructions: pickupInstructions,
	}).Render(r.Context(), w) //nolint:errcheck
}

// handleSwitchToPickup applies the switch.
func (d *Deps) handleSwitchToPickup(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("t")
	if token == "" {
		token = r.URL.Query().Get("t")
	}
	orderID, err := d.verifySwitchToPickupToken(token)
	if err != nil {
		d.renderSwitchToPickupProblem(w, r, err)
		return
	}

	var order *domain.Order
	var pickupInstructions string
	err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		// The customer is the actor: this is their own request, arriving with
		// proof they hold a link the shop sent them. Attributing it to the
		// system would hide a customer-driven change in the audit log.
		actor, txErr := d.orderCustomerActor(r, tx, orderID)
		if txErr != nil {
			return txErr
		}
		o, _, txErr := d.OrderService.SwitchToPickup(r.Context(), tx, orderID, actor)
		if txErr != nil {
			return txErr
		}
		order = o
		if cfg, cfgErr := d.CheckoutService.GetShippingConfig(r.Context(), tx); cfgErr == nil {
			pickupInstructions = cfg.LocalPickupInstructions
		}
		return nil
	})
	if err != nil {
		d.renderSwitchToPickupProblem(w, r, err)
		return
	}

	storefront.SwitchToPickupDonePage(storefront.SwitchToPickupProps{
		OrderNumber:        order.Number,
		PickupInstructions: pickupInstructions,
	}).Render(r.Context(), w) //nolint:errcheck
}

// verifySwitchToPickupToken checks the emailed link's signature, purpose, and
// expiry against a single clock read.
func (d *Deps) verifySwitchToPickupToken(token string) (uuid.UUID, error) {
	if d.OrderActionSigner == nil {
		return uuid.Nil, auth.ErrInvalidOrderActionToken
	}
	return d.OrderActionSigner.Verify(token, auth.OrderActionSwitchToPickup, time.Now())
}

// orderCustomerActor resolves the order's own customer as the audit actor.
// Falls back to an unattributed customer actor when the order has no customer
// row (imported or staff-created orders), which is preferable to failing a
// switch the customer is otherwise entitled to make.
func (d *Deps) orderCustomerActor(r *http.Request, tx pgx.Tx, orderID uuid.UUID) (app.Actor, error) {
	order, err := d.OrderService.GetOrderAsStaff(r.Context(), tx, orderID)
	if err != nil {
		return app.Actor{}, err
	}
	actor := app.Actor{Type: domain.AuditActorTypeCustomer, Name: "customer (email link)"}
	if order.CustomerID == nil {
		return actor, nil
	}
	customer, err := d.CustomerService.GetCustomer(r.Context(), tx, *order.CustomerID)
	if err != nil {
		return actor, nil //nolint:nilerr // actor attribution is best-effort; the switch itself is authorized by the token
	}
	actor.ID = &customer.ID
	actor.Name = customer.Email
	return actor, nil
}

// formatDeliveryDate renders the delivery date the customer is being offered a
// way out of. Empty when the order carries none.
func formatDeliveryDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return domain.DeliveryDateLabel(*t)
}

// renderSwitchToPickupProblem maps every failure to a page that tells the
// customer what to do next.
//
// A bad signature, an unknown order, and a rotated secret are deliberately
// collapsed into one generic response so a probe cannot use this endpoint to
// discover which order IDs exist. Expiry and "too late to switch" are reported
// honestly, because both are states the holder of a genuine link can reach
// through no fault of their own, and both have a real next step.
func (d *Deps) renderSwitchToPickupProblem(w http.ResponseWriter, r *http.Request, err error) {
	props := storefront.SwitchToPickupProps{}

	switch {
	case errors.Is(err, auth.ErrOrderActionTokenExpired):
		props.Reason = storefront.SwitchToPickupReasonExpired
		w.WriteHeader(http.StatusGone)
	case errors.Is(err, app.ErrOrderNotSwitchable):
		props.Reason = storefront.SwitchToPickupReasonTooLate
		w.WriteHeader(http.StatusConflict)
	case errors.Is(err, app.ErrPickupUnavailable):
		props.Reason = storefront.SwitchToPickupReasonPickupOff
		w.WriteHeader(http.StatusConflict)
	default:
		props.Reason = storefront.SwitchToPickupReasonInvalid
		w.WriteHeader(http.StatusBadRequest)
	}

	storefront.SwitchToPickupProblemPage(props).Render(r.Context(), w) //nolint:errcheck
}
