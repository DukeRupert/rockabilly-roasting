package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
)

// A hand-edited window must land on one the control strip can highlight —
// otherwise the reader cannot tell what period they are looking at.
func TestServiceCostDays(t *testing.T) {
	assert.Equal(t, 90, serviceCostDays(""), "the quarter is the period people argue about")
	assert.Equal(t, 90, serviceCostDays("90"))
	assert.Equal(t, 365, serviceCostDays("365"))
	assert.Equal(t, 0, serviceCostDays("0"), "zero is all time")
	assert.Equal(t, 90, serviceCostDays("42"), "an arbitrary window falls back to the default")
	assert.Equal(t, 90, serviceCostDays("nonsense"))
}

// Blank and zero are different states: blank takes the money column off the
// reports, zero says the shop absorbs the drive.
func TestParseOptionalRate(t *testing.T) {
	got, err := parseOptionalRate("")
	assert.NoError(t, err)
	assert.Nil(t, got, "blank is unset, not zero")

	got, err = parseOptionalRate("  ")
	assert.NoError(t, err)
	assert.Nil(t, got)

	got, err = parseOptionalRate("0")
	assert.NoError(t, err)
	if assert.NotNil(t, got) {
		assert.Equal(t, 0, *got, "an explicit zero is a decision worth storing")
	}

	got, err = parseOptionalRate("65")
	assert.NoError(t, err)
	if assert.NotNil(t, got) {
		assert.Equal(t, 6500, *got)
	}

	got, err = parseOptionalRate("65.50")
	assert.NoError(t, err)
	if assert.NotNil(t, got) {
		assert.Equal(t, 6550, *got)
	}

	// Somebody pasting a figure off an invoice brings the dollar sign with it.
	got, err = parseOptionalRate("$72.25")
	assert.NoError(t, err)
	if assert.NotNil(t, got) {
		assert.Equal(t, 7225, *got)
	}

	_, err = parseOptionalRate("sixty five")
	assert.Error(t, err)

	_, err = parseOptionalRate("-10")
	assert.Error(t, err, "a negative hourly cost is not a thing")
}

// An unset rate renders as an empty field, never "0.00" — which would read as a
// decision nobody made.
func TestRateInput(t *testing.T) {
	assert.Equal(t, "", rateInput(nil))

	zero := 0
	assert.Equal(t, "0.00", rateInput(&zero))

	odd := 6505
	assert.Equal(t, "65.05", rateInput(&odd))

	round := 6500
	assert.Equal(t, "65.00", rateInput(&round))
}

// An absent sort has to be distinguishable from an explicit one: the default
// depends on whether a labour rate exists, which the parser cannot know.
func TestServiceCostSort(t *testing.T) {
	sort, explicit := serviceCostSort(url.Values{})
	assert.False(t, explicit)
	assert.Equal(t, domain.ServiceAccountCostByHours, sort)

	sort, explicit = serviceCostSort(url.Values{"sort": {"hours"}})
	assert.True(t, explicit, "Hours is a ranking somebody can ask for, not the absence of one")
	assert.Equal(t, domain.ServiceAccountCostByHours, sort)

	sort, explicit = serviceCostSort(url.Values{"sort": {"parts"}})
	assert.True(t, explicit)
	assert.Equal(t, domain.ServiceAccountCostByParts, sort)

	sort, explicit = serviceCostSort(url.Values{"sort": {"cost"}})
	assert.True(t, explicit)
	assert.Equal(t, domain.ServiceAccountCostByCost, sort)

	// A present-but-empty sort= is a value, not the absence of one — it lands
	// on hours like any other unrecognised value. What means "no preference" is
	// the parameter being missing entirely, which is what the Hours link used
	// to do and why it came back ranked by cost.
	sort, explicit = serviceCostSort(url.Values{"sort": {""}})
	assert.True(t, explicit)
	assert.Equal(t, domain.ServiceAccountCostByHours, sort)

	_, explicit = serviceCostSort(url.Values{})
	assert.False(t, explicit, "absent is the only thing that asks for the default")

	// A mistyped sort is still a request for one. Treating it as absent would
	// hand the reader the cost default and light a tab they did not click.
	sort, explicit = serviceCostSort(url.Values{"sort": {"bogus"}})
	assert.True(t, explicit)
	assert.Equal(t, domain.ServiceAccountCostByHours, sort,
		"hours is the safe ranking, and the strip highlights it")
}

// The line between "tell the operator" and "page somebody".
//
// Every maintenance write path used to flash err.Error() whatever it was, so a
// dead database arrived as a 303 and a tidy sentence — and Sentry never heard
// about it. One of them rendered the same thing at HTTP 200.
func TestExpectedFailure(t *testing.T) {
	t.Run("named failures are the operator's to fix", func(t *testing.T) {
		for _, err := range []error{
			app.ErrPlanNameTaken,
			app.ErrPlanHasNoTasks,
			app.ErrPlanIntervalInvalid,
			app.ErrMaintenanceDateRequired,
			app.ErrMaintenanceAlreadyClosed,
			app.ErrLaborRateZero,
			app.ErrTravelRateWithoutLabor,
			app.ErrEquipmentRetired,
		} {
			assert.True(t, expectedFailure(err), "%v", err)
		}
	})

	t.Run("a missing resource is not something to flash", func(t *testing.T) {
		// Deliberate, and the odd one out: not-found is as named as anything
		// above it, but the callers are redirect-*back* helpers and the place
		// they redirect back to is the resource that is missing. Flashing "no
		// such plan" onto a 303 aimed at that plan's own page just produces a
		// 404 with the message lost on the way there. Error renders the 404.
		for _, err := range []error{
			app.ErrPlanNotFound,
			app.ErrMaintenanceNotFound,
			app.ErrPlanTaskNotFound,
			app.ErrPlanAssignmentNotFound,
		} {
			assert.False(t, expectedFailure(err), "%v", err)
			status, _ := mapError(err)
			assert.Equal(t, http.StatusNotFound, status,
				"%v is excluded for being a 404, so it had better be one", err)
		}
	})

	t.Run("anything else is ours", func(t *testing.T) {
		assert.False(t, expectedFailure(errors.New("connection reset by peer")))
		assert.False(t, expectedFailure(fmt.Errorf("audit plan assigned: %w",
			errors.New("write tcp: broken pipe"))),
			"a wrapped infrastructure error must not pass as validation text")
	})
}

// redirectOrFail must actually return on both branches.
//
// The regression: a regex that swapped the unconditional flashes for this
// helper also rewrote the helper's own body into a call to itself, so every
// *expected* failure — deleting a plan that has machines on it, say — recursed
// until the stack gave out and took the process with it. Nothing in the build
// or the type system objects to a function calling itself.
func TestRedirectOrFailTerminates(t *testing.T) {
	d := &Deps{}

	t.Run("a named failure redirects with its own text", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/service/plans/x/delete", nil)

		d.redirectOrFail(w, r, "/admin/service/plans", app.ErrPlanInUse)

		assert.Equal(t, http.StatusSeeOther, w.Code)
		assert.Contains(t, w.Header().Get("Location"), "flash_error=")
	})

	t.Run("an unexpected failure is ours to answer for", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/service/plans/x/delete", nil)

		d.redirectOrFail(w, r, "/admin/service/plans", errors.New("connection reset by peer"))

		assert.Equal(t, http.StatusInternalServerError, w.Code,
			"logged and reported, not dressed up as something the operator typed wrong")
	})
}
