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

	// --- Local delivery ---
	//
	// DeliveryDate is the run this order was booked on ("Thursday, August 13"),
	// set only for local-delivery orders with a configured schedule. Empty for
	// everything else, which collapses the whole delivery block out of both
	// templates — a shipped order must not be told about the van.
	DeliveryDate string
	// DeliveryCutoff is the order-by time that decided that date ("9am"), so a
	// customer who just missed it understands why they're waiting and what to
	// beat next time.
	DeliveryCutoff string
	// SwitchToPickupURL is the signed one-click link offering pickup instead of
	// waiting for the run. Empty when pickup is switched off in admin, or when
	// no signing secret is configured — in both cases the templates fall back
	// to "reply and we'll sort it" rather than printing a dead link.
	SwitchToPickupURL string
	// PickupInstructions is the shop address and hours, shown alongside the
	// offer so the customer can judge whether collecting is actually easier
	// before they click.
	PickupInstructions string
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

// InvoicePastDueData holds data for a wholesale past-due reminder. Stage is
// which reminder this is: 1 fires when the invoice first goes overdue, later
// stages weekly after that.
// NOTE: per-stage copy is still to be finalized — see
// docs/qb-overdue-reminders-TODO.md.
type InvoicePastDueData struct {
	CustomerName  string
	InvoiceNumber string
	OrderNumber   string
	AmountDue     int        // cents still owed (invoice total)
	DueDate       *time.Time // QB's authoritative due date; nil hides the "Was Due" row
	Stage         int        // which reminder this is (1 on going overdue, then weekly)
	PaymentURL    string
	StoreName     string
	StoreURL      string
}

// QBInvoiceAlertData holds data for the staff notification sent when a
// QuickBooks invoicing job fails permanently. Problem and NextStep are
// computed per failed step — a send-only failure must NOT tell staff to
// issue the invoice by hand (it already exists in QB; that would double-bill).
type QBInvoiceAlertData struct {
	OrderNumber string
	CompanyName string // empty hides the "for <company>" clause
	Problem     string // what went wrong, in staff terms
	NextStep    string // what staff must do about it
	FailedKind  string // job kind that failed (qb_ensure_customer / qb_create_invoice / qb_send_invoice)
	Cause       string // error message
	OrderURL    string // admin order detail page
	StoreName   string
}

// QBTokenAlertData holds data for the staff warning that the QuickBooks
// connection (refresh token) is about to lapse or already has.
type QBTokenAlertData struct {
	DaysLeft    int  // days until the refresh token expires; <= 0 when expired
	Expired     bool
	ExpiresAt   time.Time
	SettingsURL string // admin settings page with the Reconnect button
	StoreName   string
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

// WholesaleMigratedData holds data for the welcome email sent to wholesale
// customers moved over from Orderspace. Unlike the approved email, the copy
// explains the move and the new NET 7 terms.
type WholesaleMigratedData struct {
	CompanyName string
	SetupURL    string
	StoreName   string
	StoreURL    string
}

// OrderReminderData holds data for the weekly wholesale order reminder — the
// "get your order in before the cutoff" nudge that used to be sent by the
// standalone rr service against Orderspace.
type OrderReminderData struct {
	CompanyName string
	CutoffLabel string // human cutoff, e.g. "Friday afternoon"
	// LastItems is the customer's most recent order, printed so they can
	// decide in the inbox rather than after signing in. Empty when the lookup
	// failed or they have no completed order — the email still sends.
	LastItems     []OrderLineItemData
	LastOrderedOn *time.Time
	ReorderURL    string // deep link that prefills a cart from LastItems
	OrderURL      string // wholesale portal, replaces the old Orderspace link
	// UnsubscribeURL is the signed one-off opt-out link. Empty when no signing
	// secret is configured; the footer then falls back to asking them to reply.
	UnsubscribeURL string
	StoreName     string
	StoreURL      string
}

// WholesaleNoticeData holds data for a staff-composed one-off notice sent to
// the same audience as the weekly reminder (corrections, holiday cutoffs, etc).
//
// Paragraphs is plain text, not HTML: staff compose in a textarea and the body
// is split on blank lines, so the branded shell is preserved and nothing staff
// type can break the markup.
type WholesaleNoticeData struct {
	Heading    string
	Paragraphs []string
	OrderURL   string
	StoreName  string
	StoreURL   string
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

// WhiteLabelInviteData holds data for the white-label onboarding invite email
// sent to an approved wholesale customer.
type WhiteLabelInviteData struct {
	CompanyName string
	InviteURL   string
	StoreName   string
	StoreURL    string
}

// StaffInviteData holds data for the staff invite / password-setup email sent
// when an admin adds a new team member (or resends the link as a reset).
type StaffInviteData struct {
	StaffName string
	InviteURL string
	StoreName string
	StoreURL  string
}

// CustomerUserInviteData holds data for the email inviting an additional
// person to sign in on a wholesale account.
type CustomerUserInviteData struct {
	Name        string
	CompanyName string
	SetupURL    string
	StoreName   string
	StoreURL    string
}

// WhiteLabelSubmittedData holds data for the staff notification sent when a
// client submits a white-label product for review.
type WhiteLabelSubmittedData struct {
	CompanyName   string
	CustomerEmail string
	ProductName   string
	BaseCoffee    string
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
