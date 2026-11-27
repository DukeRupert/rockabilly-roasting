package web

import (
	"net/http"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/layouts"
)

// withPromoBanners gives the storefront layout its offer strip: the live
// discounts staff have written banner copy for, filtered to what this shopper
// could use. Lazy and at most once per request, so only a page that renders
// the layout runs the lookup. Best-effort: a failed lookup logs and leaves
// the strip off rather than failing the page.
//
// Must sit inside optionalCustomerSession, which puts the shopper in context.
func (d *Deps) withPromoBanners(next http.Handler) http.Handler {
	if d.DiscountService == nil || d.Pool == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		customer, _ := auth.CustomerFromContext(ctx)
		load := sync.OnceValue(func() []domain.PromoBanner {
			var banners []domain.PromoBanner
			err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
				var txErr error
				banners, txErr = d.DiscountService.StorefrontBanners(ctx, tx, customer)
				return txErr
			})
			if err != nil {
				logging.FromContext(ctx).Warn("storefront promo banners", "error", err)
				return nil
			}
			return banners
		})
		next.ServeHTTP(w, r.WithContext(layouts.WithPromoBanners(ctx, load)))
	})
}
