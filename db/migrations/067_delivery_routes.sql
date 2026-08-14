-- +goose Up
-- Delivery routes: the ordered stop list a driver works through on a run.
--
-- The route is derived from the local-delivery fulfillment queue rather than
-- owning it. Orders stay the source of truth for what needs delivering; a route
-- is a plan over them for one day, and deleting a route loses nothing but the
-- stop order. That is also what makes a skipped stop cheap (see route_stops):
-- the order is untouched and simply reappears on the next run's route.
CREATE TABLE delivery_routes (
    id                 uuid PRIMARY KEY,
    -- The delivery run this route covers. Matches orders.scheduled_delivery_date
    -- (migration 064), which the Mon/Thu cutoff already sets.
    route_date         date        NOT NULL,
    status             text        NOT NULL DEFAULT 'draft'
                                   CHECK (status IN ('draft', 'active', 'completed')),
    -- Roastery coordinates, resolved from shipping_config's origin at plan
    -- time. Stored on the route so a later change to the shop address doesn't
    -- silently re-anchor a route the driver already has open.
    origin_lat         double precision NOT NULL,
    origin_lng         double precision NOT NULL,
    origin_address     text        NOT NULL,
    -- Totals as OSRM reported them for the whole trip, return leg included
    -- when roundtrip is set.
    total_distance_m   integer     NOT NULL DEFAULT 0,
    total_duration_s   integer     NOT NULL DEFAULT 0,
    roundtrip          boolean     NOT NULL DEFAULT true,
    -- Authenticates the driver page without a login. NULL until the route is
    -- activated: a draft has no shareable URL, so a half-planned route cannot
    -- leak a stop list.
    share_token        text UNIQUE,
    created_at         timestamptz NOT NULL DEFAULT now(),
    activated_at       timestamptz,
    completed_at       timestamptz
);

-- One live route per delivery day. Re-planning replaces rather than
-- accumulates, so staff can't hand a driver yesterday's draft by accident.
-- Completed routes are exempt, which keeps the history intact.
CREATE UNIQUE INDEX idx_delivery_routes_one_live_per_date
    ON delivery_routes (route_date)
    WHERE status <> 'completed';

CREATE INDEX idx_delivery_routes_status_date
    ON delivery_routes (status, route_date DESC);

-- route_stops is the ordered stop list. lat/lng are copied from the geocode
-- cache at plan time rather than joined at read time: the driver's maps links
-- must point at exactly the pin we routed around, even if the address is later
-- re-geocoded to a slightly different point.
CREATE TABLE route_stops (
    id           uuid PRIMARY KEY,
    route_id     uuid        NOT NULL REFERENCES delivery_routes(id) ON DELETE CASCADE,
    order_id     uuid        NOT NULL REFERENCES orders(id),
    -- 1-based visiting order from OSRM. The roastery is not a stop.
    position     integer     NOT NULL CHECK (position > 0),
    address      text        NOT NULL,
    lat          double precision NOT NULL,
    lng          double precision NOT NULL,
    customer_name text       NOT NULL DEFAULT '',
    channel      text        NOT NULL DEFAULT 'retail',
    -- 'skipped' means the driver had good reason to pass this stop today (a
    -- mistake on the order, nobody home). It is a route-level outcome only:
    -- the order is untouched, stays in the delivery queue, and rolls onto the
    -- next run's route. Only 'delivered' touches the order.
    status       text        NOT NULL DEFAULT 'pending'
                              CHECK (status IN ('pending', 'delivered', 'skipped')),
    skip_reason  text        NOT NULL DEFAULT '',
    notes        text        NOT NULL DEFAULT '',
    delivered_at timestamptz
);

-- An order appears at most once on a route. Without this, a double-submit of
-- the plan action could put the same delivery on the sheet twice and send the
-- driver back to a house they already visited.
CREATE UNIQUE INDEX idx_route_stops_route_order ON route_stops (route_id, order_id);
CREATE UNIQUE INDEX idx_route_stops_route_position ON route_stops (route_id, position);
CREATE INDEX idx_route_stops_order ON route_stops (order_id);

-- +goose Down
DROP TABLE IF EXISTS route_stops;
DROP TABLE IF EXISTS delivery_routes;
