package web

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
)

// Every app sentinel should have an HTTP mapping, and this is the ratchet that
// keeps the gap from growing.
//
// The failure it guards against is quiet by construction. mapError falls
// through to 500 for anything it does not recognise, so an unmapped sentinel
// does not break a build or a test — it turns a "you typed the date wrong" into
// an Internal Server Error, and only in the one code path nobody exercised.
// This branch shipped exactly that: 25 new sentinels, none mapped, all
// rendering as 500s until a review caught them.
//
// Freezing the existing gap rather than demanding it be closed is deliberate.
// Fifty-two sentinels predate this test; fixing them is real work with real
// judgement in it (each needs a status code and a customer-readable string) and
// does not belong to whatever change happens to add the fifty-third. What this
// test buys is that there is never a fifty-third by accident.
//
// To fix a failure: add the sentinel to the right case in mapError. Do not add
// it to the list below — that list only ever shrinks.
func TestEverySentinelIsMapped(t *testing.T) {
	declared := sentinelsIn(t, "../app/errors.go")
	require.NotEmpty(t, declared, "found no sentinels — the parse is wrong, not the code")

	mapped := referencedSentinels(t, "respond.go")
	require.NotEmpty(t, mapped, "found no mappings — the parse is wrong, not the code")

	known := make(map[string]bool, len(knownUnmappedSentinels))
	for _, name := range knownUnmappedSentinels {
		known[name] = true
	}

	var missing []string
	for _, name := range declared {
		if mapped[name] || known[name] {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)

	require.Empty(t, missing,
		"these sentinels have no case in mapError, so they render as 500 Internal Server Error:\n  %s\n"+
			"Add each to the appropriate case in web/respond.go.",
		strings.Join(missing, "\n  "))

	// The other direction: the frozen list must shrink as sentinels get mapped,
	// or it becomes a place where names go to be forgotten.
	var stale []string
	for _, name := range knownUnmappedSentinels {
		if mapped[name] {
			stale = append(stale, name)
		}
	}
	require.Empty(t, stale,
		"these are mapped now — delete them from knownUnmappedSentinels:\n  %s",
		strings.Join(stale, "\n  "))
}

// sentinelsIn returns every package-level Err… identifier declared in a file.
func sentinelsIn(t *testing.T, path string) []string {
	t.Helper()

	src, err := os.ReadFile(path)
	require.NoError(t, err)

	file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	require.NoError(t, err)

	var names []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range value.Names {
				if strings.HasPrefix(name.Name, "Err") && name.IsExported() {
					names = append(names, name.Name)
				}
			}
		}
	}
	sort.Strings(names)
	return names
}

// referencedSentinels returns every app.Err… named anywhere in a web file.
//
// Reference rather than reachability: a sentinel named in respond.go is one
// somebody has decided a status code for, which is the thing being checked. A
// test that tried to prove the case is live would be asserting on the shape of
// a switch statement.
func referencedSentinels(t *testing.T, path string) map[string]bool {
	t.Helper()

	src, err := os.ReadFile(path)
	require.NoError(t, err)

	file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	require.NoError(t, err)

	found := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "app" {
			return true
		}
		if strings.HasPrefix(sel.Sel.Name, "Err") {
			found[sel.Sel.Name] = true
		}
		return true
	})
	return found
}

// knownUnmappedSentinels is the frozen pre-existing gap. It only ever shrinks —
// the test above fails if a name on it acquires a mapping and is not removed,
// and fails if a sentinel not on it is added without one.
var knownUnmappedSentinels = []string{
	"ErrAddressNotGeocodable",
	"ErrBoxPresetDimensionsInvalid",
	"ErrBoxPresetMaxWeightInvalid",
	"ErrBoxPresetNameRequired",
	"ErrCannotModifySelf",
	"ErrCouponAlreadyRedeemed",
	"ErrCouponCodeExists",
	"ErrCustomerUserEmailRequired",
	"ErrCustomerUserEmailTaken",
	"ErrCustomerUserInviteInvalid",
	"ErrCustomerUserNotFound",
	"ErrDiscountInvalid",
	"ErrEquipmentSiteIncomplete",
	"ErrEquipmentSiteNotOnAccount",
	"ErrFulfillmentUnavailable",
	"ErrInvalidStaffRole",
	"ErrInvalidTaxConfig",
	"ErrInvalidTierQuantity",
	"ErrJobNotDead",
	"ErrLastActiveAdmin",
	"ErrMagicLinkExpired",
	"ErrNoBoxPreset",
	"ErrNotWholesaleAccount",
	"ErrOrderNotSwitchable",
	"ErrPasswordTooShort",
	"ErrPaymentAmountMismatch",
	"ErrPickupUnavailable",
	"ErrPostponeAlreadyRun",
	"ErrPostponeIntoPast",
	"ErrPostponeNoSchedule",
	"ErrPostponeNotDeliveryDay",
	"ErrPostponeNotForward",
	"ErrPostponeStrandsMovedRun",
	"ErrPostponeTargetRunMoved",
	"ErrPostponeTooFar",
	"ErrRenewalPaymentDeclined",
	"ErrRestoreRunPassed",
	"ErrRunRouteActive",
	"ErrSetupTokenExpired",
	"ErrShipmentNoPhysicalItems",
	"ErrShipmentNotRefundable",
	"ErrShipmentWeightUnknown",
	"ErrSlugAlreadyExists",
	"ErrStaffEmailExists",
	"ErrStaffEmailRequired",
	"ErrStaffInviteInvalid",
	"ErrStaffNameRequired",
	"ErrTaxCalculationFailed",
	"ErrWhiteLabelInviteInvalid",
	"ErrWhiteLabelLabelRequired",
	"ErrWhiteLabelNameRequired",
	"ErrWholesalePricesStale",
}

// These sentinels used to fall through mapError's default case, which meant an
// operator who tried an unavailable move got "internal server error" and Sentry
// got paged for the application working exactly as designed. The sentinel text
// was never shown. Nothing failed a build over it — an unmapped sentinel is
// only visible at runtime, on the one path nobody exercised.
func TestPreviouslyUnmappedSentinels(t *testing.T) {
	cases := []struct {
		err    error
		status int
	}{
		// The ID came off a URL the caller typed or followed. Missing means
		// missing.
		{app.ErrSubscriptionPlanNotFound, http.StatusNotFound},
		{app.ErrPriceListNotFound, http.StatusNotFound},
		{app.ErrAttributeSetNotFound, http.StatusNotFound},
		{app.ErrAttributeKeyNotFound, http.StatusNotFound},
		{app.ErrAddressNotFound, http.StatusNotFound},
		{app.ErrRouteStopNotFound, http.StatusNotFound},

		// The record exists and the operator may touch it; the move is just not
		// available from where it stands.
		{app.ErrSubscriptionNotCancellable, http.StatusUnprocessableEntity},
		{app.ErrSubscriptionNotResumable, http.StatusUnprocessableEntity},
		{app.ErrSubscriptionNotEditable, http.StatusUnprocessableEntity},
		{app.ErrSubscriptionPlanInactive, http.StatusUnprocessableEntity},
		{app.ErrInvalidOrderStatus, http.StatusConflict},
		{app.ErrOrderHasActiveLabel, http.StatusConflict},
		{app.ErrOrderQBManaged, http.StatusConflict},
		{app.ErrStopAlreadyDelivered, http.StatusConflict},

		// Nothing the operator did — this process has no River client.
		{app.ErrJobRetryUnavailable, http.StatusServiceUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.err.Error(), func(t *testing.T) {
			status, msg := mapError(tc.err)
			assert.Equal(t, tc.status, status)
			assert.NotEqual(t, "internal server error", msg,
				"the sentinel's own words are what the operator needs")

			// Wrapped is the shape handlers actually produce, and errors.Is
			// has to carry the mapping through it.
			wrapped, _ := mapError(fmt.Errorf("load: %w", tc.err))
			assert.Equal(t, tc.status, wrapped, "mapping must survive wrapping")
		})
	}
}

// ErrInvalidOrderStatus is the one sentinel whose useful sentence lives in the
// wrap rather than the sentinel. Producers name the blocker there by
// convention, and both the batch UI and mapError read it back out.
func TestInvalidOrderStatusKeepsTheProducersPhrase(t *testing.T) {
	wrapped := fmt.Errorf("order is not ready for pickup: %w", app.ErrInvalidOrderStatus)

	status, msg := mapError(wrapped)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "order is not ready for pickup", msg,
		"the prefix is the only place the actual blocker is named")

	// Bare, or wrapped by something that is not a phrase, falls back to copy
	// that at least tells the operator what to do next.
	_, bare := mapError(app.ErrInvalidOrderStatus)
	assert.Contains(t, bare, "reload the page")
}

// brokenReference exists to defeat errors.Is, and this is the test that says
// so. A stored ID that no longer resolves is broken data: it has to stay a 500
// and page, not become the 404 that ErrAddressNotFound and
// ErrSubscriptionPlanNotFound now map to.
//
// Switching brokenReference to %w — the reflex when wrapping an error — would
// pass a build, a vet and every other test in this package. It would fail here.
func TestBrokenReferenceDoesNotCarryTheSentinel(t *testing.T) {
	order, address := uuid.New(), uuid.New()

	err := brokenReference("order", order, "address", address)

	assert.False(t, errors.Is(err, app.ErrAddressNotFound),
		"brokenReference must not wrap the sentinel with %%w — errors.Is reaching it would turn broken data into a 404")

	status, _ := mapError(err)
	assert.Equal(t, http.StatusInternalServerError, status,
		"a dangling reference has to page, not 404")

	// Both IDs belong in the message: without them the Sentry event names no
	// row to go look at.
	assert.Contains(t, err.Error(), order.String())
	assert.Contains(t, err.Error(), address.String())
}

// The counterpart: the same sentinel, reached the ordinary way, still maps to
// 404. Together these pin both halves of the split — the URL-supplied ID gets
// its 404, the stored one does not.
func TestSentinelStillMapsWhenWrappedNormally(t *testing.T) {
	status, _ := mapError(fmt.Errorf("load address: %w", app.ErrAddressNotFound))
	assert.Equal(t, http.StatusNotFound, status)
}
