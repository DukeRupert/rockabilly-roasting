package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func equipmentParams(customerID uuid.UUID) store.CreateEquipmentParams {
	return store.CreateEquipmentParams{
		CustomerID:   customerID,
		Category:     domain.EquipmentCategoryEspressoMachine,
		Make:         "La Marzocco",
		Model:        "Linea PB",
		SerialNumber: "LM-99172",
		Ownership:    domain.EquipmentOwnershipCustomer,
	}
}

func TestEquipmentCreateStartsActive(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	equip := store.NewEquipmentStore()
	customer := testutil.CreateCustomer(t, tx)

	e, err := equip.Create(ctx, tx, equipmentParams(customer.ID))
	require.NoError(t, err)

	assert.Equal(t, domain.EquipmentStatusActive, e.Status,
		"a machine is registered because it is in service")
	assert.Equal(t, domain.EquipmentCategoryEspressoMachine, e.Category)
	assert.Equal(t, domain.EquipmentOwnershipCustomer, e.Ownership)
	assert.Equal(t, "La Marzocco Linea PB", e.Description())
}

func TestEquipmentGetIsCustomerScoped(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	equip := store.NewEquipmentStore()

	owner := testutil.CreateCustomer(t, tx, testutil.WithEmail("owner@example.test"))
	other := testutil.CreateCustomer(t, tx, testutil.WithEmail("other@example.test"))
	e, err := equip.Create(ctx, tx, equipmentParams(owner.ID))
	require.NoError(t, err)

	got, err := equip.Get(ctx, tx, e.ID, owner.ID)
	require.NoError(t, err)
	assert.Equal(t, e.ID, got.ID)

	// The portal must not be able to read another cafe's machine by guessing an
	// id. This is the whole reason customerID is in the signature.
	_, err = equip.Get(ctx, tx, e.ID, other.ID)
	require.Error(t, err)
}

func TestEquipmentListHidesRetiredByDefault(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	equip := store.NewEquipmentStore()
	customer := testutil.CreateCustomer(t, tx)

	live, err := equip.Create(ctx, tx, equipmentParams(customer.ID))
	require.NoError(t, err)
	gone, err := equip.Create(ctx, tx, equipmentParams(customer.ID))
	require.NoError(t, err)
	_, err = equip.UpdateStatus(ctx, tx, gone.ID, domain.EquipmentStatusRetired)
	require.NoError(t, err)

	listed, err := equip.List(ctx, tx, store.EquipmentFilter{CustomerID: &customer.ID})
	require.NoError(t, err)
	require.Len(t, listed, 1, "the register shows what is out there now")
	assert.Equal(t, live.ID, listed[0].ID)

	// Retired machines are never deleted — the repair history hangs off them.
	all, err := equip.List(ctx, tx, store.EquipmentFilter{CustomerID: &customer.ID, IncludeRetired: true})
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

// The list builds its WHERE clause and placeholder numbers at runtime, so each
// combination is worth actually running against Postgres.
func TestEquipmentListFilters(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	equip := store.NewEquipmentStore()
	customer := testutil.CreateCustomer(t, tx)

	machine, err := equip.Create(ctx, tx, equipmentParams(customer.ID))
	require.NoError(t, err)

	loaner := equipmentParams(customer.ID)
	loaner.Category = domain.EquipmentCategoryGrinder
	loaner.Make = "Mahlkonig"
	loaner.Model = "EK43"
	loaner.SerialNumber = "MK-40021"
	loaner.Ownership = domain.EquipmentOwnershipLoaner
	grinder, err := equip.Create(ctx, tx, loaner)
	require.NoError(t, err)

	byCategory, err := equip.List(ctx, tx, store.EquipmentFilter{
		CustomerID: &customer.ID,
		Category:   domain.EquipmentCategoryGrinder,
	})
	require.NoError(t, err)
	require.Len(t, byCategory, 1)
	assert.Equal(t, grinder.ID, byCategory[0].ID)

	// "Which machines are ours?" is the question a loaner filter answers.
	byOwnership, err := equip.List(ctx, tx, store.EquipmentFilter{
		CustomerID: &customer.ID,
		Ownership:  domain.EquipmentOwnershipLoaner,
	})
	require.NoError(t, err)
	require.Len(t, byOwnership, 1)
	assert.Equal(t, grinder.ID, byOwnership[0].ID)

	// Serial is what somebody pastes in off the side of the machine, and they
	// will not match its case.
	bySerial, err := equip.List(ctx, tx, store.EquipmentFilter{Search: "lm-991"})
	require.NoError(t, err)
	require.Len(t, bySerial, 1)
	assert.Equal(t, machine.ID, bySerial[0].ID)

	byMake, err := equip.List(ctx, tx, store.EquipmentFilter{Search: "marzocco"})
	require.NoError(t, err)
	require.Len(t, byMake, 1)
	assert.Equal(t, machine.ID, byMake[0].ID)
}

func TestEquipmentUpdateKeepsStatus(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	equip := store.NewEquipmentStore()
	customer := testutil.CreateCustomer(t, tx)
	address := testutil.CreateAddress(t, tx, customer.ID)

	e, err := equip.Create(ctx, tx, equipmentParams(customer.ID))
	require.NoError(t, err)
	_, err = equip.UpdateStatus(ctx, tx, e.ID, domain.EquipmentStatusInShop)
	require.NoError(t, err)

	warranty := time.Now().AddDate(1, 0, 0)
	updated, err := equip.Update(ctx, tx, e.ID, store.UpdateEquipmentParams{
		AddressID:         &address.ID,
		Category:          domain.EquipmentCategoryEspressoMachine,
		Make:              "La Marzocco",
		Model:             "Linea Mini",
		SerialNumber:      "LM-99172",
		Ownership:         domain.EquipmentOwnershipLoaner,
		WarrantyExpiresOn: &warranty,
		Notes:             "Swapped in while the PB is out",
	})
	require.NoError(t, err)

	// Editing details must not quietly send a machine back into service — the
	// status moves through UpdateStatus and nowhere else.
	assert.Equal(t, domain.EquipmentStatusInShop, updated.Status)
	assert.Equal(t, "Linea Mini", updated.Model)
	require.NotNil(t, updated.AddressID)
	assert.Equal(t, address.ID, *updated.AddressID)
	assert.True(t, updated.UnderWarranty(time.Now()))
}
