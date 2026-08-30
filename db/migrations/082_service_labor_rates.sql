-- +goose Up

-- What an hour of the crew's time costs the shop, so the service cost reports
-- can put parts and hours into one number.
--
-- These are *cost* rates, not prices. The reports they feed say plainly that
-- they measure what work cost the shop rather than what it earned, and a
-- charge-out rate dropped into that column would quietly turn a cost report
-- into a revenue one. Billing a ticket out — still unbuilt — will want its own
-- charge rate when it lands; it is deliberately not this column.
--
-- Nullable rather than defaulted to zero. There is no rate a shop can be
-- assumed to have, and a zero would render as "$0.00 of labour" — a number that
-- looks measured. Null means unset, and the reports go on showing hours and
-- parts separately, exactly as they did before this migration.
ALTER TABLE store_settings
    ADD COLUMN service_labor_rate_cents  integer CHECK (service_labor_rate_cents >= 0),
    ADD COLUMN service_travel_rate_cents integer CHECK (service_travel_rate_cents >= 0);

COMMENT ON COLUMN store_settings.service_labor_rate_cents IS
    'Loaded cost of one hour of technician time, in cents. NULL = not set; cost reports omit the money column entirely.';

-- Travel is split from labour in service_time_entries because it bills
-- differently, or not at all. Keeping a separate rate here honours that split
-- instead of overriding it: a shop that pays a driver less than a tech, or
-- absorbs the drive entirely, can say so. NULL falls back to the labour rate,
-- which is the safer default — travel counted at the tech's rate overstates
-- nothing that was not genuinely somebody's hour.
COMMENT ON COLUMN store_settings.service_travel_rate_cents IS
    'Cost of one hour of travel, in cents. NULL falls back to the labour rate.';

-- +goose Down

ALTER TABLE store_settings
    DROP COLUMN IF EXISTS service_travel_rate_cents,
    DROP COLUMN IF EXISTS service_labor_rate_cents;
