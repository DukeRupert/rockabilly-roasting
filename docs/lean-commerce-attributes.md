# Lean Commerce — Product Attributes

Structured key-value metadata attached to products. Does not generate variants or affect pricing. Serves three UI purposes: product detail display blocks, storefront faceted filtering, and sort options.

---

## Concept

Tenants define **attribute sets** — named groups of attribute keys. A set is assigned to one or more products. Each product then holds a value per key in the set.

Example for a coffee tenant:

```
Attribute Set: "Coffee Profile"
  Keys:
    - Origin        (single)
    - Process       (single)
    - Roast Level   (single)
    - Tasting Notes (multi)
    - Region        (single)

Product: "Yirgacheffe Natural"
  Origin:        Ethiopia
  Process:       Natural
  Roast Level:   Medium-Light
  Tasting Notes: Blueberry, Jasmine, Dark Chocolate
  Region:        Yirgacheffe
```

---

## Schema

Migration: `024_product_attributes`

```sql
CREATE TYPE attribute_value_type AS ENUM ('single', 'multi');

-- Named groups of attribute keys, scoped to tenant
CREATE TABLE attribute_sets (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        text NOT NULL,            -- "Coffee Profile"
    slug        text NOT NULL,            -- "coffee-profile"
    position    int  NOT NULL DEFAULT 0,  -- display order in admin
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_attribute_set_slug UNIQUE (tenant_id, slug)
);

-- Individual attributes within a set
CREATE TABLE attribute_keys (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    attribute_set_id uuid NOT NULL REFERENCES attribute_sets(id) ON DELETE CASCADE,
    name             text NOT NULL,                        -- "Tasting Notes"
    slug             text NOT NULL,                        -- "tasting-notes"
    value_type       attribute_value_type NOT NULL DEFAULT 'single',
    position         int  NOT NULL DEFAULT 0,              -- display order within set
    filterable       bool NOT NULL DEFAULT false,          -- include in faceted search sidebar
    sortable         bool NOT NULL DEFAULT false,          -- allow storefront sort by this key
    CONSTRAINT uq_attribute_key_slug UNIQUE (attribute_set_id, slug)
);

-- Which attribute sets apply to a product (many-to-many)
CREATE TABLE product_attribute_sets (
    product_id       uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    attribute_set_id uuid NOT NULL REFERENCES attribute_sets(id) ON DELETE CASCADE,
    PRIMARY KEY (product_id, attribute_set_id)
);

-- Per-product values for each attribute key
CREATE TABLE product_attribute_values (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id       uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    attribute_key_id uuid NOT NULL REFERENCES attribute_keys(id) ON DELETE CASCADE,
    value            text,   -- single: "Washed"
    values           jsonb,  -- multi:  ["Blueberry", "Dark Chocolate"]
    CONSTRAINT uq_product_attribute UNIQUE (product_id, attribute_key_id)
);

-- Indexes
CREATE INDEX idx_attr_values_product    ON product_attribute_values (product_id);
CREATE INDEX idx_attr_values_key        ON product_attribute_values (attribute_key_id);
CREATE INDEX idx_attr_values_multi      ON product_attribute_values USING gin (values)
    WHERE values IS NOT NULL;
CREATE INDEX idx_attr_keys_set          ON attribute_keys (attribute_set_id, position);
CREATE INDEX idx_attr_sets_tenant       ON attribute_sets (tenant_id, position);
```

### Value type semantics

All values are strings. `value_type` controls storage shape and UI rendering only — no numeric casting, no range logic.

| `value_type` | Column used | Example |
|---|---|---|
| `single` | `value text` | `"Washed"` |
| `multi` | `values jsonb` | `["Blueberry", "Dark Chocolate"]` |

A row will have either `value` or `values` populated, never both. No constraint enforces this — the application layer is responsible.

---

## Go domain types

```go
// domain/attributes.go

type AttributeValueType string

const (
    AttributeValueTypeSingle AttributeValueType = "single"
    AttributeValueTypeMulti  AttributeValueType = "multi"
)

type AttributeSet struct {
    ID       uuid.UUID
    TenantID uuid.UUID
    Name     string
    Slug     string
    Position int
    Keys     []AttributeKey // populated on full fetch
}

type AttributeKey struct {
    ID             uuid.UUID
    AttributeSetID uuid.UUID
    Name           string
    Slug           string
    ValueType      AttributeValueType
    Position       int
    Filterable     bool
    Sortable       bool
}

type ProductAttributeValue struct {
    KeyID     uuid.UUID
    KeyName   string
    KeySlug   string
    ValueType AttributeValueType
    Value     *string  // single
    Values    []string // multi
}

// DisplayValue returns a display-ready string for use in templates.
// Callers do not need to know the underlying value type.
func (v ProductAttributeValue) DisplayValue() string {
    if len(v.Values) > 0 {
        return strings.Join(v.Values, ", ")
    }
    if v.Value != nil {
        return *v.Value
    }
    return ""
}
```

---

## Key query patterns

### Product detail — fetch all attribute values

```sql
SELECT
    ak.name        AS key_name,
    ak.slug        AS key_slug,
    ak.value_type,
    ak.position,
    pav.value,
    pav.values
FROM product_attribute_values pav
JOIN attribute_keys ak   ON ak.id = pav.attribute_key_id
JOIN attribute_sets aset ON aset.id = ak.attribute_set_id
WHERE pav.product_id = $1
ORDER BY aset.position, ak.position;
```

### Faceted filter — single value match

```sql
SELECT DISTINCT p.id
FROM products p
JOIN product_attribute_values pav ON pav.product_id = p.id
JOIN attribute_keys ak            ON ak.id = pav.attribute_key_id
WHERE ak.slug       = 'process'
  AND ak.filterable = true
  AND pav.value     = 'Washed';
```

### Faceted filter — multi value contains

```sql
SELECT DISTINCT p.id
FROM products p
JOIN product_attribute_values pav ON pav.product_id = p.id
JOIN attribute_keys ak            ON ak.id = pav.attribute_key_id
WHERE ak.slug       = 'tasting-notes'
  AND ak.filterable = true
  AND pav.values    ? 'Blueberry';  -- jsonb ? operator: element exists in array
```

### Facet sidebar — available single values with counts

```sql
SELECT
    ak.name              AS key_name,
    ak.slug              AS key_slug,
    pav.value,
    COUNT(*)             AS product_count
FROM product_attribute_values pav
JOIN attribute_keys ak ON ak.id = pav.attribute_key_id
WHERE ak.filterable      = true
  AND ak.value_type      = 'single'
  AND ak.attribute_set_id = $1
  AND pav.value IS NOT NULL
GROUP BY ak.name, ak.slug, pav.value
ORDER BY ak.position, pav.value;
```

### Facet sidebar — available multi values with counts

```sql
SELECT
    ak.name              AS key_name,
    ak.slug              AS key_slug,
    elem.value,
    COUNT(*)             AS product_count
FROM product_attribute_values pav
JOIN attribute_keys ak         ON ak.id = pav.attribute_key_id
CROSS JOIN jsonb_array_elements_text(pav.values) AS elem(value)
WHERE ak.filterable       = true
  AND ak.value_type       = 'multi'
  AND ak.attribute_set_id = $1
  AND pav.values IS NOT NULL
GROUP BY ak.name, ak.slug, elem.value
ORDER BY ak.position, elem.value;
```

---

## Admin UI

Three surfaces, all staff-only:

**`/admin/attributes`** — attribute sets list
- Create / edit / delete sets
- Reorder sets (position)

**`/admin/attributes/{id}`** — attribute set editor
- Add / edit / delete keys within the set
- Set `filterable` and `sortable` flags per key
- Set `value_type` (single vs multi) per key
- Reorder keys (position)

**`/admin/products/{id}` — existing product editor, gains "Attributes" tab**
- Assign / remove attribute sets from the product
- For each assigned set, render input per key:
  - `single` → standard text input
  - `multi` → tag input (Alpine.js, comma-separated or enter-to-add)
- Save all values in a single `POST`

---

## Storefront UI

**Product detail page** — render attribute values as a structured block below the description. Template receives `[]ProductAttributeValue` ordered by `aset.position, ak.position`. `DisplayValue()` handles both single and multi rendering. Multi values typically rendered as a pill/tag list rather than comma-separated prose.

**Facet sidebar** — rendered from the facet queries above. Each filterable key becomes a checkbox group. Active filters appended to URL as query params: `?process=Washed&tasting-notes=Blueberry`. Multiple values for the same key are OR'd; multiple keys are AND'd.

**Sort options** — any key with `sortable = true` appears in the sort dropdown. Sort is applied via a join to `product_attribute_values` filtered to that key. Alphabetical ascending/descending only — no numeric sort.

---

## Audit actions

```go
AuditAttributeSetCreated      = "attribute_set.created"
AuditAttributeSetUpdated      = "attribute_set.updated"
AuditAttributeSetDeleted      = "attribute_set.deleted"
AuditProductAttributesUpdated = "product.attributes_updated"
```

`product.attributes_updated` fires whenever a product's attribute values are saved (bulk update — one audit entry per save, not per key changed). `after` snapshot contains the full set of attribute values post-save.

---

## Package locations

```
domain/attributes.go          — AttributeSet, AttributeKey, ProductAttributeValue types
store/attributes.go           — sqlc queries: ListByProduct, ListFacetValues, UpsertValues
app/attributes.go             — AttributeService: Create, Update, Delete, AssignToProduct
web/ui/components/attributes/ — templ components: AttributeBlock, FacetSidebar, TagInput
web/handlers/admin/attributes.go — admin CRUD handlers
```

---

## What this is not

- **Not variants** — attributes do not generate SKUs or affect inventory. A `multi` tasting notes value of `["Blueberry", "Dark Chocolate"]` is one product, not two.
- **Not product description** — attributes are structured and queryable. Free-form prose belongs in `products.description`.
- **Not pricing metadata** — no attribute affects price. Pricing is handled by variants and wholesale price lists.
