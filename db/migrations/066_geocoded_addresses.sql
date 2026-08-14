-- +goose Up
-- Geocoding cache for delivery route planning. OSRM needs coordinates, not
-- street addresses, so every stop on a route has to be resolved once. Google
-- charges per lookup, and the same two dozen households order week after week,
-- so this table is what keeps the bill at pennies instead of a lookup per
-- planning click.
--
-- Deliberately NOT tied to customers or addresses: the cache key is the
-- normalized address text itself (domain.NormalizeAddress), so a household that
-- reorders under a new customer record, or two accounts at one office, share a
-- single row. That also means nothing here needs cascading when a customer is
-- deleted — the cache holds no ownership, only geometry.
CREATE TABLE geocoded_addresses (
    id                 uuid PRIMARY KEY,
    -- Cache key: lowercased, punctuation-stripped, USPS-abbreviated. UNIQUE is
    -- what makes the upsert an upsert; it also provides the lookup index.
    normalized_address text             NOT NULL UNIQUE,
    -- The address as last seen on an order, kept for staff reading the
    -- low-confidence report — "1234 w 4th ave # 2" is hard to eyeball.
    raw_address        text             NOT NULL,
    lat                double precision NOT NULL,
    lng                double precision NOT NULL,
    provider           text             NOT NULL,
    -- Google's location_type verbatim (ROOFTOP, RANGE_INTERPOLATED,
    -- GEOMETRIC_CENTER, APPROXIMATE). Stored provider-native rather than
    -- collapsed to a boolean so a future provider swap can be reinterpreted
    -- without re-geocoding everything.
    confidence         text             NOT NULL,
    geocoded_at        timestamptz      NOT NULL DEFAULT now()
);

-- The admin "addresses we couldn't pin precisely" report scans by confidence.
-- Partial index because the precise rows are the overwhelming majority and are
-- never the ones being listed.
CREATE INDEX idx_geocoded_addresses_low_confidence
    ON geocoded_addresses (geocoded_at DESC)
    WHERE confidence NOT IN ('ROOFTOP', 'RANGE_INTERPOLATED');

-- +goose Down
DROP INDEX IF EXISTS idx_geocoded_addresses_low_confidence;
DROP TABLE IF EXISTS geocoded_addresses;
