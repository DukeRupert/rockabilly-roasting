package admin

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

func renderCustomerShow(t *testing.T, customer *domain.Customer) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, CustomerShowContent(CustomerShowProps{
		Customer:   customer,
		MerchantTZ: time.UTC,
	}).Render(context.Background(), &buf))
	return buf.String()
}

func wholesaleCustomer(method domain.BillingMethod, company string) *domain.Customer {
	status := domain.WholesaleStatusApproved
	c := &domain.Customer{
		ID:              uuid.New(),
		Email:           "buyer@example.test",
		FirstName:       "Nathan",
		LastName:        "Mora",
		AccountType:     domain.AccountTypeWholesale,
		WholesaleStatus: &status,
		BillingMethod:   method,
	}
	if company != "" {
		c.CompanyName = &company
	}
	return c
}

// Switching an account onto ACH or card starts QuickBooks invoicing it without
// anybody asking again, so it must not be a single click on a self-submitting
// menu. The dialog host has to be on the page too: ConfirmAttrs-style dispatch
// goes nowhere without it, and a gate that silently does nothing is worse than
// no gate, because it looks like it worked.
func TestCustomerShow_BillingMethodIsGated(t *testing.T) {
	html := renderCustomerShow(t, wholesaleCustomer(domain.BillingMethodManual, "Blue Heron Cafe"))

	assert.Contains(t, html, "admin-confirm",
		"the select must raise the confirm dialog rather than submitting on change")
	assert.Contains(t, html, "adminConfirm",
		"and the dialog host must be rendered on this page, or the dispatch goes nowhere")
	assert.Contains(t, html, "Blue Heron Cafe",
		"the confirmation names the account being switched")
	assert.Contains(t, html, "nothing here checks that",
		"and says plainly that no agreement is verified, because none is")

	// Declining has to put the menu back. A select showing ACH while the
	// database says manual is worse than no gate: nothing on the page reveals
	// the disagreement.
	assert.Contains(t, html, "$el.value = previous")
	assert.Contains(t, html, "previous: &#39;manual&#39;",
		"seeded from the stored method so cancel restores the real value")

	// Turning billing off is the safe direction and stays a plain submit.
	// Unescaped here, unlike previous above: templ escapes the dynamic x-data
	// attribute and leaves this static one verbatim, so the two assertions
	// legitimately differ.
	assert.Contains(t, html, "$el.value === 'manual'")
}

// The copy has to describe the transition that is actually happening. An
// account already on ACH is not being switched on; only its pay button moves.
func TestCustomerShow_BillingMethodCopyFollowsTheTransition(t *testing.T) {
	off := renderCustomerShow(t, wholesaleCustomer(domain.BillingMethodManual, "Blue Heron Cafe"))
	assert.Contains(t, off, "Start invoicing ")
	assert.Contains(t, off, "Not invoiced automatically",
		"and the page says so before anyone opens the menu")

	on := renderCustomerShow(t, wholesaleCustomer(domain.BillingMethodACH, "Blue Heron Cafe"))
	assert.Contains(t, on, "Change how ")
	assert.Contains(t, on, "already invoiced automatically")
	assert.NotContains(t, on, "Not invoiced automatically",
		"an auto-billed account must not be described as one that is not")
}

// The dialog title is the one place the operator reads who they are about to
// bill, so it must never be blank.
func TestCustomerBillingName(t *testing.T) {
	assert.Equal(t, "Blue Heron Cafe",
		customerBillingName(wholesaleCustomer(domain.BillingMethodManual, "Blue Heron Cafe")))

	// A company name of spaces would otherwise title the dialog "Start
	// invoicing  automatically?", naming nobody.
	assert.Equal(t, "Nathan Mora",
		customerBillingName(wholesaleCustomer(domain.BillingMethodManual, "   ")))

	noName := wholesaleCustomer(domain.BillingMethodManual, "")
	noName.FirstName, noName.LastName = "", ""
	assert.Equal(t, "buyer@example.test", customerBillingName(noName),
		"falling back to something identifying rather than an empty title")
}
