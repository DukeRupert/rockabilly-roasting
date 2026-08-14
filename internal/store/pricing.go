package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// PricingStore provides database access for prices and price sets.
type PricingStore struct{}

// NewPricingStore creates a new PricingStore.
func NewPricingStore() *PricingStore {
	return &PricingStore{}
}

// GetBasePrice returns the base price for a variant in a given currency.
// Base price = no price list, no customer group, no min quantity.
func (s *PricingStore) GetBasePrice(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, currencyCode string) (*domain.Price, error) {
	row, err := sqlcgen.New(tx).GetBasePrice(ctx, sqlcgen.GetBasePriceParams{
		VariantID:    variantID,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return nil, fmt.Errorf("get base price for variant %s: %w", variantID, err)
	}
	return priceFromRow(row), nil
}

// GetOrCreatePriceSet returns the price set for a variant, creating one if it doesn't exist.
func (s *PricingStore) GetOrCreatePriceSet(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) (*domain.PriceSet, error) {
	row, err := sqlcgen.New(tx).GetPriceSetByVariant(ctx, variantID)
	if err == nil {
		return &domain.PriceSet{ID: row.ID, VariantID: row.VariantID}, nil
	}
	// Create if not found
	row, err = sqlcgen.New(tx).CreatePriceSet(ctx, sqlcgen.CreatePriceSetParams{
		ID:        uuid.New(),
		VariantID: variantID,
	})
	if err != nil {
		return nil, fmt.Errorf("create price set for variant %s: %w", variantID, err)
	}
	return &domain.PriceSet{ID: row.ID, VariantID: row.VariantID}, nil
}

// SetBasePrice deletes any existing base price and inserts a new one.
func (s *PricingStore) SetBasePrice(ctx context.Context, tx pgx.Tx, priceSetID uuid.UUID, amount int, currencyCode string) (*domain.Price, error) {
	q := sqlcgen.New(tx)

	// Delete existing base price for this price set + currency
	if err := q.DeleteBasePrice(ctx, sqlcgen.DeleteBasePriceParams{
		PriceSetID:   priceSetID,
		CurrencyCode: currencyCode,
	}); err != nil {
		return nil, fmt.Errorf("delete existing base price: %w", err)
	}

	// Insert new price
	row, err := q.UpsertBasePrice(ctx, sqlcgen.UpsertBasePriceParams{
		ID:           uuid.New(),
		PriceSetID:   priceSetID,
		Amount:       int32(amount),
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return nil, fmt.Errorf("insert base price: %w", err)
	}
	return priceFromRow(row), nil
}

// ListBasePricesByProduct returns base prices for all variants of a product, keyed by variant ID.
func (s *PricingStore) ListBasePricesByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID, currencyCode string) (map[uuid.UUID]int, error) {
	rows, err := sqlcgen.New(tx).ListBasePricesByProduct(ctx, sqlcgen.ListBasePricesByProductParams{
		ProductID:    productID,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return nil, fmt.Errorf("list base prices for product %s: %w", productID, err)
	}
	prices := make(map[uuid.UUID]int, len(rows))
	for _, r := range rows {
		prices[r.VariantID] = int(r.Amount)
	}
	return prices, nil
}

// GetPriceListPrice returns the price-list price for a variant+price-list+currency.
func (s *PricingStore) GetPriceListPrice(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, priceListID uuid.UUID, currencyCode string) (*domain.Price, error) {
	row, err := sqlcgen.New(tx).GetPriceListPrice(ctx, sqlcgen.GetPriceListPriceParams{
		VariantID:    variantID,
		CurrencyCode: currencyCode,
		PriceListID:  &priceListID,
	})
	if err != nil {
		return nil, fmt.Errorf("get price list price for variant %s list %s: %w", variantID, priceListID, err)
	}
	return priceFromRow(row), nil
}

// ListBasePricesByVariants returns base prices for the given variants, keyed by variant ID.
// Variants without a base price are omitted from the map.
func (s *PricingStore) ListBasePricesByVariants(ctx context.Context, tx pgx.Tx, variantIDs []uuid.UUID, currencyCode string) (map[uuid.UUID]int, error) {
	rows, err := sqlcgen.New(tx).ListBasePricesByVariants(ctx, sqlcgen.ListBasePricesByVariantsParams{
		Column1:      variantIDs,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return nil, fmt.Errorf("list base prices by variants: %w", err)
	}
	prices := make(map[uuid.UUID]int, len(rows))
	for _, r := range rows {
		prices[r.VariantID] = int(r.Amount)
	}
	return prices, nil
}

// ListPriceListPricesByVariants returns price-list prices for the given variants, keyed by variant ID.
// Variants without an entry on the list are omitted from the map.
func (s *PricingStore) ListPriceListPricesByVariants(ctx context.Context, tx pgx.Tx, variantIDs []uuid.UUID, priceListID uuid.UUID, currencyCode string) (map[uuid.UUID]int, error) {
	rows, err := sqlcgen.New(tx).ListPriceListPricesByVariants(ctx, sqlcgen.ListPriceListPricesByVariantsParams{
		Column1:      variantIDs,
		CurrencyCode: currencyCode,
		PriceListID:  &priceListID,
	})
	if err != nil {
		return nil, fmt.Errorf("list price list prices by variants for list %s: %w", priceListID, err)
	}
	prices := make(map[uuid.UUID]int, len(rows))
	for _, r := range rows {
		prices[r.VariantID] = int(r.Amount)
	}
	return prices, nil
}

// SetPriceListPrice deletes any existing price-list price for the (price set, list, currency)
// triple and inserts a new one.
func (s *PricingStore) SetPriceListPrice(ctx context.Context, tx pgx.Tx, priceSetID uuid.UUID, priceListID uuid.UUID, amount int, currencyCode string) (*domain.Price, error) {
	q := sqlcgen.New(tx)

	if err := q.DeletePriceListPrice(ctx, sqlcgen.DeletePriceListPriceParams{
		PriceSetID:   priceSetID,
		CurrencyCode: currencyCode,
		PriceListID:  &priceListID,
	}); err != nil {
		return nil, fmt.Errorf("delete existing price list price: %w", err)
	}

	row, err := q.UpsertPriceListPrice(ctx, sqlcgen.UpsertPriceListPriceParams{
		ID:           uuid.New(),
		PriceSetID:   priceSetID,
		Amount:       int32(amount),
		CurrencyCode: currencyCode,
		PriceListID:  &priceListID,
	})
	if err != nil {
		return nil, fmt.Errorf("insert price list price: %w", err)
	}
	return priceFromRow(row), nil
}

// DeletePriceListPrice deletes the price-list price for the (price set, list, currency) triple.
func (s *PricingStore) DeletePriceListPrice(ctx context.Context, tx pgx.Tx, priceSetID uuid.UUID, priceListID uuid.UUID, currencyCode string) error {
	return sqlcgen.New(tx).DeletePriceListPrice(ctx, sqlcgen.DeletePriceListPriceParams{
		PriceSetID:   priceSetID,
		CurrencyCode: currencyCode,
		PriceListID:  &priceListID,
	})
}

// ListPriceListPricesByProduct returns price-list prices for all variants of a product,
// keyed by variant ID then price list ID.
func (s *PricingStore) ListPriceListPricesByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID, currencyCode string) (map[uuid.UUID]map[uuid.UUID]int, error) {
	rows, err := sqlcgen.New(tx).ListPriceListPricesByProduct(ctx, sqlcgen.ListPriceListPricesByProductParams{
		ProductID:    productID,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return nil, fmt.Errorf("list price list prices for product %s: %w", productID, err)
	}
	prices := make(map[uuid.UUID]map[uuid.UUID]int)
	for _, r := range rows {
		if r.PriceListID == nil {
			continue
		}
		if prices[r.VariantID] == nil {
			prices[r.VariantID] = make(map[uuid.UUID]int)
		}
		prices[r.VariantID][*r.PriceListID] = int(r.Amount)
	}
	return prices, nil
}

// GetTierLadder returns the full volume price ladder for a variant on a price
// list — base rung plus any quantity breaks. An empty ladder means the variant
// has no entry on that list at all; callers fall back to the base price.
func (s *PricingStore) GetTierLadder(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, priceListID uuid.UUID, currencyCode string) (domain.TierLadder, error) {
	rows, err := sqlcgen.New(tx).GetPriceListLadder(ctx, sqlcgen.GetPriceListLadderParams{
		VariantID:    variantID,
		CurrencyCode: currencyCode,
		PriceListID:  &priceListID,
	})
	if err != nil {
		return domain.TierLadder{}, fmt.Errorf("get tier ladder for variant %s list %s: %w", variantID, priceListID, err)
	}
	tiers := make([]domain.PriceTier, 0, len(rows))
	for _, r := range rows {
		tiers = append(tiers, tierFromRow(r.Amount, r.MinQuantity))
	}
	return domain.NewTierLadder(tiers), nil
}

// ListTierLaddersByVariants returns volume price ladders for the given variants
// on a price list, keyed by variant ID. Variants with no entry on the list are
// omitted from the map rather than mapped to an empty ladder, matching the
// omit-on-miss behavior of the other batch readers.
func (s *PricingStore) ListTierLaddersByVariants(ctx context.Context, tx pgx.Tx, variantIDs []uuid.UUID, priceListID uuid.UUID, currencyCode string) (map[uuid.UUID]domain.TierLadder, error) {
	rows, err := sqlcgen.New(tx).ListPriceListLaddersByVariants(ctx, sqlcgen.ListPriceListLaddersByVariantsParams{
		Column1:      variantIDs,
		CurrencyCode: currencyCode,
		PriceListID:  &priceListID,
	})
	if err != nil {
		return nil, fmt.Errorf("list tier ladders by variants for list %s: %w", priceListID, err)
	}
	byVariant := make(map[uuid.UUID][]domain.PriceTier)
	for _, r := range rows {
		byVariant[r.VariantID] = append(byVariant[r.VariantID], tierFromRow(r.Amount, r.MinQuantity))
	}
	ladders := make(map[uuid.UUID]domain.TierLadder, len(byVariant))
	for id, tiers := range byVariant {
		ladders[id] = domain.NewTierLadder(tiers)
	}
	return ladders, nil
}

// ListTierLaddersByProduct returns volume price ladders for every variant of a
// product across all price lists, keyed by variant ID then price list ID. Backs
// the admin price-list editor, which shows a whole product's ladders at once.
func (s *PricingStore) ListTierLaddersByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID, currencyCode string) (map[uuid.UUID]map[uuid.UUID]domain.TierLadder, error) {
	rows, err := sqlcgen.New(tx).ListPriceListLaddersByProduct(ctx, sqlcgen.ListPriceListLaddersByProductParams{
		ProductID:    productID,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return nil, fmt.Errorf("list tier ladders for product %s: %w", productID, err)
	}
	tiers := make(map[uuid.UUID]map[uuid.UUID][]domain.PriceTier)
	for _, r := range rows {
		if r.PriceListID == nil {
			continue
		}
		if tiers[r.VariantID] == nil {
			tiers[r.VariantID] = make(map[uuid.UUID][]domain.PriceTier)
		}
		tiers[r.VariantID][*r.PriceListID] = append(tiers[r.VariantID][*r.PriceListID], tierFromRow(r.Amount, r.MinQuantity))
	}
	ladders := make(map[uuid.UUID]map[uuid.UUID]domain.TierLadder, len(tiers))
	for variantID, byList := range tiers {
		ladders[variantID] = make(map[uuid.UUID]domain.TierLadder, len(byList))
		for listID, ts := range byList {
			ladders[variantID][listID] = domain.NewTierLadder(ts)
		}
	}
	return ladders, nil
}

// SetTierPrice creates or updates one rung of a ladder. minQuantity is the
// threshold at which the rung takes effect and must be 2 or greater — the base
// rung is written through SetPriceListPrice, which stores it as min_quantity
// NULL. Rewriting an existing threshold replaces its amount.
func (s *PricingStore) SetTierPrice(ctx context.Context, tx pgx.Tx, priceSetID uuid.UUID, priceListID uuid.UUID, minQuantity, amount int, currencyCode string) (*domain.Price, error) {
	minQty := int32(minQuantity)
	row, err := sqlcgen.New(tx).UpsertTierPrice(ctx, sqlcgen.UpsertTierPriceParams{
		ID:           uuid.New(),
		PriceSetID:   priceSetID,
		Amount:       int32(amount),
		CurrencyCode: currencyCode,
		PriceListID:  &priceListID,
		MinQuantity:  &minQty,
	})
	if err != nil {
		return nil, fmt.Errorf("upsert tier price at qty %d for list %s: %w", minQuantity, priceListID, err)
	}
	return priceFromRow(row), nil
}

// DeleteTierPrice removes one rung from a ladder, leaving the rest intact.
func (s *PricingStore) DeleteTierPrice(ctx context.Context, tx pgx.Tx, priceSetID uuid.UUID, priceListID uuid.UUID, minQuantity int, currencyCode string) error {
	minQty := int32(minQuantity)
	return sqlcgen.New(tx).DeleteTierPrice(ctx, sqlcgen.DeleteTierPriceParams{
		PriceSetID:   priceSetID,
		CurrencyCode: currencyCode,
		PriceListID:  &priceListID,
		MinQuantity:  &minQty,
	})
}

// DeleteTierPricesForList removes every rung above the base for a variant on a
// price list, leaving the base price alone. Callers removing a variant from a
// list entirely must call this alongside DeletePriceListPrice — deleting only
// the base would strand the breaks as a ladder with no floor.
func (s *PricingStore) DeleteTierPricesForList(ctx context.Context, tx pgx.Tx, priceSetID uuid.UUID, priceListID uuid.UUID, currencyCode string) error {
	return sqlcgen.New(tx).DeleteTierPricesForList(ctx, sqlcgen.DeleteTierPricesForListParams{
		PriceSetID:   priceSetID,
		CurrencyCode: currencyCode,
		PriceListID:  &priceListID,
	})
}

// tierFromRow maps a price row onto a ladder rung. A NULL min_quantity is the
// base rung, which domain.NewTierLadder canonicalizes to a threshold of 1.
func tierFromRow(amount int32, minQuantity *int32) domain.PriceTier {
	t := domain.PriceTier{Amount: int(amount)}
	if minQuantity != nil {
		t.MinQuantity = int(*minQuantity)
	}
	return t
}

func priceFromRow(r sqlcgen.Price) *domain.Price {
	return &domain.Price{
		ID:           r.ID,
		PriceSetID:   r.PriceSetID,
		Amount:       int(r.Amount),
		CurrencyCode: r.CurrencyCode,
		MinQuantity:  int32PtrToIntPtr(r.MinQuantity),
		MaxQuantity:  int32PtrToIntPtr(r.MaxQuantity),
		PriceListID:  r.PriceListID,
		StartsAt:     timestampFromPG(r.StartsAt),
		EndsAt:       timestampFromPG(r.EndsAt),
	}
}
