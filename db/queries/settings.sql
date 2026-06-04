-- name: GetStoreSettings :one
SELECT * FROM store_settings WHERE id = true;

-- name: UpdateTaxConfig :one
UPDATE store_settings
SET tax_mode   = $1,
    tax_rate   = $2,
    tax_label  = $3,
    updated_at = now()
WHERE id = true
RETURNING *;

-- name: UpdateDefaultWholesalePriceList :one
UPDATE store_settings
SET default_wholesale_price_list_id = $1,
    updated_at = now()
WHERE id = true
RETURNING *;
