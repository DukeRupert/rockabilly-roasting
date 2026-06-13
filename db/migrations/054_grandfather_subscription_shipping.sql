-- +goose Up

-- Grandfather every subscription that exists right now. These customers signed
-- up when subscription renewals charged no shipping; the P2 change (renewals
-- now price shipping like a retail order) would otherwise raise their next
-- charge with no notice. The shipping_grandfathered flag tells the renewal
-- engine to keep their shipping free. Subscriptions created after this point
-- (through the post-P2 subscribe flow) carry no flag and pay shipping normally.
--
-- Scoped to renewable states — cancelled/expired subscriptions never renew, so
-- there's no reason to touch their rows (or bump updated_at).
UPDATE subscriptions
   SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{shipping_grandfathered}', 'true'::jsonb),
       updated_at = now()
 WHERE status IN ('active', 'paused', 'past_due');

-- +goose Down
UPDATE subscriptions
   SET metadata = metadata - 'shipping_grandfathered',
       updated_at = now()
 WHERE metadata ? 'shipping_grandfathered';
