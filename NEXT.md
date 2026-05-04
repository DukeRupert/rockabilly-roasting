# Next: filter unbacked option values on product detail

## Context

While fixing the grind selection bug (commit on `main`), I found a related but
distinct problem: the product detail page (`/catalog/{slug}`) renders option
pills for every `product_option_values` row, regardless of whether any variant
actually exists for that combination.

Example seen in the dev DB on `cloud-9-espresso`:

- **Weight** pills rendered: `12oz`, `3lb`, `5lb`
- **Variants** that exist: only `12oz × whole bean` and `12oz × drip`

If a customer clicks `3lb` (or `5lb`), the JS `resolveVariant()` cannot find a
matching entry in `variantMap`, so it leaves the hidden `selected-variant-id`
input untouched — the form posts the **default variant** (12oz whole bean).
Same wrong-product symptom as the grind bug, by a different path: customer
believes they bought 3lb of drip; ships 12oz of whole bean.

## What needs to happen

In `internal/web/storefront.go::handleStorefrontProduct`, when building
`options` from `ListProductOptionValues`, filter each option's `Values` to
only those that appear in at least one variant's `optionValueIDs`. The
variant-option mapping is already loaded a few lines up (`variantMap`), so the
filter is cheap.

Also worth considering as a UX improvement: render unbacked pills as
**disabled** rather than dropping them, so customers can see "we don't carry
3lb of this one" rather than the option silently disappearing. Either is fine;
disabled is more informative, hidden is less code.

## Files to touch

- `internal/web/storefront.go` (build `options` with filtered values)
- Possibly `internal/ui/storefront/product.templ` (if going the disabled-pill
  route — needs a new flag on each value)

## Verification

- Local: confirm `cloud-9-espresso` only shows `12oz` (or shows 3lb/5lb as
  disabled pills) after the change.
- Click drip + (any weight) → cart line item should be the drip variant.
- Click any non-12oz weight → no variant mismatch; either pill is disabled or
  not present.

## Why this didn't get fixed in the same commit

The reported bug was specifically grind coming through as whole bean. The
weight-mismatch path produces the same user-visible symptom but is structurally
a different bug, and worth a separate commit + clear PR title for the audit
trail (especially given orders may already be wrong in production).
