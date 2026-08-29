package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// auditActionsFor lists the audit actions recorded against one resource. The
// shared helpers assert on the *last* entry; these tests care that a sequence
// of status moves each left its own distinctly-named mark.
func auditActionsFor(t *testing.T, tx pgx.Tx, resourceType string, id uuid.UUID) []string {
	t.Helper()
	rows, err := tx.Query(context.Background(),
		`SELECT action FROM audit_log WHERE resource_type = $1 AND resource_id = $2 ORDER BY created_at`,
		resourceType, id)
	require.NoError(t, err)
	defer rows.Close()

	var actions []string
	for rows.Next() {
		var action string
		require.NoError(t, rows.Scan(&action))
		actions = append(actions, action)
	}
	require.NoError(t, rows.Err())
	return actions
}

func newEquipmentService() *app.EquipmentService {
	return app.NewEquipmentService(store.NewEquipmentStore(), audit.NewAuditWriter())
}

func registerParams(customerID uuid.UUID) app.RegisterEquipmentParams {
	return app.RegisterEquipmentParams{
		CustomerID: customerID,
		Category:   domain.EquipmentCategoryEspressoMachine,
		Make:       "  La Marzocco  ",
		Model:      " Linea PB ",
		Ownership:  domain.EquipmentOwnershipCustomer,
	}
}

func TestEquipmentRegisterTrimsAndAudits(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newEquipmentService()
	customer := testutil.CreateCustomer(t, tx)

	equipment, err := svc.Register(ctx, tx, registerParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)

	// Copied off the side of a machine, make and model arrive with whitespace.
	assert.Equal(t, "La Marzocco", equipment.Make)
	assert.Equal(t, "La Marzocco Linea PB", equipment.Description())

	entries := auditActionsFor(t, tx, "equipment", equipment.ID)
	assert.Contains(t, entries, audit.AuditEquipmentCreated)
}

func TestEquipmentRegisterRejectsNamelessMachine(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newEquipmentService()
	customer := testutil.CreateCustomer(t, tx)

	p := registerParams(customer.ID)
	p.Make = "   "
	_, err := svc.Register(ctx, tx, p, testutil.TestActor())

	// A register row with no make is unsearchable and unrecognisable.
	require.ErrorIs(t, err, app.ErrEquipmentMakeRequired)
}

func TestEquipmentRegisterRejectsUnknownEnums(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newEquipmentService()
	customer := testutil.CreateCustomer(t, tx)

	// Caught in Go rather than by the database CHECK, so a hand-posted form
	// gets a 400 with a correction rather than a 500.
	bad := registerParams(customer.ID)
	bad.Category = "roaster"
	_, err := svc.Register(ctx, tx, bad, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrInvalidEquipmentCategory)

	bad = registerParams(customer.ID)
	bad.Ownership = "rented"
	_, err = svc.Register(ctx, tx, bad, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrInvalidEquipmentOwnership)
}

func TestEquipmentSetStatusAuditsEachDestinationApart(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newEquipmentService()
	customer := testutil.CreateCustomer(t, tx)

	equipment, err := svc.Register(ctx, tx, registerParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)

	_, err = svc.SetStatus(ctx, tx, equipment.ID, domain.EquipmentStatusInShop, testutil.TestActor())
	require.NoError(t, err)
	_, err = svc.SetStatus(ctx, tx, equipment.ID, domain.EquipmentStatusActive, testutil.TestActor())
	require.NoError(t, err)
	retired, err := svc.SetStatus(ctx, tx, equipment.ID, domain.EquipmentStatusRetired, testutil.TestActor())
	require.NoError(t, err)
	assert.Equal(t, domain.EquipmentStatusRetired, retired.Status)

	// Distinct actions, because the timeline picks its label and its colour
	// from the action string alone.
	entries := auditActionsFor(t, tx, "equipment", equipment.ID)
	assert.Contains(t, entries, audit.AuditEquipmentSentToShop)
	assert.Contains(t, entries, audit.AuditEquipmentReturnedToService)
	assert.Contains(t, entries, audit.AuditEquipmentRetired)
}

func TestEquipmentSetStatusIgnoresNoOp(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newEquipmentService()
	customer := testutil.CreateCustomer(t, tx)

	equipment, err := svc.Register(ctx, tx, registerParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)

	_, err = svc.SetStatus(ctx, tx, equipment.ID, domain.EquipmentStatusRetired, testutil.TestActor())
	require.NoError(t, err)
	before := auditActionsFor(t, tx, "equipment", equipment.ID)

	// Retiring an already-retired machine must not say it was retired twice.
	_, err = svc.SetStatus(ctx, tx, equipment.ID, domain.EquipmentStatusRetired, testutil.TestActor())
	require.NoError(t, err)

	after := auditActionsFor(t, tx, "equipment", equipment.ID)
	assert.Equal(t, len(before), len(after), "a no-op status change writes no timeline entry")
}

func TestEquipmentSetStatusRejectsUnknownStatus(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newEquipmentService()
	customer := testutil.CreateCustomer(t, tx)

	equipment, err := svc.Register(ctx, tx, registerParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)

	_, err = svc.SetStatus(ctx, tx, equipment.ID, "broken", testutil.TestActor())
	require.ErrorIs(t, err, app.ErrInvalidEquipmentStatus)
}

func TestEquipmentGetHidesOtherCustomersMachine(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newEquipmentService()

	owner := testutil.CreateCustomer(t, tx, testutil.WithEmail("owner@example.test"))
	other := testutil.CreateCustomer(t, tx, testutil.WithEmail("other@example.test"))
	equipment, err := svc.Register(ctx, tx, registerParams(owner.ID), testutil.TestActor())
	require.NoError(t, err)

	// Not-yours and not-there must be indistinguishable, or a customer probing
	// ids learns which ones exist.
	_, err = svc.Get(ctx, tx, equipment.ID, other.ID)
	require.ErrorIs(t, err, app.ErrEquipmentNotFound)
}

func TestEquipmentEditMissingMachineIsNotFound(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newEquipmentService()

	_, err := svc.GetByID(ctx, tx, testutil.CreateCustomer(t, tx).ID)
	require.ErrorIs(t, err, app.ErrEquipmentNotFound)
}

func TestEquipmentListForCustomerExcludesRetired(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newEquipmentService()
	customer := testutil.CreateCustomer(t, tx)

	live, err := svc.Register(ctx, tx, registerParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)
	gone, err := svc.Register(ctx, tx, registerParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)
	_, err = svc.SetStatus(ctx, tx, gone.ID, domain.EquipmentStatusRetired, testutil.TestActor())
	require.NoError(t, err)

	// The customer card answers "what is on their counter", not "what ever was".
	listed, err := svc.ListForCustomer(ctx, tx, customer.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, live.ID, listed[0].ID)
}

func TestEquipmentListWithCustomerNamesTheCafe(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newEquipmentService()

	company := "Bunker Coffee"
	customer := testutil.CreateCustomer(t, tx, testutil.WithCustomerName("Dana", "Reyes"))
	_, err := tx.Exec(ctx, `UPDATE customers SET company_name = $2 WHERE id = $1`, customer.ID, company)
	require.NoError(t, err)

	_, err = svc.Register(ctx, tx, registerParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)

	rows, err := svc.ListWithCustomer(ctx, tx, store.EquipmentFilter{CustomerID: &customer.ID})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	// A cafe is known by the name on the sign, not by whoever signed up.
	assert.Equal(t, company, rows[0].CustomerName)
}

// The catalogue behind the add-machine form's type-ahead is a hint, never a
// whitelist. This is the pin for that, and it has to live here: the property
// belongs to validateEquipment, which is unexported and in this package, so a
// test in internal/domain cannot reach it and would stay green if somebody
// added a membership check tomorrow.
//
// The failure it guards against is not hypothetical in shape — a suggestion
// list quietly becoming a constraint would reject the first machine nobody
// anticipated, on a register whose whole value is that it holds whatever is
// actually out there.
func TestRegisterAcceptsAMakeAndModelOutsideTheCatalogue(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := app.NewEquipmentService(store.NewEquipmentStore(), audit.NewAuditWriter())
	customer := testutil.CreateCustomer(t, tx)

	// Deliberately absent from domain.EquipmentCatalog.
	unlisted := "Wollenhaupt & Sons"
	require.NotContains(t, domain.EquipmentMakes(), unlisted,
		"precondition: this make must not be in the catalogue, or the test proves nothing")

	machine, err := svc.Register(ctx, tx, app.RegisterEquipmentParams{
		CustomerID: customer.ID,
		Category:   domain.EquipmentCategoryOther,
		Make:       unlisted,
		Model:      "Mark IV Hand-Cranked",
		Ownership:  domain.EquipmentOwnershipCustomer,
	}, testutil.TestActor())

	require.NoError(t, err, "an unlisted make must register exactly as any other")
	assert.Equal(t, unlisted, machine.Make)
	assert.Equal(t, "Mark IV Hand-Cranked", machine.Model)
}
