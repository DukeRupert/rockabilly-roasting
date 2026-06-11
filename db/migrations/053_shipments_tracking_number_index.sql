-- +goose Up

-- Inbound Shippo tracking webhooks match a delivery event to a shipment by its
-- tracking number, so we look up shipments.tracking_number on every webhook.
-- There was no index on that column (it was only ever queried by order_id or
-- id). Add one so the webhook lookup stays cheap as the table grows.
CREATE INDEX idx_shipments_tracking_number ON shipments (tracking_number);

-- +goose Down
DROP INDEX IF EXISTS idx_shipments_tracking_number;
