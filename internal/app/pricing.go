package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

// customerPricingReader is the slice of CustomerStore that PricingService needs
// to resolve customer-aware prices. *store.CustomerStore satisfies it.
type customerPricingReader interface {
	GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Customer, error)
}

// settingsPricingReader is the slice of SettingsStore that PricingService needs
// to look up the store-wide default wholesale price list. *store.SettingsStore
// satisfies it. Optional — when unset, the default-list fallback is skipped and
// unassigned customers resolve to base prices.
type settingsPricingReader interface {
	GetDefaultWholesalePriceListID(ctx context.Context, tx pgx.Tx) (*uuid.UUID, error)
}

// PricingService contains business logic for pricing.
type PricingService struct {
	pricing   *store.PricingStore
	customers customerPricingReader
	settings  settingsPricingReader
}

// NewPricingService creates a new PricingService.
func NewPricingService(pricing *store.PricingStore, customers customerPricingReader) *PricingService {
	return &PricingService{pricing: pricing, customers: customers}
}

// WithSettings enables the default-wholesale-price-list fallback. When wired, a
// wholesale customer with no explicitly-assigned price list resolves against the
// store's default list (if configured) instead of base prices.
func (s *PricingService) WithSettings(settings settingsPricingReader) *PricingService {
	s.settings = settings
	return s
}

// effectivePriceListID returns the price list a customer's prices should resolve
// against: their explicitly-assigned list if any, otherwise the store-wide
// default wholesale list (wholesale accounts only). A nil result means "no list —
// use base prices". Missing entries on whichever list is chosen still fall back
// to base prices at the call site.
func (s *PricingService) effectivePriceListID(ctx context.Context, tx pgx.Tx, customer *domain.Customer) (*uuid.UUID, error) {
	if customer.PriceListID != nil {
		return customer.PriceListID, nil
	}
	if s.settings == nil || customer.AccountType != domain.AccountTypeWholesale {
		return nil, nil
	}
	defaultID, err := s.settings.GetDefaultWholesalePriceListID(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("get default wholesale price list: %w", err)
	}
	return defaultID, nil
}

// GetBasePrice returns the base price for a variant in a given currency.
func (s *PricingService) GetBasePrice(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, currencyCode string) (*domain.Price, error) {
	price, err := s.pricing.GetBasePrice(ctx, tx, variantID, currencyCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPriceNotFound
		}
		return nil, fmt.Errorf("get base price: %w", err)
	}
	return price, nil
}

// SetBasePrice sets the base price (in cents) for a variant.
// Creates a price set if one doesn't exist for the variant.
func (s *PricingService) SetBasePrice(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, amountCents int, currencyCode string) (*domain.Price, error) {
	if amountCents < 0 {
		return nil, ErrInvalidPrice
	}

	ps, err := s.pricing.GetOrCreatePriceSet(ctx, tx, variantID)
	if err != nil {
		return nil, fmt.Errorf("get or create price set: %w", err)
	}

	price, err := s.pricing.SetBasePrice(ctx, tx, ps.ID, amountCents, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("set base price: %w", err)
	}
	return price, nil
}

// GetOrCreatePriceSet returns the price set for a variant, creating one if needed.
func (s *PricingService) GetOrCreatePriceSet(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) (*domain.PriceSet, error) {
	ps, err := s.pricing.GetOrCreatePriceSet(ctx, tx, variantID)
	if err != nil {
		return nil, fmt.Errorf("get or create price set: %w", err)
	}
	return ps, nil
}

// ListBasePricesByProduct returns base prices for all variants of a product, keyed by variant ID.
func (s *PricingService) ListBasePricesByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID, currencyCode string) (map[uuid.UUID]int, error) {
	prices, err := s.pricing.ListBasePricesByProduct(ctx, tx, productID, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("list base prices: %w", err)
	}
	return prices, nil
}

// SetGroupPrice sets the group price (in cents) for a variant + customer group.
func (s *PricingService) SetGroupPrice(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, customerGroupID uuid.UUID, amountCents int, currencyCode string) (*domain.Price, error) {
	if amountCents < 0 {
		return nil, ErrInvalidPrice
	}

	ps, err := s.pricing.GetOrCreatePriceSet(ctx, tx, variantID)
	if err != nil {
		return nil, fmt.Errorf("get or create price set: %w", err)
	}

	price, err := s.pricing.SetGroupPrice(ctx, tx, ps.ID, customerGroupID, amountCents, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("set group price: %w", err)
	}
	return price, nil
}

// DeleteGroupPrice removes the group price for a variant + customer group.
func (s *PricingService) DeleteGroupPrice(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, customerGroupID uuid.UUID, currencyCode string) error {
	ps, err := s.pricing.GetOrCreatePriceSet(ctx, tx, variantID)
	if err != nil {
		return fmt.Errorf("get price set: %w", err)
	}

	if err := s.pricing.DeleteGroupPrice(ctx, tx, ps.ID, customerGroupID, currencyCode); err != nil {
		return fmt.Errorf("delete group price: %w", err)
	}
	return nil
}

// ListGroupPricesByProduct returns group prices for all variants of a product,
// keyed by variant ID then customer group ID.
func (s *PricingService) ListGroupPricesByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID, currencyCode string) (map[uuid.UUID]map[uuid.UUID]int, error) {
	prices, err := s.pricing.ListGroupPricesByProduct(ctx, tx, productID, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("list group prices: %w", err)
	}
	return prices, nil
}

// SetPriceListPrice sets the price-list price (in cents) for a variant + price list.
func (s *PricingService) SetPriceListPrice(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, priceListID uuid.UUID, amountCents int, currencyCode string) (*domain.Price, error) {
	if amountCents < 0 {
		return nil, ErrInvalidPrice
	}

	ps, err := s.pricing.GetOrCreatePriceSet(ctx, tx, variantID)
	if err != nil {
		return nil, fmt.Errorf("get or create price set: %w", err)
	}

	price, err := s.pricing.SetPriceListPrice(ctx, tx, ps.ID, priceListID, amountCents, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("set price list price: %w", err)
	}
	return price, nil
}

// DeletePriceListPrice removes the price-list price for a variant + price list.
func (s *PricingService) DeletePriceListPrice(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, priceListID uuid.UUID, currencyCode string) error {
	ps, err := s.pricing.GetOrCreatePriceSet(ctx, tx, variantID)
	if err != nil {
		return fmt.Errorf("get price set: %w", err)
	}

	if err := s.pricing.DeletePriceListPrice(ctx, tx, ps.ID, priceListID, currencyCode); err != nil {
		return fmt.Errorf("delete price list price: %w", err)
	}
	return nil
}

// ListPriceListPricesByProduct returns price-list prices for all variants of a product,
// keyed by variant ID then price list ID.
func (s *PricingService) ListPriceListPricesByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID, currencyCode string) (map[uuid.UUID]map[uuid.UUID]int, error) {
	prices, err := s.pricing.ListPriceListPricesByProduct(ctx, tx, productID, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("list price list prices: %w", err)
	}
	return prices, nil
}

// ResolveForCustomer returns the effective price (in cents) for a variant given a customer.
// The customer's effective price list (explicit assignment, else the store's default
// wholesale list) is consulted first; missing entries fall back to the base price.
// Returns ErrCustomerNotFound or ErrPriceNotFound on miss.
func (s *PricingService) ResolveForCustomer(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, customerID uuid.UUID, currencyCode string) (int64, error) {
	customer, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrCustomerNotFound
		}
		return 0, fmt.Errorf("get customer for pricing: %w", err)
	}

	listID, err := s.effectivePriceListID(ctx, tx, customer)
	if err != nil {
		return 0, err
	}

	if listID != nil {
		price, err := s.pricing.GetPriceListPrice(ctx, tx, variantID, *listID, currencyCode)
		if err == nil {
			return int64(price.Amount), nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("get price list price: %w", err)
		}
	}

	base, err := s.pricing.GetBasePrice(ctx, tx, variantID, currencyCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrPriceNotFound
		}
		return 0, fmt.Errorf("get base price: %w", err)
	}
	return int64(base.Amount), nil
}

// ResolveForCustomerBatch returns effective prices for the given variants keyed by variant ID.
// Missing entries on the customer's effective price list (explicit assignment, else the
// store's default wholesale list) fall back to the base price; variants with no base price
// are simply omitted from the returned map (consumer reads zero-value).
func (s *PricingService) ResolveForCustomerBatch(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, variantIDs []uuid.UUID, currencyCode string) (map[uuid.UUID]int, error) {
	customer, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer for pricing: %w", err)
	}

	if len(variantIDs) == 0 {
		return map[uuid.UUID]int{}, nil
	}

	listID, err := s.effectivePriceListID(ctx, tx, customer)
	if err != nil {
		return nil, err
	}

	if listID == nil {
		basePrices, err := s.pricing.ListBasePricesByVariants(ctx, tx, variantIDs, currencyCode)
		if err != nil {
			return nil, fmt.Errorf("list base prices: %w", err)
		}
		return basePrices, nil
	}

	listPrices, err := s.pricing.ListPriceListPricesByVariants(ctx, tx, variantIDs, *listID, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("list price list prices: %w", err)
	}

	missing := make([]uuid.UUID, 0, len(variantIDs))
	for _, id := range variantIDs {
		if _, ok := listPrices[id]; !ok {
			missing = append(missing, id)
		}
	}

	if len(missing) == 0 {
		return listPrices, nil
	}

	basePrices, err := s.pricing.ListBasePricesByVariants(ctx, tx, missing, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("list base prices for fallback: %w", err)
	}

	merged := make(map[uuid.UUID]int, len(listPrices)+len(basePrices))
	for id, p := range basePrices {
		merged[id] = p
	}
	for id, p := range listPrices {
		merged[id] = p
	}
	return merged, nil
}
