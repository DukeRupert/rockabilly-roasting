package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A one-digit ZIP is how a real subscription order ended up priced and queued
// for a ZIP that does not exist. Both checkouts refuse it before creating a
// customer, an address, an order or a PaymentIntent, and say which field.

func TestSubscribePaymentIntent_RejectsTruncatedZIP(t *testing.T) {
	f := newMultiLineFixture(t)
	d, fake := newSubscribePaymentDeps(t)

	w := postSubscribePaymentIntent(t, d, multiLineBody(f.twoLines(), func(m map[string]any) {
		m["postal_code"] = "9"
	}))

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "5-digit ZIP")
	assert.Equal(t, 0, fake.intentCount(), "no PaymentIntent for an undeliverable address")
}

func TestCheckoutAddress_RejectsTruncatedZIP(t *testing.T) {
	d := &Deps{}
	body := `{"email":"zip@example.test","first_name":"A","last_name":"B","line1":"1 Main St",` +
		`"city":"Ridgecrest","state":"CA","postal_code":"9","country":"US"}`
	r := httptest.NewRequest(http.MethodPost, "/api/checkout/address", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleCheckoutAddress(w, r)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	var resp struct {
		Errors map[string]string `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Enter a 5-digit ZIP code", resp.Errors["postal_code"])
}
