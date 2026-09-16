-- +goose Up

-- The realm ID is encrypted at rest from now on, alongside the tokens it sits
-- beside. Intuit's security requirements name it explicitly — "encrypt and
-- store the refresh token and realmID" — and it was the one field in that
-- table still readable in a database backup.
--
-- Any row written before this migration holds a plaintext realm, which the
-- decrypt on read will refuse. There is no way to tell the two apart in SQL
-- (ciphertext is base64 text and so is nothing else here), so rather than
-- carry a second spelling forever — a shape this codebase has paid for before
-- with shipping_method — the stored connections are deleted and the shop
-- reconnects once. Reconnecting is a two-click OAuth round trip from
-- Settings → Integrations.
--
-- Production is unaffected: it holds zero rows, because QuickBooks has never
-- been connected there (verified 2026-09-15). A development or staging box
-- with a sandbox connection will need that one reconnect.
--
-- Not reversible in the useful sense: down re-creates the table's emptiness,
-- not the credentials. That is correct — a rollback should never resurrect a
-- bearer token the operator believes they revoked.
--
-- Note for whoever next touches this table: the UNIQUE constraint on
-- realm_id (migration 031) stopped meaning anything the moment the column
-- became ciphertext. AES-GCM uses a random nonce, so the same realm encrypts
-- to a different value every time and the constraint can no longer prevent two
-- tenants sharing a company file, which is what it was written to do. It is
-- left in place here deliberately — a security fix going to production is the
-- wrong change to widen into schema cleanup, and at one tenant, with an upsert
-- keyed on tenant_id, it protects nothing either way. Drop it when the table
-- is next altered for its own reasons.
DELETE FROM qb_credentials;

-- +goose Down
-- Nothing to restore. The rows above were deleted deliberately and their
-- tokens must be re-issued by Intuit, not recovered from a migration.
SELECT 1;
