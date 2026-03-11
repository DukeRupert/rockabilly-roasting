# Product Attributes & Variants System

This document describes the product attributes system used to define configurable product dimensions (options) and their sellable combinations (variants).

## Data Model

The system uses four tables in a normalized structure:

```
products
  └── product_options        (e.g., "Roast Level", "Size", "Grind")
        └── product_option_values  (e.g., "Light", "Medium", "Dark")

  └── variants               (sellable SKUs — one per option combination)
        └── variant_option_values  (join table linking variant ↔ option values)
```

### Entity Relationship

```
product  1──*  product_option  1──*  product_option_value
product  1──*  variant
variant  *──*  product_option_value  (via variant_option_values)
```

A product with options "Size" (S, M, L) and "Color" (Red, Blue) produces up to 6 variants: S/Red, S/Blue, M/Red, M/Blue, L/Red, L/Blue.

## Schema

### product_options

Defines a configurable dimension on a product.

| Column     | Type      | Notes                           |
|------------|-----------|---------------------------------|
| id         | uuid PK   | Default `gen_random_uuid()`     |
| product_id | uuid FK   | References `products(id)`       |
| name       | text      | Display name, e.g., "Roast Level" |
| position   | int       | Sort order within product       |

Unique constraint: `(product_id, name)` — no duplicate option names per product.

### product_option_values

Defines allowed values for a single option.

| Column             | Type      | Notes                              |
|--------------------|-----------|------------------------------------|
| id                 | uuid PK   | Default `gen_random_uuid()`        |
| product_option_id  | uuid FK   | References `product_options(id)` CASCADE |
| value              | text      | Display value, e.g., "Dark"        |
| position           | int       | Sort order within option           |

Unique constraint: `(product_option_id, value)` — no duplicate values per option.

### variants

A sellable unit representing one specific combination of option values.

| Column              | Type         | Notes                                    |
|---------------------|--------------|------------------------------------------|
| id                  | uuid PK      | Default `gen_random_uuid()`              |
| product_id          | uuid FK      | References `products(id)`                |
| sku                 | text UNIQUE  | Globally unique SKU code                 |
| barcode             | text NULL     | Optional UPC/EAN                         |
| position            | int          | Sort order                               |
| is_default          | bool         | One default variant per product          |
| weight_grams        | int NULL      | Shipping weight                          |
| wholesale_min_qty   | int NULL      | Minimum order quantity for wholesale     |
| wholesale_multiple  | int NULL      | Must order in multiples of N             |
| metadata            | jsonb        | Extensible key-value data                |
| created_at          | timestamptz  |                                          |
| updated_at          | timestamptz  |                                          |

### variant_option_values

Join table linking a variant to its selected option values.

| Column                   | Type     | Notes                                        |
|--------------------------|----------|----------------------------------------------|
| variant_id               | uuid FK  | References `variants(id)` CASCADE            |
| product_option_value_id  | uuid FK  | References `product_option_values(id)` CASCADE |

Composite PK: `(variant_id, product_option_value_id)`.

Each variant has exactly one value per option. This is enforced at the application layer.

## Domain Types (Go)

```go
type ProductOption struct {
    ID        uuid.UUID
    ProductID uuid.UUID
    Name      string
    Position  int
}

type ProductOptionValue struct {
    ID              uuid.UUID
    ProductOptionID uuid.UUID
    Value           string
    Position        int
}

type Variant struct {
    ID                uuid.UUID
    ProductID         uuid.UUID
    SKU               string
    Barcode           *string
    Position          int
    IsDefault         bool
    WeightGrams       *int
    WholesaleMinQty   *int
    WholesaleMultiple *int
    Metadata          map[string]any
    CreatedAt         time.Time
    UpdatedAt         time.Time
}

type VariantOptionValue struct {
    VariantID            uuid.UUID
    ProductOptionValueID uuid.UUID
}
```

## Store Layer

All methods accept `pgx.Tx` — callers control transaction boundaries.

### Product Options
| Method | Signature | Notes |
|--------|-----------|-------|
| CreateProductOption | `(ctx, tx, productID, name, position) → *ProductOption` | |
| ListProductOptions | `(ctx, tx, productID) → []ProductOption` | Ordered by position |
| DeleteProductOption | `(ctx, tx, id)` | Cascades to values and variant links |

### Product Option Values
| Method | Signature | Notes |
|--------|-----------|-------|
| CreateProductOptionValue | `(ctx, tx, optionID, value, position) → *ProductOptionValue` | |
| ListProductOptionValues | `(ctx, tx, optionID) → []ProductOptionValue` | Ordered by position |
| DeleteProductOptionValue | `(ctx, tx, id)` | Cascades to variant links |

### Variants
| Method | Signature | Notes |
|--------|-----------|-------|
| CreateVariant | `(ctx, tx, params) → *Variant` | |
| GetVariantByID | `(ctx, tx, id) → *Variant` | |
| GetVariantBySKU | `(ctx, tx, sku) → *Variant` | |
| ListVariantsByProduct | `(ctx, tx, productID) → []Variant` | Ordered by position |
| UpdateVariant | `(ctx, tx, params) → *Variant` | |
| UpdateVariantWholesale | `(ctx, tx, id, minQty, multiple) → *Variant` | |
| DeleteVariant | `(ctx, tx, id)` | |

### Variant Option Values (Join)
| Method | Signature | Notes |
|--------|-----------|-------|
| CreateVariantOptionValue | `(ctx, tx, variantID, optionValueID)` | |
| ListVariantOptionValues | `(ctx, tx, variantID) → []VariantOptionValue` | |
| DeleteVariantOptionValues | `(ctx, tx, variantID)` | Removes all links for variant |

## App Layer (Business Logic)

`CatalogService` wraps store methods with validation and audit recording.

### Key Behaviors

**Duplicate variant prevention:** Before creating a variant, `CheckDuplicateVariantOptions()` builds a canonical key by sorting the selected option value IDs and comparing against existing variants. Two variants cannot have the same combination of option values.

**SKU uniqueness:** Validated on both create and update. SKUs are globally unique across all products.

**Option value immutability on variants:** Once a variant is created with specific option values, those values cannot be changed. To change options, delete and recreate the variant. This preserves historical order data integrity.

**Audit trail:** All create, update, and delete operations record an audit entry within the same transaction.

### Variant Creation Flow

```
1. Validate SKU uniqueness
2. Check for duplicate option value combination
3. Create variant row
4. Create variant_option_value links (one per option)
5. Record audit entry
```

All steps happen within a single transaction.

## Web Layer (Admin HTTP Handlers)

All handlers live in `web/admin_catalog.go` and follow htmx partial rendering patterns.

### Routes

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| POST | `/admin/catalog/:id/options` | handleAdminOptionCreate | Add option to product |
| POST | `/admin/catalog/:id/options/:optionID` | handleAdminOptionDelete | Remove option (cascades) |
| POST | `/admin/catalog/:id/options/:optionID/values` | handleAdminOptionValueCreate | Add value to option |
| POST | `/admin/catalog/:id/options/:optionID/values/:valueID` | handleAdminOptionValueDelete | Remove value |
| POST | `/admin/catalog/:id/variants` | handleAdminVariantCreate | Create variant |
| POST | `/admin/catalog/:id/variants/:variantID` | handleAdminVariantUpdate | Update SKU/weight/barcode |
| POST | `/admin/catalog/:id/variants/:variantID/delete` | handleAdminVariantDelete | Delete variant |

### htmx Integration

- Forms POST to handlers that re-render panels as HTML partials
- `renderOptionsPanel()` and `renderVariantsPanel()` helper functions fetch fresh data and render the appropriate templ component
- Error responses use OOB toast notifications (HTTP 200 + toast HTML)

## UI Layer (Admin Templates)

The product edit page (`product_edit.templ`) includes two htmx-swappable panels:

### OptionsPanel

- Lists all options with their values displayed as pills
- Delete buttons on each option and value
- Inline form to add new values to existing options
- Form to create a new option

### VariantsPanel

- Table of all variants showing: SKU, option value pills, base price, weight, actions
- "Default" badge on the default variant
- Add variant form with:
  - Dynamic `<select>` for each product option
  - SKU field with client-side auto-generation from option abbreviations
  - Barcode, weight, is_default fields
  - Validation feedback for duplicate combinations

### Composite Types for Rendering

```go
// Used in OptionsPanel
type OptionWithValues struct {
    Option domain.ProductOption
    Values []domain.ProductOptionValue
}

// Used in VariantsPanel
type VariantWithOptions struct {
    Variant      domain.Variant
    OptionValues []string           // Display names, e.g., ["Medium", "Dark Roast"]
    PriceCents   *int               // Base price
    GroupPrices  map[uuid.UUID]int  // Per customer-group prices
}
```

## Pricing Relationship

Pricing is separate from the attribute system but connected at the variant level:

- Each variant can have a **base price** (stored in a price set)
- Each variant can have **customer-group prices** (wholesale tiers, etc.)
- Prices are managed through the pricing module, not the catalog module
- The variant table does NOT store prices — they live in `price_set_prices`

## Storefront Rendering

On the storefront product page (`storefront/product.templ`), variants are displayed with their prices:

```go
type VariantWithPrice struct {
    Variant      domain.Variant
    BasePrice    *int
    CurrencyCode string
}
```

Option values are rendered as selectable pills for customers to choose their preferred variant.

## Design Decisions

1. **Options are product-scoped, not global.** Each product defines its own options. There is no shared option registry. This keeps things simple and avoids coupling between unrelated products.

2. **Variants are explicit, not computed.** The system does not auto-generate all combinations. Admins create only the variants they want to sell. A product with Size (S, M, L) and Color (Red, Blue) can have just 3 variants instead of all 6.

3. **Option values are immutable on variants.** Changing which options a variant represents would break historical order line items. Instead, delete and recreate.

4. **SKUs are globally unique.** A single namespace across all products prevents shipping/fulfillment confusion.

5. **Wholesale attributes live on the variant.** `wholesale_min_qty` and `wholesale_multiple` are variant-level because different sizes/configurations may have different wholesale rules.

6. **`metadata` jsonb for extensibility.** Vertical-specific attributes (origin, process method, altitude for coffee; thread count for textiles) go in metadata rather than new columns.
