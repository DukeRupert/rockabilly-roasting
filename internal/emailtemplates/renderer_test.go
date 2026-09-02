package emailtemplates

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)
	assert.NotNil(t, r)
}

func TestRender_OrderConfirm(t *testing.T) {
	r, err := New(time.UTC)
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

	// A shipped order carries no delivery date, so the whole local-delivery
	// block must collapse — a mailed order must never be told about the van.
	assert.NotContains(t, html, "Out for delivery")
	assert.NotContains(t, html, "Switch to pickup")
	assert.NotContains(t, text, "OUT FOR DELIVERY")
	assert.NotContains(t, text, "switch-to-pickup")
}

// The delivery block is the whole point of the cutoff feature: it tells the
// customer which run they made and offers the way out if it's too far off.
func TestRender_OrderConfirmLocalDelivery(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	base := OrderConfirmData{
		CustomerName:   "Jane",
		OrderNumber:    "ORD-124",
		OrderDate:      time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		Items:          []OrderLineItemData{{ProductName: "Dark Roast 12oz", Quantity: 1, UnitPrice: 1800, Total: 1800}},
		Subtotal:       1800,
		OrderTotal:     1800,
		StoreName:      "Rockabilly Roasting",
		StoreURL:       "https://rockabillyroasting.com",
		DeliveryDate:   "Thursday, August 13",
		DeliveryCutoff: "9am",
	}

	t.Run("with a switch link", func(t *testing.T) {
		data := base
		data.SwitchToPickupURL = "https://rockabillyroasting.com/orders/switch-to-pickup?t=abc.def"
		data.PickupInstructions = "101 W Kennewick Ave, Tue–Sat 8a–4p"

		html, text, err := r.Render("order_confirm", data)
		require.NoError(t, err)

		for _, out := range []string{html, text} {
			assert.Contains(t, out, "Thursday, August 13")
			assert.Contains(t, out, "9am")
			assert.Contains(t, out, "switch-to-pickup?t=abc.def")
			assert.Contains(t, out, "101 W Kennewick Ave")
		}
		// With a working link, don't also tell them to reply — one call to action.
		assert.NotContains(t, text, "reply to this email and we'll switch")
	})

	t.Run("without a signer, falls back to reply", func(t *testing.T) {
		// SwitchToPickupURL empty models an unset signing secret or a
		// pickup-disabled shop. The date must still land, and the offer must
		// degrade to a reply rather than rendering a dead link.
		html, text, err := r.Render("order_confirm", base)
		require.NoError(t, err)

		assert.Contains(t, html, "Thursday, August 13")
		assert.Contains(t, text, "Thursday, August 13")
		assert.NotContains(t, html, "href=\"\"")
		assert.NotContains(t, html, "switch-to-pickup")
		assert.NotContains(t, text, "switch-to-pickup")
		assert.Contains(t, strings.ToLower(text), "reply to this email")
	})
}

func TestRender_OrderShipped(t *testing.T) {
	r, err := New(time.UTC)
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
	r, err := New(time.UTC)
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
	r, err := New(time.UTC)
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
	r, err := New(time.UTC)
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
	r, err := New(time.UTC)
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
	r, err := New(time.UTC)
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
	r, err := New(time.UTC)
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
	r, err := New(time.UTC)
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
	r, err := New(time.UTC)
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
	r, err := New(time.UTC)
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

// TestRender_SubscriptionPastDueLadder covers every rung of the dunning email
// ladder, in both the states the copy branches on.
//
// The one thing that must never happen is a dead call to action: when the
// signer is unconfigured UpdateCardURL is empty, and the templates have to fall
// back to the sign-in link rather than rendering href="".
func TestRender_SubscriptionPastDueLadder(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	base := SubscriptionPastDueData{
		CustomerName: "Jane",
		ProductName:  "Dark Roast 12oz",
		PlanName:     "Every 30 Days",
		EndsOn:       time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC),
		StoreName:    "Test Store",
		StoreURL:     "https://example.com",
		AccountURL:   "https://example.com/account/subscriptions",
	}

	for _, name := range []string{
		"subscription_past_due",
		"subscription_past_due_reminder",
		"subscription_past_due_final",
	} {
		t.Run(name+"/with one-click link", func(t *testing.T) {
			data := base
			data.UpdateCardURL = "https://example.com/subscriptions/update-card?t=tok"
			html, text, rerr := r.Render(name, data)
			require.NoError(t, rerr)
			for _, body := range []string{html, text} {
				assert.Contains(t, body, "Dark Roast 12oz")
				assert.Contains(t, body, "/subscriptions/update-card?t=tok")
			}
		})

		t.Run(name+"/no signer falls back to sign-in", func(t *testing.T) {
			html, text, rerr := r.Render(name, base) // UpdateCardURL empty
			require.NoError(t, rerr)
			for _, body := range []string{html, text} {
				assert.Contains(t, body, "/account/subscriptions")
				assert.NotContains(t, body, `href=""`)
				assert.NotContains(t, body, "update-card")
			}
		})

		t.Run(name+"/hard decline swaps the explanation", func(t *testing.T) {
			data := base
			data.HardDecline = true
			html, _, rerr := r.Render(name, data)
			require.NoError(t, rerr)
			// Telling someone to check with their bank about a card the bank
			// already killed wastes their time — that copy must not survive.
			assert.NotContains(t, html, "hold from the bank")
			assert.NotContains(t, html, "keep trying the card on file")
		})
	}

	// Only the later two notices name the closing date; the first is too early
	// for a deadline to read as anything but a threat.
	for _, name := range []string{"subscription_past_due_reminder", "subscription_past_due_final"} {
		html, text, rerr := r.Render(name, base)
		require.NoError(t, rerr)
		assert.Contains(t, html, "August 17, 2026", name)
		assert.Contains(t, text, "August 17, 2026", name)
	}
}

func TestRender_SubscriptionCancelled(t *testing.T) {
	r, err := New(time.UTC)
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
	r, err := New(time.UTC)
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

func TestRender_QBInvoiceAlert(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	html, text, err := r.Render("qb_invoice_alert", QBInvoiceAlertData{
		OrderNumber: "RR-1042",
		CompanyName: "Blue Heron Cafe",
		Problem:     "The order could not be invoiced in QuickBooks and the job has stopped retrying.",
		NextStep:    "Fix the underlying problem, then invoice the order by hand in QuickBooks or retry the job.",
		FailedKind:  "qb_create_invoice",
		Cause:       "quickbooks: bad request (data problem)",
		OrderURL:    "https://rockabillyroasting.com/admin/orders/abc",
		StoreName:   "Rockabilly Roasting",
	})
	require.NoError(t, err)

	assert.Contains(t, html, "RR-1042")
	assert.Contains(t, html, "Blue Heron Cafe")
	assert.Contains(t, html, "qb_create_invoice")
	assert.Contains(t, html, "could not be invoiced")
	assert.Contains(t, html, "Fix the underlying problem")

	assert.Contains(t, text, "RR-1042")
	assert.Contains(t, text, "bad request")
	assert.Contains(t, text, "/admin/orders/abc")
}

func TestRender_QBInvoiceAlert_NoCompany(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	html, _, err := r.Render("qb_invoice_alert", QBInvoiceAlertData{
		OrderNumber: "RR-1042",
		Problem:     "The invoice was created in QuickBooks but could not be emailed to the customer.",
		NextStep:    "Send the existing invoice manually from QuickBooks — do not create a new one.",
		FailedKind:  "qb_send_invoice",
		Cause:       "boom",
		OrderURL:    "https://example.com/admin/orders/abc",
		StoreName:   "Rockabilly Roasting",
	})
	require.NoError(t, err)
	assert.NotContains(t, html, " for <strong>")
	assert.Contains(t, html, "do not create a new one")
}

func TestRender_QBTokenAlert(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	expiring, _, err := r.Render("qb_token_alert", QBTokenAlertData{
		DaysLeft:    5,
		Expired:     false,
		ExpiresAt:   time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
		SettingsURL: "https://example.com/admin/settings",
		StoreName:   "Rockabilly Roasting",
	})
	require.NoError(t, err)
	assert.Contains(t, expiring, "expires in <strong>5 days</strong>")
	assert.Contains(t, expiring, "/admin/settings")

	expired, text, err := r.Render("qb_token_alert", QBTokenAlertData{
		Expired:     true,
		ExpiresAt:   time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC),
		SettingsURL: "https://example.com/admin/settings",
		StoreName:   "Rockabilly Roasting",
	})
	require.NoError(t, err)
	assert.Contains(t, expired, "Connection Expired")
	assert.Contains(t, expired, "stalled")
	assert.Contains(t, text, "stalled")
}

func TestRender_SubscriptionSkipped(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	data := SubscriptionSkippedData{
		CustomerName:    "Jane",
		ProductName:     "Switchblade Espresso",
		PlanName:        "Monthly",
		SkippedCount:    2,
		PreviousOrderOn: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		NextChargeOn:    time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		UndoURL:         "https://rockabillyroasting.com/subscriptions/undo-skip?t=abc.def",
		StoreName:       "Rockabilly Roasting",
		StoreURL:        "https://rockabillyroasting.com",
		AccountURL:      "https://rockabillyroasting.com/account/subscriptions",
	}

	html, text, err := r.Render("subscription_skipped", data)
	require.NoError(t, err)

	for _, body := range []string{html, text} {
		// Both dates, so the customer can see what moved and where it landed.
		assert.Contains(t, body, "September 1, 2026")
		assert.Contains(t, body, "November 1, 2026")
		assert.Contains(t, body, "2 shipments")
		assert.Contains(t, body, data.UndoURL)
	}
	// The undo CTA replaces the "reply to us" fallback when a link is present.
	assert.NotContains(t, text, "Reply to this email")
}

// Without a signing secret the link is empty; the mail must still make sense
// and must not print a bare or broken href.
func TestRender_SubscriptionSkipped_NoUndoLink(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	html, text, err := r.Render("subscription_skipped", SubscriptionSkippedData{
		CustomerName: "Jane",
		ProductName:  "Switchblade Espresso",
		PlanName:     "Monthly",
		SkippedCount: 1,
		NextChargeOn: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		StoreName:    "Rockabilly Roasting",
		StoreURL:     "https://rockabillyroasting.com",
		AccountURL:   "https://rockabillyroasting.com/account/subscriptions",
	})
	require.NoError(t, err)

	assert.NotContains(t, html, "undo-skip")
	// One skipped shipment reads as "the next shipment", not "the next 1 shipments".
	assert.Contains(t, html, "the next shipment")
	// A zero previous date must not print as year 0001 — the row drops out.
	assert.NotContains(t, html, "0001")
	assert.NotContains(t, html, "Was billing")
	assert.Contains(t, text, "Reply to this email")
	assert.NotContains(t, strings.ToLower(text), "href")
}

func TestRender_SubscriptionSkipUndone(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	html, text, err := r.Render("subscription_skip_undone", SubscriptionSkipUndoneData{
		CustomerName: "Jane",
		ProductName:  "Switchblade Espresso",
		PlanName:     "Monthly",
		SkippedTo:    time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		NextChargeOn: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		StoreName:    "Rockabilly Roasting",
		StoreURL:     "https://rockabillyroasting.com",
		AccountURL:   "https://rockabillyroasting.com/account/subscriptions",
	})
	require.NoError(t, err)

	for _, body := range []string{html, text} {
		// The whole point of this mail is that the charge moved earlier, so both
		// dates have to be on the page.
		assert.Contains(t, body, "November 1, 2026")
		assert.Contains(t, body, "September 1, 2026")
	}
}

// A SQL `date` is not an instant, and must not be moved by the renderer's zone.
//
// pgx decodes `date` as midnight UTC. Converting that to America/Los_Angeles
// lands at 5pm the previous day, which turned an invoice due April 1 into one
// due March 31 — on wholesale billing mail, where the date is a payment
// obligation and QuickBooks says otherwise. Caught in review after the zone
// conversion was added for subscription dates and applied to every dated field
// without checking which of them were calendar dates.
func TestRender_CalendarDatesAreNotShiftedByTheZone(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	merchant, err := New(la)
	require.NoError(t, err)

	// Exactly what pgx produces for DATE '2026-04-01'.
	due := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	html, text, err := merchant.Render("invoice_sent", InvoiceSentData{
		CustomerName:  "Blue Heron Cafe",
		InvoiceNumber: "INV-1001",
		OrderNumber:   "ORD-1001",
		Total:         12500,
		DueDate:       &due,
		StoreName:     "Rockabilly Roasting",
		StoreURL:      "https://rockabillyroasting.com",
	})
	require.NoError(t, err)
	for _, body := range []string{html, text} {
		assert.Contains(t, body, "April 1, 2026", "the day on the invoice, not the day before")
		assert.NotContains(t, body, "March 31, 2026")
	}

	// The same guard on the two other calendar-date surfaces: the past-due
	// notice a customer reads, and the digest staff reconcile against QB.
	html, text, err = merchant.Render("invoice_past_due", InvoicePastDueData{
		CustomerName:  "Blue Heron Cafe",
		InvoiceNumber: "INV-1001",
		OrderNumber:   "ORD-1001",
		AmountDue:     12500,
		DueDate:       &due,
		Stage:         1,
		StoreName:     "Rockabilly Roasting",
		StoreURL:      "https://rockabillyroasting.com",
	})
	require.NoError(t, err)
	for _, body := range []string{html, text} {
		assert.Contains(t, body, "April 1, 2026")
		assert.NotContains(t, body, "March 31, 2026")
	}
}

// A nil zone is refused rather than defaulted, so a wiring slip fails at
// startup instead of quietly mailing UTC dates to customers. main.go builds the
// renderer from MERCHANT_TIMEZONE, and there is no test over main; this is what
// stands between that wiring and a silent regression.
func TestNewRefusesANilLocation(t *testing.T) {
	r, err := New(nil)
	require.Error(t, err)
	assert.Nil(t, r)
	assert.Contains(t, err.Error(), "merchant zone")
}

// A date in a customer's inbox has to be the merchant's date.
//
// Timestamps reach the renderer in the database session zone — UTC on the
// server — so formatting them as they arrive prints a merchant evening as the
// following day. That stayed invisible only because renewals are anchored at
// 2am, where Los Angeles and UTC agree on the date; RENEWAL_ANCHOR_HOUR accepts
// any hour, so the agreement was a setting rather than a property, and the card
// and the email would have drifted a day from the admin toast with nothing
// failing loudly.
func TestRender_FormatsDatesInTheConfiguredZone(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	// 10pm in Los Angeles is already the next day in UTC.
	evening := time.Date(2027, 3, 12, 22, 0, 0, 0, la)
	data := SubscriptionResumedData{
		CustomerName: "Jane",
		ProductName:  "Switchblade Espresso",
		PlanName:     "Weekly",
		NextChargeOn: evening.UTC(),
		StoreName:    "Rockabilly Roasting",
		StoreURL:     "https://rockabillyroasting.com",
		AccountURL:   "https://rockabillyroasting.com/account/subscriptions",
	}

	merchant, err := New(la)
	require.NoError(t, err)
	html, text, err := merchant.Render("subscription_resumed", data)
	require.NoError(t, err)
	for _, body := range []string{html, text} {
		assert.Contains(t, body, "March 12, 2027", "the merchant's day, not the stored value's")
		assert.NotContains(t, body, "March 13, 2027")
	}

	// And the zone is the renderer's, not the value's: same instant, UTC
	// renderer, the other date.
	utc, err := New(time.UTC)
	require.NoError(t, err)
	html, text, err = utc.Render("subscription_resumed", data)
	require.NoError(t, err)
	for _, body := range []string{html, text} {
		assert.Contains(t, body, "March 13, 2027")
	}
}

func TestRender_SubscriptionResumed(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	html, text, err := r.Render("subscription_resumed", SubscriptionResumedData{
		CustomerName: "Jane",
		ProductName:  "Switchblade Espresso",
		PlanName:     "Weekly",
		NextChargeOn: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		StoreName:    "Rockabilly Roasting",
		StoreURL:     "https://rockabillyroasting.com",
		AccountURL:   "https://rockabillyroasting.com/account/subscriptions",
	})
	require.NoError(t, err)

	for _, body := range []string{html, text} {
		// A resume charges on the next renewal run, so the date is the message. An email
		// that only said "you're back on" would be the same silence the customer
		// got before.
		assert.Contains(t, body, "September 1, 2026")
		assert.Contains(t, body, "Switchblade Espresso")
		assert.Contains(t, body, "Jane")
	}
}

func staleDigestTicket(number string, quietDays int, down bool) ServiceStaleDigestTicket {
	sev := "routine"
	if down {
		sev = "down"
	}
	return ServiceStaleDigestTicket{
		Number:    number,
		Title:     "Grinder throwing a burr error",
		Customer:  "Blue Heron Cafe",
		Severity:  sev,
		Status:    "Waiting on parts",
		QuietDays: quietDays,
		URL:       "https://rockabillyroasting.com/admin/service/tickets/abc",
		Down:      down,
	}
}

func TestRender_ServiceStaleDigest(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	html, text, err := r.Render("service_stale_digest", ServiceStaleDigestData{
		Tickets:    []ServiceStaleDigestTicket{staleDigestTicket("SVC-A1B2C3D4E5", 11, true)},
		Total:      1,
		WindowDays: 7,
		QueueURL:   "https://rockabillyroasting.com/admin/service?scope=stale",
		StoreName:  "Rockabilly Roasting",
	})
	require.NoError(t, err)

	for _, want := range []string{"SVC-A1B2C3D4E5", "Blue Heron Cafe", "Waiting on parts", "DOWN"} {
		assert.Contains(t, html, want)
		assert.Contains(t, text, want)
	}

	// A single stale ticket must not read "1 open tickets have".
	assert.Contains(t, html, "One open ticket has")
	assert.Contains(t, text, "One open ticket has")
	assert.Contains(t, html, "quiet for 11 days")

	// Nothing to truncate, so no "showing N of M" line.
	assert.NotContains(t, html, "quietest of")
	assert.NotContains(t, text, "quietest of")
}

// The digest is capped, and a capped list that implied it was the whole set
// would be worse than no digest — staff would work the list and stop.
func TestRender_ServiceStaleDigest_SaysWhatItLeftOut(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	listed := []ServiceStaleDigestTicket{
		staleDigestTicket("SVC-0000000001", 9, false),
		staleDigestTicket("SVC-0000000002", 8, false),
	}
	html, text, err := r.Render("service_stale_digest", ServiceStaleDigestData{
		Tickets:    listed,
		Total:      31,
		WindowDays: 7,
		QueueURL:   "https://example.com/admin/service?scope=stale",
		StoreName:  "Rockabilly Roasting",
	})
	require.NoError(t, err)

	assert.Contains(t, html, "31 open tickets have")
	assert.Contains(t, html, "Showing the 2 quietest of 31")
	assert.Contains(t, text, "Showing the 2 quietest of 31")

	// Plural agreement on the per-row day count.
	assert.Contains(t, html, "quiet for 9 days")
}

// One day is singular; the row must not say "quiet for 1 days".
func TestRender_ServiceStaleDigest_SingularDay(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	html, text, err := r.Render("service_stale_digest", ServiceStaleDigestData{
		Tickets:    []ServiceStaleDigestTicket{staleDigestTicket("SVC-0000000003", 1, false)},
		Total:      1,
		WindowDays: 7,
		QueueURL:   "https://example.com/admin/service?scope=stale",
		StoreName:  "Rockabilly Roasting",
	})
	require.NoError(t, err)

	// The point is the missing "s", so assert on its absence rather than on
	// whatever markup happens to follow the phrase.
	assert.Contains(t, html, "quiet for 1 day")
	assert.NotContains(t, html, "quiet for 1 days")
	assert.Contains(t, text, "quiet for 1 day")
	assert.NotContains(t, text, "quiet for 1 days")

	// A routine ticket carries no DOWN flag.
	assert.NotContains(t, html, "DOWN")
	assert.NotContains(t, text, "[DOWN]")
}

func TestQBShadowDigestRenders(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	due := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	data := QBShadowDigestData{
		Invoices: []QBShadowDigestInvoice{
			{OrderNumber: "WO-1", Customer: "Blue Heron Cafe", TotalCents: 7200,
				Terms: "Net 7", DueDate: due, BillEmail: "buyer@example.test", URL: "https://x/admin/orders/1"},
			{OrderNumber: "WO-2", Customer: "Roadside Diner", TotalCents: 19900,
				Terms: "Due on receipt", DueDate: due, URL: "https://x/admin/orders/2",
				Problem: "No matching QuickBooks customer."},
		},
		// Total exceeds the listed rows, exercising the truncation branch.
		Total: 5, TotalAmtCents: 41800, Attention: 1, Listed: 5, Days: 7,
		ReviewURL: "https://x/admin/settings/integrations/quickbooks/preview",
		StoreName: "Rockabilly Roasting Co.",
	}

	html, text, err := r.Render("qb_shadow_digest", data)
	require.NoError(t, err)

	for _, body := range []string{html, text} {
		assert.Contains(t, body, "WO-1")
		assert.Contains(t, body, "$72.00", "money is formatted by the cents helper")
		assert.Contains(t, body, "No matching QuickBooks customer.")
		assert.Contains(t, body, "Showing 2 of 5", "a capped list must say it is capped")
		assert.NotContains(t, body, "{{", "no unrendered template directives")
	}
	// The digest must never read as if anything was billed.
	assert.Contains(t, text, "Nothing below was billed")
}

func TestQBShadowDigestSingularInvoice(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	html, text, err := r.Render("qb_shadow_digest", QBShadowDigestData{
		Invoices: []QBShadowDigestInvoice{
			{OrderNumber: "WO-9", Customer: "One Shop", TotalCents: 500,
				Terms: "Net 7", DueDate: time.Now(), URL: "https://x"},
		},
		Total: 1, TotalAmtCents: 500, Listed: 1, Days: 7, ReviewURL: "https://x", StoreName: "Rockabilly",
	})
	require.NoError(t, err)
	for _, body := range []string{html, text} {
		assert.Contains(t, body, "One invoice")
		assert.Contains(t, body, "Nothing needs attention")
		assert.NotContains(t, body, "Showing 1 of 1", "an untruncated list must not claim to be truncated")
	}
}

func TestQBShadowDigestAllManualWeek(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	// The shape every shop starts in: nothing is billed automatically because
	// no account has an agreement yet. The digest must still say what is
	// waiting, and must not read as "nothing happened".
	html, text, err := r.Render("qb_shadow_digest", QBShadowDigestData{
		Invoices: []QBShadowDigestInvoice{
			{OrderNumber: "WO-7", Customer: "Blue Heron Cafe", TotalCents: 7200,
				Terms: "Net 7", DueDate: time.Now(), URL: "https://x", Manual: true},
		},
		Total: 0, TotalAmtCents: 0, AwaitingManual: 4, Listed: 4, Days: 7,
		ReviewURL: "https://x", StoreName: "Rockabilly",
	})
	require.NoError(t, err)

	for _, body := range []string{html, text} {
		assert.Contains(t, body, "invoicing by hand",
			"an all-manual week must say what is waiting, not report an empty week")
		assert.Contains(t, body, "Blue Heron Cafe")
		assert.Contains(t, body, "Showing 1 of 4",
			"truncation counts every listed row, not just the billable ones")
		assert.NotContains(t, body, "{{")
	}
	assert.Contains(t, text, "[MANUAL]", "a manual row is marked as such")
}
