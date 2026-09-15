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
	// TxnTaxDetail is omitted entirely on an untaxed invoice. Sending an empty
	// one on a company that charges no sales tax is a needless way to find out
	// how QBO reacts to a tax code it has no rate for.
	TxnTaxDetail *qbTxnTaxDetail `json:"TxnTaxDetail,omitempty"`
}

// qbTxnTaxDetail carries the invoice's tax. Both halves are required: the
// TxnTaxCodeRef is what the transaction is taxed under, and the TaxLine is
// what actually produces an amount. Supplying TotalTax on its own is accepted
// with a 200 and silently ignored — see taxcodes.go.
type qbTxnTaxDetail struct {
	TxnTaxCodeRef qbRef       `json:"TxnTaxCodeRef"`
	TaxLine       []qbTaxLine `json:"TaxLine"`
}

type qbTaxLine struct {
	DetailType    string          `json:"DetailType"` // always "TaxLineDetail"
	Amount        float64         `json:"Amount"`
	TaxLineDetail qbTaxLineDetail `json:"TaxLineDetail"`
}

type qbTaxLineDetail struct {
	TaxRateRef       qbRef   `json:"TaxRateRef"`
	PercentBased     bool    `json:"PercentBased"`
	TaxPercent       float64 `json:"TaxPercent"`
	NetAmountTaxable float64 `json:"NetAmountTaxable"`
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
	ItemRef qbRef `json:"ItemRef"` // required by QBO on every sales line
	// TaxCodeRef marks the line taxable ("TAX") or not ("NON"). It decides the
	// taxable base QBO applies the rate to, so an exempt line here is what
	// keeps bagged coffee out of the tax on an invoice that also bills a
	// taxable grinder. Omitted on an untaxed invoice, where the company may
	// have no tax codes at all.
	TaxCodeRef *qbRef  `json:"TaxCodeRef,omitempty"`
	Qty        float64 `json:"Qty,omitempty"`
	UnitPrice  float64 `json:"UnitPrice,omitempty"`
}

// qbInvoiceResponse is the JSON response from QB invoice endpoints.
type qbInvoiceResponse struct {
	Invoice struct {
		ID           string  `json:"Id"`
		DocNumber    string  `json:"DocNumber"`
		Balance      float64 `json:"Balance"`
		TotalAmt     float64 `json:"TotalAmt"`
		TxnTaxDetail struct {
			TotalTax float64 `json:"TotalTax"`
		} `json:"TxnTaxDetail"`
		DueDate     string `json:"DueDate"` // YYYY-MM-DD
		EmailStatus string `json:"EmailStatus"`
		SyncToken   string `json:"SyncToken"`
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
		TaxTotal:    resp.Invoice.TxnTaxDetail.TotalTax,
	}
}

// buildInvoiceLines maps InvoiceParams lines (plus the shipping line) to QB
// request lines, stamping each with the item it bills against. shippingItemID
// falls back to salesItemID when empty.
func buildInvoiceLines(p InvoiceParams, salesItemID, shippingItemID string) []qbInvoiceLine {
	if shippingItemID == "" {
		shippingItemID = salesItemID
	}
	taxed := p.Tax.Amount > 0

	lines := make([]qbInvoiceLine, 0, len(p.Lines)+1)
	for _, line := range p.Lines {
		detail := &qbSalesItemDetail{
			ItemRef:   qbRef{Value: salesItemID},
			Qty:       float64(line.Quantity),
			UnitPrice: centsToFloat(line.UnitAmount),
		}
		if taxed {
			detail.TaxCodeRef = &qbRef{Value: lineTaxCode(line.Taxable)}
		}
		lines = append(lines, qbInvoiceLine{
			DetailType:          "SalesItemLineDetail",
			Amount:              centsToFloat(line.Amount),
			Description:         line.Description,
			SalesItemLineDetail: detail,
		})
	}

	if p.Shipping > 0 {
		detail := &qbSalesItemDetail{
			ItemRef:   qbRef{Value: shippingItemID},
			Qty:       1,
			UnitPrice: centsToFloat(p.Shipping),
		}
		if taxed {
			// Shipping follows the shop's own calculation, which does not tax
			// it: domain.CalculateFlatRateTax works from line items only. A
			// taxable shipping line here would have QBO add tax Hiri never
			// charged, and the invoice would stop matching the order.
			detail.TaxCodeRef = &qbRef{Value: lineTaxCode(false)}
		}
		lines = append(lines, qbInvoiceLine{
			DetailType:          "SalesItemLineDetail",
			Amount:              centsToFloat(p.Shipping),
			Description:         "Shipping",
			SalesItemLineDetail: detail,
		})
	}

	return lines
}

// lineTaxCode maps taxability to QBO's two built-in codes. Every company that
// charges sales tax has both.
func lineTaxCode(taxable bool) string {
	if taxable {
		return "TAX"
	}
	return "NON"
}

// taxableBaseCents is the amount QBO applies the rate to: the taxable lines,
// and nothing else. It is sent as NetAmountTaxable so QBO's arithmetic starts
// from the same base Hiri's did.
func taxableBaseCents(p InvoiceParams) int {
	base := 0
	for _, line := range p.Lines {
		if line.Taxable {
			base += line.Amount
		}
	}
	return base
}

// resolveInvoiceItems decides which items an invoice's lines bill against:
// the caller's choice, or the client's configured fallback.
//
// One source or the other, never a field from each. Chosen whole because an
// empty shipping item is a real answer — "bill shipping against the same item
// as the products" — and not an absence to be filled in from somewhere else.
// Mixing per field meant a shop that picked its sales item in the admin while
// an old QB_SHIPPING_ITEM_ID lingered in the environment billed shipping
// against the stale one, with nothing visibly wrong and the revenue landing in
// the wrong income account.
func resolveInvoiceItems(p InvoiceParams, config ClientConfig) (salesItemID, shippingItemID string) {
	if p.SalesItemID != "" {
		return p.SalesItemID, p.ShippingItemID
	}
	return config.SalesItemID, config.ShippingItemID
}

// buildInvoiceRequest assembles the request body, including every refusal that
// can be decided without talking to QBO.
//
// Split from CreateInvoice so the body an invoice is actually billed from can
// be asserted in a test. The previous test built this struct by hand and
// checked its JSON tags, which cannot fail when the builder is wrong — and the
// builder is where the money is.
func buildInvoiceRequest(p InvoiceParams, config ClientConfig) (qbInvoiceRequest, error) {
	// Wrapped in ErrBadRequest so IsRetryable classifies it permanent — a
	// missing item mapping never fixes itself on retry.
	salesItemID, shippingItemID := resolveInvoiceItems(p, config)
	if salesItemID == "" {
		return qbInvoiceRequest{}, fmt.Errorf("%w: no QuickBooks item is configured for invoice lines — choose one under Settings, Integrations", ErrBadRequest)
	}

	body := qbInvoiceRequest{
		CustomerRef:                  qbRef{Value: p.CustomerID},
		DocNumber:                    p.DocNumber,
		DueDate:                      p.DueDate.Format("2006-01-02"),
		Line:                         buildInvoiceLines(p, salesItemID, shippingItemID),
		AllowOnlineACHPayment:        p.AllowOnlineACHPayment,
		AllowOnlineCreditCardPayment: p.AllowOnlineCreditCardPayment,
	}
	if p.BillEmail != "" {
		body.BillEmail = &qbEmailAddr{Address: p.BillEmail}
	}
	if p.TermID != "" {
		body.SalesTermRef = &qbRef{Value: p.TermID}
	}
	if p.Tax.Amount > 0 {
		if p.Tax.TaxCodeID == "" || p.Tax.TaxRateID == "" {
			// Reaching QBO with tax to charge and nothing to charge it under
			// would create an invoice short by the tax, silently — the exact
			// failure a TotalTax-only request produces. Refuse instead: the job
			// alerts staff and the order stays billable once the rate exists.
			return qbInvoiceRequest{}, fmt.Errorf("%w: invoice carries tax but no QuickBooks tax code was resolved", ErrBadRequest)
		}
		body.TxnTaxDetail = &qbTxnTaxDetail{
			TxnTaxCodeRef: qbRef{Value: p.Tax.TaxCodeID},
			TaxLine: []qbTaxLine{{
				DetailType: "TaxLineDetail",
				Amount:     centsToFloat(p.Tax.Amount),
				TaxLineDetail: qbTaxLineDetail{
					TaxRateRef:       qbRef{Value: p.Tax.TaxRateID},
					PercentBased:     true,
					TaxPercent:       p.Tax.RatePercent,
					NetAmountTaxable: centsToFloat(taxableBaseCents(p)),
				},
			}},
		}
	}
	return body, nil
}

// CreateInvoice creates an invoice in QBO.
func (c *QBClient) CreateInvoice(ctx context.Context, p InvoiceParams) (*Invoice, error) {
	body, err := buildInvoiceRequest(p, c.config)
	if err != nil {
		return nil, err
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
