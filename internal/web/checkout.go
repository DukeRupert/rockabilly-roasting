package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// --- Request/Response types ---

type checkoutCartItem struct {
	VariantID    string `json:"variant_id"`
	ProductTitle string `json:"product_title"`
	SKU          string `json:"sku"`
	Quantity     int    `json:"quantity"`
	UnitPrice    int    `json:"unit_price"`
	LineTotal    int    `json:"line_total"`
}

type checkoutCartResponse struct {
	CartID   string             `json:"cart_id"`
	Items    []checkoutCartItem `json:"items"`
	Subtotal int                `json:"subtotal"`
	Currency string             `json:"currency"`
}

type checkoutAddressRequest struct {
	Email      string `json:"email"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	Line1      string `json:"line1"`
	Line2      string `json:"line2,omitempty"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
}

type checkoutAddressResponse struct {
	AddressID  string `json:"address_id"`
	CustomerID string `json:"customer_id"`
}

type checkoutPaymentIntentRequest struct {
	CartID     string `json:"cart_id"`
	AddressID  string `json:"address_id"`
	CustomerID string `json:"customer_id"`
}

type checkoutPaymentIntentResponse struct {
	ClientSecret string `json:"client_secret"`
	Amount       int    `json:"amount"`
	Currency     string `json:"currency"`
}

type checkoutConfirmRequest struct {
	CartID          string `json:"cart_id"`
	CustomerID      string `json:"customer_id"`
	AddressID       string `json:"address_id"`
	PaymentIntentID string `json:"payment_intent_id"`
}

type checkoutConfirmResponse struct {
	OrderID     string `json:"order_id"`
	OrderNumber string `json:"order_number"`
}

// --- Handlers ---

// handleCheckoutPage renders the checkout page with the Svelte mount point.
func (d *Deps) handleCheckoutPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cartID := getCartID(r)

	cartIDStr := ""
	if cartID != nil {
		cartIDStr = cartID.String()
	}

	stripeKey := os.Getenv("STRIPE_PUBLISHABLE_KEY")
	cartCount := d.cartItemCountFromCookie(r)

	props := storefront.CheckoutPageProps{
		CartID:    cartIDStr,
		StripeKey: stripeKey,
		CartCount: cartCount,
	}

	if IsHTMX(r) {
		storefront.CheckoutContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.CheckoutPage(props).Render(ctx, w) //nolint:errcheck
}

// handleCheckoutCart returns cart contents for the checkout UI.
func (d *Deps) handleCheckoutCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	cartID := getCartID(r)
	if cartID == nil {
		JSON(w, http.StatusOK, checkoutCartResponse{Items: []checkoutCartItem{}})
		return
	}

	var resp checkoutCartResponse

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		cart, txErr := d.CartService.GetOrCreateCart(ctx, tx, cartID)
		if txErr != nil {
			return txErr
		}
		resp.CartID = cart.ID.String()
		resp.Currency = cart.CurrencyCode

		items, txErr := d.CartService.ListItems(ctx, tx, cart.ID)
		if txErr != nil {
			return txErr
		}

		for _, ci := range items {
			variant, vErr := d.CatalogService.GetVariant(ctx, tx, ci.VariantID)
			if vErr != nil {
				return vErr
			}
			product, pErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
			if pErr != nil {
				return pErr
			}

			lineTotal := ci.UnitPrice * ci.Quantity
			resp.Subtotal += lineTotal

			resp.Items = append(resp.Items, checkoutCartItem{
				VariantID:    ci.VariantID.String(),
				ProductTitle: product.Title,
				SKU:          variant.SKU,
				Quantity:     ci.Quantity,
				UnitPrice:    ci.UnitPrice,
				LineTotal:    lineTotal,
			})
		}
		return nil
	})
	if err != nil {
		logger.Error("checkout cart", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load cart"})
		return
	}

	if resp.Items == nil {
		resp.Items = []checkoutCartItem{}
	}

	JSON(w, http.StatusOK, resp)
}

// handleCheckoutAddress validates and saves a shipping address, creating a guest customer if needed.
func (d *Deps) handleCheckoutAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	var req checkoutAddressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	validationErrors := map[string]string{}
	if strings.TrimSpace(req.Email) == "" {
		validationErrors["email"] = "Email is required"
	}
	if strings.TrimSpace(req.FirstName) == "" {
		validationErrors["first_name"] = "First name is required"
	}
	if strings.TrimSpace(req.LastName) == "" {
		validationErrors["last_name"] = "Last name is required"
	}
	if strings.TrimSpace(req.Line1) == "" {
		validationErrors["line1"] = "Address is required"
	}
	if strings.TrimSpace(req.City) == "" {
		validationErrors["city"] = "City is required"
	}
	if strings.TrimSpace(req.State) == "" {
		validationErrors["state"] = "State is required"
	}
	if strings.TrimSpace(req.PostalCode) == "" {
		validationErrors["postal_code"] = "Postal code is required"
	}
	if strings.TrimSpace(req.Country) == "" {
		req.Country = "US"
	}

	if len(validationErrors) > 0 {
		JSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": validationErrors})
		return
	}

	var resp checkoutAddressResponse

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// Find or create guest customer by email
		customer, txErr := d.CustomerService.GetCustomerByEmail(ctx, tx, req.Email)
		if txErr != nil {
			if !errors.Is(txErr, app.ErrCustomerNotFound) {
				return fmt.Errorf("lookup customer: %w", txErr)
			}
			customer, txErr = d.CustomerService.CreateGuest(ctx, tx, req.Email, req.FirstName, req.LastName)
			if txErr != nil {
				return fmt.Errorf("create guest customer: %w", txErr)
			}
		}
		resp.CustomerID = customer.ID.String()

		var line2 *string
		if req.Line2 != "" {
			line2 = &req.Line2
		}
		addr, txErr := d.CustomerService.CreateAddress(ctx, tx, store.CreateAddressParams{
			CustomerID:  &customer.ID,
			FirstName:   req.FirstName,
			LastName:    req.LastName,
			Line1:       req.Line1,
			Line2:       line2,
			City:        req.City,
			State:       req.State,
			PostalCode:  req.PostalCode,
			CountryCode: req.Country,
		})
		if txErr != nil {
			return fmt.Errorf("create address: %w", txErr)
		}
		resp.AddressID = addr.ID.String()

		return nil
	})
	if err != nil {
		logger.Error("checkout address", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save address"})
		return
	}

	JSON(w, http.StatusOK, resp)
}

// handleCheckoutPaymentIntent creates a Stripe PaymentIntent for the cart total.
func (d *Deps) handleCheckoutPaymentIntent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	var req checkoutPaymentIntentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	cartID, err := uuid.Parse(req.CartID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid cart_id"})
		return
	}
	addressID, err := uuid.Parse(req.AddressID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid address_id"})
		return
	}
	customerID, err := uuid.Parse(req.CustomerID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid customer_id"})
		return
	}

	var subtotal int
	var shippingAddr *domain.Address

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		items, txErr := d.CartService.ListItems(ctx, tx, cartID)
		if txErr != nil {
			return fmt.Errorf("list cart items: %w", txErr)
		}
		if len(items) == 0 {
			return app.ErrCartEmpty
		}

		for _, ci := range items {
			subtotal += ci.UnitPrice * ci.Quantity
		}

		shippingAddr, txErr = d.CustomerService.GetAddressByID(ctx, tx, addressID)
		if txErr != nil {
			return fmt.Errorf("get address: %w", txErr)
		}

		return nil
	})
	if err != nil {
		logger.Error("checkout payment-intent", "error", err)
		if errors.Is(err, app.ErrCartEmpty) {
			JSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "cart is empty"})
			return
		}
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to prepare payment"})
		return
	}

	// Shipping: $0 for now
	totalCents := subtotal

	// Create Stripe PaymentIntent (external call — outside transaction)
	pi, err := d.PaymentProvider.CreatePaymentIntent(ctx, payments.CreatePaymentIntentRequest{
		AmountCents: int64(totalCents),
		Currency:    "usd",
		Metadata: map[string]string{
			"cart_id":     cartID.String(),
			"customer_id": customerID.String(),
			"address_id":  addressID.String(),
		},
		ShippingAddress: &payments.ShippingAddress{
			Name:       shippingAddr.FirstName + " " + shippingAddr.LastName,
			Line1:      shippingAddr.Line1,
			Line2:      ptrToString(shippingAddr.Line2),
			City:       shippingAddr.City,
			State:      shippingAddr.State,
			PostalCode: shippingAddr.PostalCode,
			Country:    shippingAddr.CountryCode,
		},
	})
	if err != nil {
		logger.Error("create payment intent", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create payment"})
		return
	}

	JSON(w, http.StatusOK, checkoutPaymentIntentResponse{
		ClientSecret: pi.ClientSecret,
		Amount:       totalCents,
		Currency:     "usd",
	})
}

// handleCheckoutConfirm finalizes the order after Stripe payment succeeds.
func (d *Deps) handleCheckoutConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	var req checkoutConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	cartID, err := uuid.Parse(req.CartID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid cart_id"})
		return
	}
	customerID, err := uuid.Parse(req.CustomerID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid customer_id"})
		return
	}
	addressID, err := uuid.Parse(req.AddressID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid address_id"})
		return
	}

	// Verify the payment intent succeeded (external call — outside transaction)
	pi, err := d.PaymentProvider.GetPaymentIntent(ctx, req.PaymentIntentID)
	if err != nil {
		logger.Error("get payment intent", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify payment"})
		return
	}
	if pi.Status != payments.PaymentIntentStatusSucceeded {
		JSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "payment has not succeeded"})
		return
	}

	var resp checkoutConfirmResponse

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		items, txErr := d.CartService.ListItems(ctx, tx, cartID)
		if txErr != nil {
			return fmt.Errorf("list cart items: %w", txErr)
		}
		if len(items) == 0 {
			return app.ErrCartEmpty
		}

		orderItems := make([]app.CartItem, len(items))
		for i, ci := range items {
			orderItems[i] = app.CartItem{
				VariantID: ci.VariantID,
				Quantity:  ci.Quantity,
				UnitPrice: ci.UnitPrice,
			}
		}

		order, txErr := d.CheckoutService.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:        customerID,
			Items:             orderItems,
			ShippingAddressID: addressID,
			BillingAddressID:  addressID,
			CurrencyCode:      "USD",
			ShippingCents:     0,
			TaxCents:          0, // TODO: pull from Stripe automatic tax
		}, app.Actor{
			Type: "customer",
			ID:   &customerID,
			Name: "guest checkout",
		})
		if txErr != nil {
			return fmt.Errorf("place order: %w", txErr)
		}

		_, txErr = d.OrderService.UpdateStripePaymentIntentID(ctx, tx, order.ID, req.PaymentIntentID)
		if txErr != nil {
			return fmt.Errorf("update stripe payment intent: %w", txErr)
		}

		_, txErr = d.OrderService.UpdatePaymentStatus(ctx, tx, order.ID, domain.PaymentStatusCaptured, app.Actor{
			Type: "system",
			Name: "checkout",
		})
		if txErr != nil {
			return fmt.Errorf("update payment status: %w", txErr)
		}

		txErr = d.CartService.DeleteCart(ctx, tx, cartID)
		if txErr != nil {
			return fmt.Errorf("delete cart: %w", txErr)
		}

		resp.OrderID = order.ID.String()
		resp.OrderNumber = order.Number

		return nil
	})
	if err != nil {
		logger.Error("checkout confirm", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create order"})
		return
	}

	// Clear cart cookie
	http.SetCookie(w, &http.Cookie{
		Name:   cartCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	JSON(w, http.StatusOK, resp)
}

func ptrToString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
