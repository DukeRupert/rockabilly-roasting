package app_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// The module registry is what makes a section of the app optional, so the
// properties worth pinning are the ones a toggle bug would break quietly:
// the default is off, a toggle survives a reload, an unknown key is refused,
// and the cache does not move until someone refreshes it.

func newModuleService() *app.ModuleService {
	return app.NewModuleService(store.NewModuleStore(), audit.NewAuditWriter())
}

func TestModuleServiceDefaultsToDisabled(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newModuleService()

	require.NoError(t, svc.Refresh(t.Context(), tx))

	assert.False(t, svc.Enabled(domain.ModuleEquipmentService),
		"a freshly migrated shop must not have optional modules switched on")
}

func TestModuleServiceEnableThenRefresh(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newModuleService()
	require.NoError(t, svc.Refresh(ctx, tx))

	actor := app.Actor{Type: domain.AuditActorTypeStaff, Name: "Test Staff"}
	info, err := svc.SetEnabled(ctx, tx, string(domain.ModuleEquipmentService), true, actor)
	require.NoError(t, err)
	assert.Equal(t, domain.ModuleEquipmentService, info.Key)

	// The cache is deliberately untouched by SetEnabled: callers refresh after
	// the transaction commits, so a rolled-back toggle never reaches readers.
	assert.False(t, svc.Enabled(domain.ModuleEquipmentService),
		"SetEnabled must not publish the change before the caller refreshes")

	require.NoError(t, svc.Refresh(ctx, tx))
	assert.True(t, svc.Enabled(domain.ModuleEquipmentService))
}

func TestModuleServiceDisableKeepsRow(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newModuleService()
	actor := app.Actor{Type: domain.AuditActorTypeStaff, Name: "Test Staff"}

	_, err := svc.SetEnabled(ctx, tx, string(domain.ModuleEquipmentService), true, actor)
	require.NoError(t, err)
	_, err = svc.SetEnabled(ctx, tx, string(domain.ModuleEquipmentService), false, actor)
	require.NoError(t, err)

	require.NoError(t, svc.Refresh(ctx, tx))
	assert.False(t, svc.Enabled(domain.ModuleEquipmentService))

	// Disabling is not deleting — the row survives so the settings screen can
	// still say who turned it off and when.
	states, err := svc.List(ctx, tx)
	require.NoError(t, err)
	require.Len(t, states, len(domain.ModuleRegistry()))
	assert.Equal(t, domain.ModuleEquipmentService, states[0].Key)
	assert.False(t, states[0].Enabled)
	assert.NotNil(t, states[0].ChangedAt, "a toggled module records when it changed")
	assert.Equal(t, "", states[0].ChangedByName, "no staff row was attached to this actor")
}

func TestModuleServiceRejectsUnknownKey(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newModuleService()

	_, err := svc.SetEnabled(ctx, tx, "not_a_module", true, app.Actor{Type: domain.AuditActorTypeStaff, Name: "Test Staff"})
	require.ErrorIs(t, err, app.ErrUnknownModule)
}

func TestModuleServiceSetIsACopy(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newModuleService()
	actor := app.Actor{Type: domain.AuditActorTypeStaff, Name: "Test Staff"}

	_, err := svc.SetEnabled(ctx, tx, string(domain.ModuleEquipmentService), true, actor)
	require.NoError(t, err)
	require.NoError(t, svc.Refresh(ctx, tx))

	// Every admin request gets this set on its context. One request mutating it
	// must not change what the next one sees.
	set := svc.Set()
	set[domain.ModuleEquipmentService] = false
	assert.True(t, svc.Enabled(domain.ModuleEquipmentService))
}

func TestLookupModuleRejectsUnknown(t *testing.T) {
	_, ok := domain.LookupModule("not_a_module")
	assert.False(t, ok)

	info, ok := domain.LookupModule(string(domain.ModuleEquipmentService))
	require.True(t, ok)
	assert.NotEmpty(t, info.Name)
	assert.NotEmpty(t, info.Detail, "the settings screen needs a 'what changes' line for every module")
}

func TestModuleSetZeroValueReportsDisabled(t *testing.T) {
	var set domain.ModuleSet
	assert.False(t, set.Enabled(domain.ModuleEquipmentService),
		"a page rendered outside the admin middleware must show no module sections")
}
