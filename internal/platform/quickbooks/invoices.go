package quickbooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// qbInvoiceRequest is the JSON body for creating a QB invoice.
type qbInvoiceRequest struct {
	CustomerRef qbRef           `json:"CustomerRef"`
	DocNumber   string          `json:"DocNumber,omitempty"`
	DueDate     string          `json:"DueDate"` // YYYY-MM-DD
	Line        []qbInvoiceLine `json:"Line"`
	// Omitted when empty so a customer with no email still yields a valid
	// invoice; only the send step fails, and that already alerts staff.
	BillEmail *qbEmailAddr `json:"BillEmail,omitempty"`
	// Omitted when unset so an invoice is still created if the Term lookup
	// could not be resolved.
	SalesTermRef *qbRef `json:"SalesTermRef,omitempty"`
	// Payment flags are always sent explicitly — an omitempty here would drop
	// `false` and let the QBO company default re-enable the pay button the
	// caller meant to turn off.
	AllowOnlineACHPayment        bool `json:"AllowOnlineACHPayment"`
	AllowOnlineCreditCardPayment bool `json:"AllowOnlineCreditCardPayment"`
}

type qbRef struct {
	Value string `json:"value"`
}

type qbEmailAddr struct {
	Address string `json:"Address"`
}

type qbInvoiceLine struct {
	DetailType          string             `json:"DetailType"`
	Amount              float64            `json:"Amount"`
	Description         string             `json:"Description,omitempty"`
	SalesItemLineDetail *qbSalesItemDetail `json:"SalesItemLineDetail,omitempty"`
}

type qbSalesItemDetail struct {
	ItemRef   qbRef   `json:"ItemRef"` // required by QBO on every sales line
	Qty       float64 `json:"Qty,omitempty"`
	UnitPrice float64 `json:"UnitPrice,omitempty"`
}

// qbInvoiceResponse is the JSON response from QB invoice endpoints.
type qbInvoiceResponse struct {
	Invoice struct {
		ID          string  `json:"Id"`
		DocNumber   string  `json:"DocNumber"`
		Balance     float64 `json:"Balance"`
		TotalAmt    float64 `json:"TotalAmt"`
		DueDate     string  `json:"DueDate"` // YYYY-MM-DD
		EmailStatus string  `json:"EmailStatus"`
		SyncToken   string  `json:"SyncToken"`
	} `json:"Invoice"`
}

// invoiceFromResponse maps a decoded QB invoice payload to the domain-facing
// Invoice, parsing the YYYY-MM-DD due date (best-effort; a zero time signals
// "unknown" to callers).
func invoiceFromResponse(resp qbInvoiceResponse) *Invoice {
	var dueDate time.Time
	if resp.Invoice.DueDate != "" {
		if d, err := time.Parse("2006-01-02", resp.Invoice.DueDate); err == nil {
			dueDate = d
		}
	}
	return &Invoice{
		ID:          resp.Invoice.ID,
		DocNumber:   resp.Invoice.DocNumber,
		Balance:     resp.Invoice.Balance,
		TotalAmt:    resp.Invoice.TotalAmt,
		DueDate:     dueDate,
		EmailStatus: resp.Invoice.EmailStatus,
	}
}

// buildInvoiceLines maps InvoiceParams lines (plus the shipping line) to QB
// request lines, stamping each with the item it bills against. shippingItemID
// falls back to salesItemID when empty.
func buildInvoiceLines(p InvoiceParams, salesItemID, shippingItemID string) []qbInvoiceLine {
	if shippingItemID == "" {
		shippingItemID = salesItemID
	}

	lines := make([]qbInvoiceLine, 0, len(p.Lines)+1)
	for _, line := range p.Lines {
		lines = append(lines, qbInvoiceLine{
			DetailType:  "SalesItemLineDetail",
			Amount:      centsToFloat(line.Amount),
			Description: line.Description,
			SalesItemLineDetail: &qbSalesItemDetail{
				ItemRef:   qbRef{Value: salesItemID},
				Qty:       float64(line.Quantity),
				UnitPrice: centsToFloat(line.UnitAmount),
			},
		})
	}

	if p.Shipping > 0 {
		lines = append(lines, qbInvoiceLine{
			DetailType:  "SalesItemLineDetail",
			Amount:      centsToFloat(p.Shipping),
			Description: "Shipping",
			SalesItemLineDetail: &qbSalesItemDetail{
				ItemRef:   qbRef{Value: shippingItemID},
				Qty:       1,
				UnitPrice: centsToFloat(p.Shipping),
			},
		})
	}

	return lines
}

// CreateInvoice creates an invoice in QBO.
func (c *QBClient) CreateInvoice(ctx context.Context, p InvoiceParams) (*Invoice, error) {
	// Wrapped in ErrBadRequest so IsRetryable classifies it permanent — a
	// missing item mapping never fixes itself on retry.
	// Per-invoice items win over the client's configured defaults: the choice
	// belongs to the shop's settings, and the config values are the fallback
	// for a deployment still supplying them through the environment.
	salesItemID := p.SalesItemID
	if salesItemID == "" {
		salesItemID = c.config.SalesItemID
	}
	shippingItemID := p.ShippingItemID
	if shippingItemID == "" {
		shippingItemID = c.config.ShippingItemID
	}
	if salesItemID == "" {
		return nil, fmt.Errorf("%w: no QuickBooks item is configured for invoice lines — choose one under Settings, Integrations", ErrBadRequest)
	}

	lines := buildInvoiceLines(p, salesItemID, shippingItemID)

	body := qbInvoiceRequest{
		CustomerRef:                  qbRef{Value: p.CustomerID},
		DocNumber:                    p.DocNumber,
		DueDate:                      p.DueDate.Format("2006-01-02"),
		Line:                         lines,
		AllowOnlineACHPayment:        p.AllowOnlineACHPayment,
		AllowOnlineCreditCardPayment: p.AllowOnlineCreditCardPayment,
	}
	if p.BillEmail != "" {
		body.BillEmail = &qbEmailAddr{Address: p.BillEmail}
	}
	if p.TermID != "" {
		body.SalesTermRef = &qbRef{Value: p.TermID}
	}

	respBody, err := c.doAPI(ctx, "POST", "/invoice", body)
	if err != nil && p.TermID != "" && isStaleTermRefError(err) {
		// The Term reference is the one part of this request that can go stale
		// without anything local changing: deleted or deactivated in QBO after
		// we cached its ID. QBO answers 400, which IsRetryable calls permanent,
		// so the invoice job would cancel and alert — turning a presentational
		// label into a billing outage. Drop the cached ID and bill without the
		// terms label rather than not billing at all. If the 400 was about
		// something else the retry fails the same way and that error is
		// returned.
		c.terms.forget(p.TermID)
		body.SalesTermRef = nil
		respBody, err = c.doAPI(ctx, "POST", "/invoice", body)
	}
	if err != nil {
		return nil, fmt.Errorf("create QB invoice: %w", err)
	}

	var resp qbInvoiceResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal QB invoice response: %w", err)
	}

	return invoiceFromResponse(resp), nil
}

// isStaleTermRefError reports whether a rejected invoice looks like it was
// rejected because of its Term reference.
//
// Narrower than "any 400" on purpose. QBO also answers 400 for unrelated
// conditions — a duplicate DocNumber being the common one — and retrying those
// without the Term evicts a perfectly good cached Term ID, repeats a request
// that fails identically, and leaves every later invoice in the process paying
// for an extra Term lookup.
func isStaleTermRefError(err error) bool {
	if !errors.Is(err, ErrBadRequest) {
		return false
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	// Whole words only. A bare substring match also fires on "determined",
	// which QBO uses in unrelated fault details — and a false positive here
	// evicts a good cached Term and bills without the label this exists to
	// add. In production the fault text arrives in Detail; Message is just
	// http.StatusText(400).
	haystack := strings.ToLower(apiErr.Message + " " + apiErr.Detail)
	return termWordRe.MatchString(haystack)
}

// termWordRe matches a whole word "term" or "terms", or QBO's SalesTermRef
// field name.
var termWordRe = regexp.MustCompile(`\bterms?\b|salestermref`)

// qbInvoiceQueryResponse is the response shape for invoice queries.
type qbInvoiceQueryResponse struct {
	QueryResponse struct {
		Invoice []struct {
			ID          string  `json:"Id"`
			DocNumber   string  `json:"DocNumber"`
			Balance     float64 `json:"Balance"`
			TotalAmt    float64 `json:"TotalAmt"`
			DueDate     string  `json:"DueDate"`
			EmailStatus string  `json:"EmailStatus"`
		} `json:"Invoice"`
	} `json:"QueryResponse"`
}

// FindInvoiceByDocNumber returns the QBO invoice carrying the given DocNumber,
// or nil (not an error) if none exists. Used by the create-invoice job to
// adopt an invoice a previous attempt created but failed to persist, instead
// of creating (and emailing) a duplicate.
func (c *QBClient) FindInvoiceByDocNumber(ctx context.Context, docNumber string) (*Invoice, error) {
	query := fmt.Sprintf("SELECT * FROM Invoice WHERE DocNumber = '%s'", escapeQBQuery(docNumber))
	respBody, err := c.doAPI(ctx, "GET", "/query?query="+urlEncode(query), nil)
	if err != nil {
		return nil, fmt.Errorf("qb invoice query: %w", err)
	}

	var resp qbInvoiceQueryResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal qb invoice query: %w", err)
	}
	if len(resp.QueryResponse.Invoice) == 0 {
		return nil, nil
	}

	match := resp.QueryResponse.Invoice[0]
	var dueDate time.Time
	if match.DueDate != "" {
		if d, err := time.Parse("2006-01-02", match.DueDate); err == nil {
			dueDate = d
		}
	}
	return &Invoice{
		ID:          match.ID,
		DocNumber:   match.DocNumber,
		Balance:     match.Balance,
		TotalAmt:    match.TotalAmt,
		DueDate:     dueDate,
		EmailStatus: match.EmailStatus,
	}, nil
}

// SendInvoice has QBO email the invoice to its BillEmail address. The send
// endpoint takes an empty body but requires Content-Type
// application/octet-stream.
func (c *QBClient) SendInvoice(ctx context.Context, qbInvoiceID string) error {
	_, err := c.doAPIContentType(ctx, "POST",
		fmt.Sprintf("/invoice/%s/send", qbInvoiceID), nil, "application/octet-stream")
	if err != nil {
		return fmt.Errorf("send QB invoice: %w", err)
	}
	return nil
}

// GetInvoice fetches the current state of an invoice from QBO.
func (c *QBClient) GetInvoice(ctx context.Context, qbInvoiceID string) (*Invoice, error) {
	respBody, err := c.doAPI(ctx, "GET", fmt.Sprintf("/invoice/%s", qbInvoiceID), nil)
	if err != nil {
		return nil, fmt.Errorf("get QB invoice: %w", err)
	}

	var resp qbInvoiceResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal QB invoice: %w", err)
	}

	return invoiceFromResponse(resp), nil
}

// centsToFloat converts an amount in cents to a float64 dollar amount.
// Note: float64 division by 100 can introduce rounding for some values
// (e.g., 33 cents → 0.32999... instead of 0.33). This is acceptable because
// QB rounds to 2 decimal places on display and in calculations. For amounts
// up to $999,999.99, float64 has more than enough precision (15+ significant
// digits) to represent any cent value exactly after QB's rounding.
func centsToFloat(cents int) float64 {
	return float64(cents) / 100.0
}
