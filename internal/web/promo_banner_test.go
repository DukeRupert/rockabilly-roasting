package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/testutil"
	"github.com/dukerupert/hiri/internal/ui/layouts"
)

// The storefront's offer strip, end to end from the middleware to the
// rendered layout. Who sees which offer is pinned in internal/app; this pins
// that the layout gets it, for the shopper in the session.
//
// Commits, like everything driving a handler here — see setup_test.go.

// promotedDiscount commits a discount promoted with a line unique to this
// test, removed afterwards so no other page test sees it.
func promotedDiscount(t *testing.T, d *Deps, opts ...testutil.DiscountOption) (*domain.Discount, string) {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	disc := testutil.CreateDiscount(t, tx, opts...)
	text := "Banner " + uuid.NewString()[:8]
	require.NoError(t, d.DiscountService.SetBannerText(ctx, tx, disc.ID, text, testutil.TestActor()))
	require.NoError(t, tx.Commit(ctx))
	t.Cleanup(func() {
		_, err := testPool.Exec(context.Background(), `DELETE FROM discounts WHERE id = $1`, disc.ID)
		assert.NoError(t, err)
	})
	return disc, text
}

// renderStorefront serves a storefront page through withPromoBanners, as the
// customer in ctx (none for a guest), and returns the HTML.
func renderStorefront(t *testing.T, d *Deps, customer *domain.Customer) string {
	t.Helper()
	page := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, layouts.Storefront(layouts.StorefrontProps{Title: "t"}).Render(r.Context(), w))
	})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if customer != nil {
		r = r.WithContext(auth.WithCustomer(r.Context(), customer))
	}
	w := httptest.NewRecorder()
	d.withPromoBanners(page).ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	return w.Body.String()
}

func TestWithPromoBanners(t *testing.T) {
	d := newDiscountAdminDeps(t)

	t.Run("a live promoted discount appears above the header, linking to where it is used", func(t *testing.T) {
		_, text := promotedDiscount(t, d)
		page := renderStorefront(t, d, nil)

		strip := strings.Index(page, `aria-label="Current offers"`)
		require.GreaterOrEqual(t, strip, 0, "the strip is rendered")
		assert.Less(t, strip, strings.Index(page, "<header"), "above the sticky header")
		assert.Contains(t, page[strip:], `href="/catalog"`)
		assert.Contains(t, page[strip:], text)
	})

	t.Run("the shopper in the session decides who sees a members-only offer", func(t *testing.T) {
		_, text := promotedDiscount(t, d, testutil.WithEligibility(domain.DiscountEligibilitySubscribers))
		member := committedMember(t)

		assert.NotContains(t, renderStorefront(t, d, nil), text, "a guest")
		assert.Contains(t, renderStorefront(t, d, member), text, "a signed-in member")
	})

	t.Run("no strip at all when nothing is promoted to this shopper", func(t *testing.T) {
		// Each subtest's discount is deleted when it ends, so this one is the
		// only promoted discount, and a guest cannot use it.
		promotedDiscount(t, d, testutil.WithEligibility(domain.DiscountEligibilitySubscribers))
		assert.NotContains(t, renderStorefront(t, d, nil), `aria-label="Current offers"`,
			"an empty strip would be a stray coloured bar")
	})

	t.Run("a failed lookup leaves the strip off and the page up", func(t *testing.T) {
		promotedDiscount(t, d)
		closed, err := pgxpool.NewWithConfig(context.Background(), testPool.Config())
		require.NoError(t, err)
		closed.Close()
		broken := *d
		broken.Pool = closed

		assert.NotContains(t, renderStorefront(t, &broken, nil), `aria-label="Current offers"`)
	})
}

// committedMember commits a customer with an active subscription.
func committedMember(t *testing.T) *domain.Customer {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	var planID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO subscription_plans (name, interval, interval_count, is_active)
		 VALUES ($1, 'month', 1, true) RETURNING id`, "Plan "+uuid.NewString()[:8]).Scan(&planID))
	_, err = tx.Exec(ctx,
		`INSERT INTO subscriptions
		     (customer_id, plan_id, status, shipping_address_id, variant_id, quantity,
		      current_period_start, current_period_end, next_order_at)
		 VALUES ($1, $2, 'active', $3, $4, 1, now(), now() + interval '1 month', now() + interval '1 month')`,
		customer.ID, planID, addr.ID, variant.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return customer
}

func TestAdminDiscountBanner(t *testing.T) {
	d := newDiscountAdminDeps(t)
	disc, _ := promotedDiscount(t, d)
	back := "/admin/discounts/" + disc.ID.String()

	t.Run("saves the line and returns to the discount", func(t *testing.T) {
		w := postDiscountForm(t, d.handleAdminDiscountBanner, back+"/banner",
			url.Values{"banner_text": {"20% off with GIFT20"}}, disc.ID.String())
		require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
		assert.Equal(t, back, w.Header().Get("Location"))
		assert.Equal(t, "20% off with GIFT20", discountByID(t, disc.ID).BannerText)
	})

	t.Run("a line over the limit is refused with a message", func(t *testing.T) {
		w := postDiscountForm(t, d.handleAdminDiscountBanner, back+"/banner",
			url.Values{"banner_text": {strings.Repeat("a", domain.MaxBannerTextLen+1)}}, disc.ID.String())
		require.Equal(t, http.StatusSeeOther, w.Code)
		loc, err := url.Parse(w.Header().Get("Location"))
		require.NoError(t, err)
		assert.Contains(t, loc.Query().Get("error"), "at most 120 characters")
		assert.Equal(t, "20% off with GIFT20", discountByID(t, disc.ID).BannerText, "unchanged")
	})

	t.Run("an empty line stops promoting it", func(t *testing.T) {
		w := postDiscountForm(t, d.handleAdminDiscountBanner, back+"/banner",
			url.Values{"banner_text": {""}}, disc.ID.String())
		require.Equal(t, http.StatusSeeOther, w.Code)
		assert.Empty(t, discountByID(t, disc.ID).BannerText)
	})

	t.Run("an unknown discount", func(t *testing.T) {
		id := uuid.NewString()
		w := postDiscountForm(t, d.handleAdminDiscountBanner, "/admin/discounts/"+id+"/banner",
			url.Values{"banner_text": {"x"}}, id)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func discountByID(t *testing.T, id uuid.UUID) *domain.Discount {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	got, err := newDiscountAdminDeps(t).DiscountService.GetDiscount(ctx, tx, id)
	require.NoError(t, err)
	return got
}
