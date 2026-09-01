package admin

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
)

func renderTimeline(t *testing.T, c templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, c.Render(context.Background(), &buf))
	return buf.String()
}

func entry(action string, actor string, at time.Time) domain.AuditEntry {
	return domain.AuditEntry{
		ID:           uuid.New(),
		ActorType:    domain.AuditActorTypeStaff,
		ActorName:    actor,
		Action:       action,
		ResourceType: "delivery_route",
		ResourceID:   uuid.New(),
		CreatedAt:    at,
	}
}

// The label mappers are plain switch statements, so the risk is not that they
// compute the wrong string — it is that an action is missing from the switch and
// silently falls through to the generic "Verb noun" fallback. Render the real
// component and read the copy back.
func TestRouteActivity_LabelsEveryRouteAction(t *testing.T) {
	now := time.Now().UTC()
	entries := []domain.AuditEntry{
		entry(audit.AuditRouteCompleted, "Logan", now),
		entry(audit.AuditRouteStopSkipped, "Driver", now.Add(-time.Hour)),
		entry(audit.AuditRouteActivated, "Logan", now.Add(-2*time.Hour)),
		entry(audit.AuditRoutePlanned, "Logan", now.Add(-3*time.Hour)),
	}

	html := renderTimeline(t, RouteActivity(entries, time.UTC))

	for _, want := range []string{"Route completed", "Stop skipped", "Route started", "Route planned"} {
		assert.Contains(t, html, want)
	}
	// A skipped stop is the one event on this page that costs a customer their
	// delivery, so it must not render in the neutral colour.
	assert.Contains(t, html, "bg-rr-red")
}

func TestInvoiceActivity_LabelsEveryInvoiceAction(t *testing.T) {
	now := time.Now().UTC()
	entries := []domain.AuditEntry{
		entry(audit.AuditInvoiceVoided, "Logan", now),
		entry(audit.AuditInvoicePaymentRecorded, "Logan", now.Add(-time.Hour)),
		entry(audit.AuditInvoiceSent, "system", now.Add(-2*time.Hour)),
		entry(audit.AuditInvoiceCreated, "Logan", now.Add(-3*time.Hour)),
	}

	html := renderTimeline(t, InvoiceActivity(entries, time.UTC))

	for _, want := range []string{"Invoice voided", "Payment recorded", "Emailed to customer", "Invoice created"} {
		assert.Contains(t, html, want)
	}
}

// An unmapped action must still render something readable rather than a raw
// dotted key, because new audit actions get added without touching these files.
func TestActivityTimeline_UnmappedActionFallsBackReadably(t *testing.T) {
	html := renderTimeline(t,
		RouteActivity([]domain.AuditEntry{entry("delivery_route.rerouted", "Logan", time.Now().UTC())}, time.UTC))

	assert.Contains(t, html, "Rerouted")
	assert.NotContains(t, html, "delivery_route.rerouted")
}

// Every detail page shares this component, so the empty state is worth pinning:
// a page with no history must say so rather than render a bare heading.
func TestActivityTimeline_EmptyState(t *testing.T) {
	html := renderTimeline(t, InvoiceActivity(nil, time.UTC))

	assert.Contains(t, html, "No activity recorded yet")
	assert.NotContains(t, html, "<ol")
}

// The shared component must be what each page's wrapper renders — if a wrapper
// ever grows its own markup again the feeds drift apart.
//
// Every wrapper is listed here, not a sample: the drift this pins is exactly
// the kind that shows up in the one page nobody checked.
func TestActivityWrappers_ShareOneShape(t *testing.T) {
	now := time.Now().UTC()
	e := []domain.AuditEntry{entry(audit.AuditRoutePlanned, "Logan", now)}

	wrappers := map[string]templ.Component{
		"announcement": AnnouncementActivity(e, time.UTC),
		"customer":     CustomerActivity(uuid.New(), e, time.UTC),
		"discount":     DiscountActivity(e, time.UTC),
		"invoice":      InvoiceActivity(e, time.UTC),
		"order":        OrderActivity(e, time.UTC),
		"price_list":   PriceListActivity(e, time.UTC),
		"product":      ProductActivity(e, time.UTC),
		"route":        RouteActivity(e, time.UTC),
		"subscription": SubscriptionTimeline(e, time.UTC),
	}

	// Same wrapper, same heading, same row scaffolding; only the label differs.
	for name, component := range wrappers {
		html := renderTimeline(t, component)
		for _, shared := range []string{`<h2 class="text-sm font-semibold text-rr-heading">Activity</h2>`, `<ol role="list"`} {
			assert.Contains(t, html, shared, name)
		}
		assert.Equal(t, 1, strings.Count(html, `<li class=`), name)
	}
}
