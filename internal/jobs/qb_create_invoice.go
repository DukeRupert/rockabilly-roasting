package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/quickbooks"
	"github.com/dukerupert/hiri/internal/store"
)

// CreateQBInvoiceWorker creates a QB invoice for a wholesale order, then
// chains to SendQBInvoice so QBO emails it to the customer.
type CreateQBInvoiceWorker struct {
	river.WorkerDefaults[CreateQBInvoiceArgs]
	orders    *store.OrderStore
	customers *store.CustomerStore
	catalog   *store.CatalogStore
	settings  *store.SettingsStore
	previews  *store.QBPreviewStore
	// envSalesItemID is the fallback item the server was started with, if
	// any. Held so a preview can tell "nothing is configured anywhere" from
	// "nothing is chosen in the admin but the environment still supplies one".
	envSalesItemID string
	qb             quickbooks.Client
	audit          *audit.AuditWriter
	pool           *pgxpool.Pool
	riverClient    *river.Client[pgx.Tx]
	metrics        *metrics.Registry
}

// NewCreateQBInvoiceWorker creates a new CreateQBInvoiceWorker.
func NewCreateQBInvoiceWorker(
	orders *store.OrderStore,
	customers *store.CustomerStore,
	catalog *store.CatalogStore,
	settings *store.SettingsStore,
	previews *store.QBPreviewStore,
	envSalesItemID string,
	qb quickbooks.Client,
	auditWriter *audit.AuditWriter,
	pool *pgxpool.Pool,
	riverClient *river.Client[pgx.Tx],
	m *metrics.Registry,
) *CreateQBInvoiceWorker {
	return &CreateQBInvoiceWorker{
		orders:         orders,
		customers:      customers,
		catalog:        catalog,
		settings:       settings,
		previews:       previews,
		envSalesItemID: envSalesItemID,
		qb:             qb,
		audit:          auditWriter,
		pool:           pool,
		riverClient:    riverClient,
		metrics:        m,
	}
}

// Work processes a single CreateQBInvoice job.
func (w *CreateQBInvoiceWorker) Work(ctx context.Context, job *river.Job[CreateQBInvoiceArgs]) error {
	start := time.Now()

	err := w.work(ctx, job)

	metrics.TrackJob(w.metrics, "qb_create_invoice", start, err)
	if err != nil {
		// Permanent QuickBooks failures cancel, and a cancelled job never
		// reaches jobs.ErrorHandler — so the level has to be decided here.
		terminal := !quickbooks.IsRetryable(err)
		logWorkerFailure(ctx, "qb_create_invoice", terminal,
			"job_kind", "qb_create_invoice",
			"job_id", job.ID,
			"attempt", job.Attempt,
			"order_id", job.Args.OrderID,
			"error", err.Error(),
		)
		if terminal {
			enqueueQBInvoiceAlert(ctx, w.pool, w.riverClient, job.Args.OrderID, "qb_create_invoice", err)
			return river.JobCancel(fmt.Errorf("create qb invoice for order %s: %w", job.Args.OrderID, err))
		}
		if job.Attempt >= job.MaxAttempts {
			// Final retry burned — River discards the job after this return,
			// so alert now or the failure is silent.
			enqueueQBInvoiceAlert(ctx, w.pool, w.riverClient, job.Args.OrderID, "qb_create_invoice", err)
		}
	}
	return err
}

func (w *CreateQBInvoiceWorker) work(ctx context.Context, job *river.Job[CreateQBInvoiceArgs]) error {
	// Read order, line items, and the customer's NET terms
	var order *domain.Order
	var items []domain.LineItem
	var customer *domain.Customer
	var mode domain.QBBillingMode
	var qbItems store.QBItemConfig
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		var txErr error
		if mode, txErr = w.settings.GetQBBillingMode(ctx, tx); txErr != nil {
			return txErr
		}
		if qbItems, txErr = w.settings.GetQBItemConfig(ctx, tx); txErr != nil {
			return txErr
		}
		order, txErr = w.orders.GetOrderByIDAsStaff(ctx, tx, job.Args.OrderID)
		if txErr != nil {
			return txErr
		}
		// Idempotency: if the invoice already exists (job retry), skip the
		// remaining reads — the early return below never uses them.
		if order.QBInvoiceID != nil {
			return nil
		}
		items, txErr = w.orders.ListLineItems(ctx, tx, order.ID)
		if txErr != nil {
			return txErr
		}
		if order.CustomerID != nil {
			customer, txErr = w.customers.GetByID(ctx, tx, *order.CustomerID)
			return txErr
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}
	if order.QBInvoiceID != nil {
		return nil
	}
	termsDays := app.EffectivePaymentTermsDays(customer)

	// Build QB invoice lines
	lines := make([]quickbooks.InvoiceLine, 0, len(items))
	for _, item := range items {
		// Build a description from variant info
		desc := fmt.Sprintf("Order item (qty %d)", item.Quantity)

		// Try to get product/variant description from catalog
		_ = store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
			variant, txErr := w.catalog.GetVariantByID(ctx, tx, item.VariantID)
			if txErr == nil && variant != nil {
				product, pErr := w.catalog.GetProductByID(ctx, tx, variant.ProductID)
				if pErr == nil && product != nil {
					desc = product.Title
					if variant.SKU != "" {
						desc += " (" + variant.SKU + ")"
					}
				}
			}
			return nil // non-fatal if we can't get descriptions
		})

		lines = append(lines, quickbooks.InvoiceLine{
			Description: desc,
			Quantity:    item.Quantity,
			UnitAmount:  item.UnitPrice,
			Amount:      item.Total,
		})
	}

	// The order-level idempotency check above can't see an invoice a previous
	// attempt created in QBO but failed to persist locally. DocNumber is the
	// order number, so probe for it and adopt rather than create a duplicate —
	// especially important now that every created invoice chains a send job
	// that emails the customer.
	docNumber := formatOrderRef(order.Number)

	// A manual account is invoiced by hand, so nothing here bills it — in
	// either mode. The preview is still written, and is what the review page
	// lists as waiting for a person: an order that simply vanished would read
	// as nothing to bill, which is the reading this project keeps having to
	// correct. Bill now on that row is the deliberate way to invoice one.
	if customer != nil && !customer.BillingMethod.AutoInvoiced() && !job.Args.StaffRequested {
		slog.Info("qb create invoice: account is on manual billing, not invoicing automatically",
			"order_id", order.ID, "billing_method", customer.BillingMethod)
		return w.recordPreview(ctx, job, order, customer, lines, docNumber, termsDays, qbItems)
	}

	// Shadow mode stops here: record what would have been billed and return.
	// The lookups it performs are read-only, which is the point — an account
	// that silently fails to match a QBO customer is invisible until money is
	// involved, and that is exactly what a proof period exists to surface.
	if !mode.IsLive() {
		return w.recordPreview(ctx, job, order, customer, lines, docNumber, termsDays, qbItems)
	}

	// A shadow run chains here with an empty QBCustomerID when nothing matched.
	// If the mode is flipped to live between the two jobs, that empty value
	// would reach QBO as a blank CustomerRef. Start the chain again instead:
	// the live EnsureQBCustomer path establishes the customer properly, and
	// the DocNumber probe below still guards against a duplicate invoice.
	// Restart when local state does not know this customer, not merely when the
	// job carries no id. A shadow run that *matched* a customer passes the id
	// forward without persisting it — deliberately, since linking is what the
	// proof period exists to have a human confirm. Flip to live between the two
	// jobs and the invoice would bill correctly against a customer whose
	// qb_customer_id is still null, after which SyncQBPayment finds nil and
	// silently stops recording that account's payments.
	if job.Args.QBCustomerID == "" || customer == nil || customer.QBCustomerID == nil {
		if order.CustomerID == nil {
			// Unreachable through either entry point — checkout and BillOrderNow
			// both require a customer — but this is the one place that would
			// otherwise dereference it blind.
			return fmt.Errorf("%w: order %s has no customer to invoice", quickbooks.ErrBadRequest, order.ID)
		}
		slog.Info("qb create invoice: no qb customer on a live run, restarting the chain",
			"order_id", order.ID)
		return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
			_, txErr := w.riverClient.InsertTx(ctx, tx, EnsureQBCustomerArgs{
				CustomerID:     *order.CustomerID,
				OrderID:        order.ID,
				StaffRequested: job.Args.StaffRequested,
			}, nil)
			return txErr
		})
	}

	invoice, err := w.qb.FindInvoiceByDocNumber(ctx, docNumber)
	if err != nil {
		return fmt.Errorf("qb find invoice by doc number: %w", err)
	}
	adopted := invoice != nil

	if invoice == nil {
		// Create QB invoice (external call outside transaction). Due date
		// follows the customer's NET terms; net-7 when none are set. QBO
		// emails the invoice (chained SendQBInvoice job below) with whichever
		// online-payment button matches the
		// customer's own agreement — see BillingMethod. The
		// bill-to email is set explicitly below — QBO does not fill it in
		// from the linked customer, and the send step fails without it.
		params := quickbooks.InvoiceParams{
			CustomerID:     job.Args.QBCustomerID,
			DocNumber:      docNumber,
			DueDate:        order.PlacedAt.Add(time.Duration(termsDays) * 24 * time.Hour),
			Lines:          lines,
			Shipping:       order.ShippingTotal,
			SalesItemID:    qbItems.SalesItemID,
			ShippingItemID: qbItems.ShippingItemID,
		}
		if customer != nil {
			// Both buttons follow the account's agreement. ACH used to be sent
			// unconditionally, which would have put a pay-now button on the
			// invoice of every account that had never agreed to one — and
			// every wholesale account was manual at the time.
			params.AllowOnlineACHPayment = customer.BillingMethod.OnlineACHAllowed()
			params.AllowOnlineCreditCardPayment = customer.BillingMethod.OnlineCardAllowed()
			// EnsureQBCustomer syncs this same address onto the QB customer's
			// PrimaryEmailAddr, so local and QB agree by construction.
			params.BillEmail = customer.Email
		}
		if params.BillEmail == "" {
			// The invoice is still worth creating — staff can add the address
			// in QBO and resend — but the chained send will fail permanently
			// and alert, so record why here rather than leaving that alert
			// looking like a QBO problem.
			slog.Warn("qb create invoice: no bill-to address, invoice will be created but cannot be emailed",
				"order_id", order.ID, "customer_id", order.CustomerID)
		}

		// Stamp the invoice with a QBO Term so its Terms field reads "Net 15"
		// rather than sitting blank, and so QBO's own reporting can group by
		// terms. DueDate above remains authoritative for when payment is due,
		// which is why a failure here is logged and not returned: the terms
		// label is presentational, and refusing to bill a customer because a
		// cosmetic lookup failed would be the worse outcome.
		termID, termErr := w.qb.FindOrCreateTerm(ctx, termsDays)
		if termErr != nil {
			slog.Warn("qb create invoice: term lookup failed, invoicing without terms label",
				"order_id", order.ID, "terms_days", termsDays, "error", termErr)
		} else {
			params.TermID = termID
		}
		invoice, err = w.qb.CreateInvoice(ctx, params)
		if err != nil {
			return fmt.Errorf("qb create invoice: %w", err)
		}
	}

	// Persist QB invoice ID and chain the send job in one transaction with
	// audit — if the send fails later it retries on its own, and can never
	// re-create the invoice.
	return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		if txErr := w.orders.SetQBInvoice(ctx, tx, order.ID, invoice.ID, invoice.DocNumber); txErr != nil {
			return txErr
		}
		// The order is billed, so any shadow-mode preview of it has served its
		// purpose. Clearing it in the same transaction keeps the review page
		// meaning "recorded but not billed" — otherwise a billed order would
		// sit there still offering a Bill now button and still counting toward
		// the badge.
		if txErr := w.previews.DeleteByOrder(ctx, tx, order.ID); txErr != nil {
			return txErr
		}
		if _, txErr := w.riverClient.InsertTx(ctx, tx, SendQBInvoiceArgs{
			OrderID:     order.ID,
			QBInvoiceID: invoice.ID,
		}, nil); txErr != nil {
			return txErr
		}
		return w.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "qb_create_invoice",
			Action:       audit.AuditQBInvoiceCreated,
			ResourceType: "order",
			ResourceID:   order.ID,
			Metadata: map[string]any{
				"qb_invoice_id":  invoice.ID,
				"qb_doc_number":  invoice.DocNumber,
				"net_terms_days": termsDays,
				"adopted":        adopted,
				"river_job_id":   job.ID,
			},
		})
	})
}

// recordPreview writes what a live run would have billed for this order.
//
// Every QBO call here is read-only. Failures are recorded on the preview
// rather than returned: a proof period whose list is missing an order reads as
// "nothing to bill for that one", which is the single conclusion it must never
// invite by accident. The row says what went wrong instead.
func (w *CreateQBInvoiceWorker) recordPreview(
	ctx context.Context,
	job *river.Job[CreateQBInvoiceArgs],
	order *domain.Order,
	customer *domain.Customer,
	lines []quickbooks.InvoiceLine,
	docNumber string,
	termsDays int,
	qbItems store.QBItemConfig,
) error {
	preview := &domain.QBInvoicePreview{
		OrderID:       order.ID,
		CustomerID:    order.CustomerID,
		DocNumber:     docNumber,
		TermsDays:     termsDays,
		DueDate:       order.PlacedAt.Add(time.Duration(termsDays) * 24 * time.Hour),
		SubtotalCents: order.Subtotal,
		ShippingCents: order.ShippingTotal,
		TotalCents:    order.Total,
	}
	// Default true so an order with no customer row — which cannot happen
	// through either entry point — is not quietly filed as "someone will
	// invoice this by hand".
	preview.AutoBilled = true
	if customer != nil {
		preview.BillEmail = customer.Email
		preview.BillingMethod = customer.BillingMethod
		preview.AutoBilled = customer.BillingMethod.AutoInvoiced()
	}
	if job.Args.QBCustomerID != "" {
		qbID := job.Args.QBCustomerID
		preview.QBCustomerID = &qbID
	} else {
		// EnsureQBCustomer looked and found nothing. Recorded for manual
		// accounts too: the lookup really did run for them, and Bill now has
		// to be able to say that invoicing this order would create a customer
		// in the merchant's books. Problem() still keeps it off the review
		// page's attention count, where it would be noise.
		preview.WouldCreateCustomer = true
	}
	for _, line := range lines {
		preview.Lines = append(preview.Lines, domain.QBInvoiceLinePreview{
			Description: line.Description,
			Quantity:    line.Quantity,
			UnitCents:   line.UnitAmount,
			AmountCents: line.Amount,
		})
	}

	var lookupErrs []string
	// An unconfigured item is the one problem a proof period can see coming
	// that would otherwise only surface as a failed invoice after go-live. The
	// runbook sends staff here to check exactly this, so it has to be said
	// here rather than only on the settings page.
	// Not gated on AutoBilled, unlike an earlier version of this check. A
	// manual account with no configured item is exactly the row somebody
	// reaches for Bill now on, and CreateInvoice refuses without an item —
	// permanently, since it is an ErrBadRequest. Suppressing it here would
	// have left that click with no warning and no chance of succeeding.
	if qbItems.SalesItemID == "" && w.envSalesItemID == "" {
		lookupErrs = append(lookupErrs,
			"no QuickBooks item is chosen for invoice lines — set one under Settings, Integrations, or invoices cannot be created")
	}
	if job.Args.CustomerLookupError != "" {
		// Raised by EnsureQBCustomer, which carries it here rather than
		// failing so this order still appears on the review page.
		lookupErrs = append(lookupErrs, "customer lookup: "+job.Args.CustomerLookupError)
		// A lookup that failed did not establish that no customer exists, so
		// this must not read as "going live would create one".
		preview.WouldCreateCustomer = false
	}
	if existing, err := w.qb.FindInvoiceByDocNumber(ctx, docNumber); err != nil {
		lookupErrs = append(lookupErrs, "invoice lookup: "+err.Error())
	} else if existing != nil {
		id := existing.ID
		preview.ExistingQBInvoiceID = &id
	}
	// FindTerm, not FindOrCreateTerm — a shadow run must not write a Term into
	// the merchant's books. An absent Term is reported as none rather than
	// created, and going live creates it on the first real invoice.
	if termID, err := w.qb.FindTerm(ctx, termsDays); err != nil {
		lookupErrs = append(lookupErrs, "term lookup: "+err.Error())
	} else if termID != "" {
		preview.TermID = &termID
	}
	if len(lookupErrs) > 0 {
		joined := strings.Join(lookupErrs, "; ")
		preview.LookupError = &joined
	}

	return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		if txErr := w.previews.Upsert(ctx, tx, preview); txErr != nil {
			return txErr
		}
		return w.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "qb_create_invoice",
			Action:       audit.AuditQBInvoicePreviewed,
			ResourceType: "order",
			ResourceID:   order.ID,
			Metadata: map[string]any{
				"qb_doc_number":         docNumber,
				"net_terms_days":        termsDays,
				"total_cents":           order.Total,
				"would_create_customer": preview.WouldCreateCustomer,
				"river_job_id":          job.ID,
			},
		})
	})
}

// formatOrderRef formats an order number for use as the QB DocNumber.
func formatOrderRef(number string) string {
	return number // the order already has a formatted number
}
