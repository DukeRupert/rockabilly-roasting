package layouts

import (
	"context"

	"github.com/dukerupert/hiri/internal/domain"
)

type promoBannersKey struct{}

// WithPromoBanners attaches the source of the storefront's offer strip to a
// request. The source is called only when a page renders the storefront
// layout, so API calls and assets never pay for the lookup.
func WithPromoBanners(ctx context.Context, src func() []domain.PromoBanner) context.Context {
	return context.WithValue(ctx, promoBannersKey{}, src)
}

// promoBanners is the offer strip for this request, or nothing when no source
// was attached.
func promoBanners(ctx context.Context) []domain.PromoBanner {
	src, ok := ctx.Value(promoBannersKey{}).(func() []domain.PromoBanner)
	if !ok || src == nil {
		return nil
	}
	return src()
}
