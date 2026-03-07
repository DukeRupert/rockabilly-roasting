package web

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

func (d *Deps) handleAdminInvoiceShow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var invoice *domain.Invoice
	var lines []domain.InvoiceLine
	var payments []domain.InvoicePayment

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		invoice, txErr = d.InvoiceService.GetInvoice(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		lines, txErr = d.InvoiceService.ListInvoiceLines(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		payments, txErr = d.InvoiceService.ListInvoicePayments(ctx, tx, id)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.InvoiceShowProps{
		Invoice:   invoice,
		Lines:     lines,
		Payments:  payments,
		StaffName: name,
		StaffRole: role,
	}

	if IsHTMX(r) {
		admin.InvoiceShowContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.InvoiceShow(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminInvoiceCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	orderID, err := uuid.Parse(r.FormValue("order_id"))
	if err != nil {
		http.Error(w, "invalid order_id", http.StatusBadRequest)
		return
	}

	var dueDate *string
	if dd := r.FormValue("due_date"); dd != "" {
		dueDate = &dd
	}
	var notes *string
	if n := r.FormValue("notes"); n != "" {
		notes = &n
	}

	actor := staffActor(r)

	var invoice *domain.Invoice

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		invoice, txErr = d.InvoiceService.CreateFromOrder(ctx, tx, orderID, dueDate, notes, actor)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/invoices/"+invoice.ID.String(), http.StatusSeeOther)
}

func (d *Deps) handleAdminInvoiceSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	actor := staffActor(r)

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.InvoiceService.SendInvoice(ctx, tx, id, actor)
		if txErr != nil {
			return txErr
		}

		// Enqueue invoice email in the same transaction.
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.InvoiceSendArgs{
			InvoiceID: id,
		}, nil)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/invoices/"+id.String(), http.StatusSeeOther)
}

func (d *Deps) handleAdminInvoiceRecordPayment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	amountStr := r.FormValue("amount")
	amount, err := strconv.Atoi(amountStr)
	if err != nil || amount <= 0 {
		http.Error(w, "invalid amount", http.StatusBadRequest)
		return
	}

	method := r.FormValue("method")
	if method == "" {
		http.Error(w, "payment method is required", http.StatusBadRequest)
		return
	}

	var reference *string
	if ref := r.FormValue("reference"); ref != "" {
		reference = &ref
	}
	var note *string
	if n := r.FormValue("note"); n != "" {
		note = &n
	}

	actor := staffActor(r)

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.InvoiceService.RecordPayment(ctx, tx, id, amount, method, reference, note, actor)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/invoices/"+id.String(), http.StatusSeeOther)
}

func (d *Deps) handleAdminInvoiceVoid(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	actor := staffActor(r)

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.InvoiceService.VoidInvoice(ctx, tx, id, actor)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/invoices/"+id.String(), http.StatusSeeOther)
}
