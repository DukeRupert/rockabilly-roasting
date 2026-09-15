-- +goose Up

-- Record the sales tax an invoice would carry on the shadow-billing preview.
--
-- total_cents already includes tax — it is the order total — so this is the
-- breakdown rather than a correction: the review page is the instrument of the
-- proof period, and "how much of this is tax" is the question somebody
-- checking a tax rollout is there to answer.
--
-- Defaulted to 0 rather than backfilled from orders. Every preview written
-- before this migration was written for an untaxed order (wholesale was exempt
-- by construction until 2026-09-15), so 0 is the true value for all of them,
-- and previews are upserted on every re-run anyway.
--
-- Numbered 088, skipping 087: the unmerged qb/config-out-of-env branch already
-- holds 087_qb_app_config.sql and was renumbered once already. Leaving its
-- number alone costs a gap in the sequence if that branch is ever abandoned,
-- which is cheaper than renumbering an in-flight branch a second time.
ALTER TABLE qb_invoice_previews
    ADD COLUMN tax_cents integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE qb_invoice_previews DROP COLUMN IF EXISTS tax_cents;
