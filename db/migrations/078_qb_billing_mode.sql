-- +goose Up

-- Shadow billing for QuickBooks.
--
-- Before real invoices go to real wholesale customers, the shop wants to run
-- the whole chain for a week or two and read back exactly what it would have
-- billed: which customer, which lines, what terms, which due date, sent to
-- which address. Nothing about that period may reach QBO as a write or reach a
-- customer as an email.
--
-- The mode lives on store_settings rather than in the environment because
-- starting and ending the proof period must not need a deploy, and rather than
-- in modules because modules answer "does this section of the app exist" — the
-- router and sidebar ask that. This is a different question about a section
-- that already exists: is it allowed to move money.
--
-- It defaults to 'shadow' for the same reason modules seed disabled. A deploy
-- must never be the thing that starts billing customers; going live is a
-- deliberate act in the admin, with a confirmation and an audit record.
ALTER TABLE store_settings
    ADD COLUMN qb_billing_mode text NOT NULL DEFAULT 'shadow';

ALTER TABLE store_settings
    ADD CONSTRAINT store_settings_qb_billing_mode_check
    CHECK (qb_billing_mode IN ('shadow', 'live'));

-- A shop that is already invoicing through QuickBooks must not be switched off
-- by a deploy. Defaulting the column to 'shadow' is right for a new install and
-- wrong for a running one: the chain would start recording previews instead of
-- billing, and nothing re-enqueues the chain for an order once it has been
-- placed, so those orders would be silently uninvoiced with no way to bill them
-- from the admin.
--
-- Whether a shop is already live is a question its own data answers: an order
-- carrying a QuickBooks invoice means real invoicing has happened here. On
-- Rockabilly this matches no rows (the OAuth callback could never complete
-- until this release, so nothing was ever invoiced) and the shop correctly
-- starts in shadow.
UPDATE store_settings
   SET qb_billing_mode = 'live'
 WHERE EXISTS (SELECT 1 FROM orders WHERE qb_invoice_id IS NOT NULL);

-- What the shop would have billed, had the mode been live.
--
-- One row per order, refreshed rather than appended when the job runs again:
-- the invoice chain is idempotent and may be retried, and a proof period is
-- much easier to read when each order appears once, showing what would be
-- billed now.
CREATE TABLE qb_invoice_previews (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id    uuid NOT NULL UNIQUE REFERENCES orders(id) ON DELETE CASCADE,
    customer_id uuid REFERENCES customers(id) ON DELETE SET NULL,

    -- Who QBO would have been billed against. qb_customer_id is the match
    -- found by a read-only lookup; when nothing matched, the live run would
    -- have created a customer, and would_create_customer records that. This is
    -- the single most valuable thing the proof period surfaces — an account
    -- that silently fails to match is invisible until money is involved.
    qb_customer_id        text,
    would_create_customer boolean NOT NULL DEFAULT false,

    -- The invoice itself, in the same units the rest of the schema uses.
    doc_number     text NOT NULL,
    bill_email     text NOT NULL DEFAULT '',
    terms_days     integer NOT NULL,
    due_date       date NOT NULL,
    subtotal_cents integer NOT NULL,
    shipping_cents integer NOT NULL,
    total_cents    integer NOT NULL,
    -- The QBO Term that would be referenced, when one already exists. A live
    -- run may create a Term; a shadow run must not, so this is null when the
    -- company has no matching Term yet.
    term_id        text,
    -- Line items as they would be sent, so the digest and the admin page can
    -- show the invoice without re-deriving it from an order that may since
    -- have been edited.
    lines          jsonb NOT NULL DEFAULT '[]'::jsonb,

    -- An invoice already carrying this DocNumber in QBO. Non-null means a live
    -- run would have adopted it instead of creating one, which during a proof
    -- period almost always means someone invoiced this order by hand.
    existing_qb_invoice_id text,

    -- Set when a read-only QBO lookup failed. The preview is still written —
    -- an order missing from the list would read as "nothing to bill", which is
    -- the one conclusion a proof period must never invite by accident.
    lookup_error text,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The digest and admin list both read newest-first over a date window.
CREATE INDEX idx_qb_invoice_previews_created ON qb_invoice_previews (created_at DESC);

-- +goose Down

DROP TABLE IF EXISTS qb_invoice_previews;

ALTER TABLE store_settings DROP CONSTRAINT IF EXISTS store_settings_qb_billing_mode_check;
ALTER TABLE store_settings DROP COLUMN IF EXISTS qb_billing_mode;
