package emailtemplates

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	r, err := New()
	require.NoError(t, err)
	assert.NotNil(t, r)
}

func TestRender_OrderConfirm(t *testing.T) {
	r, err := New()
	require.NoError(t, err)

	data := OrderConfirmData{
		CustomerName: "Jane",
		OrderNumber:  "ORD-123",
		OrderDate:    time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC),
		Items: []OrderLineItemData{
			{ProductName: "Dark Roast 12oz", Quantity: 2, UnitPrice: 1800, Total: 3600},
		},
		Subtotal:      3600,
		ShippingTotal: 800,
		TaxTotal:      352,
		OrderTotal:    4752,
		ShippingAddr:  "123 Main St, Austin, TX 78701",
		StoreName:     "Rockabilly Roasting",
		StoreURL:      "https://rockabillyroasting.com",
	}

	html, text, err := r.Render("order_confirm", data)
	require.NoError(t, err)

	assert.Contains(t, html, "ORD-123")
	assert.Contains(t, html, "Jane")
	assert.Contains(t, html, "Dark Roast 12oz")
	assert.Contains(t, html, "$36.00")
	assert.Contains(t, html, "$47.52")

	assert.Contains(t, text, "ORD-123")
	assert.Contains(t, text, "Jane")
	assert.Contains(t, text, "$36.00")
}

func TestRender_MagicLink(t *testing.T) {
	r, err := New()
	require.NoError(t, err)

	data := MagicLinkData{
		CustomerName: "John",
		MagicLinkURL: "https://example.com/account/magic?token=abc123",
		ExpiresIn:    "15 minutes",
		StoreName:    "Test Store",
		StoreURL:     "https://example.com",
	}

	html, text, err := r.Render("magic_link", data)
	require.NoError(t, err)

	assert.Contains(t, html, "abc123")
	assert.Contains(t, html, "15 minutes")
	assert.Contains(t, text, "abc123")
}

func TestRender_InvoiceSent(t *testing.T) {
	r, err := New()
	require.NoError(t, err)

	due := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	data := InvoiceSentData{
		CustomerName:  "Acme Corp",
		InvoiceNumber: "INV-001",
		OrderNumber:   "ORD-456",
		Total:         25000,
		DueDate:       &due,
		PaymentURL:    "https://example.com/invoices/xyz/pay",
		StoreName:     "Test Store",
		StoreURL:      "https://example.com",
	}

	html, text, err := r.Render("invoice_sent", data)
	require.NoError(t, err)

	assert.Contains(t, html, "INV-001")
	assert.Contains(t, html, "$250.00")
	assert.Contains(t, html, "April 1, 2026")
	assert.Contains(t, text, "INV-001")
	assert.Contains(t, text, "$250.00")
}

func TestRender_SubscriptionConfirm(t *testing.T) {
	r, err := New()
	require.NoError(t, err)

	data := SubscriptionConfirmData{
		CustomerName: "Jane",
		PlanName:     "Every 30 Days",
		ProductName:  "Dark Roast 12oz",
		Quantity:     1,
		Interval:     "Every 30 days",
		UnitPrice:    1620,
		StoreName:    "Test Store",
		StoreURL:     "https://example.com",
	}

	html, text, err := r.Render("subscription_confirm", data)
	require.NoError(t, err)

	assert.Contains(t, html, "Dark Roast 12oz")
	assert.Contains(t, html, "$16.20")
	assert.Contains(t, text, "Dark Roast 12oz")
}

func TestRender_WholesaleApproved(t *testing.T) {
	r, err := New()
	require.NoError(t, err)

	data := WholesaleApprovedData{
		CompanyName: "Bean Co",
		SetupURL:    "https://example.com/wholesale/setup?token=abc123",
		StoreName:   "Test Store",
		StoreURL:    "https://example.com",
	}

	html, text, err := r.Render("wholesale_approved", data)
	require.NoError(t, err)

	assert.Contains(t, html, "Bean Co")
	assert.Contains(t, html, "/wholesale/setup?token=abc123")
	assert.Contains(t, text, "Bean Co")
	assert.Contains(t, text, "/wholesale/setup?token=abc123")
}

func TestRender_WholesaleApplication(t *testing.T) {
	r, err := New()
	require.NoError(t, err)

	data := WholesaleApplicationData{
		CompanyName:   "Bean Co",
		CustomerEmail: "bean@example.com",
		ReviewURL:     "https://example.com/admin/wholesale",
		StoreName:     "Test Store",
	}

	html, text, err := r.Render("wholesale_application", data)
	require.NoError(t, err)

	assert.Contains(t, html, "Bean Co")
	assert.Contains(t, html, "bean@example.com")
	assert.Contains(t, text, "Bean Co")
}

func TestFormatCents(t *testing.T) {
	assert.Equal(t, "$0.00", formatCents(0))
	assert.Equal(t, "$1.00", formatCents(100))
	assert.Equal(t, "$18.50", formatCents(1850))
	assert.Equal(t, "-$5.00", formatCents(-500))
}

func TestFormatAddress(t *testing.T) {
	addr := FormatAddress("123 Main St", "Austin", "TX", "78701")
	assert.True(t, strings.Contains(addr, "Austin"))
	assert.True(t, strings.Contains(addr, "78701"))
}
