package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newCheckoutService() *app.CheckoutService {
	return app.NewCheckoutService(
		store.NewOrderStore(),
		store.NewCustomerStore(),
		store.NewDiscountStore(),
		store.NewSettingsStore(),
		store.NewShippingStore(),
		nil, // payment provider not needed for unit tests
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	)
}

func TestCheckoutService_PlaceOrder(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCheckoutService()
	ctx := context.Background()
	actor := testutil.TestActor()

	customer := testutil.CreateCustomer(t, tx)
	shipping := testutil.CreateAddress(t, tx, customer.ID)
	billing := testutil.CreateAddress(t, tx, customer.ID)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)

	order, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
		CustomerID:        customer.ID,
		ShippingAddressID: shipping.ID,
		BillingAddressID:  billing.ID,
		CurrencyCode:      "USD",
		Items: []app.CartItem{
			{VariantID: variant.ID, Quantity: 2, UnitPrice: 1500},
		},
		ShippingCents: 500,
		TaxCents:      240,
	}, actor)
	require.NoError(t, err)

	assert.Equal(t, 3000, order.Subtotal)
	assert.Equal(t, 0, order.DiscountTotal)
	assert.Equal(t, 500, order.ShippingTotal)
	assert.Equal(t, 240, order.TaxTotal)
	assert.Equal(t, 3740, order.Total)
	assert.Equal(t, domain.OrderStatusPending, order.Status)

	entry := testutil.LastAuditEntry(t, tx, "order", order.ID)
	assert.Equal(t, audit.AuditOrderCreated, entry.Action)
}

func TestCheckoutService_PlaceOrder_EmptyCart(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCheckoutService()
	ctx := context.Background()
	actor := testutil.TestActor()

	_, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
		CustomerID:   uuid.New(),
		CurrencyCode: "USD",
		Items:        []app.CartItem{},
	}, actor)
	assert.ErrorIs(t, err, app.ErrCartEmpty)
}

func TestCheckoutService_PlaceOrder_BadCustomer(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCheckoutService()
	ctx := context.Background()
	actor := testutil.TestActor()

	_, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
		CustomerID:   uuid.New(),
		CurrencyCode: "USD",
		Items:        []app.CartItem{{VariantID: uuid.New(), Quantity: 1, UnitPrice: 1000}},
	}, actor)
	assert.ErrorIs(t, err, app.ErrCustomerNotFound)
}

func TestCheckoutService_PlaceOrder_WithCoupon(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCheckoutService()
	ctx := context.Background()
	actor := testutil.TestActor()

	customer := testutil.CreateCustomer(t, tx)
	shipping := testutil.CreateAddress(t, tx, customer.ID)
	billing := testutil.CreateAddress(t, tx, customer.ID)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)

	discount := testutil.CreateDiscount(t, tx,
		testutil.WithDiscountType(domain.DiscountTypePercentage),
		testutil.WithDiscountValue(20),
	)
	coupon := testutil.CreateCouponCode(t, tx, discount.ID, testutil.WithCouponCode("SAVE20"))

	code := coupon.Code
	order, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
		CustomerID:        customer.ID,
		ShippingAddressID: shipping.ID,
		BillingAddressID:  billing.ID,
		CurrencyCode:      "USD",
		Items: []app.CartItem{
			{VariantID: variant.ID, Quantity: 1, UnitPrice: 10000},
		},
		CouponCode: &code,
	}, actor)
	require.NoError(t, err)

	assert.Equal(t, 10000, order.Subtotal)
	assert.Equal(t, 2000, order.DiscountTotal) // 20% of 10000
	assert.Equal(t, 8000, order.Total)         // 10000 - 2000
}

func TestCheckoutService_PlaceOrder_PercentageDiscount(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCheckoutService()
	ctx := context.Background()
	actor := testutil.TestActor()

	customer := testutil.CreateCustomer(t, tx)
	shipping := testutil.CreateAddress(t, tx, customer.ID)
	billing := testutil.CreateAddress(t, tx, customer.ID)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)

	discount := testutil.CreateDiscount(t, tx,
		testutil.WithDiscountType(domain.DiscountTypePercentage),
		testutil.WithDiscountValue(15),
	)
	coupon := testutil.CreateCouponCode(t, tx, discount.ID)

	code := coupon.Code
	order, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
		CustomerID:        customer.ID,
		ShippingAddressID: shipping.ID,
		BillingAddressID:  billing.ID,
		CurrencyCode:      "USD",
		Items: []app.CartItem{
			{VariantID: variant.ID, Quantity: 2, UnitPrice: 2000},
		},
		CouponCode: &code,
	}, actor)
	require.NoError(t, err)

	assert.Equal(t, 4000, order.Subtotal)
	assert.Equal(t, 600, order.DiscountTotal) // 15% of 4000
	assert.Equal(t, 3400, order.Total)
}

func TestCheckoutService_PlaceOrder_FixedDiscount(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCheckoutService()
	ctx := context.Background()
	actor := testutil.TestActor()

	customer := testutil.CreateCustomer(t, tx)
	shipping := testutil.CreateAddress(t, tx, customer.ID)
	billing := testutil.CreateAddress(t, tx, customer.ID)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)

	// Fixed discount larger than subtotal should cap at subtotal.
	discount := testutil.CreateDiscount(t, tx,
		testutil.WithDiscountType(domain.DiscountTypeFixedAmount),
		testutil.WithDiscountValue(99999),
	)
	coupon := testutil.CreateCouponCode(t, tx, discount.ID)

	code := coupon.Code
	order, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
		CustomerID:        customer.ID,
		ShippingAddressID: shipping.ID,
		BillingAddressID:  billing.ID,
		CurrencyCode:      "USD",
		Items: []app.CartItem{
			{VariantID: variant.ID, Quantity: 1, UnitPrice: 5000},
		},
		CouponCode: &code,
	}, actor)
	require.NoError(t, err)

	assert.Equal(t, 5000, order.Subtotal)
	assert.Equal(t, 5000, order.DiscountTotal) // capped at subtotal
	assert.Equal(t, 0, order.Total)
}

func TestCheckoutService_PlaceOrder_CouponErrors(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	svc := newCheckoutService()
	actor := testutil.TestActor()

	t.Run("coupon not found", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx)
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)

		code := "NONEXISTENT"
		_, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:   customer.ID,
			CurrencyCode: "USD",
			Items:        []app.CartItem{{VariantID: variant.ID, Quantity: 1, UnitPrice: 1000}},
			CouponCode:   &code,
		}, actor)
		assert.ErrorIs(t, err, app.ErrCouponNotFound)
	})

	t.Run("coupon already used", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx)
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)

		discount := testutil.CreateDiscount(t, tx)
		coupon := testutil.CreateCouponCode(t, tx, discount.ID)

		// Mark as redeemed.
		store.NewDiscountStore().MarkCouponCodeRedeemed(ctx, tx, coupon.ID, &customer.ID)

		code := coupon.Code
		_, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:   customer.ID,
			CurrencyCode: "USD",
			Items:        []app.CartItem{{VariantID: variant.ID, Quantity: 1, UnitPrice: 1000}},
			CouponCode:   &code,
		}, actor)
		assert.ErrorIs(t, err, app.ErrCouponAlreadyUsed)
	})

	t.Run("discount inactive", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx)
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)

		discount := testutil.CreateDiscount(t, tx, testutil.WithDiscountActive(false))
		coupon := testutil.CreateCouponCode(t, tx, discount.ID)

		code := coupon.Code
		_, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:   customer.ID,
			CurrencyCode: "USD",
			Items:        []app.CartItem{{VariantID: variant.ID, Quantity: 1, UnitPrice: 1000}},
			CouponCode:   &code,
		}, actor)
		assert.ErrorIs(t, err, app.ErrDiscountNotActive)
	})

	t.Run("discount expired", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx)
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)

		past := time.Now().Add(-24 * time.Hour)
		discount := testutil.CreateDiscount(t, tx, testutil.WithDiscountExpiry(past))
		coupon := testutil.CreateCouponCode(t, tx, discount.ID)

		code := coupon.Code
		_, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:   customer.ID,
			CurrencyCode: "USD",
			Items:        []app.CartItem{{VariantID: variant.ID, Quantity: 1, UnitPrice: 1000}},
			CouponCode:   &code,
		}, actor)
		assert.ErrorIs(t, err, app.ErrDiscountExpired)
	})

	t.Run("minimum order not met", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx)
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)

		discount := testutil.CreateDiscount(t, tx, testutil.WithMinimumOrder(50000))
		coupon := testutil.CreateCouponCode(t, tx, discount.ID)

		code := coupon.Code
		_, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:   customer.ID,
			CurrencyCode: "USD",
			Items:        []app.CartItem{{VariantID: variant.ID, Quantity: 1, UnitPrice: 1000}},
			CouponCode:   &code,
		}, actor)
		assert.ErrorIs(t, err, app.ErrMinimumOrderNotMet)
	})
}

func TestCheckoutService_ApplyCoupon(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	svc := newCheckoutService()

	t.Run("valid coupon returns discount", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		discount := testutil.CreateDiscount(t, tx)
		coupon := testutil.CreateCouponCode(t, tx, discount.ID)

		got, err := svc.ApplyCoupon(ctx, tx, coupon.Code)
		require.NoError(t, err)
		assert.Equal(t, discount.ID, got.ID)
	})

	t.Run("expired discount errors", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		past := time.Now().Add(-24 * time.Hour)
		discount := testutil.CreateDiscount(t, tx, testutil.WithDiscountExpiry(past))
		coupon := testutil.CreateCouponCode(t, tx, discount.ID)

		_, err := svc.ApplyCoupon(ctx, tx, coupon.Code)
		assert.ErrorIs(t, err, app.ErrDiscountExpired)
	})

	t.Run("redeemed coupon errors", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		discount := testutil.CreateDiscount(t, tx)
		coupon := testutil.CreateCouponCode(t, tx, discount.ID)
		customer := testutil.CreateCustomer(t, tx)

		store.NewDiscountStore().MarkCouponCodeRedeemed(ctx, tx, coupon.ID, &customer.ID)

		_, err := svc.ApplyCoupon(ctx, tx, coupon.Code)
		assert.ErrorIs(t, err, app.ErrCouponAlreadyUsed)
	})
}
