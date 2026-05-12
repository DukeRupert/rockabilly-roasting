-- +goose Up
-- Box presets: named carton sizes the merchant ships in. When buying a label,
-- the system picks the smallest preset whose max_weight_oz covers the order's
-- total physical weight (line item weights + tare). Dimensions feed the
-- carrier rate quote; max_weight_oz is the merchant's own threshold, not a
-- carrier service ceiling.
CREATE TABLE box_presets (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text    NOT NULL,
    length_in       numeric(6,2) NOT NULL CHECK (length_in > 0),
    width_in        numeric(6,2) NOT NULL CHECK (width_in  > 0),
    height_in       numeric(6,2) NOT NULL CHECK (height_in > 0),
    max_weight_oz   numeric(8,2) NOT NULL CHECK (max_weight_oz > 0),
    sort_order      integer NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- Selection scans in ascending max_weight order.
CREATE INDEX idx_box_presets_max_weight ON box_presets (max_weight_oz);

-- +goose Down
DROP TABLE IF EXISTS box_presets;
