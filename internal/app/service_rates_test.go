package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newRatedTicketService() *app.ServiceTicketService {
	return app.NewServiceTicketService(store.NewServiceTicketStore(), store.NewEquipmentStore(), audit.NewAuditWriter()).
		WithSettings(store.NewSettingsStore())
}

func cents(n int) *int { return &n }

func TestLaborRatesRoundTrip(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()

	got, err := svc.LaborRates(ctx, tx)
	require.NoError(t, err)
	assert.False(t, got.Set(), "a fresh shop has no rate, and that is not an error")

	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour:  cents(6500),
		TravelCentsPerHour: cents(4000),
	}, testutil.TestActor()))

	got, err = svc.LaborRates(ctx, tx)
	require.NoError(t, err)
	require.NotNil(t, got.LaborCentsPerHour)
	assert.Equal(t, 6500, *got.LaborCentsPerHour)
	require.NotNil(t, got.TravelCentsPerHour)
	assert.Equal(t, 4000, *got.TravelCentsPerHour)
}

// Clearing the rate takes the money column off the reports. It has to be
// possible: a shop that set a rate it no longer trusts should be able to go
// back to hours and parts rather than living with a wrong number.
func TestLaborRatesCanBeCleared(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()

	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour: cents(6500),
	}, testutil.TestActor()))
	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{}, testutil.TestActor()))

	got, err := svc.LaborRates(ctx, tx)
	require.NoError(t, err)
	assert.False(t, got.Set())
	assert.Nil(t, got.LaborCentsPerHour)
}

func TestLaborRatesValidation(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()

	t.Run("a travel rate alone does nothing", func(t *testing.T) {
		err := svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
			TravelCentsPerHour: cents(4000),
		}, testutil.TestActor())
		assert.ErrorIs(t, err, app.ErrTravelRateWithoutLabor)
	})

	t.Run("cents typed into a dollars field", func(t *testing.T) {
		err := svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
			LaborCentsPerHour: cents(650000000),
		}, testutil.TestActor())
		assert.ErrorIs(t, err, app.ErrLaborRateInvalid)
	})

	t.Run("zero travel is a decision, not a mistake", func(t *testing.T) {
		err := svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
			LaborCentsPerHour:  cents(6500),
			TravelCentsPerHour: cents(0),
		}, testutil.TestActor())
		assert.NoError(t, err, "a shop that absorbs the drive must be able to say so")
	})
}

// A rate change is auditable: every cost figure the reports show is computed
// from whatever is set now, so the change history is the only way to explain a
// jump.
func TestSetLaborRatesIsAudited(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()

	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour: cents(6500),
	}, testutil.TestActor()))

	var action string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT action FROM audit_log WHERE resource_type = 'store_settings' ORDER BY created_at DESC LIMIT 1`,
	).Scan(&action))
	assert.Equal(t, "service.labor_rates_updated", action)
}

// The point of the whole feature: with a rate set, accounts can be ranked by
// what they actually cost — which is a different order from hours alone.
func TestCostByAccountRanksByCostOnceRated(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()
	tickets := store.NewServiceTicketStore()
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	staffID := testutil.CreateStaff(t, tx)

	// One account with a lot of hours and no parts, one with a little time and
	// an expensive part.
	hoursHeavy := testutil.CreateCustomer(t, tx)
	partsHeavy := testutil.CreateCustomer(t, tx)

	t1, err := tickets.Create(ctx, tx, store.CreateServiceTicketParams{
		Number: "SVC-RATE-1", CustomerID: hoursHeavy.ID, Title: "long job", Severity: domain.ServiceSeverityRoutine,
	})
	require.NoError(t, err)
	t2, err := tickets.Create(ctx, tx, store.CreateServiceTicketParams{
		Number: "SVC-RATE-2", CustomerID: partsHeavy.ID, Title: "expensive part", Severity: domain.ServiceSeverityRoutine,
	})
	require.NoError(t, err)

	// 10 hours of labour: at $65/h that is $650.
	_, err = tickets.CreateTimeEntry(ctx, tx, store.CreateTimeEntryParams{
		TicketID: t1.ID, StaffID: staffID, Kind: domain.ServiceTimeKindLabor,
		Minutes: 600, PerformedOn: now.AddDate(0, 0, -5),
	})
	require.NoError(t, err)
	// 30 minutes and a $500 part: $32.50 + $500 = $532.50.
	_, err = tickets.CreateTimeEntry(ctx, tx, store.CreateTimeEntryParams{
		TicketID: t2.ID, StaffID: staffID, Kind: domain.ServiceTimeKindLabor,
		Minutes: 30, PerformedOn: now.AddDate(0, 0, -5),
	})
	require.NoError(t, err)
	_, err = tickets.CreatePart(ctx, tx, store.CreatePartParams{
		TicketID: t2.ID, Name: "Burr set", Quantity: 1, UnitCostCents: 50000,
	})
	require.NoError(t, err)

	// Before a rate exists, a cost ranking is impossible and falls back.
	report, err := svc.CostByAccount(ctx, tx, 90, domain.ServiceAccountCostByCost, now)
	require.NoError(t, err)
	assert.Equal(t, domain.ServiceAccountCostByHours, report.Sort,
		"a cost ranking with no rate behind it falls back rather than misleading")

	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour: cents(6500),
	}, testutil.TestActor()))

	report, err = svc.CostByAccount(ctx, tx, 90, domain.ServiceAccountCostByCost, now)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(report.Rows), 2)
	assert.True(t, report.Rates.Set())
	assert.Equal(t, hoursHeavy.ID, report.Rows[0].CustomerID,
		"$650 of hours outranks a $532.50 part — the ranking hours alone would also give, but for the right reason")
	assert.Equal(t, 65000, report.Rows[0].Summary.TotalCostCents(report.Rates))

	// By parts spend the order inverts, which is the comparison the rate makes
	// meaningful rather than redundant.
	byParts, err := svc.CostByAccount(ctx, tx, 90, domain.ServiceAccountCostByParts, now)
	require.NoError(t, err)
	assert.Equal(t, partsHeavy.ID, byParts.Rows[0].CustomerID)
}
