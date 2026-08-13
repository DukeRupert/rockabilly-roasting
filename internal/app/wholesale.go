package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// WholesaleService contains business logic for wholesale customer management and ordering.
type WholesaleService struct {
	customers      *store.CustomerStore
	customerUsers  *store.CustomerUserStore
	customerGroups *store.CustomerGroupStore
	catalog        *store.CatalogStore
	orders         *store.OrderStore
	carts          *store.CartStore
	audit          *audit.AuditWriter
	metrics        *metrics.Registry
	auth           *AuthService // populated via WithEmail; used to mint setup tokens for SendApprovalEmail
	email          EmailEnv     // populated via WithEmail; required for Send* methods
	// unsubscribe signs the opt-out links in the weekly reminder. Never nil —
	// an unconfigured signer reports Enabled() false and the link is omitted.
	unsubscribe *auth.UnsubscribeSigner

	// shipping + merchantTZ resolve the local-delivery run a wholesale order
	// rides. Both are optional: without them a wholesale local-delivery order
	// is placed with no promised date, exactly as before.
	shipping   *store.ShippingStore
	merchantTZ *time.Location
}

// WithDeliverySchedule attaches what PlaceWholesaleOrder needs to stamp a
// local-delivery date on the order.
func (s *WholesaleService) WithDeliverySchedule(shipping *store.ShippingStore, loc *time.Location) *WholesaleService {
	s.shipping = shipping
	s.merchantTZ = loc
	return s
}

// NewWholesaleService creates a new WholesaleService.
func NewWholesaleService(
	customers *store.CustomerStore,
	customerUsers *store.CustomerUserStore,
	customerGroups *store.CustomerGroupStore,
	catalog *store.CatalogStore,
	orders *store.OrderStore,
	carts *store.CartStore,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *WholesaleService {
	return &WholesaleService{
		unsubscribe:    auth.NewUnsubscribeSigner(""),
		customers:      customers,
		customerUsers:  customerUsers,
		customerGroups: customerGroups,
		catalog:        catalog,
		orders:         orders,
		carts:          carts,
		audit:          audit,
		metrics:        metrics,
	}
}

// WithEmail attaches email-send environment and the AuthService used to mint
// password-setup tokens. Must be called before any of the Send* methods.
func (s *WholesaleService) WithEmail(env EmailEnv, authSvc *AuthService) *WholesaleService {
	s.email = env
	s.auth = authSvc
	return s
}

// WithUnsubscribeSigner attaches the signer used for reminder opt-out links.
// Without it the reminder still sends, minus the link.
func (s *WholesaleService) WithUnsubscribeSigner(signer *auth.UnsubscribeSigner) *WholesaleService {
	s.unsubscribe = signer
	return s
}

// --- Application management ---

// ApplyParams holds the fields needed for a wholesale application.
type ApplyParams struct {
	Email       string
	FirstName   string
	LastName    string
	Phone       *string
	CompanyName string
	Website     *string
}

// SubmitApplication creates a new wholesale customer application.
func (s *WholesaleService) SubmitApplication(ctx context.Context, tx pgx.Tx, p ApplyParams) (*domain.Customer, error) {
	// Normalize before the duplicate probe, so an applicant who capitalizes
	// their address cannot slip a second account past the existing-email check.
	p.Email = domain.NormalizeEmail(p.Email)

	// Check for existing account.
	existing, err := s.customers.GetByEmail(ctx, tx, p.Email)
	if err == nil && existing != nil {
		return nil, ErrEmailAlreadyExists
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check existing email: %w", err)
	}

	c, err := s.customers.Create(ctx, tx, store.CreateCustomerParams{
		Email:     p.Email,
		FirstName: p.FirstName,
		LastName:  p.LastName,
		Phone:     p.Phone,
	})
	if err != nil {
		return nil, fmt.Errorf("create wholesale customer: %w", err)
	}

	// Promote the customer to pending wholesale + record company details.
	if err := s.customers.SetWholesaleApplicationFields(ctx, tx, c.ID, p.CompanyName, p.Website); err != nil {
		return nil, fmt.Errorf("set wholesale fields: %w", err)
	}

	// Re-fetch to get the updated record.
	c, err = s.customers.GetByID(ctx, tx, c.ID)
	if err != nil {
		return nil, fmt.Errorf("refetch customer: %w", err)
	}

	return c, nil
}

// ApproveApplication approves a pending wholesale application.
func (s *WholesaleService) ApproveApplication(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, actor Actor) (*domain.Customer, error) {
	customer, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer: %w", err)
	}

	if customer.AccountType != domain.AccountTypeWholesale {
		return nil, ErrWholesaleNotPending
	}
	if customer.WholesaleStatus == nil || *customer.WholesaleStatus != domain.WholesaleStatusPending {
		return nil, ErrWholesaleNotPending
	}

	if err := s.customers.SetWholesaleApproved(ctx, tx, customerID, actor.ID, time.Now()); err != nil {
		return nil, fmt.Errorf("approve wholesale: %w", err)
	}

	updated, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		return nil, fmt.Errorf("refetch customer: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditWholesaleApplicationApproved,
		ResourceType: "customer",
		ResourceID:   customerID,
		After:        updated,
	}); err != nil {
		return nil, fmt.Errorf("audit wholesale approved: %w", err)
	}

	return updated, nil
}

// DeclineApplication declines a pending wholesale application with notes.
func (s *WholesaleService) DeclineApplication(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, notes string, actor Actor) error {
	customer, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCustomerNotFound
		}
		return fmt.Errorf("get customer: %w", err)
	}

	if customer.AccountType != domain.AccountTypeWholesale {
		return ErrWholesaleNotPending
	}
	if customer.WholesaleStatus == nil || *customer.WholesaleStatus != domain.WholesaleStatusPending {
		return ErrWholesaleNotPending
	}

	if err := s.customers.SetWholesaleDeclined(ctx, tx, customerID, notes); err != nil {
		return fmt.Errorf("decline wholesale: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditWholesaleApplicationDeclined,
		ResourceType: "customer",
		ResourceID:   customerID,
		Metadata:     map[string]any{"notes": notes},
	}); err != nil {
		return fmt.Errorf("audit wholesale declined: %w", err)
	}

	return nil
}

// SuspendAccount suspends an approved wholesale account.
func (s *WholesaleService) SuspendAccount(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, notes string, actor Actor) (*domain.Customer, error) {
	customer, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer: %w", err)
	}

	if customer.AccountType != domain.AccountTypeWholesale {
		return nil, ErrWholesaleNotApproved
	}
	if customer.WholesaleStatus == nil || *customer.WholesaleStatus != domain.WholesaleStatusApproved {
		return nil, ErrWholesaleNotApproved
	}

	if err := s.customers.SetWholesaleSuspended(ctx, tx, customerID, notes); err != nil {
		return nil, fmt.Errorf("suspend wholesale: %w", err)
	}

	updated, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		return nil, fmt.Errorf("refetch customer: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditWholesaleAccountSuspended,
		ResourceType: "customer",
		ResourceID:   customerID,
		After:        updated,
		Metadata:     map[string]any{"notes": notes},
	}); err != nil {
		return nil, fmt.Errorf("audit wholesale suspended: %w", err)
	}

	return updated, nil
}

// ReactivateAccount reactivates a suspended wholesale account.
func (s *WholesaleService) ReactivateAccount(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, actor Actor) (*domain.Customer, error) {
	customer, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer: %w", err)
	}

	if customer.AccountType != domain.AccountTypeWholesale {
		return nil, ErrWholesaleNotApproved
	}
	if customer.WholesaleStatus == nil || *customer.WholesaleStatus != domain.WholesaleStatusSuspended {
		return nil, ErrWholesaleNotApproved
	}

	if err := s.customers.SetWholesaleReactivated(ctx, tx, customerID); err != nil {
		return nil, fmt.Errorf("reactivate wholesale: %w", err)
	}

	updated, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		return nil, fmt.Errorf("refetch customer: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditWholesaleAccountReactivated,
		ResourceType: "customer",
		ResourceID:   customerID,
		After:        updated,
	}); err != nil {
		return nil, fmt.Errorf("audit wholesale reactivated: %w", err)
	}

	return updated, nil
}

// --- Quick order ---

// QuickOrderVariant represents a single variant row in the quick order table.
type QuickOrderVariant struct {
	ID           uuid.UUID
	SKU          string
	OptionValues []string // Values matching the product's option columns
	UnitPrice    int      // Cents
	MinQty       *int
	Multiple     *int
}

// QuickOrderProduct represents a product with all its variants for the quick order page.
type QuickOrderProduct struct {
	ID       uuid.UUID
	Title    string
	ImageURL string
	Options  []string // Option column headers (e.g., "Size", "Grind")
	Variants []QuickOrderVariant
}

// QuickOrderCatalog returns products grouped with their variants, options, and prices
// for the wholesale quick order page. Pricing reflects the customer's assigned price
// list; visibility is filtered to the wholesale tier and any restricted products
// granted to one of the customer's groups.
func (s *WholesaleService) QuickOrderCatalog(
	ctx context.Context,
	tx pgx.Tx,
	groupIDs []uuid.UUID,
	customerID uuid.UUID,
	pricing *PricingService,
	currencyCode string,
) ([]QuickOrderProduct, error) {
	products, err := s.catalog.ListProducts(ctx, tx, store.ProductFilter{
		Status: ptrTo(domain.ProductStatusActive),
		Visibility: &store.VisibilityContext{
			IsWholesale: true,
			GroupIDs:    groupIDs,
			// Include 'private' white-label products granted to this customer.
			CustomerID: &customerID,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}

	// Alphabetical, not created_at: buyers scan the same order sheet week after
	// week, so its row order must not reshuffle when a new product launches.
	sort.Slice(products, func(i, j int) bool {
		return strings.ToLower(products[i].Title) < strings.ToLower(products[j].Title)
	})

	type productCtx struct {
		product  domain.Product
		variants []domain.Variant
		options  []domain.ProductOption
		imageURL string
	}

	productCtxs := make([]productCtx, 0, len(products))
	allVariantIDs := make([]uuid.UUID, 0)
	for _, p := range products {
		variants, err := s.catalog.ListActiveVariantsByProduct(ctx, tx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("list variants for %s: %w", p.ID, err)
		}
		// Hide variants not available on the wholesale channel (e.g. 1lb retail-only).
		// A product whose variants are all wholesale-hidden drops out entirely.
		variants = domain.FilterVariantsForChannel(variants, domain.ChannelWholesale)
		if len(variants) == 0 {
			continue
		}

		options, err := s.catalog.ListProductOptions(ctx, tx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("list options for %s: %w", p.ID, err)
		}

		var imageURL string
		media, err := s.catalog.ListProductMedia(ctx, tx, p.ID)
		if err == nil && len(media) > 0 {
			imageURL = media[0].R2Key
		}

		for _, v := range variants {
			allVariantIDs = append(allVariantIDs, v.ID)
		}
		productCtxs = append(productCtxs, productCtx{
			product:  p,
			variants: variants,
			options:  options,
			imageURL: imageURL,
		})
	}

	priceMap, err := pricing.ResolveForCustomerBatch(ctx, tx, customerID, allVariantIDs, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("resolve prices for wholesale catalog: %w", err)
	}

	result := make([]QuickOrderProduct, 0, len(productCtxs))
	for _, pc := range productCtxs {
		optionNames := make([]string, len(pc.options))
		for i, opt := range pc.options {
			optionNames[i] = opt.Name
		}

		qVariants := make([]QuickOrderVariant, 0, len(pc.variants))
		for _, v := range pc.variants {
			vovs, err := s.catalog.ListVariantOptionValues(ctx, tx, v.ID)
			if err != nil {
				return nil, fmt.Errorf("list variant option values for %s: %w", v.ID, err)
			}
			vovMap := make(map[uuid.UUID]bool, len(vovs))
			for _, vov := range vovs {
				vovMap[vov.ProductOptionValueID] = true
			}

			optValues := make([]string, len(pc.options))
			for i, opt := range pc.options {
				vals, _ := s.catalog.ListProductOptionValues(ctx, tx, opt.ID)
				for _, val := range vals {
					if vovMap[val.ID] {
						optValues[i] = val.Value
						break
					}
				}
			}

			qVariants = append(qVariants, QuickOrderVariant{
				ID:           v.ID,
				SKU:          v.SKU,
				OptionValues: optValues,
				UnitPrice:    priceMap[v.ID],
				MinQty:       v.WholesaleMinQty,
				Multiple:     v.WholesaleMultiple,
			})
		}

		result = append(result, QuickOrderProduct{
			ID:       pc.product.ID,
			Title:    pc.product.Title,
			ImageURL: pc.imageURL,
			Options:  optionNames,
			Variants: qVariants,
		})
	}

	return result, nil
}

// --- Wholesale checkout ---

// PlaceWholesaleOrderParams holds the input for a wholesale order.
type PlaceWholesaleOrderParams struct {
	CustomerID        uuid.UUID
	Items             []CartItem
	ShippingAddressID uuid.UUID
	BillingAddressID  uuid.UUID
	CurrencyCode      string
	CustomerPONumber  *string
	ShippingCents     int
	TaxCents          int
	// ShippingMethod records how the buyer chose to receive the order
	// (local delivery, pickup, or free shipping). Nil leaves it unset.
	ShippingMethod *domain.ShippingMethod
	Notes          *string
	Metadata       map[string]any
}

// PlaceWholesaleOrder creates an order with payment_status = pending_invoice (no Stripe).
func (s *WholesaleService) PlaceWholesaleOrder(ctx context.Context, tx pgx.Tx, p PlaceWholesaleOrderParams, actor Actor) (*domain.Order, error) {
	if len(p.Items) == 0 {
		return nil, ErrCartEmpty
	}

	// Verify customer is an approved wholesale account.
	customer, err := s.customers.GetByID(ctx, tx, p.CustomerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer: %w", err)
	}
	if customer.AccountType != domain.AccountTypeWholesale ||
		customer.WholesaleStatus == nil ||
		*customer.WholesaleStatus != domain.WholesaleStatusApproved {
		return nil, ErrWholesaleNotApproved
	}

	// Validate MOQ constraints.
	variantIDs := make([]uuid.UUID, len(p.Items))
	for i, item := range p.Items {
		variantIDs[i] = item.VariantID
	}
	variants, err := s.fetchVariants(ctx, tx, variantIDs)
	if err != nil {
		return nil, err
	}

	cartItems := make([]domain.CartItem, len(p.Items))
	for i, item := range p.Items {
		cartItems[i] = domain.CartItem{
			VariantID: item.VariantID,
			Quantity:  item.Quantity,
		}
	}
	violations := domain.ValidateWholesaleCart(cartItems, variants)
	if len(violations) > 0 {
		return nil, &MOQViolationError{Violations: violations}
	}

	// Calculate totals.
	subtotal := 0
	for _, item := range p.Items {
		subtotal += item.UnitPrice * item.Quantity
	}
	total := subtotal + p.ShippingCents + p.TaxCents

	orderNumber := fmt.Sprintf("WO-%d", time.Now().UnixMilli())
	customerID := p.CustomerID

	placedAt := time.Now()
	scheduledDelivery := scheduleLocalDelivery(ctx, tx, s.shipping, p.ShippingMethod, placedAt, s.merchantTZ)

	order, err := s.orders.CreateOrder(ctx, tx, store.CreateOrderParams{
		Number:                orderNumber,
		CustomerID:            &customerID,
		Channel:               domain.OrderChannelWholesale,
		Status:                domain.OrderStatusConfirmed,
		PaymentStatus:         domain.PaymentStatusPendingInvoice,
		FulfillmentStatus:     domain.FulfillmentStatusUnfulfilled,
		CurrencyCode:          p.CurrencyCode,
		Subtotal:              subtotal,
		ShippingTotal:         p.ShippingCents,
		TaxTotal:              p.TaxCents,
		Total:                 total,
		ShippingAddressID:     p.ShippingAddressID,
		BillingAddressID:      p.BillingAddressID,
		ShippingMethod:        p.ShippingMethod,
		ScheduledDeliveryDate: scheduledDelivery,
		Notes:                 p.Notes,
		Metadata:              p.Metadata,
		PlacedAt:              placedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create wholesale order: %w", err)
	}

	// Set wholesale-specific fields.
	if p.CustomerPONumber != nil {
		if err := s.orders.SetCustomerPONumber(ctx, tx, order.ID, *p.CustomerPONumber); err != nil {
			return nil, fmt.Errorf("set PO number: %w", err)
		}
	}

	// Create line items.
	for _, item := range p.Items {
		lineSubtotal := item.UnitPrice * item.Quantity
		_, err := s.orders.CreateLineItem(ctx, tx, store.CreateLineItemParams{
			OrderID:   order.ID,
			VariantID: item.VariantID,
			Quantity:  item.Quantity,
			UnitPrice: item.UnitPrice,
			Subtotal:  lineSubtotal,
			Total:     lineSubtotal,
		})
		if err != nil {
			return nil, fmt.Errorf("create line item: %w", err)
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderCreated,
		ResourceType: "order",
		ResourceID:   order.ID,
		After:        order,
		Metadata:     map[string]any{"wholesale": true, "po_number": p.CustomerPONumber},
	}); err != nil {
		return nil, fmt.Errorf("audit wholesale order: %w", err)
	}

	return order, nil
}

// fetchVariants loads variant records for the given IDs.
func (s *WholesaleService) fetchVariants(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) ([]domain.Variant, error) {
	variants := make([]domain.Variant, 0, len(ids))
	for _, id := range ids {
		v, err := s.catalog.GetVariantByID(ctx, tx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrVariantNotFound
			}
			return nil, fmt.Errorf("get variant %s: %w", id, err)
		}
		variants = append(variants, *v)
	}
	return variants, nil
}

func ptrTo[T any](v T) *T {
	return &v
}
