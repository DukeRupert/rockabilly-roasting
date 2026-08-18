package admin

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rendering the card and reading the markup: the swap target id, the toggle
// hrefs, and the delta arrows are all things a templ mistake compiles right
// past — a component called inline after other content on the same line is
// emitted as literal text rather than invoked, and only the output shows it.

func renderActiveCustomers(t *testing.T, props ActiveCustomersProps) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, ActiveCustomersCard(props).Render(context.Background(), &buf))
	return buf.String()
}

func TestActiveCustomersCard_RendersWindowsAndToggle(t *testing.T) {
	html := renderActiveCustomers(t, ActiveCustomersProps{
		Channel: "all",
		Week:    12, WeekPrior: 10,
		Month: 48, MonthPrior: 60,
		Quarter: 90, QuarterPrior: 90,
	})

	// The htmx swap target has to match what the toggles aim at, or the card
	// replaces the whole page.
	assert.Contains(t, html, `id="active-customers-card"`)
	assert.Contains(t, html, `hx-target="#active-customers-card"`)

	assert.Contains(t, html, "Last week")
	assert.Contains(t, html, "Last month")
	assert.Contains(t, html, "Last quarter")
	assert.Contains(t, html, ">12<")
	assert.Contains(t, html, ">48<")
	assert.Contains(t, html, ">90<")

	// Growth is teal, decline is rust, no movement reads "flat".
	assert.Contains(t, html, "↑ 20.0%")
	assert.Contains(t, html, "↓ 20.0%")
	assert.Contains(t, html, "flat")

	// The active scope is a plain label; the other two are swap links.
	assert.NotContains(t, html, "/admin/dashboard/active-customers?channel=all")
	assert.Contains(t, html, "/admin/dashboard/active-customers?channel=retail")
	assert.Contains(t, html, "/admin/dashboard/active-customers?channel=wholesale")
}

func TestActiveCustomersCard_ScopedChannelSwapsTheActiveToggle(t *testing.T) {
	html := renderActiveCustomers(t, ActiveCustomersProps{Channel: "wholesale", Week: 3})

	assert.Contains(t, html, "Active customers · wholesale")
	assert.NotContains(t, html, "?channel=wholesale")
	assert.Contains(t, html, "?channel=all")
	assert.Contains(t, html, "?channel=retail")
}

// A brand-new store has no prior window to compare against; showing "↓ 100%" or
// dividing by zero there would be worse than showing nothing.
func TestActiveCustomersCard_HidesDeltaWithoutPriorWindow(t *testing.T) {
	html := renderActiveCustomers(t, ActiveCustomersProps{Channel: "all", Week: 5})

	assert.Contains(t, html, ">5<")
	assert.False(t, strings.Contains(html, "vs prior"), "delta must be hidden when the prior window is empty")
}
