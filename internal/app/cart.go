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

// productAccessChecker enforces product visibility at cart-write time. *CatalogService
// satisfies it. The cart owns the invariant ("you cannot add what you cannot access")
// but not the predicate — that lives in CatalogService so list, detail, and add-to-cart
// can never disagree.
type productAccessChecker interface {
	ResolveViewer(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (domain.ProductViewer, error)
	CanAccessVariant(ctx context.Context, tx pgx.Tx, v domain.ProductViewer, variantID uuid.UUID) (bool, error)
}

// CartService contains business logic for shopping carts.
type CartService struct {
	carts   *store.CartStore
	catalog *store.CatalogStore
	pricing *PricingService
	access  productAccessChecker
}

// NewCartService creates a new CartService.
func NewCartService(carts *store.CartStore, catalog *store.CatalogStore, pricing *PricingService, access productAccessChecker) *CartService {
	return &CartService{carts: carts, catalog: catalog, pricing: pricing, access: access}
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

	if err := s.assertVariantPurchasable(ctx, tx, variantID); err != nil {
		return nil, err
	}

	// Anonymous/retail add path: enforce against the public-only retail viewer so a
	// guessed variant ID for a wholesale/restricted product cannot be added.
	if err := s.assertVariantAccessible(ctx, tx, domain.ProductViewer{}, variantID); err != nil {
		return nil, err
	}
	if err := s.assertVariantInChannel(ctx, tx, variantID, domain.ChannelRetail); err != nil {
		return nil, err
	}

	price, err := s.pricing.GetBasePrice(ctx, tx, variantID, "USD")
	if err != nil {
		return nil, err
	}

	item, err := s.carts.UpsertCartItem(ctx, tx, cartID, variantID, quantity, price.Amount)
	if err != nil {
		return nil, fmt.Errorf("add item to cart: %w", err)
	}
	return item, nil
}

// AddItemForCustomer adds a variant to the cart at the customer's effective price.
// For wholesale customers with an assigned price list, list prices override base prices;
// otherwise it falls back to the base price.
func (s *CartService) AddItemForCustomer(ctx context.Context, tx pgx.Tx, cartID, variantID uuid.UUID, quantity int, customerID uuid.UUID, currencyCode string) (*domain.CartItem, error) {
	if quantity <= 0 {
		return nil, ErrInvalidQuantity
	}

	if err := s.assertVariantPurchasable(ctx, tx, variantID); err != nil {
		return nil, err
	}

	// Enforce product access against the customer's resolved viewer — this is the one
	// authoritative gate; handlers must not pre-check and must not bypass it.
	viewer, err := s.access.ResolveViewer(ctx, tx, customerID)
	if err != nil {
		return nil, err
	}
	if err := s.assertVariantAccessible(ctx, tx, viewer, variantID); err != nil {
		return nil, err
	}
	// Enforce the variant is sold on the viewer's channel (e.g. a 1lb variant marked
	// wholesale_available=false cannot be added by a wholesale customer).
	if err := s.assertVariantInChannel(ctx, tx, variantID, domain.ChannelFor(viewer)); err != nil {
		return nil, err
	}

	// Adding to a line that already exists must price the quantity the line will
	// end up holding, not the quantity being added — 12 added to 12 is 24 units
	// and earns the 24 rung. Resolving the delta would leave the buyer on the
	// rung for a quantity they no longer have.
	existing, err := s.lineQuantity(ctx, tx, cartID, variantID)
	if err != nil {
		return nil, err
	}
	resulting := existing + quantity

	price, err := s.pricing.ResolveForCustomer(ctx, tx, variantID, customerID, resulting, currencyCode)
	if err != nil {
		return nil, err
	}

	// Set rather than upsert: the resulting quantity is already computed above,
	// and UpsertCartItem would increment it a second time while keeping the
	// stale unit price.
	item, err := s.carts.SetCartItemByVariant(ctx, tx, cartID, variantID, resulting, int(price))
	if err != nil {
		return nil, fmt.Errorf("add item to cart: %w", err)
	}
	return item, nil
}

// lineQuantity returns the current quantity of a variant's cart line, or 0 when
// the cart has no line for it.
func (s *CartService) lineQuantity(ctx context.Context, tx pgx.Tx, cartID, variantID uuid.UUID) (int, error) {
	items, err := s.carts.ListCartItems(ctx, tx, cartID)
	if err != nil {
		return 0, fmt.Errorf("list cart items: %w", err)
	}
	for _, item := range items {
		if item.VariantID == variantID {
			return item.Quantity, nil
		}
	}
	return 0, nil
}

// SetItemForCustomer makes the cart line for a variant exactly quantity at the
// customer's current effective price — replacing any existing line rather than
// incrementing it. Quantity 0 removes the line (a no-op if absent) and returns
// nil. This is the write behind order-sheet style forms, where the submitted
// quantities are the whole truth, and behind price refreshes.
func (s *CartService) SetItemForCustomer(ctx context.Context, tx pgx.Tx, cartID, variantID uuid.UUID, quantity int, customerID uuid.UUID, currencyCode string) (*domain.CartItem, error) {
	if quantity < 0 {
		return nil, ErrInvalidQuantity
	}
	if quantity == 0 {
		if err := s.carts.RemoveCartItemByVariant(ctx, tx, cartID, variantID); err != nil {
			return nil, err
		}
		return nil, nil
	}

	if err := s.assertVariantPurchasable(ctx, tx, variantID); err != nil {
		return nil, err
	}
	viewer, err := s.access.ResolveViewer(ctx, tx, customerID)
	if err != nil {
		return nil, err
	}
	if err := s.assertVariantAccessible(ctx, tx, viewer, variantID); err != nil {
		return nil, err
	}
	if err := s.assertVariantInChannel(ctx, tx, variantID, domain.ChannelFor(viewer)); err != nil {
		return nil, err
	}

	// Set semantics: quantity is the whole truth for this line, so it is also
	// the quantity the volume rung is chosen by.
	price, err := s.pricing.ResolveForCustomer(ctx, tx, variantID, customerID, quantity, currencyCode)
	if err != nil {
		return nil, err
	}

	item, err := s.carts.SetCartItemByVariant(ctx, tx, cartID, variantID, quantity, int(price))
	if err != nil {
		return nil, fmt.Errorf("set cart item: %w", err)
	}
	return item, nil
}

// assertVariantPurchasable returns ErrVariantArchived if the variant is archived,
// or ErrVariantNotFound if it doesn't exist.
func (s *CartService) assertVariantPurchasable(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) error {
	v, err := s.catalog.GetVariantByID(ctx, tx, variantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVariantNotFound
		}
		return fmt.Errorf("check variant purchasable: %w", err)
	}
	if v.ArchivedAt != nil {
		return ErrVariantArchived
	}
	return nil
}

// assertVariantInChannel returns ErrVariantNotInChannel if the variant is not available
// on the given sales channel (e.g. a wholesale-hidden size added by a wholesale customer),
// or ErrVariantNotFound if it doesn't exist.
func (s *CartService) assertVariantInChannel(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, channel domain.SalesChannel) error {
	v, err := s.catalog.GetVariantByID(ctx, tx, variantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVariantNotFound
		}
		return fmt.Errorf("check variant channel: %w", err)
	}
	if !v.Available(channel) {
		return ErrVariantNotInChannel
	}
	return nil
}

// assertVariantAccessible returns ErrProductNotAccessible if the variant's product is
// not visible to the given viewer.
func (s *CartService) assertVariantAccessible(ctx context.Context, tx pgx.Tx, v domain.ProductViewer, variantID uuid.UUID) error {
	ok, err := s.access.CanAccessVariant(ctx, tx, v, variantID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrProductNotAccessible
	}
	return nil
}

// RepricedLine is a cart line whose unit price was rewritten, together with the
// volume rung it lost if the write moved it down one.
type RepricedLine struct {
	Item *domain.CartItem
	// Drop is non-nil only when a quantity reduction crossed below a volume
	// break and raised the unit price. It is advisory — the write has already
	// happened at the new price — and exists so a buyer learns their price moved
	// when they move it, rather than discovering it at checkout.
	Drop *domain.Drop
}

// UpdateItemQuantityForCustomer sets a cart line's quantity and reprices it at
// the customer's rung for that quantity. cartID is used for ownership
// verification — the item must belong to this cart.
//
// This is the customer-facing quantity control. UpdateItemQuantity, which does
// not reprice, remains only for the anonymous retail cart, where prices are
// always single-rung.
func (s *CartService) UpdateItemQuantityForCustomer(ctx context.Context, tx pgx.Tx, cartID, itemID uuid.UUID, quantity int, customerID uuid.UUID, currencyCode string) (RepricedLine, error) {
	if quantity <= 0 {
		return RepricedLine{}, ErrInvalidQuantity
	}

	items, err := s.carts.ListCartItems(ctx, tx, cartID)
	if err != nil {
		return RepricedLine{}, fmt.Errorf("list cart items: %w", err)
	}
	var current *domain.CartItem
	for i := range items {
		if items[i].ID == itemID {
			current = &items[i]
			break
		}
	}
	if current == nil {
		return RepricedLine{}, ErrCartNotFound
	}

	ladder, err := s.pricing.LadderForCustomer(ctx, tx, current.VariantID, customerID, currencyCode)
	if err != nil {
		return RepricedLine{}, err
	}

	// Read the drop off the same ladder that sets the price, so the notice and
	// the charge cannot disagree.
	var drop *domain.Drop
	if d, ok := ladder.Drop(current.Quantity, quantity); ok {
		drop = &d
	}

	updated, err := s.carts.SetCartItemByVariant(ctx, tx, cartID, current.VariantID, quantity, ladder.UnitPriceAt(quantity))
	if err != nil {
		return RepricedLine{}, fmt.Errorf("update cart item quantity: %w", err)
	}
	return RepricedLine{Item: updated, Drop: drop}, nil
}

// RepriceCart rewrites every line whose stored unit price no longer matches the
// customer's current price at that line's quantity, and returns only the lines
// that moved. Idempotent: a second call on an unchanged cart returns nothing.
//
// This is the guard against a cart that has sat while its price list changed
// underneath it. Returned lines carry no Drop — nothing about the buyer's
// quantities changed, only what those quantities cost.
func (s *CartService) RepriceCart(ctx context.Context, tx pgx.Tx, cartID, customerID uuid.UUID, currencyCode string) ([]RepricedLine, error) {
	items, err := s.carts.ListCartItems(ctx, tx, cartID)
	if err != nil {
		return nil, fmt.Errorf("list cart items: %w", err)
	}
	if len(items) == 0 {
		return nil, nil
	}

	quantities := make(map[uuid.UUID]int, len(items))
	for _, ci := range items {
		quantities[ci.VariantID] = ci.Quantity
	}
	fresh, err := s.pricing.ResolveForCustomerBatch(ctx, tx, customerID, quantities, currencyCode)
	if err != nil {
		return nil, err
	}

	var moved []RepricedLine
	for _, ci := range items {
		price, ok := fresh[ci.VariantID]
		if !ok || price == ci.UnitPrice {
			continue
		}
		updated, err := s.carts.SetCartItemByVariant(ctx, tx, cartID, ci.VariantID, ci.Quantity, price)
		if err != nil {
			return nil, fmt.Errorf("reprice cart item: %w", err)
		}
		moved = append(moved, RepricedLine{Item: updated})
	}
	return moved, nil
}

// UpdateItemQuantity sets the quantity of a cart item without repricing it.
//
// Retail only. Anonymous carts are priced from base prices, which are always
// single-rung, so quantity cannot change the unit price. A customer cart must
// use UpdateItemQuantityForCustomer instead — leaving the price untouched there
// would strand the line on the rung for a quantity it no longer holds.
//
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
