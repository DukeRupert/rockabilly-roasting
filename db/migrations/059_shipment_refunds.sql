-- +goose Up
-- Support requesting a Shippo refund for an unused / erroneously-purchased
-- label. Two things are needed that the shipments table never stored:
--   1. provider_transaction_id — the Shippo transaction object_id, which the
--      refund endpoint (POST /refunds) takes. Legacy/imported shipments have
--      none, so it's nullable and those rows are simply not refundable.
--   2. refund_* columns — the async refund lifecycle. A refund is requested
--      (QUEUED), then settles over up to 14 days to SUCCESS or ERROR; a poll
--      job walks it from 'requested' to 'refunded'/'failed'.
ALTER TABLE shipments
    ADD COLUMN provider_transaction_id text,
    ADD COLUMN refund_status text NOT NULL DEFAULT 'none'
        CHECK (refund_status IN ('none', 'requested', 'refunded', 'failed')),
    ADD COLUMN refund_id text,
    ADD COLUMN refund_requested_at timestamptz,
    ADD COLUMN refund_requested_by uuid,
    ADD COLUMN refunded_at timestamptz;

-- +goose Down
ALTER TABLE shipments
    DROP COLUMN provider_transaction_id,
    DROP COLUMN refund_status,
    DROP COLUMN refund_id,
    DROP COLUMN refund_requested_at,
    DROP COLUMN refund_requested_by,
    DROP COLUMN refunded_at;
