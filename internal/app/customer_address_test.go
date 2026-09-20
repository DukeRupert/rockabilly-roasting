package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// pocatello is the destination that prompted this work, in the spelling a
// browser autofill submits.
func pocatello(customerID uuid.UUID) store.CreateAddressParams {
	return store.CreateAddressParams{
		CustomerID:  &customerID,
		FirstName:   "Jahnavi",
		LastName:    "Lewis",
		Line1:       "724 S 3rd Ave",
		City:        "Pocatello",
		State:       "Idaho",
		PostalCode:  "83201-6529",
		CountryCode: "us",
	}
}

// TestCreateAddress_StoresCanonicalForm is the guarantee that the service, not
// the seven call sites, owns canonicalization.
func TestCreateAddress_StoresCanonicalForm(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	addr, err := svc.CreateAddress(ctx, tx, pocatello(customer.ID), testActor(customer.ID))
	require.NoError(t, err)

	assert.Equal(t, "ID", addr.State)
	assert.Equal(t, "83201", addr.PostalCode)
	assert.Equal(t, "US", addr.CountryCode)

	// Read back through the store: the canonical form is what landed in the
	// table, not just what the service handed back.
	reloaded, err := svc.GetAddress(ctx, tx, addr.ID, customer.ID)
	require.NoError(t, err)
	assert.Equal(t, "ID", reloaded.State)
	assert.Equal(t, "83201", reloaded.PostalCode)
	assert.Equal(t, "US", reloaded.CountryCode)
}

// TestCreateAddress_PreservesLabelFields pins that normalization touches only
// the three comparison fields. Names and street lines go on a shipping label
// and are stored as typed.
func TestCreateAddress_PreservesLabelFields(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	addr, err := svc.CreateAddress(ctx, tx, pocatello(customer.ID), testActor(customer.ID))
	require.NoError(t, err)

	assert.Equal(t, "Jahnavi", addr.FirstName)
	assert.Equal(t, "Lewis", addr.LastName)
	assert.Equal(t, "724 S 3rd Ave", addr.Line1)
	assert.Equal(t, "Pocatello", addr.City)
}

// TestCreateAddress_AlwaysInserts pins that CreateAddress does not dedup. Its
// name and its audit entry both promise a row was added.
func TestCreateAddress_AlwaysInserts(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	first, err := svc.CreateAddress(ctx, tx, pocatello(customer.ID), testActor(customer.ID))
	require.NoError(t, err)
	second, err := svc.CreateAddress(ctx, tx, pocatello(customer.ID), testActor(customer.ID))
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID)
	testutil.LastAuditEntryWithAction(t, tx, "address", second.ID, audit.AuditCustomerAddressAdded)
}

func TestFindOrCreateAddress_CreatesWhenAbsent(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	addr, err := svc.FindOrCreateAddress(ctx, tx, customer.ID, pocatello(customer.ID), testActor(customer.ID))
	require.NoError(t, err)

	assert.Equal(t, "ID", addr.State)
	assert.Equal(t, "83201", addr.PostalCode)
	testutil.LastAuditEntryWithAction(t, tx, "address", addr.ID, audit.AuditCustomerAddressAdded)
}

// TestFindOrCreateAddress_IsIdempotent is the bug this work exists to fix: a
// returning customer checking out repeatedly must not accumulate copies.
func TestFindOrCreateAddress_IsIdempotent(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	actor := testActor(customer.ID)

	first, err := svc.FindOrCreateAddress(ctx, tx, customer.ID, pocatello(customer.ID), actor)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		again, err := svc.FindOrCreateAddress(ctx, tx, customer.ID, pocatello(customer.ID), actor)
		require.NoError(t, err)
		assert.Equal(t, first.ID, again.ID, "call %d created a duplicate", i+2)
	}

	all, err := svc.ListAddresses(ctx, tx, customer.ID)
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

// TestFindOrCreateAddress_MatchesLegacyRow is the guarantee that makes
// forward-only dedup work without a backfill. The stored row predates
// canonicalization and is spelled "Idaho / 83201-6529"; a fresh canonical
// submission of the same house must match it rather than mint a fifth copy.
func TestFindOrCreateAddress_MatchesLegacyRow(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	legacy := testutil.CreateAddress(t, tx, customer.ID,
		testutil.WithAddressName("Jahnavi", "Lewis"),
		testutil.WithAddressLine1("724 S 3rd Ave"),
		testutil.WithAddressCity("Pocatello"),
		testutil.WithAddressState("Idaho"),
		testutil.WithAddressPostalCode("83201-6529"),
	)

	got, err := svc.FindOrCreateAddress(ctx, tx, customer.ID, pocatello(customer.ID), testActor(customer.ID))
	require.NoError(t, err)
	assert.Equal(t, legacy.ID, got.ID)

	// Returned as stored. Forward-only means history is not rewritten: this row
	// is the shipping record for whatever orders already point at it.
	assert.Equal(t, "Idaho", got.State)
	assert.Equal(t, "83201-6529", got.PostalCode)
}

// TestFindOrCreateAddress_MatchesLegacyRowWithUnit covers the apartment number
// on the STORED side. The other legacy-row test has no line2, and every other
// line2 case keys two freshly-created rows -- so without this, a projection
// that dropped line2 would still match a stored unit against a different one
// and ship to the wrong door in the same building.
func TestFindOrCreateAddress_MatchesLegacyRowWithUnit(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	unit2 := testutil.CreateAddress(t, tx, customer.ID,
		testutil.WithAddressName("Jahnavi", "Lewis"),
		testutil.WithAddressLine1("724 S 3rd Ave"),
		testutil.WithAddressLine2("Apt 2"),
		testutil.WithAddressCity("Pocatello"),
		testutil.WithAddressState("Idaho"),
		testutil.WithAddressPostalCode("83201-6529"),
	)

	same := pocatello(customer.ID)
	apt2 := "Apt 2"
	same.Line2 = &apt2
	got, err := svc.FindOrCreateAddress(ctx, tx, customer.ID, same, testActor(customer.ID))
	require.NoError(t, err)
	assert.Equal(t, unit2.ID, got.ID, "same unit should match the stored legacy row")

	other := pocatello(customer.ID)
	apt3 := "Apt 3"
	other.Line2 = &apt3
	got3, err := svc.FindOrCreateAddress(ctx, tx, customer.ID, other, testActor(customer.ID))
	require.NoError(t, err)
	assert.NotEqual(t, unit2.ID, got3.ID, "a different unit is a different door")
}

// TestFindOrCreateAddress_MatchRecordsNoAudit -- nothing changed, so nothing is
// recorded. An address_added entry for a row that already existed would be a
// lie in the audit log.
func TestFindOrCreateAddress_MatchRecordsNoAudit(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	legacy := testutil.CreateAddress(t, tx, customer.ID,
		testutil.WithAddressName("Jahnavi", "Lewis"),
		testutil.WithAddressLine1("724 S 3rd Ave"),
		testutil.WithAddressCity("Pocatello"),
		testutil.WithAddressState("Idaho"),
		testutil.WithAddressPostalCode("83201-6529"),
	)

	_, err := svc.FindOrCreateAddress(ctx, tx, customer.ID, pocatello(customer.ID), testActor(customer.ID))
	require.NoError(t, err)

	testutil.AssertNoAuditEntry(t, tx, "address", legacy.ID)
}

// TestFindOrCreateAddress_LeavesIsDefaultAlone protects the comment at
// admin_equipment.go: a machine's location is the last address that should
// silently become where a customer's coffee gets sent.
func TestFindOrCreateAddress_LeavesIsDefaultAlone(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	existing := testutil.CreateAddress(t, tx, customer.ID,
		testutil.WithAddressName("Jahnavi", "Lewis"),
		testutil.WithAddressLine1("724 S 3rd Ave"),
		testutil.WithAddressCity("Pocatello"),
		testutil.WithAddressState("ID"),
		testutil.WithAddressPostalCode("83201"),
		testutil.WithAddressDefault(),
	)
	require.True(t, existing.IsDefault)

	// Equipment siting submits IsDefault:false for the same place.
	p := pocatello(customer.ID)
	p.IsDefault = false
	got, err := svc.FindOrCreateAddress(ctx, tx, customer.ID, p, testActor(customer.ID))
	require.NoError(t, err)

	assert.Equal(t, existing.ID, got.ID)
	assert.True(t, got.IsDefault, "a match must not demote the customer's default address")

	reloaded, err := svc.GetAddress(ctx, tx, existing.ID, customer.ID)
	require.NoError(t, err)
	assert.True(t, reloaded.IsDefault)
}

// TestFindOrCreateAddress_ScopedToOneCustomer -- two customers at one address
// (a couple, an office) each keep their own row.
func TestFindOrCreateAddress_ScopedToOneCustomer(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	alice := testutil.CreateCustomer(t, tx)
	bob := testutil.CreateCustomer(t, tx)

	aliceAddr, err := svc.FindOrCreateAddress(ctx, tx, alice.ID, pocatello(alice.ID), testActor(alice.ID))
	require.NoError(t, err)
	bobAddr, err := svc.FindOrCreateAddress(ctx, tx, bob.ID, pocatello(bob.ID), testActor(bob.ID))
	require.NoError(t, err)

	assert.NotEqual(t, aliceAddr.ID, bobAddr.ID)
}

// TestFindOrCreateAddress_DistinctDestinations -- dedup must not overreach.
func TestFindOrCreateAddress_DistinctDestinations(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	actor := testActor(customer.ID)

	base := pocatello(customer.ID)
	first, err := svc.FindOrCreateAddress(ctx, tx, customer.ID, base, actor)
	require.NoError(t, err)

	unit2 := pocatello(customer.ID)
	apt := "Apt 2"
	unit2.Line2 = &apt
	second, err := svc.FindOrCreateAddress(ctx, tx, customer.ID, unit2, actor)
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, second.ID, "a unit number is a different delivery stop")

	housemate := pocatello(customer.ID)
	housemate.FirstName = "Marcus"
	third, err := svc.FindOrCreateAddress(ctx, tx, customer.ID, housemate, actor)
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, third.ID, "a second occupant must keep their own name on a label")
}

func TestFindOrCreateAddress_RejectsBadScope(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	other := testutil.CreateCustomer(t, tx)

	// Both params must be nil-scoped, or the mismatch guard below fires first
	// and this passes without ever reaching the nil guard it names.
	t.Run("nil customer id", func(t *testing.T) {
		p := pocatello(customer.ID)
		p.CustomerID = nil
		_, err := svc.FindOrCreateAddress(ctx, tx, uuid.Nil, p, testActor(customer.ID))
		assert.ErrorContains(t, err, "customer id is required")
	})

	t.Run("params disagree with scope", func(t *testing.T) {
		_, err := svc.FindOrCreateAddress(ctx, tx, customer.ID, pocatello(other.ID), testActor(customer.ID))
		assert.Error(t, err, "matching one customer's book then writing onto another's is a caller bug")
	})
}

// TestUpdateAddress_StoresCanonicalForm -- an edit that reintroduced "Idaho"
// would start a fresh duplicate lineage on the customer's next order.
func TestUpdateAddress_StoresCanonicalForm(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	addr, err := svc.CreateAddress(ctx, tx, pocatello(customer.ID), testActor(customer.ID))
	require.NoError(t, err)

	edit := pocatello(customer.ID)
	edit.Line1 = "724 S 3rd Ave Rear"
	updated, err := svc.UpdateAddress(ctx, tx, addr.ID, customer.ID, edit, testActor(customer.ID))
	require.NoError(t, err)

	assert.Equal(t, "ID", updated.State)
	assert.Equal(t, "83201", updated.PostalCode)
	assert.Equal(t, "US", updated.CountryCode)
}

// TestFindOrCreateAddress_KeyAgreesWithDomain guards the seam between the
// service and domain.AddressKey: the service keys create-params, domain keys
// stored rows, and a field dropped from the projection would silently make
// every address look unique.
func TestFindOrCreateAddress_KeyAgreesWithDomain(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	p := pocatello(customer.ID)
	apt := "Apt 2"
	p.Line2 = &apt

	addr, err := svc.FindOrCreateAddress(ctx, tx, customer.ID, p, testActor(customer.ID))
	require.NoError(t, err)

	want := domain.AddressKey(domain.Address{
		FirstName: p.FirstName, LastName: p.LastName,
		Line1: p.Line1, Line2: p.Line2, City: p.City,
		State: p.State, PostalCode: p.PostalCode, CountryCode: p.CountryCode,
	})
	assert.Equal(t, want, domain.AddressKey(*addr))
}
