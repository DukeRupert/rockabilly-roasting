# Lean Commerce — Product Attributes

Structured key-value metadata attached to products. Does not generate variants or affect pricing. Serves three UI purposes: product detail display blocks, storefront faceted filtering, and sort options.

---

## Concept

Tenants define **attribute sets** — named groups of attribute keys. A set is assigned to one or more products. Each product then holds a value per key in the set.

Example for a coffee tenant:

```
Attribute Set: "Coffee Profile"
  Keys:
    - Origin        (text)
    - Process       (enum: washed, natural, honey)
    - Roast Level   (enum: light, medium-light, medium, medium-dark, dark)
    - Tasting Notes (multi_text)
    - Brew Methods  (multi_enum: espresso, drip, pour-over, french-press, cold-brew)
    - Seasonal      (boolean)

Product: "Yirgacheffe Natural"
  Origin:        Ethiopia
  Process:       Natural
  Roast Level:   Medium-Light
  Tasting Notes: Blueberry, Jasmine, Dark Chocolate
  Brew Methods:  Pour-Over, Drip
  Seasonal:      true
```

---

## Schema

Migration: `024_product_attributes` (initial), `034_attribute_value_types` (expanded types)

```sql
CREATE TYPE attribute_value_type AS ENUM ('text', 'multi_text', 'enum', 'multi_enum', 'boolean');

-- Named groups of attribute keys
CREATE TABLE attribute_sets (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,            -- "Coffee Profile"
    slug        text NOT NULL,            -- "coffee-profile"
    position    int  NOT NULL DEFAULT 0,  -- display order in admin
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_attribute_set_slug UNIQUE (slug)
);

-- Individual attributes within a set
CREATE TABLE attribute_keys (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    attribute_set_id uuid NOT NULL REFERENCES attribute_sets(id) ON DELETE CASCADE,
    name             text NOT NULL,                        -- "Tasting Notes"
    slug             text NOT NULL,                        -- "tasting-notes"
    value_type       attribute_value_type NOT NULL DEFAULT 'text',
    position         int  NOT NULL DEFAULT 0,              -- display order within set
    filterable       bool NOT NULL DEFAULT false,          -- include in faceted search sidebar
    sortable         bool NOT NULL DEFAULT false,          -- allow storefront sort by this key
    allowed_values   jsonb,                                -- predefined choices for enum/multi_enum
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
    value            text,   -- text/enum/boolean: "Washed", "true"
    values           jsonb,  -- multi_text/multi_enum: ["Blueberry", "Dark Chocolate"]
    CONSTRAINT uq_product_attribute UNIQUE (product_id, attribute_key_id)
);

-- Indexes
CREATE INDEX idx_attr_values_product    ON product_attribute_values (product_id);
CREATE INDEX idx_attr_values_key        ON product_attribute_values (attribute_key_id);
CREATE INDEX idx_attr_values_multi      ON product_attribute_values USING gin (values)
    WHERE values IS NOT NULL;
CREATE INDEX idx_attr_keys_set          ON attribute_keys (attribute_set_id, position);
```

### Value types

All values are strings. `value_type` controls storage shape, admin UI input, and server-side validation.

| `value_type` | Column used | Admin UI | Example |
|---|---|---|---|
| `text` | `value text` | `<input type="text">` | `"Ethiopia"` |
| `enum` | `value text` | `<select>` from `allowed_values` | `"washed"` |
| `multi_text` | `values jsonb` | `<input type="text">` (comma-separated) | `["Blueberry", "Dark Chocolate"]` |
| `multi_enum` | `values jsonb` | Checkboxes from `allowed_values` | `["espresso", "pour-over"]` |
| `boolean` | `value text` | `<input type="checkbox">` | `"true"` or `"false"` |

A row will have either `value` or `values` populated, never both. No constraint enforces this — the application layer is responsible.

### `allowed_values` column

`attribute_keys.allowed_values` stores a JSON array of strings: `["light", "medium", "dark"]`. Required for `enum` and `multi_enum` types. Ignored for other types.

Server-side validation rejects values not in this list. This prevents typos from breaking storefront rendering and filtering.

---

## Go domain types

```go
// domain/attributes.go

type AttributeValueType string

const (
    AttributeValueTypeText      AttributeValueType = "text"
    AttributeValueTypeEnum      AttributeValueType = "enum"
    AttributeValueTypeMultiText AttributeValueType = "multi_text"
    AttributeValueTypeMultiEnum AttributeValueType = "multi_enum"
    AttributeValueTypeBoolean   AttributeValueType = "boolean"
)

type AttributeSet struct {
    ID       uuid.UUID
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
    AllowedValues  []string // predefined choices for enum/multi_enum
}

// IsMultiType returns true for value types that store multiple values.
func (k AttributeKey) IsMultiType() bool

// IsEnumType returns true for value types that constrain values to AllowedValues.
func (k AttributeKey) IsEnumType() bool

type ProductAttributeValue struct {
    KeyID     uuid.UUID
    KeyName   string
    KeySlug   string
    ValueType AttributeValueType
    Value     *string  // text/enum/boolean
    Values    []string // multi_text/multi_enum
}

// DisplayValue returns a display-ready string for use in templates.
// Boolean → "Yes"/"No", multi → comma-joined, single → raw value.
func (v ProductAttributeValue) DisplayValue() string

// BoolValue returns true if the stored value is "true".
func (v ProductAttributeValue) BoolValue() bool
```

---

## Validation

### Key creation/update

- `enum` and `multi_enum` types **require** `AllowedValues` — `ErrAttributeAllowedValuesRequired` if missing.
- Other types ignore `AllowedValues`.

### Product attribute save

- `enum`: value must be in `AllowedValues` — `ErrAttributeValueNotAllowed` if not.
- `multi_enum`: every value must be in `AllowedValues`.
- `boolean`: value must be `"true"` or `"false"`.
- `text` and `multi_text`: no constraint.

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

### Faceted filter — single value match (text/enum)

```sql
SELECT DISTINCT p.id
FROM products p
JOIN product_attribute_values pav ON pav.product_id = p.id
JOIN attribute_keys ak            ON ak.id = pav.attribute_key_id
WHERE ak.slug       = 'process'
  AND ak.filterable = true
  AND pav.value     = 'Washed';
```

### Faceted filter — multi value contains (multi_text/multi_enum)

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
  AND ak.value_type      IN ('text', 'enum')
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
  AND ak.value_type       IN ('multi_text', 'multi_enum')
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
- Key badges show value type for each key

**`/admin/attributes/{id}`** — attribute set editor
- Add / edit / delete keys within the set
- Set `value_type` (text, enum, multi_text, multi_enum, boolean) per key
- Set `allowed_values` (comma-separated input, required for enum types)
- Set `filterable` and `sortable` flags per key
- Reorder keys (position)
- Key table shows type badge + allowed value pills for enum types

**`/admin/products/{id}` — existing product editor, gains "Attributes" panel**
- Assign / remove attribute sets from the product
- For each assigned set, render input per key by value type:
  - `text` → standard text input
  - `enum` → `<select>` dropdown with options from `allowed_values`
  - `multi_text` → text input with comma-separated hint
  - `multi_enum` → checkboxes for each `allowed_values` entry
  - `boolean` → single checkbox (absent = `"false"`)
- Save all values in a single `POST`

---

## Storefront UI

**Product detail page** — render attribute values as a structured block below the description. Template receives `[]ProductAttributeValue` ordered by `aset.position, ak.position`. `DisplayValue()` handles all five types. Multi values typically rendered as a pill/tag list rather than comma-separated prose. Booleans render as "Yes"/"No".

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
store/attributes.go           — sqlc queries + hand-written ListProductAttributeValues
app/attributes.go             — AttributeService: Create, Update, Delete, Validate, AssignToProduct
web/admin_attributes.go       — admin CRUD handlers
ui/admin/attribute_list.templ  — attribute sets list page
ui/admin/attribute_edit.templ  — attribute set editor with key management
ui/admin/attribute_panel.templ — product edit attribute panel (5-type rendering)
```

---

## What this is not

- **Not variants** — attributes do not generate SKUs or affect inventory. A `multi_text` tasting notes value of `["Blueberry", "Dark Chocolate"]` is one product, not two.
- **Not product description** — attributes are structured and queryable. Free-form prose belongs in `products.description`.
- **Not pricing metadata** — no attribute affects price. Pricing is handled by variants and wholesale price lists.
