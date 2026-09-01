package admin

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// The audit log's controls are all links carrying the whole filter state. The
// failure mode is not a crash: it is a pill that quietly forgets the search
// term, so narrowing by date throws away the name you typed. That only shows
// up in the rendered href, so these read the markup.

func fullyFilteredAuditProps() AuditListProps {
	return AuditListProps{
		Search:          "wanda",
		ActorTypeFilter: "staff",
		Area:            "order",
		ActionFilter:    "order.refunded",
		ResourceFilter:  "order",
		ActorID:         "11111111-1111-4111-8111-111111111111",
		ResourceID:      "22222222-2222-4222-8222-222222222222",
		Range:           "custom",
		From:            "2026-08-01",
		To:              "2026-08-15",
		Sort:            "oldest",
		Page:            3,
		PerPage:         50,
		TotalCount:      120,
		MerchantTZ:      time.UTC,
	}
}

func parseAuditURL(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	assert.Equal(t, "/admin/audit", u.Path)
	return u.Query()
}

func TestAuditPagingCarriesEveryFilter(t *testing.T) {
	props := fullyFilteredAuditProps()
	q := parseAuditURL(t, auditURL(props, 4, nil))

	assert.Equal(t, "4", q.Get("page"))
	for name, want := range map[string]string{
		"q":             "wanda",
		"actor_type":    "staff",
		"area":          "order",
		"action":        "order.refunded",
		"resource_type": "order",
		"actor_id":      props.ActorID,
		"resource_id":   props.ResourceID,
		"range":         "custom",
		"from":          "2026-08-01",
		"to":            "2026-08-15",
		"sort":          "oldest",
	} {
		assert.Equal(t, want, q.Get(name), "page 4 dropped %s", name)
	}
}

func TestAuditFilterChangeResetsPageAndClearsWhatNoLongerApplies(t *testing.T) {
	props := fullyFilteredAuditProps()

	t.Run("changing a filter goes back to page 1", func(t *testing.T) {
		// Page 3 of one result set is not page 3 of another.
		q := parseAuditURL(t, auditFilterURL(props, "actor_type", "system"))
		assert.Equal(t, "", q.Get("page"))
		assert.Equal(t, "system", q.Get("actor_type"))
		assert.Equal(t, "wanda", q.Get("q"), "the search term survives a filter change")
	})

	t.Run("leaving the custom range drops its dates", func(t *testing.T) {
		q := parseAuditURL(t, auditFilterURL(props, "range", "7d"))
		assert.Equal(t, "7d", q.Get("range"))
		assert.Equal(t, "", q.Get("from"), "a stale custom date under a preset is a lie")
		assert.Equal(t, "", q.Get("to"))
	})

	t.Run("changing the area drops the exact action", func(t *testing.T) {
		q := parseAuditURL(t, auditFilterURL(props, "area", "customer"))
		assert.Equal(t, "customer", q.Get("area"))
		assert.Equal(t, "", q.Get("action"),
			"order.refunded has no meaning inside the customer area")
	})

	t.Run("clearing everything returns the bare path", func(t *testing.T) {
		assert.Equal(t, "/admin/audit", auditURL(AuditListProps{}, 1, nil))
	})
}

func TestAuditRowsPinTheThingYouClicked(t *testing.T) {
	actorID := uuid.New()
	resourceID := uuid.New()
	entry := domain.AuditEntry{
		ID:           uuid.New(),
		ActorType:    domain.AuditActorTypeStaff,
		ActorID:      &actorID,
		ActorName:    "Wanda Jackson",
		Action:       "order.refunded",
		ResourceType: "order",
		ResourceID:   resourceID,
		CreatedAt:    time.Now(),
	}
	props := AuditListProps{Entries: []domain.AuditEntry{entry}, Page: 1, PerPage: 50, TotalCount: 1, MerchantTZ: time.UTC}

	assert.Equal(t, actorID.String(), parseAuditURL(t, auditActorHref(props, entry)).Get("actor_id"),
		"clicking a name is how you ask what else that person did")

	resourceQ := parseAuditURL(t, auditResourceHref(props, entry))
	assert.Equal(t, resourceID.String(), resourceQ.Get("resource_id"))
	assert.Equal(t, "order", resourceQ.Get("resource_type"))

	// A system entry has no actor to pin, and must not render a link to nothing.
	system := domain.AuditEntry{ActorType: domain.AuditActorTypeSystem, ActorName: "system", Action: "job.retried"}
	assert.Equal(t, "", auditActorHref(props, system))
}

func TestAuditListRendersFiltersAndRows(t *testing.T) {
	props := fullyFilteredAuditProps()
	props.Areas = []string{"customer", "order"}
	props.Actions = []string{"order.refunded", "order.shipped"}
	props.ResourceTypes = []string{"customer", "order"}
	props.ActorLabel = "Wanda Jackson"
	props.Entries = []domain.AuditEntry{{
		ID:           uuid.New(),
		ActorType:    domain.AuditActorTypeStaff,
		ActorName:    "Wanda Jackson",
		Action:       "order.refunded",
		ResourceType: "order",
		ResourceID:   uuid.New(),
		CreatedAt:    time.Now(),
	}}

	var buf bytes.Buffer
	require.NoError(t, AuditListContent(props).Render(context.Background(), &buf))
	html := buf.String()

	assert.Contains(t, html, `value="wanda"`, "the search box echoes the active term")
	assert.Contains(t, html, `hx-get="/admin/audit"`, "search refreshes the table inline")
	assert.Contains(t, html, "Order refunded", "actions read as prose, not as dotted keys")
	assert.Contains(t, html, "Wanda Jackson", "the pinned actor is named, not left as a uuid")
	assert.Contains(t, html, "Clear all", "a fully filtered list always offers a way out")
	assert.Contains(t, html, "</span> of <span", "a filtered list gets an exact total")

	// The action select only appears once an area narrows it — otherwise it
	// would list every action in the system.
	assert.Contains(t, html, `id="audit-action"`)
	props.Actions = nil
	buf.Reset()
	require.NoError(t, AuditListContent(props).Render(context.Background(), &buf))
	assert.NotContains(t, buf.String(), `id="audit-action"`)
}

// The unfiltered view deliberately has no total: counting it is a scan of the
// whole log. It still has to say where you are.
func TestAuditUnfilteredPaginationShowsAPageNumberInsteadOfATotal(t *testing.T) {
	props := AuditListProps{
		Entries:    []domain.AuditEntry{{ActorType: domain.AuditActorTypeSystem, ActorName: "system", Action: "job.retried", ResourceType: "job", ResourceID: uuid.New(), CreatedAt: time.Now()}},
		Page:       3,
		PerPage:    50,
		TotalCount: 0,
		MerchantTZ: time.UTC,
	}
	var buf bytes.Buffer
	require.NoError(t, AuditListContent(props).Render(context.Background(), &buf))
	html := buf.String()
	assert.Contains(t, html, "Page <span")
	assert.Contains(t, html, ">3</span>")
	assert.NotContains(t, html, "</span> of <span", "there is no total to show, so none is claimed")
}

func TestAuditEmptyStateNamesTheCulprit(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, AuditListContent(AuditListProps{MerchantTZ: time.UTC, Page: 1, PerPage: 50}).Render(context.Background(), &buf))
	assert.Contains(t, buf.String(), "Nothing logged yet",
		"an empty log is not the same as an over-filtered one")

	buf.Reset()
	filtered := AuditListProps{Search: "zzz", MerchantTZ: time.UTC, Page: 1, PerPage: 50}
	require.NoError(t, AuditListContent(filtered).Render(context.Background(), &buf))
	out := buf.String()
	assert.Contains(t, out, "Nothing matches")
	assert.Contains(t, out, "Clear all filters")
}

func TestAuditActionDisplayReadsAsAnEvent(t *testing.T) {
	assert.Equal(t, "Order refunded", auditActionDisplay("order.refunded"))
	assert.Equal(t, "Service ticket status changed", auditActionDisplay("service_ticket.status_changed"))
	assert.Equal(t, "login", auditActionDisplay("login"), "an action with no namespace is left alone")

	// The area select beside it already says which area you are in, so the
	// option labels drop the namespace.
	assert.Equal(t, "Refunded", auditOptionLabel("order.refunded"))
	assert.Equal(t, "Service ticket", auditOptionLabel("service_ticket"))
	assert.False(t, strings.Contains(auditOptionLabel("order.refunded"), "order"))
}
