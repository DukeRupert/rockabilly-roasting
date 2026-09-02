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

// New parses all embedded HTML and text templates and returns a Renderer whose
// {{date}} func renders in loc. That is every dated field in every template —
// order and shipment dates, renewal and skip dates, token expiry — so
// MERCHANT_TIMEZONE is load-bearing for what customers read, not just for
// dashboard boundaries. Calendar dates go through {{day}} instead and are not
// converted; see formatDay.
//
// The zone is a constructor argument because a date in a customer's inbox has
// to be the merchant's date. Timestamps arrive from pgx in the database session
// zone (UTC on the server), so formatting them as they arrive prints the
// merchant's evening as the following day — silently, and only for whoever is
// far enough east. It stayed invisible while renewals were anchored at 2am,
// where Los Angeles and UTC share a calendar day; RENEWAL_ANCHOR_HOUR takes any
// hour 0–23, so that agreement is a setting, not a property.
//
// nil is an error rather than a UTC default. A renderer that quietly falls back
// is how this bug shipped in the first place — the zone was never chosen, so
// nothing looked wrong until an anchor hour moved. Callers that genuinely have
// no merchant zone (the one-off senders under cmd/, none of whose templates
// print a date) say time.UTC out loud.
func New(loc *time.Location) (*Renderer, error) {
	if loc == nil {
		return nil, fmt.Errorf("email renderer: nil location (pass the merchant zone, or time.UTC for a sender with no dated template)")
	}
	dateIn := func(t any) string { return formatDateIn(t, loc) }
	funcMap := template.FuncMap{
		"cents": formatCents,
		"date":  dateIn,
		"day":   formatDay,
	}
	textFuncMap := text.FuncMap{
		"cents": formatCents,
		"date":  dateIn,
		"day":   formatDay,
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

// QBShadowDigestInvoice is one would-be invoice in the shadow billing digest.
//
// Money stays in cents and dates stay as times: the templates have `cents` and
// `date` for exactly this, and preformatting here would put currency and
// locale decisions in the wrong layer.
type QBShadowDigestInvoice struct {
	OrderNumber string
	Customer    string
	TotalCents  int
	Terms       string // "Net 7" / "Due on receipt"
	DueDate     time.Time
	BillEmail   string
	URL         string // admin order detail page
	// Manual marks an account nothing invoices automatically. Listed because
	// the work is real even though the billing is not.
	Manual bool
	// Problem is empty on a clean row and otherwise says, in one phrase, what
	// a human needs to resolve before going live.
	Problem string
}

// QBShadowDigestData is the weekly summary of what QuickBooks billing would
// have done, sent while the shop is in shadow mode.
//
// The digest exists because a proof period nobody looks at proves nothing. It
// leads with the rows needing attention rather than the total: an account that
// would silently fail to match in QBO is the finding, and the money is the
// context.
//
// Total is the full count and may exceed len(Invoices), which is capped. The
// template says so rather than letting the list imply it is everything.
type QBShadowDigestData struct {
	Invoices      []QBShadowDigestInvoice
	Total         int
	TotalAmtCents int
	Attention     int // how many rows need a human before going live
	// AwaitingManual is how many orders sit on accounts nothing invoices
	// automatically. Counted apart from Total, which is money we expect to
	// collect — folding them together would overstate it.
	AwaitingManual int
	// Listed is every row the digest covers, billable and manual together.
	// The truncation notice compares against this, not against Total, or an
	// all-manual week reads "showing 40 of 0".
	Listed    int
	Days      int // length of the window this covers
	ReviewURL string
	StoreName string
}

// ServiceStaleDigestTicket is one line of the stale-ticket digest.
//
// QuietDays is precomputed rather than passed as a timestamp: the template
// should not be doing date arithmetic, and "quiet for 11 days" is the phrase
// that makes somebody pick up the phone.
type ServiceStaleDigestTicket struct {
	Number    string
	Title     string
	Customer  string
	Severity  string // down / degraded / routine
	Status    string
	QuietDays int
	URL       string // admin ticket detail page
	Down      bool   // severity == down; the template leads with these
}

// ServiceStaleDigestData holds the daily digest of open service tickets nobody
// has spoken to the customer about.
//
// Total is the full count and may exceed len(Tickets), which is capped — a
// digest listing two hundred rows is a digest nobody reads. The template says
// so rather than letting the list imply it is everything.
type ServiceStaleDigestData struct {
	Tickets    []ServiceStaleDigestTicket
	Total      int
	WindowDays int
	QueueURL   string // admin service queue, stale scope
	StoreName  string
}

// ServiceTicketOpenedData holds the staff notification sent when a wholesale
// customer reports a broken machine from their account.
//
// Everything the crew needs to act without opening the admin is on the face of
// it — what broke, whose it is, and a number to ring. The link is there for the
// person who is going to work the ticket, not for the person deciding whether
// to get in the van.
type ServiceTicketOpenedData struct {
	Number      string
	Title       string
	Description string // the customer's own words
	Severity    string // down / degraded / routine
	// SeverityLabel is the same value in the words a cafe would use.
	SeverityLabel string
	// Down leads the template and earns the shouted subject line: it is the
	// difference between "go now" and "put it on the list".
	Down          bool
	Machine       string // make and model, blank if the machine row has gone
	SerialNumber  string
	Customer      string
	CustomerEmail string
	Phone         string
	ReportedAt    time.Time
	TicketURL     string // admin ticket detail page
	StoreName     string
}

// QBTokenAlertData holds data for the staff warning that the QuickBooks// QBTokenAlertData holds data for the staff warning that the QuickBooks
// connection (refresh token) is about to lapse or already has.
type QBTokenAlertData struct {
	DaysLeft    int // days until the refresh token expires; <= 0 when expired
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
	StoreName      string
	StoreURL       string
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

// SubscriptionPastDueData holds data for every rung of the past-due email
// ladder — the first notice, the mid-window reminder, and the final warning all
// render from this one shape.
type SubscriptionPastDueData struct {
	CustomerName string
	ProductName  string
	PlanName     string
	// HardDecline is true when the issuer has permanently blocked the card. The
	// copy swaps to "this card won't work again, add a different one", because
	// telling someone to check with their bank about a card reported stolen
	// wastes their time.
	HardDecline bool
	// EndsOn is the day the subscription is given up on if nothing changes.
	EndsOn    time.Time
	StoreName string
	StoreURL  string
	// AccountURL is the signed-in account page — the fallback call to action.
	AccountURL string
	// UpdateCardURL is the one-click card link, empty when the signer is not
	// configured. Templates must fall back to AccountURL when it is empty
	// rather than rendering a dead link.
	UpdateCardURL string
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

// SubscriptionSkippedData holds data for the "we've skipped your next
// shipment" notice, sent whether the customer skipped it themselves or asked
// staff to. UndoURL is a signed one-click link back to the previous schedule;
// it is empty when no signing secret is configured, and the template then falls
// back to pointing at the account page.
type SubscriptionSkippedData struct {
	CustomerName    string
	ProductName     string
	PlanName        string
	SkippedCount    int       // shipments skipped; 0 when the customer named a restart date instead
	PreviousOrderOn time.Time // when the next shipment would have been billed
	NextChargeOn    time.Time // when it will be billed now
	UndoURL         string
	StoreName       string
	StoreURL        string
	AccountURL      string
}

// SubscriptionSkipUndoneData holds data for the notice sent when staff put a
// skipped subscription back on its original schedule. Undoing moves the charge
// date *earlier*, so the customer is told before their card is billed on a day
// they were last told was cancelled. Customer-driven undos send nothing — they
// just saw the confirmation page.
type SubscriptionSkipUndoneData struct {
	CustomerName string
	ProductName  string
	PlanName     string
	SkippedTo    time.Time // the date the skip had moved them to
	NextChargeOn time.Time // the restored date
	StoreName    string
	StoreURL     string
	AccountURL   string
}

// SubscriptionResumedData holds data for the notice sent when a paused
// subscription is put back on. Resuming bills at the next renewal window rather
// than a full interval out, so the customer is told the date their card is
// charged before it happens — the whole reason this email exists.
type SubscriptionResumedData struct {
	CustomerName string
	ProductName  string
	PlanName     string
	NextChargeOn time.Time // when the resumed subscription's next order is placed
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

// formatDay renders a calendar date that was never an instant — a SQL `date`
// column, or a "2006-01-02" string from an API. pgx hands those over as
// midnight UTC, so converting them to a western zone lands at 5pm the *previous
// day* and prints an invoice as due a day before QuickBooks says it is. A
// calendar date has no zone to convert to: April 1st is April 1st.
//
// Templates say {{day .DueDate}} for these and {{date .X}} for anything that is
// a real moment (placed_at, shipped_at, next_order_at). Getting the two mixed
// up is silent — both compile, both print a plausible date — so the rule is
// worth checking against the column type rather than guessing from the field
// name.
func formatDay(t any) string {
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

// formatDateIn renders an instant in loc. See formatDay for why a calendar date
// must not come through here.
func formatDateIn(t any, loc *time.Location) string {
	switch v := t.(type) {
	case time.Time:
		return v.In(loc).Format("January 2, 2006")
	case *time.Time:
		if v == nil {
			return ""
		}
		return v.In(loc).Format("January 2, 2006")
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
