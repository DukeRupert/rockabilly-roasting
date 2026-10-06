package admin

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An order that started or renewed several subscriptions leaves
// orders.subscription_id null, so the order page has to link each one it
// covers or staff cannot answer "which subscriptions came from this order?".

func renderSubscriptionLinks(t *testing.T, ctx context.Context, ids []uuid.UUID) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, orderSubscriptionLinks(ids).Render(ctx, &buf))
	return buf.String()
}

func TestOrderSubscriptionLinks_OneLinkPerSubscription(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	html := renderSubscriptionLinks(t, context.Background(), []uuid.UUID{a, b})

	assert.Contains(t, html, "/admin/subscriptions/"+a.String())
	assert.Contains(t, html, "/admin/subscriptions/"+b.String())
	assert.Contains(t, html, "Subscriptions", "the row is labelled for several")
}

func TestOrderSubscriptionLinks_OneSubscriptionReadsAsBefore(t *testing.T) {
	a := uuid.New()
	html := renderSubscriptionLinks(t, context.Background(), []uuid.UUID{a})

	assert.Contains(t, html, "/admin/subscriptions/"+a.String())
	assert.Contains(t, html, "View plan")
	assert.Equal(t, 1, strings.Count(html, "/admin/subscriptions/"))
}

func TestOrderSubscriptionLinks_NothingForARetailOrder(t *testing.T) {
	html := renderSubscriptionLinks(t, context.Background(), nil)
	assert.NotContains(t, html, "/admin/subscriptions/")
	assert.NotContains(t, html, "Subscription")
}
