-- +goose Up

-- A claim a renewal holds on the subscriptions it is charging, from before it
-- reads them until after it has written the result.
--
-- A solo renewal and a batch renewal are different River job kinds, and a
-- job's kind is part of its unique key, so River's uniqueness cannot stop one
-- running beside the other. After a batch decline every member is past due and
-- every row offers Retry; a customer or staff Retry queued while the batch's
-- own retry was pending charged that member twice. Renewal has three phases —
-- read, charge Stripe, write — and the charge sits between two transactions, so
-- nothing inside either transaction can see the other renewal.
--
-- Why a column and not a Postgres advisory lock. A session lock is held by a
-- connection, so each in-flight renewal would pin a pooled connection across
-- the Stripe call while also needing another for its own transactions. The
-- pool is pgxpool's default size and River runs ten workers: enough renewals
-- at once and every connection is a lock holder waiting for a connection.
--
-- Why a timestamp and not a boolean. A process that dies between claiming and
-- releasing never releases. The claim is a lease: one older than the lease
-- (RenewalService says how long) is treated as abandoned and taken over, so a
-- crash delays that subscription's renewal rather than stopping it for ever.
--
-- Not in subscriptions.metadata with the dunning keys. The claim is taken with
-- one conditional UPDATE over every member of a batch, all or nothing, and that
-- wants a column the WHERE clause can read directly.
ALTER TABLE subscriptions ADD COLUMN renewal_claimed_at timestamptz;

-- +goose Down

ALTER TABLE subscriptions DROP COLUMN renewal_claimed_at;
