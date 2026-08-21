-- +goose Up
-- A route may finish somewhere other than the roastery.
--
-- Monday runs come back to the shop; on Thursday the driver takes the van home
-- after the last delivery. Those are different routes over the same stops: the
-- optimizer should finish next to the driver's house, not next to the shop, and
-- the "how far is this run" total should not include a return leg nobody drives.
--
-- Empty end_address means "ends where it started", which is what every route
-- planned before this migration did — hence the '' default rather than NULL.
-- The coordinates are NULL in that case: there is no second pin to remember.
ALTER TABLE delivery_routes
    ADD COLUMN end_address text NOT NULL DEFAULT '',
    -- Resolved at plan time and frozen here for the same reason origin_lat/lng
    -- are: a route the driver already has open must not silently re-anchor if
    -- the address is later re-geocoded to a slightly different point.
    ADD COLUMN end_lat     double precision,
    ADD COLUMN end_lng     double precision;

-- +goose Down
ALTER TABLE delivery_routes
    DROP COLUMN IF EXISTS end_address,
    DROP COLUMN IF EXISTS end_lat,
    DROP COLUMN IF EXISTS end_lng;
