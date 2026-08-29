package store_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// newPreviewOrder makes a wholesale order a preview can hang off.
func newPreviewOrder(t *testing.T, tx pgx.Tx) *domain.Order {
	t.Helper()
	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	return testutil.CreateOrder(t, tx, customer.ID, addr.ID, addr.ID)
}

func previewFor(order *domain.Order) *domain.QBInvoicePreview {
	return &domain.QBInvoicePreview{
		OrderID:       order.ID,
		CustomerID:    order.CustomerID,
		DocNumber:     order.Number,
		BillEmail:     "buyer@example.test",
		TermsDays:     7,
		DueDate:       time.Now().AddDate(0, 0, 7),
		SubtotalCents: 7200,
		TotalCents:    7200,
		Lines: []domain.QBInvoiceLinePreview{
			{Description: "Coffee", Quantity: 4, UnitCents: 1800, AmountCents: 7200},
		},
	}
}

func TestQBPreviewUpsertReplacesRatherThanAppends(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	previews := store.NewQBPreviewStore()
	order := newPreviewOrder(t, tx)

	p := previewFor(order)
	require.NoError(t, previews.Upsert(ctx, tx, p))

	// The chain is idempotent and may run again for the same order. The review
	// page reads as "what would be billed now", not as a list of attempts.
	p.TotalCents = 9900
	p.WouldCreateCustomer = true
	require.NoError(t, previews.Upsert(ctx, tx, p))

	rows, err := previews.List(ctx, tx, 50)
	require.NoError(t, err)
	require.Len(t, rows, 1, "an order should appear once, showing its current figures")
	assert.Equal(t, 9900, rows[0].TotalCents)
	assert.True(t, rows[0].WouldCreateCustomer)
	assert.Equal(t, order.Number, rows[0].OrderNumber)
	require.Len(t, rows[0].Lines, 1, "line items survive the round trip")
	assert.Equal(t, "Coffee", rows[0].Lines[0].Description)
}

func TestQBPreviewTotalsCountEveryRowNotJustAPage(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	previews := store.NewQBPreviewStore()

	clean := previewFor(newPreviewOrder(t, tx))
	require.NoError(t, previews.Upsert(ctx, tx, clean))

	unmatched := previewFor(newPreviewOrder(t, tx))
	unmatched.WouldCreateCustomer = true
	require.NoError(t, previews.Upsert(ctx, tx, unmatched))

	noEmail := previewFor(newPreviewOrder(t, tx))
	noEmail.BillEmail = ""
	require.NoError(t, previews.Upsert(ctx, tx, noEmail))

	// The zero time means every row, which is what the review page shows.
	totals, err := previews.Totals(ctx, tx, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, 3, totals.Count)
	assert.Equal(t, 7200*3, totals.TotalCents)
	assert.Equal(t, 2, totals.NeedingAttention,
		"an unmatched customer and a missing bill-to address both need a human")

	// The page caps its list; the figures above must still describe everything.
	rows, err := previews.List(ctx, tx, 1)
	require.NoError(t, err)
	assert.Len(t, rows, 1)

	n, err := previews.Count(ctx, tx)
	require.NoError(t, err)
	assert.Equal(t, 3, n, "the badge counts every preview, not a page of them")
}

func TestQBPreviewDigestWindowFollowsUpdatesNotCreation(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	previews := store.NewQBPreviewStore()
	order := newPreviewOrder(t, tx)

	p := previewFor(order)
	require.NoError(t, previews.Upsert(ctx, tx, p))
	// Backdate creation to before the digest window, leaving updated_at now:
	// an order first seen weeks ago and re-previewed today is news today.
	_, err := tx.Exec(ctx,
		`UPDATE qb_invoice_previews SET created_at = now() - interval '30 days' WHERE order_id = $1`,
		order.ID)
	require.NoError(t, err)

	since := time.Now().AddDate(0, 0, -7)
	rows, err := previews.ListSince(ctx, tx, since)
	require.NoError(t, err)
	assert.Len(t, rows, 1,
		"a refreshed preview must reach the digest, or a newly-failing order is never reported")

	totals, err := previews.Totals(ctx, tx, since)
	require.NoError(t, err)
	assert.Equal(t, 1, totals.Count)
}

func TestQBPreviewDeletedOnceTheOrderIsBilled(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	previews := store.NewQBPreviewStore()
	order := newPreviewOrder(t, tx)

	require.NoError(t, previews.Upsert(ctx, tx, previewFor(order)))
	require.NoError(t, previews.DeleteByOrder(ctx, tx, order.ID))

	n, err := previews.Count(ctx, tx)
	require.NoError(t, err)
	assert.Zero(t, n,
		"once billed, the invoice is the record — a leftover preview would keep offering to bill it again")
}
