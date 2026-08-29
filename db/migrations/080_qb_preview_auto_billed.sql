-- +goose Up

-- Whether this order would be billed by QuickBooks without anybody asking.
--
-- False for an account on manual billing: nobody has an invoicing and payment
-- agreement with them, so automated billing leaves them alone and somebody
-- invoices them by hand. Every wholesale account is manual today, which is
-- exactly why this cannot be left implicit — going live must not start sending
-- QuickBooks invoices to sixty-two businesses that were never told to expect
-- them.
--
-- The row is still written for such an order, deliberately. An order absent
-- from the review list reads as "nothing to bill", and this project has been
-- caught by that reading more than once; "not billed automatically, invoice it
-- by hand" is a different fact and has to be visible as one. It also gives the
-- Bill now button something to act on, so the review page doubles as the list
-- of orders waiting for a human.
ALTER TABLE qb_invoice_previews
    ADD COLUMN auto_billed boolean NOT NULL DEFAULT true;

-- The billing method as it stood when the order was previewed. Kept because
-- "why was this one not billed" is asked weeks later, by which time the
-- customer's method may have changed.
ALTER TABLE qb_invoice_previews
    ADD COLUMN billing_method text NOT NULL DEFAULT '';

-- The review page and the digest both split on this, and the split is the
-- first thing either of them counts.
CREATE INDEX idx_qb_invoice_previews_auto_billed ON qb_invoice_previews (auto_billed);

-- +goose Down

DROP INDEX IF EXISTS idx_qb_invoice_previews_auto_billed;

ALTER TABLE qb_invoice_previews
    DROP COLUMN IF EXISTS auto_billed,
    DROP COLUMN IF EXISTS billing_method;
