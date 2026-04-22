package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

// CartService contains business logic for shopping carts.
type CartService struct {
	carts   *store.CartStore
	pricing *store.PricingStore
}

// NewCartService creates a new CartService.
func NewCartService(carts *store.CartStore, pricing *store.PricingStore) *CartService {
	return &CartService{carts: carts, pricing: pricing}
}

// GetCart returns a cart by ID.
func (s *CartService) GetCart(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Cart, error) {
	cart, err := s.carts.GetCartByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCartNotFound
		}
		return nil, fmt.Errorf("get cart: %w", err)
	}
	return cart, nil
}

// GetOrCreateCart returns the cart for the given ID, or creates a new one.
func (s *CartService) GetOrCreateCart(ctx context.Context, tx pgx.Tx, cartID *uuid.UUID) (*domain.Cart, error) {
	if cartID != nil {
		cart, err := s.carts.GetCartByIDAsStaff(ctx, tx, *cartID)
		if err == nil {
			return cart, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get cart: %w", err)
		}
		// Cart not found — fall through to create
	}

	expiresAt := time.Now().Add(30 * 24 * time.Hour) // 30 days
	cart, err := s.carts.CreateCart(ctx, tx, "USD", &expiresAt)
	if err != nil {
		return nil, fmt.Errorf("create cart: %w", err)
	}
	return cart, nil
}

// AddItem adds a variant to the cart at its current base price.
func (s *CartService) AddItem(ctx context.Context, tx pgx.Tx, cartID, variantID uuid.UUID, quantity int) (*domain.CartItem, error) {
	if quantity <= 0 {
		return nil, ErrInvalidQuantity
	}

	// Resolve current base price for the variant
	price, err := s.pricing.GetBasePrice(ctx, tx, variantID, "USD")
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPriceNotFound
		}
		return nil, fmt.Errorf("get price for variant: %w", err)
	}

	item, err := s.carts.UpsertCartItem(ctx, tx, cartID, variantID, quantity, price.Amount)
	if err != nil {
		return nil, fmt.Errorf("add item to cart: %w", err)
	}
	return item, nil
}

// UpdateItemQuantity sets the quantity of a cart item.
// cartID is used for ownership verification — the item must belong to this cart.
func (s *CartService) UpdateItemQuantity(ctx context.Context, tx pgx.Tx, cartID, itemID uuid.UUID, quantity int) (*domain.CartItem, error) {
	if quantity <= 0 {
		return nil, ErrInvalidQuantity
	}
	item, err := s.carts.SetCartItemQuantity(ctx, tx, itemID, quantity)
	if err != nil {
		return nil, fmt.Errorf("update cart item quantity: %w", err)
	}
	if item.CartID != cartID {
		return nil, ErrCartNotFound
	}
	return item, nil
}

// RemoveItem removes an item from the cart.
// cartID is used for ownership verification — the item must belong to this cart.
func (s *CartService) RemoveItem(ctx context.Context, tx pgx.Tx, cartID, itemID uuid.UUID) error {
	// Verify item belongs to this cart by listing items and checking.
	items, err := s.carts.ListCartItems(ctx, tx, cartID)
	if err != nil {
		return fmt.Errorf("list cart items for ownership check: %w", err)
	}
	found := false
	for _, item := range items {
		if item.ID == itemID {
			found = true
			break
		}
	}
	if !found {
		return ErrCartNotFound
	}
	if err := s.carts.DeleteCartItem(ctx, tx, itemID); err != nil {
		return fmt.Errorf("remove cart item: %w", err)
	}
	return nil
}

// ListItems returns all items in a cart.
func (s *CartService) ListItems(ctx context.Context, tx pgx.Tx, cartID uuid.UUID) ([]domain.CartItem, error) {
	items, err := s.carts.ListCartItems(ctx, tx, cartID)
	if err != nil {
		return nil, fmt.Errorf("list cart items: %w", err)
	}
	return items, nil
}

// DeleteCart removes a cart and all its items.
func (s *CartService) DeleteCart(ctx context.Context, tx pgx.Tx, cartID uuid.UUID) error {
	if err := s.carts.DeleteCart(ctx, tx, cartID); err != nil {
		return fmt.Errorf("delete cart: %w", err)
	}
	return nil
}

// GetItemCount returns total quantity of items in a cart.
func (s *CartService) GetItemCount(ctx context.Context, tx pgx.Tx, cartID uuid.UUID) (int, error) {
	count, err := s.carts.GetCartItemCount(ctx, tx, cartID)
	if err != nil {
		return 0, fmt.Errorf("get cart item count: %w", err)
	}
	return count, nil
}
