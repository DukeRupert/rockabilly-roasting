package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// CartStore provides database access for shopping carts and cart items.
type CartStore struct{}

// NewCartStore creates a new CartStore.
func NewCartStore() *CartStore {
	return &CartStore{}
}

// CreateCart creates a new cart for an anonymous session (no customer).
func (s *CartStore) CreateCart(ctx context.Context, tx pgx.Tx, currencyCode string, expiresAt *time.Time) (*domain.Cart, error) {
	row, err := sqlcgen.New(tx).CreateCartForSession(ctx, sqlcgen.CreateCartForSessionParams{
		ID:           uuid.New(),
		CurrencyCode: currencyCode,
		ExpiresAt:    timestampToPG(expiresAt),
	})
	if err != nil {
		return nil, fmt.Errorf("create cart: %w", err)
	}
	return cartFromRow(row), nil
}

// GetCartByIDAsStaff returns a cart by ID.
func (s *CartStore) GetCartByIDAsStaff(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Cart, error) {
	row, err := sqlcgen.New(tx).GetCartByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get cart %s: %w", id, err)
	}
	return cartFromRow(row), nil
}

// DeleteCart removes a cart and all its items (CASCADE).
func (s *CartStore) DeleteCart(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteCart(ctx, id); err != nil {
		return fmt.Errorf("delete cart: %w", err)
	}
	return nil
}

// UpsertCartItem adds a variant to the cart or increments its quantity.
func (s *CartStore) UpsertCartItem(ctx context.Context, tx pgx.Tx, cartID, variantID uuid.UUID, quantity, unitPriceCents int) (*domain.CartItem, error) {
	row, err := sqlcgen.New(tx).UpsertCartItem(ctx, sqlcgen.UpsertCartItemParams{
		ID:        uuid.New(),
		CartID:    cartID,
		VariantID: variantID,
		Quantity:  int32(quantity),
		UnitPrice: int32(unitPriceCents),
	})
	if err != nil {
		return nil, fmt.Errorf("upsert cart item: %w", err)
	}
	return cartItemFromRow(row), nil
}

// SetCartItemByVariant inserts or replaces a cart line for a variant, setting
// quantity and unit price to exactly the given values (unlike UpsertCartItem,
// which increments quantity and keeps the original price).
func (s *CartStore) SetCartItemByVariant(ctx context.Context, tx pgx.Tx, cartID, variantID uuid.UUID, quantity, unitPriceCents int) (*domain.CartItem, error) {
	row, err := sqlcgen.New(tx).SetCartItemByVariant(ctx, sqlcgen.SetCartItemByVariantParams{
		ID:        uuid.New(),
		CartID:    cartID,
		VariantID: variantID,
		Quantity:  int32(quantity),
		UnitPrice: int32(unitPriceCents),
	})
	if err != nil {
		return nil, fmt.Errorf("set cart item by variant: %w", err)
	}
	return cartItemFromRow(row), nil
}

// SetCartItemQuantity updates the quantity of a cart item.
func (s *CartStore) SetCartItemQuantity(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, quantity int) (*domain.CartItem, error) {
	row, err := sqlcgen.New(tx).SetCartItemQuantity(ctx, sqlcgen.SetCartItemQuantityParams{
		ID:       itemID,
		Quantity: int32(quantity),
	})
	if err != nil {
		return nil, fmt.Errorf("set cart item quantity: %w", err)
	}
	return cartItemFromRow(row), nil
}

// DeleteCartItem removes a cart item by ID.
func (s *CartStore) DeleteCartItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteCartItem(ctx, itemID); err != nil {
		return fmt.Errorf("delete cart item: %w", err)
	}
	return nil
}

// RemoveCartItemByVariant removes a variant from a cart.
func (s *CartStore) RemoveCartItemByVariant(ctx context.Context, tx pgx.Tx, cartID, variantID uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteCartItemByCartAndVariant(ctx, sqlcgen.DeleteCartItemByCartAndVariantParams{
		CartID:    cartID,
		VariantID: variantID,
	}); err != nil {
		return fmt.Errorf("remove cart item by variant: %w", err)
	}
	return nil
}

// ListCartItems returns all items in a cart.
func (s *CartStore) ListCartItems(ctx context.Context, tx pgx.Tx, cartID uuid.UUID) ([]domain.CartItem, error) {
	rows, err := sqlcgen.New(tx).ListCartItems(ctx, cartID)
	if err != nil {
		return nil, fmt.Errorf("list cart items: %w", err)
	}
	items := make([]domain.CartItem, len(rows))
	for i, r := range rows {
		items[i] = *cartItemFromRow(r)
	}
	return items, nil
}

// GetCartItemCount returns the total number of items (sum of quantities) in a cart.
func (s *CartStore) GetCartItemCount(ctx context.Context, tx pgx.Tx, cartID uuid.UUID) (int, error) {
	count, err := sqlcgen.New(tx).GetCartItemCount(ctx, cartID)
	if err != nil {
		return 0, fmt.Errorf("get cart item count: %w", err)
	}
	return int(count), nil
}

func cartItemFromRow(r sqlcgen.CartItem) *domain.CartItem {
	return &domain.CartItem{
		ID:        r.ID,
		CartID:    r.CartID,
		VariantID: r.VariantID,
		Quantity:  int(r.Quantity),
		UnitPrice: int(r.UnitPrice),
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// cartFromRow is defined in orders.go — this store reuses it via the same sqlcgen.Cart type.
