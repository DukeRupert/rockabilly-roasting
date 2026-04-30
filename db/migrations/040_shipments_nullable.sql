-- +goose Up
-- Relax NOT NULL constraints for fields Pirate Ship CSV imports don't carry.
-- EasyPost shipments still populate every column; imports leave label_url and
-- the box dimensions NULL so we don't have to fabricate values that read like
-- real measurements but aren't.
ALTER TABLE shipments
    ALTER COLUMN label_url DROP NOT NULL,
    ALTER COLUMN length_in DROP NOT NULL,
    ALTER COLUMN width_in  DROP NOT NULL,
    ALTER COLUMN height_in DROP NOT NULL;

-- +goose Down
-- Re-imposing NOT NULL would fail if Pirate-Ship-imported rows exist; fill any
-- NULLs with sentinel zeros / empty strings before tightening.
UPDATE shipments SET label_url = '' WHERE label_url IS NULL;
UPDATE shipments SET length_in = 0 WHERE length_in IS NULL;
UPDATE shipments SET width_in  = 0 WHERE width_in  IS NULL;
UPDATE shipments SET height_in = 0 WHERE height_in IS NULL;

ALTER TABLE shipments
    ALTER COLUMN label_url SET NOT NULL,
    ALTER COLUMN length_in SET NOT NULL,
    ALTER COLUMN width_in  SET NOT NULL,
    ALTER COLUMN height_in SET NOT NULL;
