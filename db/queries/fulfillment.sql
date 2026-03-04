-- name: CreateFulfillment :one
INSERT INTO fulfillments (id, order_id, location_id, status, tracking_number, tracking_url, provider, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetFulfillmentByID :one
SELECT * FROM fulfillments WHERE id = $1;

-- name: ListFulfillmentsByOrder :many
SELECT * FROM fulfillments
WHERE order_id = $1
ORDER BY id;

-- name: UpdateFulfillmentStatus :one
UPDATE fulfillments
SET status = $2
WHERE id = $1
RETURNING *;

-- name: UpdateFulfillmentTracking :one
UPDATE fulfillments
SET tracking_number = $2, tracking_url = $3, provider = $4, shipped_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateFulfillmentItem :one
INSERT INTO fulfillment_items (id, fulfillment_id, line_item_id, quantity)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListFulfillmentItemsByFulfillment :many
SELECT * FROM fulfillment_items
WHERE fulfillment_id = $1
ORDER BY id;

-- name: CreateStockLocation :one
INSERT INTO stock_locations (id, name, address_id, is_active)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetStockLocationByID :one
SELECT * FROM stock_locations WHERE id = $1;

-- name: ListStockLocations :many
SELECT * FROM stock_locations ORDER BY name;

-- name: UpdateStockLocationActive :exec
UPDATE stock_locations SET is_active = $2 WHERE id = $1;

-- name: CreateInventoryItem :one
INSERT INTO inventory_items (id, variant_id, track_inventory, requires_shipping)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetInventoryItemByID :one
SELECT * FROM inventory_items WHERE id = $1;

-- name: GetInventoryItemByVariantID :one
SELECT * FROM inventory_items WHERE variant_id = $1;

-- name: CreateStockLevel :one
INSERT INTO stock_levels (id, inventory_item_id, location_id, quantity_on_hand, quantity_reserved)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetStockLevelByInventoryAndLocation :one
SELECT * FROM stock_levels
WHERE inventory_item_id = $1 AND location_id = $2;

-- name: ListStockLevelsByInventory :many
SELECT * FROM stock_levels
WHERE inventory_item_id = $1
ORDER BY location_id;

-- name: AdjustStockQuantity :one
UPDATE stock_levels
SET quantity_on_hand = quantity_on_hand + @delta::int
WHERE id = $1
RETURNING *;

-- name: ReserveStock :one
UPDATE stock_levels
SET quantity_reserved = quantity_reserved + @delta::int
WHERE id = $1
RETURNING *;

-- name: ReleaseReservation :one
UPDATE stock_levels
SET quantity_reserved = quantity_reserved - @delta::int
WHERE id = $1
RETURNING *;
