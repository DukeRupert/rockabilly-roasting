package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// qbInvoiceRequest is the JSON body for creating a QB invoice.
type qbInvoiceRequest struct {
	CustomerRef qbRef           `json:"CustomerRef"`
	DocNumber   string          `json:"DocNumber,omitempty"`
	DueDate     string          `json:"DueDate"` // YYYY-MM-DD
	Line        []qbInvoiceLine `json:"Line"`
}

type qbRef struct {
	Value string `json:"value"`
}

type qbInvoiceLine struct {
	DetailType          string               `json:"DetailType"`
	Amount              float64              `json:"Amount"`
	Description         string               `json:"Description,omitempty"`
	SalesItemLineDetail *qbSalesItemDetail   `json:"SalesItemLineDetail,omitempty"`
}

type qbSalesItemDetail struct {
	ItemRef   qbRef   `json:"ItemRef"` // required by QBO on every sales line
	Qty       float64 `json:"Qty,omitempty"`
	UnitPrice float64 `json:"UnitPrice,omitempty"`
}

// qbInvoiceResponse is the JSON response from QB invoice endpoints.
type qbInvoiceResponse struct {
	Invoice struct {
		ID        string  `json:"Id"`
		DocNumber string  `json:"DocNumber"`
		Balance   float64 `json:"Balance"`
		TotalAmt  float64 `json:"TotalAmt"`
		DueDate   string  `json:"DueDate"` // YYYY-MM-DD
		SyncToken string  `json:"SyncToken"`
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
		ID:        resp.Invoice.ID,
		DocNumber: resp.Invoice.DocNumber,
		Balance:   resp.Invoice.Balance,
		TotalAmt:  resp.Invoice.TotalAmt,
		DueDate:   dueDate,
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
	if c.config.SalesItemID == "" {
		return nil, fmt.Errorf("%w: QB sales item not configured (QB_SALES_ITEM_ID)", ErrBadRequest)
	}

	lines := buildInvoiceLines(p, c.config.SalesItemID, c.config.ShippingItemID)

	body := qbInvoiceRequest{
		CustomerRef: qbRef{Value: p.CustomerID},
		DocNumber:   p.DocNumber,
		DueDate:     p.DueDate.Format("2006-01-02"),
		Line:        lines,
	}

	respBody, err := c.doAPI(ctx, "POST", "/invoice", body)
	if err != nil {
		return nil, fmt.Errorf("create QB invoice: %w", err)
	}

	var resp qbInvoiceResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal QB invoice response: %w", err)
	}

	return invoiceFromResponse(resp), nil
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
