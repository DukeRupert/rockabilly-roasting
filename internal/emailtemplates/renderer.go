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

// MagicLinkData holds data for the magic link email.
type MagicLinkData struct {
	CustomerName string
	MagicLinkURL string
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
	Interval     string
	UnitPrice    int
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
