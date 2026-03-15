# Coffee Attributes Schema

Product attributes for structured coffee metadata. These live on the `Product` as typed attributes — they do not create variants.

## Schema

```
CoffeeAttributes
  roast_level:    enum (light, medium-light, medium, medium-dark, dark)
  process:        enum (washed, natural, honey)
  origin_type:    enum (single-origin, blend)
  regions:        []string (e.g. ["Ethiopia", "Guatemala"])
  tasting_notes:  []string (max 4-5, e.g. ["berry", "dark chocolate", "brown sugar"])
  body:           enum (light, medium, full)
  acidity:        enum (low, medium, high)
  sweetness:      enum (low, medium, high)
  finish:         string (short descriptive, e.g. "clean and crisp")
  brew_methods:   []enum (espresso, drip, pour-over, french-press, cold-brew)
  caffeine_level: enum (decaf, regular) — only include if selling decaf
  is_seasonal:    bool
  certifications: []enum (organic, fair-trade, rainforest-alliance)
```

## Display Tiers

### Product Card (catalog grid, subscription grid)

Show 1-2 signals for at-a-glance differentiation:

- **Roast level** — small visual indicator (colored dot or label)
- **Tasting notes** (top 2-3) — muted text below price, e.g. "Berry, Dark Chocolate"

Nothing else on the card. Origin and process compete for the same space and tasting notes sell better.

### Product Detail Page

**Above the fold** (near price/description):
- Roast level (visual indicator)
- Origin type + regions (e.g. "Single Origin — Ethiopia")
- Tasting notes as tags

**Below the fold** (structured tasting profile section):
- Body / Acidity / Sweetness as simple visual scales (dots or bars, not numbers)
- Process (washed / natural / honey)
- Brew method recommendations
- Finish description
- Certifications as small badges
- Seasonal badge if applicable

## Fields Intentionally Excluded

| Field | Reason |
|---|---|
| `roast_style` (full-city, etc.) | Jargon — casual buyers don't know it, nerds infer it from roast level |
| `altitude_masl` | Only ultra-nerds care; mention in description prose if notable |
| `farm_or_coop` | Reads better in product description copy than as a data field |
| `micro-lot` (origin type) | Fold into single-origin or call it out in description |
| `acidity` 5-tier scale | Simplified to 3 tiers — "medium-low" vs "medium" means nothing to most people |
| `caffeine_level` multi-tier | Simplified to decaf/regular — "low" and "high" are too vague to be useful |
