package emailtemplates

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"
	text "text/template"
	"time"
)

//go:embed html/*.html text/*.txt
var templateFiles embed.FS

// Renderer renders email templates from embedded files.
type Renderer struct {
	html *template.Template
	text *text.Template
}

// New parses all embedded HTML and text templates and returns a Renderer.
func New() (*Renderer, error) {
	funcMap := template.FuncMap{
		"cents": formatCents,
		"date":  formatDate,
	}
	textFuncMap := text.FuncMap{
		"cents": formatCents,
		"date":  formatDate,
	}

	htmlTmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFiles, "html/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse html templates: %w", err)
	}

	textTmpl, err := text.New("").Funcs(textFuncMap).ParseFS(templateFiles, "text/*.txt")
	if err != nil {
		return nil, fmt.Errorf("parse text templates: %w", err)
	}

	return &Renderer{html: htmlTmpl, text: textTmpl}, nil
}

// Render executes the named template with the given data and returns HTML and
// plain text bodies. Template names correspond to filenames without extension
// (e.g. "order_confirm").
func (r *Renderer) Render(name string, data any) (htmlBody, textBody string, err error) {
	var htmlBuf bytes.Buffer
	if err := r.html.ExecuteTemplate(&htmlBuf, name+".html", data); err != nil {
		return "", "", fmt.Errorf("render html %s: %w", name, err)
	}

	var textBuf bytes.Buffer
	if err := r.text.ExecuteTemplate(&textBuf, name+".txt", data); err != nil {
		return "", "", fmt.Errorf("render text %s: %w", name, err)
	}

	return htmlBuf.String(), textBuf.String(), nil
}

// --- Template data types ---

// OrderConfirmData holds data for the order confirmation email.
type OrderConfirmData struct {
	CustomerName  string
	OrderNumber   string
	OrderDate     time.Time
	Items         []OrderLineItemData
	Subtotal      int
	DiscountTotal int
	ShippingTotal int
	TaxTotal      int
	OrderTotal    int
	ShippingAddr  string
	StoreName     string
	StoreURL      string
}

// OrderLineItemData is a line item for email rendering.
type OrderLineItemData struct {
	ProductName string
	Quantity    int
	UnitPrice   int
	Total       int
}

// InvoiceSentData holds data for the invoice email.
type InvoiceSentData struct {
	CustomerName  string
	InvoiceNumber string
	OrderNumber   string
	Total         int
	DueDate       *time.Time
	PaymentURL    string
	StoreName     string
	StoreURL      string
}

// InvoicePaidData holds data for the wholesale invoice payment-confirmation
// email, sent when a QuickBooks invoice is paid in full.
type InvoicePaidData struct {
	CustomerName  string
	InvoiceNumber string
	OrderNumber   string
	AmountPaid    int // cents paid in full
	StoreName     string
	StoreURL      string
	AccountURL    string
}

// InvoicePastDueData holds data for a wholesale past-due reminder. Stage is the
// milestone (days since the order was placed) that triggered the reminder.
// NOTE: copy for the 7/14/21/30 milestones is still to be finalized — see
// docs/qb-overdue-reminders-TODO.md.
type InvoicePastDueData struct {
	CustomerName  string
	InvoiceNumber string
	OrderNumber   string
	AmountDue     int        // cents still owed (invoice total)
	DueDate       *time.Time // net-terms due date
	Stage         int        // reminder milestone in days-since-placed (7/14/21/30)
	PaymentURL    string
	StoreName     string
	StoreURL      string
}

// MagicLinkData holds data for the magic link email.
type MagicLinkData struct {
	CustomerName string
	MagicLinkURL string
	ExpiresIn    string
	StoreName    string
	StoreURL     string
}

// VerifyEmailData holds data for the email verification email.
type VerifyEmailData struct {
	CustomerName string
	VerifyURL    string
	ExpiresIn    string
	StoreName    string
	StoreURL     string
}

// SubscriptionConfirmData holds data for the subscription confirmation email.
type SubscriptionConfirmData struct {
	CustomerName string
	PlanName     string
	ProductName  string
	Quantity     int
	IntervalDays int       // billing cadence in days (e.g. 30 for "every 30 days")
	NextChargeOn time.Time // when the next renewal payment will run
	StoreName    string
	StoreURL     string
	AccountURL   string
}

// PasswordSetupData holds data for the admin-triggered password setup / reset
// email. IsReset toggles the wording: false for accounts that have never had a
// password (set), true for accounts that have one (reset).
type PasswordSetupData struct {
	CustomerName string
	SetupURL     string
	IsReset      bool
	StoreName    string
	StoreURL     string
}

// WholesaleApprovedData holds data for the wholesale approved welcome email.
type WholesaleApprovedData struct {
	CompanyName string
	SetupURL    string
	StoreName   string
	StoreURL    string
}

// WholesaleSuspendedData holds data for the wholesale suspended notification email.
type WholesaleSuspendedData struct {
	CompanyName string
	StoreName   string
	StoreURL    string
}

// WholesaleApplicationData holds data for the wholesale application staff notification.
type WholesaleApplicationData struct {
	CompanyName   string
	CustomerEmail string
	ReviewURL     string
	StoreName     string
}

// SubscriptionRenewalReceiptData holds data for a subscription renewal receipt
// — sent after a successful off-session charge creates a renewal order.
type SubscriptionRenewalReceiptData struct {
	CustomerName  string
	OrderNumber   string
	OrderDate     time.Time
	Items         []OrderLineItemData
	Subtotal      int
	DiscountTotal int
	ShippingTotal int
	TaxTotal      int
	OrderTotal    int
	ShippingAddr  string
	NextChargeOn  *time.Time // when the next renewal is scheduled, nil if cancelled-at-period-end
	StoreName     string
	StoreURL      string
	AccountURL    string
}

// SubscriptionPastDueData holds data for the past-due / payment-failed notice.
type SubscriptionPastDueData struct {
	CustomerName string
	ProductName  string
	PlanName     string
	StoreName    string
	StoreURL     string
	AccountURL   string
}

// SubscriptionCancelledData holds data for the subscription-cancelled confirmation.
type SubscriptionCancelledData struct {
	CustomerName string
	ProductName  string
	PlanName     string
	StoreName    string
	StoreURL     string
	AccountURL   string
}

// SubscriptionDunningEndedData holds data for the "we couldn't renew, your
// subscription has ended" notice sent when dunning retries are exhausted.
type SubscriptionDunningEndedData struct {
	CustomerName string
	ProductName  string
	PlanName     string
	StoreName    string
	StoreURL     string
	AccountURL   string
}

// RefundConfirmationData holds data for the refund-issued confirmation.
type RefundConfirmationData struct {
	CustomerName string
	OrderNumber  string
	RefundAmount int // cents refunded (full or partial)
	StoreName    string
	StoreURL     string
}

// OrderShippedData holds data for the "your order is on the road" notification
// — sent after a Pirate Ship tracking row imports successfully and the order
// flips to shipped.
type OrderShippedData struct {
	CustomerName   string
	OrderNumber    string
	CarrierName    string
	ServiceName    string
	TrackingNumber string
	TrackingURL    string // optional carrier tracking URL; blank if unknown
	ShippedOn      *time.Time
	ShippingAddr   string
	StoreName      string
	StoreURL       string
	AccountURL     string
}

// AccountNotMigratedData holds data for the support reply explaining that the
// customer's old account didn't carry over (only active subscribers were
// migrated) and they'll be re-created automatically on their next order.
type AccountNotMigratedData struct {
	CustomerName string
	StoreName    string
	StoreURL     string
}

// --- Template helpers ---

func formatCents(cents int) string {
	dollars := cents / 100
	remainder := cents % 100
	if remainder < 0 {
		remainder = -remainder
	}
	sign := ""
	if cents < 0 {
		sign = "-"
		dollars = -dollars
	}
	return fmt.Sprintf("%s$%d.%02d", sign, dollars, remainder)
}

func formatDate(t any) string {
	switch v := t.(type) {
	case time.Time:
		return v.Format("January 2, 2006")
	case *time.Time:
		if v == nil {
			return ""
		}
		return v.Format("January 2, 2006")
	default:
		return ""
	}
}

// FormatAddress builds a one-line address string for email display.
func FormatAddress(line1, city, state, postalCode string) string {
	parts := []string{line1, city}
	if state != "" {
		parts = append(parts, state)
	}
	return strings.Join(parts, ", ") + " " + postalCode
}

// FormatRecipientAddress builds a one-line shipping address including the
// recipient's name and second address line — the full destination as it
// appears on the label, so the customer can spot a wrong apartment or an
// old address before the order ships.
func FormatRecipientAddress(firstName, lastName, line1 string, line2 *string, city, state, postalCode string) string {
	parts := []string{}
	if name := strings.TrimSpace(firstName + " " + lastName); name != "" {
		parts = append(parts, name)
	}
	parts = append(parts, line1)
	if line2 != nil && strings.TrimSpace(*line2) != "" {
		parts = append(parts, strings.TrimSpace(*line2))
	}
	parts = append(parts, city)
	if state != "" {
		parts = append(parts, state)
	}
	return strings.Join(parts, ", ") + " " + postalCode
}
