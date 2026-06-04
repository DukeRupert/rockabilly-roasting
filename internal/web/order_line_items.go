package web

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// enrichOrderLineItems resolves the product title and variant option label
// (e.g. "Rebel Blend" + "Whole Bean / 12 oz") for each line item, producing the
// customer-facing view model used by storefront order detail. Without this,
// order history can only show a raw variant ID — useless to the buyer and a
// blocker for reorder. Mirrors the admin order enrichment (buildVariantLabel +
// per-product option-label cache) but returns the leaner storefront shape.
//
// A variant or product that has since been deleted falls back to a stable label
// ("Product") so historical orders still render rather than erroring.
func (d *Deps) enrichOrderLineItems(ctx context.Context, tx pgx.Tx, items []domain.LineItem) ([]storefront.OrderLineItemView, error) {
	views := make([]storefront.OrderLineItemView, len(items))
	// Per-product cache so multi-line orders don't re-query options.
	optionLabelByProduct := map[string]map[string]string{}

	for i, li := range items {
		views[i] = storefront.OrderLineItemView{
			ProductName: "Product",
			Quantity:    li.Quantity,
			UnitPrice:   li.UnitPrice,
			Total:       li.Total,
		}

		variant, err := d.CatalogService.GetVariant(ctx, tx, li.VariantID)
		if err != nil {
			if errors.Is(err, app.ErrVariantNotFound) {
				continue
			}
			return nil, err
		}
		views[i].SKU = variant.SKU

		product, err := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
		if err != nil {
			if errors.Is(err, app.ErrProductNotFound) {
				continue
			}
			return nil, err
		}
		views[i].ProductName = product.Title

		labels, ok := optionLabelByProduct[product.ID.String()]
		if !ok {
			labels = map[string]string{}
			opts, err := d.CatalogService.ListProductOptions(ctx, tx, product.ID)
			if err != nil {
				return nil, err
			}
			for _, opt := range opts {
				vals, err := d.CatalogService.ListProductOptionValues(ctx, tx, opt.ID)
				if err != nil {
					return nil, err
				}
				for _, val := range vals {
					labels[val.ID.String()] = val.Value
				}
			}
			optionLabelByProduct[product.ID.String()] = labels
		}

		vovs, err := d.CatalogService.ListVariantOptionValues(ctx, tx, variant.ID)
		if err != nil {
			return nil, err
		}
		var label string
		for _, vov := range vovs {
			if s, ok := labels[vov.ProductOptionValueID.String()]; ok && s != "" {
				if label != "" {
					label += " / "
				}
				label += s
			}
		}
		views[i].VariantLabel = label
	}

	return views, nil
}
