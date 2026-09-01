package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// The audit list builds its WHERE clause and placeholder numbering at runtime
// from whichever filters are set, which is exactly the code a real Postgres has
// to vouch for: a mis-numbered placeholder or a clause that quietly matches
// nothing produces an empty page, not an error, and an empty audit log looks
// plausible. These tests pin each dimension to a row it must and must not find.

// auditSeed is one entry to plant, with an explicit created_at — the column
// defaults to now(), so the date filters can only be tested by writing it.
type auditSeed struct {
	actorType    domain.AuditActorType
	actorID      *uuid.UUID
	actorName    string
	action       string
	resourceType string
	resourceID   uuid.UUID
	at           time.Time
}

func insertAudit(t *testing.T, ctx context.Context, tx pgx.Tx, s auditSeed) {
	t.Helper()
	_, err := tx.Exec(ctx,
		`INSERT INTO audit_log (id, actor_type, actor_id, actor_name, action, resource_type, resource_id, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		uuid.New(), string(s.actorType), s.actorID, s.actorName, s.action, s.resourceType, s.resourceID, s.at)
	require.NoError(t, err)
}

// listAudit runs the filter and returns the actions it matched, which is
// enough to identify every seeded row.
func listAudit(t *testing.T, ctx context.Context, tx pgx.Tx, entries *store.AuditStore, f store.AuditFilter) []string {
	t.Helper()
	rows, err := entries.List(ctx, tx, f)
	require.NoError(t, err)

	count, err := entries.Count(ctx, tx, f)
	require.NoError(t, err)
	assert.Equal(t, len(rows), count,
		"Count and List must read the same filter — the pagination total sits directly under these rows")

	actions := make([]string, len(rows))
	for i, r := range rows {
		actions[i] = r.Action
	}
	return actions
}

// seedAuditFixture plants four entries spread across actors, areas, resources,
// and time, and returns the staff actor and the order they mostly concern.
func seedAuditFixture(t *testing.T, ctx context.Context, tx pgx.Tx, now time.Time) (staffID, orderID, customerID uuid.UUID) {
	t.Helper()
	staffID = uuid.New()
	customerID = uuid.New()
	orderID = uuid.New()
	productID := uuid.New()

	insertAudit(t, ctx, tx, auditSeed{
		actorType: domain.AuditActorTypeStaff, actorID: &staffID, actorName: "Wanda Jackson",
		action: "order.refunded", resourceType: "order", resourceID: orderID,
		at: now.Add(-1 * time.Hour),
	})
	insertAudit(t, ctx, tx, auditSeed{
		actorType: domain.AuditActorTypeSystem, actorName: "system",
		action: "email.shipment_sent", resourceType: "order", resourceID: orderID,
		at: now.Add(-2 * time.Hour),
	})
	insertAudit(t, ctx, tx, auditSeed{
		actorType: domain.AuditActorTypeCustomer, actorID: &customerID, actorName: "Eddie Cochran",
		action: "customer.logged_in", resourceType: "customer", resourceID: customerID,
		at: now.Add(-3 * time.Hour),
	})
	insertAudit(t, ctx, tx, auditSeed{
		actorType: domain.AuditActorTypeStaff, actorID: &staffID, actorName: "Wanda Jackson",
		action: "product.updated", resourceType: "product", resourceID: productID,
		at: now.AddDate(0, 0, -40),
	})
	return staffID, orderID, customerID
}

func TestAuditListFiltersByActorAreaAndResource(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	entries := store.NewAuditStore()
	now := time.Now()
	staffID, orderID, _ := seedAuditFixture(t, ctx, tx, now)

	t.Run("actor type", func(t *testing.T) {
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{ActorType: ptr("staff")})
		assert.ElementsMatch(t, []string{"order.refunded", "product.updated"}, got)
	})

	t.Run("actor id spans areas", func(t *testing.T) {
		// The point of pinning a person: everything they touched, regardless of
		// what kind of thing it was.
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{ActorID: &staffID})
		assert.ElementsMatch(t, []string{"order.refunded", "product.updated"}, got)
	})

	t.Run("action area", func(t *testing.T) {
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{ActionArea: ptr("order")})
		assert.Equal(t, []string{"order.refunded"}, got,
			"the area is the action's namespace, so the shipment email must not come back")
	})

	t.Run("exact action", func(t *testing.T) {
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{Action: ptr("order.refunded")})
		assert.Equal(t, []string{"order.refunded"}, got)
	})

	t.Run("resource type is not the action area", func(t *testing.T) {
		// The distinction the two controls exist for: an email about a shipment
		// is filed against the order it shipped.
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{ResourceType: ptr("order")})
		assert.ElementsMatch(t, []string{"order.refunded", "email.shipment_sent"}, got)
	})

	t.Run("resource id", func(t *testing.T) {
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{ResourceID: &orderID})
		assert.ElementsMatch(t, []string{"order.refunded", "email.shipment_sent"}, got)
	})

	t.Run("filters compose", func(t *testing.T) {
		// Two filters at once is where runtime placeholder numbering goes wrong.
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{
			ActorType:    ptr("staff"),
			ResourceType: ptr("order"),
		})
		assert.Equal(t, []string{"order.refunded"}, got)
	})
}

// The customer filter is the one dimension that reads two columns at once: a
// customer's own logins live in actor_id, while a staff approval of their
// account lives in resource_id. Getting either half wrong loses history that
// staff will assume never happened.
func TestAuditListForACustomerReadsBothSides(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	entries := store.NewAuditStore()
	now := time.Now()
	staffID, _, customerID := seedAuditFixture(t, ctx, tx, now)

	// A staff action recorded against the customer, not by them.
	insertAudit(t, ctx, tx, auditSeed{
		actorType: domain.AuditActorTypeStaff, actorID: &staffID, actorName: "Wanda Jackson",
		action: "wholesale.application_approved", resourceType: "customer", resourceID: customerID,
		at: now.Add(-30 * time.Minute),
	})

	got := listAudit(t, ctx, tx, entries, store.AuditFilter{CustomerID: &customerID})
	assert.ElementsMatch(t,
		[]string{"customer.logged_in", "wholesale.application_approved"}, got,
		"what they did and what was done to them are both their history")

	// The detail page's timeline must agree with the audit log's filter — they
	// share one definition, and this is what pins that.
	timeline, err := entries.ListForCustomer(ctx, tx, customerID, 25)
	require.NoError(t, err)
	require.Len(t, timeline, 2)
	assert.Equal(t, "wholesale.application_approved", timeline[0].Action, "newest first")
}

func TestAuditListSearch(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	entries := store.NewAuditStore()
	now := time.Now()
	staffID, orderID, _ := seedAuditFixture(t, ctx, tx, now)

	t.Run("matches a partial name, case-insensitively", func(t *testing.T) {
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{Search: "wanda"})
		assert.ElementsMatch(t, []string{"order.refunded", "product.updated"}, got)
	})

	t.Run("matches a word in the action", func(t *testing.T) {
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{Search: "refund"})
		assert.Equal(t, []string{"order.refunded"}, got)
	})

	t.Run("a full uuid searches identifiers, not text", func(t *testing.T) {
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{Search: orderID.String()})
		assert.ElementsMatch(t, []string{"order.refunded", "email.shipment_sent"}, got)

		byActor := listAudit(t, ctx, tx, entries, store.AuditFilter{Search: staffID.String()})
		assert.ElementsMatch(t, []string{"order.refunded", "product.updated"}, byActor)
	})

	t.Run("the 8-character fragment the list displays is searchable", func(t *testing.T) {
		// Staff copy what is on screen. If that fragment found nothing we would
		// be rendering an identifier nobody can look up.
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{Search: orderID.String()[:8]})
		assert.ElementsMatch(t, []string{"order.refunded", "email.shipment_sent"}, got)
	})

	t.Run("a short hex-looking word is still text", func(t *testing.T) {
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{Search: "added"})
		assert.Empty(t, got, "no seeded action contains 'added'")

		got = listAudit(t, ctx, tx, entries, store.AuditFilter{Search: "cochran"})
		assert.Equal(t, []string{"customer.logged_in"}, got)
	})
}

func TestAuditListDateBoundsAndSort(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	entries := store.NewAuditStore()
	now := time.Now()
	_, _, _ = seedAuditFixture(t, ctx, tx, now)

	from := now.AddDate(0, 0, -7)
	recent := listAudit(t, ctx, tx, entries, store.AuditFilter{From: &from})
	assert.ElementsMatch(t,
		[]string{"order.refunded", "email.shipment_sent", "customer.logged_in"}, recent,
		"the 40-day-old product edit is outside the window")

	to := now.AddDate(0, 0, -30)
	old := listAudit(t, ctx, tx, entries, store.AuditFilter{To: &to})
	assert.Equal(t, []string{"product.updated"}, old)

	newest := listAudit(t, ctx, tx, entries, store.AuditFilter{From: &from})
	assert.Equal(t, "order.refunded", newest[0], "newest first is the default")

	oldest := listAudit(t, ctx, tx, entries, store.AuditFilter{From: &from, Sort: store.AuditSortOldest})
	assert.Equal(t, "customer.logged_in", oldest[0])
}

func TestAuditFacetsComeFromTheLog(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	entries := store.NewAuditStore()
	_, _, _ = seedAuditFixture(t, ctx, tx, time.Now())

	areas, err := entries.ListActionAreas(ctx, tx)
	require.NoError(t, err)
	assert.Subset(t, areas, []string{"order", "email", "customer", "product"})

	resources, err := entries.ListResourceTypes(ctx, tx)
	require.NoError(t, err)
	assert.Subset(t, resources, []string{"order", "customer", "product"})

	// The second dropdown only ever shows the actions inside the chosen area —
	// that is what keeps it from being a list of every action in the system.
	actions, err := entries.ListActionsInArea(ctx, tx, "order")
	require.NoError(t, err)
	assert.Equal(t, []string{"order.refunded"}, actions)
}

func TestAuditListPaginates(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	entries := store.NewAuditStore()
	resourceID := uuid.New()
	now := time.Now()
	for i := range 5 {
		insertAudit(t, ctx, tx, auditSeed{
			actorType: domain.AuditActorTypeSystem, actorName: "system",
			action:       "job.retried",
			resourceType: "job", resourceID: resourceID,
			at: now.Add(-time.Duration(i) * time.Minute),
		})
	}

	f := store.AuditFilter{ResourceID: &resourceID, Limit: 2, Offset: 2}
	page, err := entries.List(ctx, tx, f)
	require.NoError(t, err)
	assert.Len(t, page, 2)

	// Count must ignore limit and offset, or "3–4 of 2" appears under the rows.
	total, err := entries.Count(ctx, tx, f)
	require.NoError(t, err)
	assert.Equal(t, 5, total)
}

func ptr[T any](v T) *T { return &v }
