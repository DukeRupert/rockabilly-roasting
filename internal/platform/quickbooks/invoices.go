package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
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
	Qty       float64 `json:"Qty,omitempty"`
	UnitPrice float64 `json:"UnitPrice,omitempty"`
}

// qbInvoiceResponse is the JSON response from QB invoice endpoints.
type qbInvoiceResponse struct {
	Invoice struct {
		ID        string  `json:"Id"`
		DocNumber string  `json:"DocNumber"`
		Balance   float64 `json:"Balance"`
		SyncToken string  `json:"SyncToken"`
	} `json:"Invoice"`
}

// CreateInvoice creates an invoice in QBO.
func (c *QBClient) CreateInvoice(ctx context.Context, p InvoiceParams) (*Invoice, error) {
	lines := make([]qbInvoiceLine, 0, len(p.Lines)+1)

	for _, line := range p.Lines {
		lines = append(lines, qbInvoiceLine{
			DetailType:  "SalesItemLineDetail",
			Amount:      centsToFloat(line.Amount),
			Description: line.Description,
			SalesItemLineDetail: &qbSalesItemDetail{
				Qty:       float64(line.Quantity),
				UnitPrice: centsToFloat(line.UnitAmount),
			},
		})
	}

	// Add shipping as a line item if present
	if p.Shipping > 0 {
		lines = append(lines, qbInvoiceLine{
			DetailType:  "SalesItemLineDetail",
			Amount:      centsToFloat(p.Shipping),
			Description: "Shipping",
			SalesItemLineDetail: &qbSalesItemDetail{
				Qty:       1,
				UnitPrice: centsToFloat(p.Shipping),
			},
		})
	}

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

	return &Invoice{
		ID:        resp.Invoice.ID,
		DocNumber: resp.Invoice.DocNumber,
		Balance:   resp.Invoice.Balance,
	}, nil
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

	return &Invoice{
		ID:        resp.Invoice.ID,
		DocNumber: resp.Invoice.DocNumber,
		Balance:   resp.Invoice.Balance,
	}, nil
}

// centsToFloat converts an amount in cents to a float64 dollar amount.
func centsToFloat(cents int) float64 {
	return float64(cents) / 100.0
}
