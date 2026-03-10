package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
)

// qbPaymentRequest is the JSON body for creating a QB payment.
type qbPaymentRequest struct {
	CustomerRef  qbRef            `json:"CustomerRef"`
	TotalAmt     float64          `json:"TotalAmt"`
	Line         []qbPaymentLine  `json:"Line"`
	PaymentRefNum string          `json:"PaymentRefNum,omitempty"`
	PrivateNote   string          `json:"PrivateNote,omitempty"`
}

// qbPaymentLine links a payment to an invoice.
type qbPaymentLine struct {
	Amount    float64          `json:"Amount"`
	LinkedTxn []qbLinkedTxn   `json:"LinkedTxn"`
}

// qbLinkedTxn references the invoice being paid.
type qbLinkedTxn struct {
	TxnId   string `json:"TxnId"`
	TxnType string `json:"TxnType"`
}

// qbPaymentResponse is the JSON response from QB payment endpoints.
type qbPaymentResponse struct {
	Payment struct {
		ID       string  `json:"Id"`
		TotalAmt float64 `json:"TotalAmt"`
	} `json:"Payment"`
}

// CreatePayment records a payment against a QB invoice.
func (c *QBClient) CreatePayment(ctx context.Context, p PaymentParams) (*Payment, error) {
	amount := centsToFloat(p.Amount)

	body := qbPaymentRequest{
		CustomerRef:   qbRef{Value: p.CustomerID},
		TotalAmt:      amount,
		PaymentRefNum: p.Reference,
		Line: []qbPaymentLine{
			{
				Amount: amount,
				LinkedTxn: []qbLinkedTxn{
					{
						TxnId:   p.InvoiceID,
						TxnType: "Invoice",
					},
				},
			},
		},
	}

	if p.Method != "" {
		body.PrivateNote = fmt.Sprintf("Payment method: %s", p.Method)
	}

	respBody, err := c.doAPI(ctx, "POST", "/payment", body)
	if err != nil {
		return nil, fmt.Errorf("create QB payment: %w", err)
	}

	var resp qbPaymentResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal QB payment response: %w", err)
	}

	return &Payment{
		ID:     resp.Payment.ID,
		Amount: resp.Payment.TotalAmt,
	}, nil
}
