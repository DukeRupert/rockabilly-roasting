package web

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

// The audit log's query parameters are read straight off a URL a staffer may
// have edited, bookmarked months ago, or received in a message. None of that
// should be able to produce an error page or a silently empty list — the whole
// value of the page is that it answers a question when something has already
// gone wrong.

func TestAuditActorTypeIsClamped(t *testing.T) {
	assert.Equal(t, "staff", normalizeAuditActorType("staff"))
	assert.Equal(t, "customer", normalizeAuditActorType("customer"))
	assert.Equal(t, "system", normalizeAuditActorType("system"))
	assert.Equal(t, "", normalizeAuditActorType("robot"),
		"an unknown actor type widens the list rather than filtering to nothing")
	assert.Equal(t, "", normalizeAuditActorType(""))
}

func TestAuditSortDefaultsToNewestFirst(t *testing.T) {
	assert.Equal(t, store.AuditSortOldest, normalizeAuditSort("oldest"))
	assert.Equal(t, store.AuditSortNewest, normalizeAuditSort("newest"))
	assert.Equal(t, store.AuditSortNewest, normalizeAuditSort(""))
	assert.Equal(t, store.AuditSortNewest, normalizeAuditSort("sideways"),
		"a log is read from the top unless you ask otherwise")
}

func TestAuditActionIsDroppedWhenItLeavesItsArea(t *testing.T) {
	assert.Equal(t, "order.refunded", auditActionForArea("order.refunded", "order"))

	// The bug this guards: a stale action from a previous area survives into
	// the new one and every row is filtered away, while both dropdowns look
	// perfectly sensible.
	assert.Equal(t, "", auditActionForArea("product.updated", "order"))
	assert.Equal(t, "", auditActionForArea("order.refunded", ""),
		"an exact action without an area has no control to sit under")
	assert.Equal(t, "", auditActionForArea("", "order"))

	// A namespace that merely prefixes another must not match: "service" is not
	// "service_ticket".
	assert.Equal(t, "", auditActionForArea("service_ticket.opened", "service"))
}

// A chip labelled with the resource *type* is the same string for every order
// in the system, so the operator cannot tell which record they pinned. The
// fragment is what distinguishes them.
func TestAuditResourceChipNamesTheRecordNotItsKind(t *testing.T) {
	a := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	b := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	rows := []domain.AuditEntry{
		{ResourceType: "order", ResourceID: a},
		{ResourceType: "order", ResourceID: b},
	}

	la := auditResourceLabel(&a, rows)
	lb := auditResourceLabel(&b, rows)
	assert.Equal(t, "order 11111111", la)
	assert.NotEqual(t, la, lb, "two different orders must not pin as the same chip")
	assert.Contains(t, la, a.String()[:8], "the fragment is the part that identifies it")

	// No visible row naming the type still beats rendering nothing useful.
	c := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	assert.Equal(t, "33333333", auditResourceLabel(&c, rows))

	assert.Equal(t, "", auditResourceLabel(nil, rows))
}

func TestAuditUUIDParamsToleratesJunk(t *testing.T) {
	id := uuid.New()
	got := parseUUIDParam(id.String())
	require.NotNil(t, got)
	assert.Equal(t, id, *got)

	assert.Nil(t, parseUUIDParam(""))
	assert.Nil(t, parseUUIDParam("not-a-uuid"),
		"a mangled link widens the list; it must not 400 the page")
	assert.Equal(t, "", uuidParam(nil))
	assert.Equal(t, id.String(), uuidParam(&id))
}
