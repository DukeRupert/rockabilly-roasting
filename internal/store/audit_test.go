package store_test

import (
	"context"
	"fmt"
	"sort"
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

	// Count deliberately ignores limit and offset, so the two only have to
	// agree when the filter is not paging. Paginated cases assert the total
	// themselves.
	if f.Limit == 0 && f.Offset == 0 {
		count, err := entries.Count(ctx, tx, f)
		require.NoError(t, err)
		assert.Equal(t, len(rows), count,
			"Count and List must read the same filter — the pagination total sits directly under these rows")
	}

	actions := make([]string, len(rows))
	for i, r := range rows {
		actions[i] = r.Action
	}
	return actions
}

// seedAuditFixture plants five entries spread across actors, areas, resources,
// and time, and returns the staff actor, the order, and the customer.
//
// The two customer rows are shaped the way production shapes them, which the
// first version of this fixture got wrong and hid a hole in the tests. A
// customer's own login is filed against a *session*, and their address edit
// against an *address* — not against the customer — so the customer filter's
// actor half is the only thing that finds them. Filing the login against
// resource_type "customer" made both halves redundant and let a mutation that
// deleted one of them pass the whole suite.
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
	// The customer acting on their own account, filed against the session.
	// Only reachable through actor_id.
	insertAudit(t, ctx, tx, auditSeed{
		actorType: domain.AuditActorTypeCustomer, actorID: &customerID, actorName: "Eddie Cochran",
		action: "customer.login", resourceType: "session", resourceID: uuid.New(),
		at: now.Add(-3 * time.Hour),
	})
	// Staff acting on the customer's account. Only reachable through
	// resource_id — the actor here is not the customer.
	insertAudit(t, ctx, tx, auditSeed{
		actorType: domain.AuditActorTypeStaff, actorID: &staffID, actorName: "Wanda Jackson",
		action: "customer.price_list_updated", resourceType: "customer", resourceID: customerID,
		at: now.Add(-4 * time.Hour),
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
		assert.ElementsMatch(t,
			[]string{"order.refunded", "customer.price_list_updated", "product.updated"}, got)
	})

	t.Run("actor id spans areas", func(t *testing.T) {
		// The point of pinning a person: everything they touched, regardless of
		// what kind of thing it was.
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{ActorID: &staffID})
		assert.ElementsMatch(t,
			[]string{"order.refunded", "customer.price_list_updated", "product.updated"}, got)
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
// customer's own actions live in actor_id against some other resource entirely
// (a session, an address), while staff actions on their account live in
// resource_id with someone else as the actor. Delete either half of that OR and
// real history silently disappears from the page — 52 logins and 83 address
// edits, on the dev copy alone.
func TestAuditListForACustomerReadsBothSides(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	entries := store.NewAuditStore()
	_, _, customerID := seedAuditFixture(t, ctx, tx, time.Now())

	got := listAudit(t, ctx, tx, entries, store.AuditFilter{CustomerID: &customerID})
	assert.ElementsMatch(t,
		[]string{"customer.login", "customer.price_list_updated"}, got,
		"what they did and what was done to them are both their history")

	// Each half named separately, so a failure says which one broke rather
	// than just that the count is off.
	assert.Contains(t, got, "customer.login",
		"the login is filed against a session — only actor_id finds it")
	assert.Contains(t, got, "customer.price_list_updated",
		"the price list change was made by staff — only resource_id finds it")

	// The detail page's timeline must agree with the audit log's filter — they
	// share one definition, and this is what pins that.
	timeline, err := entries.ListForCustomer(ctx, tx, customerID, 25)
	require.NoError(t, err)
	require.Len(t, timeline, 2)
	assert.Equal(t, "customer.login", timeline[0].Action, "newest first")
}

func TestAuditListSearch(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	entries := store.NewAuditStore()
	now := time.Now()
	staffID, orderID, _ := seedAuditFixture(t, ctx, tx, now)

	t.Run("matches a partial name, case-insensitively", func(t *testing.T) {
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{Search: "wanda"})
		assert.ElementsMatch(t,
			[]string{"order.refunded", "customer.price_list_updated", "product.updated"}, got)
	})

	t.Run("matches a word in the action", func(t *testing.T) {
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{Search: "refund"})
		assert.Equal(t, []string{"order.refunded"}, got)
	})

	t.Run("a full uuid searches identifiers, not text", func(t *testing.T) {
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{Search: orderID.String()})
		assert.ElementsMatch(t, []string{"order.refunded", "email.shipment_sent"}, got)

		byActor := listAudit(t, ctx, tx, entries, store.AuditFilter{Search: staffID.String()})
		assert.ElementsMatch(t,
			[]string{"order.refunded", "customer.price_list_updated", "product.updated"}, byActor)
	})

	t.Run("the 8-character fragment the list displays is searchable", func(t *testing.T) {
		// Staff copy what is on screen. If that fragment found nothing we would
		// be rendering an identifier nobody can look up.
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{Search: orderID.String()[:8]})
		assert.ElementsMatch(t, []string{"order.refunded", "email.shipment_sent"}, got)
	})

	t.Run("the floor is exactly the length the list displays", func(t *testing.T) {
		// Bracketing, not sampling. Seven hex characters is below the floor, so
		// it is read as text and matches nothing; eight is at the floor and
		// finds the record. Together these pin the boundary to 8 — the earlier
		// version of this test probed with "added" and "cochran" and passed at
		// every threshold, because neither term changes branch at any value
		// anyone would mutate it to.
		seven := listAudit(t, ctx, tx, entries, store.AuditFilter{Search: orderID.String()[:7]})
		assert.Empty(t, seven, "seven characters is a word, not the fragment the list shows")

		eight := listAudit(t, ctx, tx, entries, store.AuditFilter{Search: orderID.String()[:8]})
		assert.ElementsMatch(t, []string{"order.refunded", "email.shipment_sent"}, eight)
	})

	t.Run("an ordinary word made only of hex letters is still text", func(t *testing.T) {
		// The real-world case the floor exists for: plenty of English words are
		// nothing but a-f, and none of them should be read as an identifier.
		// Seeded here rather than in the shared fixture so the name can be
		// chosen purely for its letters without skewing every other count.
		insertAudit(t, ctx, tx, auditSeed{
			actorType: domain.AuditActorTypeStaff, actorID: &staffID, actorName: "Facade Deed",
			action: "staff.login", resourceType: "session", resourceID: uuid.New(),
			at: time.Now().Add(-5 * time.Minute),
		})
		got := listAudit(t, ctx, tx, entries, store.AuditFilter{Search: "facade"})
		assert.Equal(t, []string{"staff.login"}, got,
			"six hex letters is a word — searching it must match the name, not an id")

		got = listAudit(t, ctx, tx, entries, store.AuditFilter{Search: "cochran"})
		assert.Equal(t, []string{"customer.login"}, got)
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
		[]string{"order.refunded", "email.shipment_sent", "customer.login", "customer.price_list_updated"},
		recent, "the 40-day-old product edit is outside the window")

	to := now.AddDate(0, 0, -30)
	old := listAudit(t, ctx, tx, entries, store.AuditFilter{To: &to})
	assert.Equal(t, []string{"product.updated"}, old)

	newest := listAudit(t, ctx, tx, entries, store.AuditFilter{From: &from})
	assert.Equal(t, "order.refunded", newest[0], "newest first is the default")

	oldest := listAudit(t, ctx, tx, entries, store.AuditFilter{From: &from, Sort: store.AuditSortOldest})
	assert.Equal(t, "customer.price_list_updated", oldest[0])
}

func TestAuditFacetsComeFromTheLog(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	entries := store.NewAuditStore()
	_, _, _ = seedAuditFixture(t, ctx, tx, time.Now())

	areas, err := entries.ListActionAreas(ctx, tx)
	require.NoError(t, err)
	assert.Subset(t, areas, []string{"order", "email", "customer", "product"})
	assert.True(t, sort.StringsAreSorted(areas), "the dropdown renders in this order")

	resources, err := entries.ListResourceTypes(ctx, tx)
	require.NoError(t, err)
	assert.Subset(t, resources, []string{"order", "customer", "product", "session"})
	assert.True(t, sort.StringsAreSorted(resources), "the dropdown renders in this order")

	// Both facet queries are recursive loose index scans, which is easy to get
	// subtly wrong: a broken recursion returns the first value and stops.
	assert.Greater(t, len(areas), 1, "the walk must not stop after the first area")
	assert.Greater(t, len(resources), 1, "the walk must not stop after the first resource type")

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
	// Distinct reasons, one per row, so a page can be identified rather than
	// merely counted. Asserting only the length let a disabled OFFSET through:
	// with five rows and a limit of two, every page is two rows long.
	for i := range 5 {
		insertAudit(t, ctx, tx, auditSeed{
			actorType: domain.AuditActorTypeSystem, actorName: "system",
			action:       fmt.Sprintf("job.retried_%d", i),
			resourceType: "job", resourceID: resourceID,
			at: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	f := store.AuditFilter{ResourceID: &resourceID, Limit: 2}

	first := f
	first.Offset = 0
	assert.Equal(t, []string{"job.retried_0", "job.retried_1"},
		listAudit(t, ctx, tx, entries, first), "page 1, newest first")

	second := f
	second.Offset = 2
	assert.Equal(t, []string{"job.retried_2", "job.retried_3"},
		listAudit(t, ctx, tx, entries, second), "page 2 must skip page 1, not repeat it")

	last := f
	last.Offset = 4
	assert.Equal(t, []string{"job.retried_4"},
		listAudit(t, ctx, tx, entries, last), "a short final page")

	// Count must ignore limit and offset, or "3-4 of 2" appears under the rows.
	total, err := entries.Count(ctx, tx, second)
	require.NoError(t, err)
	assert.Equal(t, 5, total)
}

// Narrows decides whether the page counts at all, so an unfiltered filter
// reporting true would put the whole-log scan straight back on every request.
func TestAuditFilterNarrowsOnlyWhenSomethingIsSet(t *testing.T) {
	assert.False(t, store.AuditFilter{}.Narrows())
	assert.False(t, store.AuditFilter{Limit: 50, Offset: 100, Sort: store.AuditSortOldest}.Narrows(),
		"paging and sorting are not narrowing — they reorder the same set")

	id := uuid.New()
	at := time.Now()
	for name, f := range map[string]store.AuditFilter{
		"search":        {Search: "wanda"},
		"actor type":    {ActorType: ptr("staff")},
		"actor id":      {ActorID: &id},
		"action":        {Action: ptr("order.refunded")},
		"area":          {ActionArea: ptr("order")},
		"resource type": {ResourceType: ptr("order")},
		"resource id":   {ResourceID: &id},
		"customer":      {CustomerID: &id},
		"from":          {From: &at},
		"to":            {To: &at},
	} {
		assert.True(t, f.Narrows(), "%s narrows the log", name)
	}
}

func ptr[T any](v T) *T { return &v }
