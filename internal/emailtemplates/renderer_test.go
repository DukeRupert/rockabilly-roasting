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

func TestRender_OrderShipped(t *testing.T) {
	r, err := New()
	require.NoError(t, err)

	shipDate := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	data := OrderShippedData{
		CustomerName:   "Jane",
		OrderNumber:    "RR-1001",
		CarrierName:    "USPS",
		ServiceName:    "Ground Advantage",
		TrackingNumber: "9400111202555842761523",
		TrackingURL:    "https://tools.usps.com/track?qtc_tLabels1=9400111202555842761523",
		ShippedOn:      &shipDate,
		ShippingAddr:   "123 Main St, Austin, TX 78701",
		StoreName:      "Rockabilly Roasting",
		StoreURL:       "https://rockabillyroasting.com",
	}

	html, text, err := r.Render("order_shipped", data)
	require.NoError(t, err)

	for _, want := range []string{"RR-1001", "Jane", "USPS", "Ground Advantage", "9400111202555842761523"} {
		assert.Contains(t, html, want)
		assert.Contains(t, text, want)
	}
	// Tracking CTA only renders when a URL is provided.
	assert.Contains(t, html, data.TrackingURL)
	// Ship date formatted via the date helper.
	assert.Contains(t, html, "April 30, 2026")
}

func TestRender_OrderShipped_OmitsTrackingCTAWhenURLEmpty(t *testing.T) {
	r, err := New()
	require.NoError(t, err)

	html, _, err := r.Render("order_shipped", OrderShippedData{
		CustomerName:   "Jane",
		OrderNumber:    "RR-1002",
		CarrierName:    "USPS",
		TrackingNumber: "9400111202555842761523",
		StoreName:      "Rockabilly Roasting",
		StoreURL:       "https://rockabillyroasting.com",
	})
	require.NoError(t, err)
	// Without a TrackingURL, no CTA anchor is emitted.
	assert.NotContains(t, html, "Track shipment")
}

func TestRender_PasswordSetup(t *testing.T) {
	r, err := New()
	require.NoError(t, err)

	setup := PasswordSetupData{
		CustomerName: "John",
		SetupURL:     "https://example.com/account/password-setup?token=abc123",
		IsReset:      false,
		StoreName:    "Test Store",
		StoreURL:     "https://example.com",
	}
	html, text, err := r.Render("password_setup", setup)
	require.NoError(t, err)
	assert.Contains(t, html, "abc123")
	assert.Contains(t, html, "Set your password")
	assert.NotContains(t, html, "Pick a new password")
	assert.Contains(t, text, "abc123")
	assert.Contains(t, text, "SET YOUR PASSWORD")

	reset := setup
	reset.IsReset = true
	html, text, err = r.Render("password_setup", reset)
	require.NoError(t, err)
	assert.Contains(t, html, "Pick a new password")
	assert.Contains(t, html, "Reset Password")
	assert.Contains(t, text, "PICK A NEW PASSWORD")
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
		IntervalDays: 30,
		NextChargeOn: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC),
		StoreName:    "Test Store",
		StoreURL:     "https://example.com",
		AccountURL:   "https://example.com/account/subscriptions",
	}

	html, text, err := r.Render("subscription_confirm", data)
	require.NoError(t, err)

	assert.Contains(t, html, "Dark Roast 12oz")
	assert.Contains(t, html, "Every 30 days")
	assert.Contains(t, html, "May 15, 2026")
	assert.Contains(t, html, "/account/subscriptions")
	assert.Contains(t, text, "Dark Roast 12oz")
	assert.Contains(t, text, "Every 30 days")
	assert.Contains(t, text, "May 15, 2026")
	assert.Contains(t, text, "/account/subscriptions")
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

func TestRender_SubscriptionRenewalReceipt(t *testing.T) {
	r, err := New()
	require.NoError(t, err)

	next := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	data := SubscriptionRenewalReceiptData{
		CustomerName: "Jane",
		OrderNumber:  "SUB-789",
		OrderDate:    time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC),
		Items: []OrderLineItemData{
			{ProductName: "Dark Roast 12oz", Quantity: 1, UnitPrice: 1620, Total: 1620},
		},
		Subtotal:      1620,
		ShippingTotal: 0,
		TaxTotal:      130,
		OrderTotal:    1750,
		ShippingAddr:  "123 Main St, Austin, TX 78701",
		NextChargeOn:  &next,
		StoreName:     "Test Store",
		StoreURL:      "https://example.com",
		AccountURL:    "https://example.com/account/subscriptions",
	}

	html, text, err := r.Render("subscription_renewal_receipt", data)
	require.NoError(t, err)

	assert.Contains(t, html, "SUB-789")
	assert.Contains(t, html, "Dark Roast 12oz")
	assert.Contains(t, html, "$17.50")
	assert.Contains(t, html, "May 6, 2026")
	assert.Contains(t, text, "SUB-789")
	assert.Contains(t, text, "May 6, 2026")
}

func TestRender_SubscriptionPastDue(t *testing.T) {
	r, err := New()
	require.NoError(t, err)

	data := SubscriptionPastDueData{
		CustomerName: "Jane",
		ProductName:  "Dark Roast 12oz",
		PlanName:     "Every 30 Days",
		StoreName:    "Test Store",
		StoreURL:     "https://example.com",
		AccountURL:   "https://example.com/account/subscriptions",
	}

	html, text, err := r.Render("subscription_past_due", data)
	require.NoError(t, err)

	assert.Contains(t, html, "Dark Roast 12oz")
	assert.Contains(t, html, "Every 30 Days")
	assert.Contains(t, html, "/account/subscriptions")
	assert.Contains(t, text, "Dark Roast 12oz")
}

func TestRender_SubscriptionCancelled(t *testing.T) {
	r, err := New()
	require.NoError(t, err)

	data := SubscriptionCancelledData{
		CustomerName: "Jane",
		ProductName:  "Dark Roast 12oz",
		PlanName:     "Every 30 Days",
		StoreName:    "Test Store",
		StoreURL:     "https://example.com",
		AccountURL:   "https://example.com/account/subscriptions",
	}

	html, text, err := r.Render("subscription_cancelled", data)
	require.NoError(t, err)

	assert.Contains(t, html, "Dark Roast 12oz")
	assert.Contains(t, html, "Every 30 Days")
	assert.Contains(t, text, "Dark Roast 12oz")
}

func TestRender_RefundConfirmation(t *testing.T) {
	r, err := New()
	require.NoError(t, err)

	data := RefundConfirmationData{
		CustomerName: "Jane",
		OrderNumber:  "ORD-123",
		RefundAmount: 4752,
		StoreName:    "Test Store",
		StoreURL:     "https://example.com",
	}

	html, text, err := r.Render("refund_confirmation", data)
	require.NoError(t, err)

	assert.Contains(t, html, "ORD-123")
	assert.Contains(t, html, "$47.52")
	assert.Contains(t, text, "ORD-123")
	assert.Contains(t, text, "$47.52")
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
