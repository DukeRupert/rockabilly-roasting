package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// The edit form on the order page, end to end: the corrected address lands in
// the customer's book, the order points at it, and a bad ZIP comes back as a
// flash naming the problem rather than an error page.

func addressEditDeps() *Deps {
	return &Deps{
		Pool:            testPool,
		CustomerService: app.NewCustomerService(store.NewCustomerStore(), audit.NewAuditWriter(), metrics.NewRegistry()),
		OrderService: app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry()).
			WithEmail(app.EmailEnv{}, store.NewCustomerStore(), store.NewCustomerUserStore(), store.NewCatalogStore(), store.NewSubscriptionStore(nil)).
			WithShipments(store.NewShippingStore()),
	}
}

// committedPackedOrder is a packed order shipping to ZIP "9", committed so the
// handler's own transaction can see it.
func committedPackedOrder(t *testing.T) (*domain.Order, *domain.Address) {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	c := testutil.CreateCustomer(t, tx)
	bad := testutil.CreateAddress(t, tx, c.ID, testutil.WithAddressPostalCode("9"))
	order := testutil.CreateOrder(t, tx, c.ID, bad.ID, bad.ID,
		testutil.WithOrderStatus(domain.OrderStatusProcessing),
		testutil.WithFulfillmentStatus(domain.FulfillmentStatusFulfilled))
	require.NoError(t, tx.Commit(ctx))
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1`, order.ID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM addresses WHERE customer_id = $1`, c.ID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, c.ID)
	})
	return order, bad
}

func postAddressEdit(t *testing.T, d *Deps, orderID uuid.UUID, zip string, extra ...func(url.Values)) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{
		"first_name": {"Jennifer"}, "last_name": {"Waltz"},
		"line1": {"1221 W Tamarisk Ave"}, "city": {"Ridgecrest"},
		"state": {"CA"}, "postal_code": {zip},
	}
	for _, f := range extra {
		f(form)
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/orders/"+orderID.String()+"/shipping-address", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", orderID.String())
	w := httptest.NewRecorder()
	d.handleAdminOrderShippingAddress(w, r)
	return w
}

func orderShipTo(t *testing.T, orderID uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	var id uuid.UUID
	var zip string
	require.NoError(t, testPool.QueryRow(context.Background(),
		`SELECT a.id, a.postal_code FROM orders o JOIN addresses a ON a.id = o.shipping_address_id WHERE o.id = $1`,
		orderID).Scan(&id, &zip))
	return id, zip
}

func TestAdminOrderShippingAddress_CorrectsPackedOrder(t *testing.T) {
	d := addressEditDeps()
	order, bad := committedPackedOrder(t)

	w := postAddressEdit(t, d, order.ID, "93555")

	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	assert.Contains(t, w.Header().Get("Location"), "Ship-to+address+updated")
	id, zip := orderShipTo(t, order.ID)
	assert.NotEqual(t, bad.ID, id, "repointed, not edited in place")
	assert.Equal(t, "93555", zip)
}

func TestAdminOrderShippingAddress_BadZIPFlashesAndChangesNothing(t *testing.T) {
	d := addressEditDeps()
	order, bad := committedPackedOrder(t)

	w := postAddressEdit(t, d, order.ID, "9355")

	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	loc, err := url.Parse(w.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "Address not saved: enter a 5-digit ZIP code", loc.Query().Get("flash"))
	id, _ := orderShipTo(t, order.ID)
	assert.Equal(t, bad.ID, id)
}

// The review's probe: an edit that only adds a company must change the
// order, not report success while matching the row it meant to fix.
func TestAdminOrderShippingAddress_CompanyOnlyEditTakesEffect(t *testing.T) {
	d := addressEditDeps()
	order, _ := committedPackedOrder(t)
	require.Equal(t, http.StatusSeeOther, postAddressEdit(t, d, order.ID, "93555").Code)
	before, _ := orderShipTo(t, order.ID)

	w := postAddressEdit(t, d, order.ID, "93555", func(v url.Values) { v.Set("company", "Acme Coffee Co") })

	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	assert.Contains(t, w.Header().Get("Location"), "Ship-to+address+updated")
	after, _ := orderShipTo(t, order.ID)
	assert.NotEqual(t, before, after)
	var company *string
	require.NoError(t, testPool.QueryRow(context.Background(), `SELECT company FROM addresses WHERE id = $1`, after).Scan(&company))
	require.NotNil(t, company)
	assert.Equal(t, "Acme Coffee Co", *company)
}

func TestAdminOrderShippingAddress_UnchangedSubmitSaysSo(t *testing.T) {
	d := addressEditDeps()
	order, _ := committedPackedOrder(t)
	require.Equal(t, http.StatusSeeOther, postAddressEdit(t, d, order.ID, "93555").Code)

	w := postAddressEdit(t, d, order.ID, "93555")

	loc, err := url.Parse(w.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "Nothing to save: that is already the ship-to address.", loc.Query().Get("flash"))
}
