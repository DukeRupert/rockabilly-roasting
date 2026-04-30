-- name: GetShippingConfig :one
SELECT * FROM shipping_config LIMIT 1;

-- name: UpdateShippingConfig :exec
UPDATE shipping_config
SET flat_rate_cents = $1,
    free_shipping_threshold = $2,
    currency = $3,
    local_zip_codes = $4,
    origin_name = $5,
    origin_street1 = $6,
    origin_street2 = $7,
    origin_city = $8,
    origin_state = $9,
    origin_zip = $10,
    origin_country = $11,
    tare_weight_oz = $12;

-- name: CreateShipment :one
INSERT INTO shipments (id, order_id, status, provider, tracking_number, label_url,
                       carrier_name, service_name, label_cost_cents, label_currency,
                       weight_oz, length_in, width_in, height_in, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING *;

-- name: GetShipmentByID :one
SELECT * FROM shipments WHERE id = $1;

-- name: ListShipmentsByOrder :many
SELECT * FROM shipments
WHERE order_id = $1
ORDER BY created_at ASC;

-- name: UpdateShipmentStatus :one
UPDATE shipments
SET status = $2
WHERE id = $1
RETURNING *;

-- name: UpdateShipmentTracking :one
UPDATE shipments
SET tracking_number = $2, label_url = $3, label_created_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateShipmentDelivered :exec
UPDATE shipments
SET status = 'delivered', delivered_at = now()
WHERE id = $1;

-- name: UpdateShipmentLabel :exec
UPDATE shipments
SET label_r2_key = $2, label_format = $3
WHERE id = $1;

-- name: GetShipmentLabelKey :one
SELECT label_r2_key FROM shipments WHERE id = $1;
