-- +goose Up

-- Cards the bank refused for good: lost, stolen, closed. Charging one again
-- earns a card-network fine and sours the issuer on this customer's next
-- legitimate charge, so a renewal never charges a card listed here.
--
-- Kept per customer, not per subscription, because the card is the customer's.
-- The list used to live in each subscription's metadata, so a customer's other
-- subscriptions knew nothing of it: the next one to renew charged the dead card
-- again, and the batch renewal path charged with no list at all.
--
-- Rows are never cleared. Stripe gives a card a new payment method id each time
-- it is added, so a customer who puts the same card back on file is charging a
-- new id, and a renewal that succeeds on another card says nothing about this
-- one. Deleting the customer deletes their rows.
CREATE TABLE customer_dead_payment_methods (
    customer_id       uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    payment_method_id text NOT NULL,
    -- The issuer's reason when it gave one, for staff.
    decline_code      text NOT NULL DEFAULT '',
    recorded_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, payment_method_id)
);

-- Every card a subscription had recorded, under both the list key and the
-- single-card key an earlier build wrote. Subscriptions in every status: a
-- card the bank refused while a subscription was past due is no less dead now
-- that the subscription has ended.
INSERT INTO customer_dead_payment_methods (customer_id, payment_method_id, decline_code, recorded_at)
SELECT DISTINCT ON (customer_id, pm) customer_id, pm, decline_code, updated_at
FROM (
    SELECT s.customer_id, pm.value AS pm,
           COALESCE(s.metadata->>'dunning_decline_code', '') AS decline_code,
           s.updated_at
    FROM subscriptions s,
         jsonb_array_elements_text(
             CASE WHEN jsonb_typeof(s.metadata->'dunning_dead_payment_methods') = 'array'
                  THEN s.metadata->'dunning_dead_payment_methods'
                  ELSE '[]'::jsonb END) AS pm(value)
    UNION ALL
    SELECT s.customer_id, s.metadata->>'dunning_dead_payment_method',
           COALESCE(s.metadata->>'dunning_decline_code', ''),
           s.updated_at
    FROM subscriptions s
    WHERE jsonb_typeof(s.metadata->'dunning_dead_payment_method') = 'string'
) recorded
WHERE pm <> ''
ORDER BY customer_id, pm, updated_at;

-- +goose Down

-- Not a clean undo. The Up leaves the subscriptions' own lists in place, but
-- nothing writes them after it, and a successful renewal still wipes them. So
-- after a rollback, a subscription latched since the Up stays blocked even on
-- a new card, and one whose latch lifted since can batch onto a dead card.
DROP TABLE customer_dead_payment_methods;
