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

// DeletePriceListPrice removes a variant from a price list entirely — its base
// rung and every volume break above it.
//
// The breaks go too because a ladder with no floor is not a valid price: the
// variant would still resolve a price at high quantities while resolving nothing
// at low ones. Removing a variant from a list means removing it, not leaving
// half a ladder behind.
func (s *PricingService) DeletePriceListPrice(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, priceListID uuid.UUID, currencyCode string) error {
	ps, err := s.pricing.GetOrCreatePriceSet(ctx, tx, variantID)
	if err != nil {
		return fmt.Errorf("get price set: %w", err)
	}

	if err := s.pricing.DeleteTierPricesForList(ctx, tx, ps.ID, priceListID, currencyCode); err != nil {
		return fmt.Errorf("delete tier prices: %w", err)
	}

	if err := s.pricing.DeletePriceListPrice(ctx, tx, ps.ID, priceListID, currencyCode); err != nil {
		return fmt.Errorf("delete price list price: %w", err)
	}
	return nil
}

// SetTierPrice creates or updates one volume break on a variant's price-list
// ladder. minQuantity is the quantity at which the break takes effect and must
// be 2 or greater — the rung at quantity 1 is the list price itself, set through
// SetPriceListPrice.
//
// Re-authoring an existing threshold replaces its amount. A break priced at or
// above the rung below it is accepted and stored: staff may be mid-edit on a
// ladder, and rejecting the intermediate state would make some orderings of the
// same final ladder unreachable. It simply never produces a nudge.
func (s *PricingService) SetTierPrice(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, priceListID uuid.UUID, minQuantity, amountCents int, currencyCode string) error {
	if amountCents < 0 {
		return ErrInvalidPrice
	}
	if minQuantity < 2 {
		return ErrInvalidTierQuantity
	}

	ps, err := s.pricing.GetOrCreatePriceSet(ctx, tx, variantID)
	if err != nil {
		return fmt.Errorf("get or create price set: %w", err)
	}

	if _, err := s.pricing.SetTierPrice(ctx, tx, ps.ID, priceListID, minQuantity, amountCents, currencyCode); err != nil {
		return fmt.Errorf("set tier price: %w", err)
	}
	return nil
}

// DeleteTierPrice removes one volume break, leaving the rest of the ladder and
// the list price intact.
func (s *PricingService) DeleteTierPrice(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, priceListID uuid.UUID, minQuantity int, currencyCode string) error {
	if minQuantity < 2 {
		return ErrInvalidTierQuantity
	}

	ps, err := s.pricing.GetOrCreatePriceSet(ctx, tx, variantID)
	if err != nil {
		return fmt.Errorf("get price set: %w", err)
	}

	if err := s.pricing.DeleteTierPrice(ctx, tx, ps.ID, priceListID, minQuantity, currencyCode); err != nil {
		return fmt.Errorf("delete tier price: %w", err)
	}
	return nil
}

// ClearTierPrices removes every volume break for a variant on a price list,
// leaving the list price itself in place. This is the write behind replacing a
// whole ladder: the editor submits every break it wants to survive, so clearing
// first is what makes a removed row actually disappear.
func (s *PricingService) ClearTierPrices(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, priceListID uuid.UUID, currencyCode string) error {
	ps, err := s.pricing.GetOrCreatePriceSet(ctx, tx, variantID)
	if err != nil {
		return fmt.Errorf("get price set: %w", err)
	}

	if err := s.pricing.DeleteTierPricesForList(ctx, tx, ps.ID, priceListID, currencyCode); err != nil {
		return fmt.Errorf("clear tier prices: %w", err)
	}
	return nil
}

// ListTierLaddersByProduct returns the volume ladders for every variant of a
// product across all price lists, keyed by variant ID then price list ID. Backs
// the admin price-list editor.
func (s *PricingService) ListTierLaddersByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID, currencyCode string) (map[uuid.UUID]map[uuid.UUID]domain.TierLadder, error) {
	ladders, err := s.pricing.ListTierLaddersByProduct(ctx, tx, productID, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("list tier ladders: %w", err)
	}
	return ladders, nil
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

// ResolveForCustomer returns the effective price (in cents) for one unit of a
// variant, given a customer and the quantity being priced. The customer's
// effective price list (explicit assignment, else the store's default wholesale
// list) is consulted first; missing entries fall back to the base price.
//
// quantity selects the volume rung, so callers must pass the quantity the line
// will actually hold. Passing 1 to sidestep the parameter silently disables
// volume pricing for that line.
//
// Returns ErrCustomerNotFound or ErrPriceNotFound on miss.
func (s *PricingService) ResolveForCustomer(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, customerID uuid.UUID, quantity int, currencyCode string) (int64, error) {
	ladder, err := s.LadderForCustomer(ctx, tx, variantID, customerID, currencyCode)
	if err != nil {
		return 0, err
	}
	return int64(ladder.UnitPriceAt(quantity)), nil
}

// LadderForCustomer returns the volume price ladder a customer resolves against
// for a variant. Base prices are single-rung ladders, so a customer on no price
// list — or on a list that omits this variant — gets a ladder that prices every
// quantity identically. That equivalence is why there is no separate untiered
// path to keep in step with this one.
//
// Returns ErrCustomerNotFound or ErrPriceNotFound on miss; the returned ladder
// is never empty when err is nil.
func (s *PricingService) LadderForCustomer(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, customerID uuid.UUID, currencyCode string) (domain.TierLadder, error) {
	customer, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.TierLadder{}, ErrCustomerNotFound
		}
		return domain.TierLadder{}, fmt.Errorf("get customer for pricing: %w", err)
	}

	listID, err := s.effectivePriceListID(ctx, tx, customer)
	if err != nil {
		return domain.TierLadder{}, err
	}

	if listID != nil {
		ladder, err := s.pricing.GetTierLadder(ctx, tx, variantID, *listID, currencyCode)
		if err != nil {
			return domain.TierLadder{}, fmt.Errorf("get tier ladder: %w", err)
		}
		if !ladder.IsEmpty() {
			return ladder, nil
		}
	}

	base, err := s.pricing.GetBasePrice(ctx, tx, variantID, currencyCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.TierLadder{}, ErrPriceNotFound
		}
		return domain.TierLadder{}, fmt.Errorf("get base price: %w", err)
	}
	return domain.NewTierLadder([]domain.PriceTier{{MinQuantity: 1, Amount: base.Amount}}), nil
}

// ResolveForCustomerBatch returns effective unit prices for the given variants,
// keyed by variant ID, each priced at that variant's quantity in
// quantityByVariant. Variants with no price at all are omitted from the returned
// map (consumer reads zero-value), matching the prior behavior.
//
// Callers holding real quantities must pass them: a variant priced at the wrong
// quantity resolves to the wrong rung.
func (s *PricingService) ResolveForCustomerBatch(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, quantityByVariant map[uuid.UUID]int, currencyCode string) (map[uuid.UUID]int, error) {
	if len(quantityByVariant) == 0 {
		return map[uuid.UUID]int{}, nil
	}

	variantIDs := make([]uuid.UUID, 0, len(quantityByVariant))
	for id := range quantityByVariant {
		variantIDs = append(variantIDs, id)
	}

	ladders, err := s.LaddersForCustomerBatch(ctx, tx, customerID, variantIDs, currencyCode)
	if err != nil {
		return nil, err
	}

	prices := make(map[uuid.UUID]int, len(ladders))
	for id, ladder := range ladders {
		prices[id] = ladder.UnitPriceAt(quantityByVariant[id])
	}
	return prices, nil
}

// LaddersForCustomerBatch returns the volume price ladders the customer resolves
// against for the given variants, keyed by variant ID. Entries missing from the
// customer's effective price list fall back to a single-rung ladder built from
// the base price; variants with no price at all are omitted.
//
// This is what the wholesale order sheet reads: it renders a catalog before any
// quantity exists, so it needs the whole ladder rather than one resolved price.
func (s *PricingService) LaddersForCustomerBatch(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, variantIDs []uuid.UUID, currencyCode string) (map[uuid.UUID]domain.TierLadder, error) {
	customer, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer for pricing: %w", err)
	}

	if len(variantIDs) == 0 {
		return map[uuid.UUID]domain.TierLadder{}, nil
	}

	listID, err := s.effectivePriceListID(ctx, tx, customer)
	if err != nil {
		return nil, err
	}

	ladders := map[uuid.UUID]domain.TierLadder{}
	if listID != nil {
		ladders, err = s.pricing.ListTierLaddersByVariants(ctx, tx, variantIDs, *listID, currencyCode)
		if err != nil {
			return nil, fmt.Errorf("list tier ladders: %w", err)
		}
	}

	missing := make([]uuid.UUID, 0, len(variantIDs))
	for _, id := range variantIDs {
		if _, ok := ladders[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return ladders, nil
	}

	basePrices, err := s.pricing.ListBasePricesByVariants(ctx, tx, missing, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("list base prices for fallback: %w", err)
	}
	for id, amount := range basePrices {
		ladders[id] = domain.NewTierLadder([]domain.PriceTier{{MinQuantity: 1, Amount: amount}})
	}
	return ladders, nil
}

// AttachVariantLadders fills each search result's ListLadders so the manual-order
// typeahead can show what volume breaks exist.
//
// The typeahead deliberately does not resolve a customer's tiered price: it runs
// before the customer is known — the manual-order form's customer is an email
// that may be looked up or created on submit — and before any quantity is
// entered. Guessing a price from a half-typed email would be wrong silently.
// Showing the ladders lets staff pick the right figure themselves, which they
// already do routinely since the prefilled price is editable.
func (s *PricingService) AttachVariantLadders(ctx context.Context, tx pgx.Tx, results []domain.VariantSearchResult, currencyCode string) ([]domain.VariantSearchResult, error) {
	if len(results) == 0 {
		return results, nil
	}
	ids := make([]uuid.UUID, len(results))
	for i, r := range results {
		ids[i] = r.VariantID
	}
	ladders, err := s.pricing.ListLaddersByVariants(ctx, tx, ids, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("list ladders for variant search: %w", err)
	}
	for i, r := range results {
		for _, ll := range ladders[r.VariantID] {
			if ll.Ladder.IsTiered() {
				results[i].ListLadders = append(results[i].ListLadders, ll)
			}
		}
	}
	return results, nil
}
