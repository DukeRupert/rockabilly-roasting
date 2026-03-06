-- +goose Up
CREATE TABLE invoices (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        uuid NOT NULL REFERENCES orders(id),
    number          text NOT NULL UNIQUE,
    status          text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'sent', 'partially_paid', 'paid', 'void')),
    subtotal        int NOT NULL,
    shipping        int NOT NULL DEFAULT 0,
    tax_total       int NOT NULL DEFAULT 0,
    total           int NOT NULL,
    amount_paid     int NOT NULL DEFAULT 0,
    amount_due      int GENERATED ALWAYS AS (total - amount_paid) STORED,
    due_date        date,
    notes           text,
    internal_note   text,
    sent_at         timestamptz,
    paid_at         timestamptz,
    voided_at       timestamptz,
    created_by      uuid REFERENCES staff(id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_invoices_order ON invoices (order_id);
CREATE INDEX idx_invoices_status ON invoices (status);

CREATE TABLE invoice_lines (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id  uuid NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    variant_id  uuid REFERENCES variants(id),
    description text NOT NULL,
    quantity    int NOT NULL,
    unit_price  int NOT NULL,
    total       int NOT NULL
);

CREATE INDEX idx_invoice_lines_invoice ON invoice_lines (invoice_id);

CREATE TABLE invoice_payments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id      uuid NOT NULL REFERENCES invoices(id),
    amount          int NOT NULL,
    method          text NOT NULL
        CHECK (method IN ('stripe', 'ach', 'check', 'cash', 'other')),
    reference       text,
    note            text,
    recorded_by     uuid REFERENCES staff(id),
    paid_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_invoice_payments_invoice ON invoice_payments (invoice_id);

-- +goose Down
DROP TABLE IF EXISTS invoice_payments;
DROP TABLE IF EXISTS invoice_lines;
DROP TABLE IF EXISTS invoices;
