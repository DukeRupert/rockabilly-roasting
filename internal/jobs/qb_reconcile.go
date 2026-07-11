package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/quickbooks"
)

// fetchQBInvoiceFacts pulls QuickBooks' current view of an invoice and maps it
// to app.QBInvoiceFacts (the primitive form the reconcile seam consumes). A 404
// is reported as NotFound rather than an error, so the reconcile treats a
// deleted invoice as voided. This is the QB I/O step — it must run OUTSIDE any
// database transaction.
func fetchQBInvoiceFacts(ctx context.Context, qb quickbooks.Client, qbInvoiceID string) (app.QBInvoiceFacts, error) {
	inv, err := qb.GetInvoice(ctx, qbInvoiceID)
	if err != nil {
		if errors.Is(err, quickbooks.ErrNotFound) {
			return app.QBInvoiceFacts{NotFound: true}, nil
		}
		return app.QBInvoiceFacts{}, fmt.Errorf("qb fetch invoice: %w", err)
	}
	return app.QBInvoiceFacts{
		BalanceCents: inv.BalanceCents(),
		TotalCents:   inv.TotalCents(),
		DueDate:      inv.DueDate,
	}, nil
}
