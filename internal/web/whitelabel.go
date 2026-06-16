package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// maxLabelImageBytes caps the white-label label upload. Labels are small; this
// guards against oversized uploads on a public (token-gated) endpoint.
const maxLabelImageBytes = 10 << 20 // 10 MiB

// --- Wholesale white-label onboarding (token from invite email) ---

func (d *Deps) handleWhiteLabelPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
		return
	}

	props := storefront.WhiteLabelProps{Token: token}
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		customerID, txErr := d.AuthService.LookupWhiteLabelInvite(ctx, tx, token)
		if txErr != nil {
			return txErr
		}
		customer, txErr := d.CustomerService.GetCustomer(ctx, tx, customerID)
		if txErr != nil {
			return txErr
		}
		if customer.CompanyName != nil {
			props.CompanyName = *customer.CompanyName
		}
		choices, txErr := d.WhiteLabelService.BaseCoffeeChoices(ctx, tx)
		if txErr != nil {
			return txErr
		}
		props.Choices = toWhiteLabelChoices(choices)
		return nil
	})
	if err != nil {
		if errors.Is(err, app.ErrWhiteLabelInviteInvalid) {
			d.renderWhiteLabel(w, r, storefront.WhiteLabelProps{InvalidToken: true})
			return
		}
		Error(w, r, err)
		return
	}

	d.renderWhiteLabel(w, r, props)
}

func (d *Deps) handleWhiteLabelSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseMultipartForm(maxLabelImageBytes); err != nil {
		http.Error(w, "Upload too large or malformed.", http.StatusBadRequest)
		return
	}

	token := r.FormValue("token")
	if token == "" {
		http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
		return
	}
	name := r.FormValue("name")
	baseIDRaw := r.FormValue("base_product_id")

	// Re-render the form with an error, preserving what they typed.
	renderErr := func(msg string) {
		props := storefront.WhiteLabelProps{Token: token, Name: name, SelectedBaseID: baseIDRaw, Error: msg}
		if err := d.loadWhiteLabelChoices(r, &props); err != nil {
			Error(w, r, err)
			return
		}
		d.renderWhiteLabel(w, r, props)
	}

	baseProductID, err := uuid.Parse(baseIDRaw)
	if err != nil {
		renderErr("Pick a coffee to base your label on.")
		return
	}

	// Read the uploaded label image and stage it in R2 before touching the DB.
	r2Key, uploadMsg := d.uploadLabelImage(r)
	if uploadMsg != "" {
		renderErr(uploadMsg)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		customerID, txErr := d.AuthService.RedeemWhiteLabelInvite(ctx, tx, token)
		if txErr != nil {
			return txErr
		}
		p, txErr := d.WhiteLabelService.SubmitWhiteLabel(ctx, tx, customerID, app.WhiteLabelSubmission{
			BaseProductID: baseProductID,
			Name:          name,
			LabelR2Key:    r2Key,
		}, app.Actor{Type: domain.AuditActorTypeCustomer, ID: &customerID, Name: "white-label onboarding"})
		if txErr != nil {
			return txErr
		}
		// Notify staff to review the draft — rides on commit.
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.WhiteLabelSubmittedArgs{ProductID: p.ID}, nil)
		return txErr
	})
	if err != nil {
		switch {
		case errors.Is(err, app.ErrWhiteLabelInviteInvalid):
			d.renderWhiteLabel(w, r, storefront.WhiteLabelProps{InvalidToken: true})
		case errors.Is(err, app.ErrWhiteLabelNameRequired):
			renderErr("Give your coffee a name.")
		case errors.Is(err, app.ErrWhiteLabelBaseInvalid):
			renderErr("That coffee isn't available — pick another.")
		case errors.Is(err, app.ErrWhiteLabelLabelRequired):
			renderErr("Upload your label image.")
		default:
			Error(w, r, err)
		}
		return
	}

	d.renderWhiteLabel(w, r, storefront.WhiteLabelProps{Success: true})
}

// uploadLabelImage reads the posted label file and stores it in R2. It returns
// the object key on success, or a non-empty user-facing message describing why
// the upload was rejected.
func (d *Deps) uploadLabelImage(r *http.Request) (r2Key, userMsg string) {
	file, header, err := r.FormFile("label_image")
	if err != nil {
		return "", "Upload your label image."
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxLabelImageBytes+1))
	if err != nil {
		return "", "We couldn't read that file — try again."
	}
	if len(data) == 0 {
		return "", "Upload your label image."
	}
	if len(data) > maxLabelImageBytes {
		return "", "That image is too large (10 MB max)."
	}

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	switch contentType {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return "", "Label must be a PNG, JPG, or WebP image."
	}

	key := fmt.Sprintf("white-label/%s", uuid.New().String())
	if err := d.R2Client.PutObject(r.Context(), key, data, contentType); err != nil {
		return "", "We couldn't store that image — try again."
	}
	return key, ""
}

// loadWhiteLabelChoices populates props.Choices for an error re-render.
func (d *Deps) loadWhiteLabelChoices(r *http.Request, props *storefront.WhiteLabelProps) error {
	return store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		choices, err := d.WhiteLabelService.BaseCoffeeChoices(r.Context(), tx)
		if err != nil {
			return err
		}
		props.Choices = toWhiteLabelChoices(choices)
		return nil
	})
}

func (d *Deps) renderWhiteLabel(w http.ResponseWriter, r *http.Request, props storefront.WhiteLabelProps) {
	if IsHTMX(r) {
		storefront.WhiteLabelContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.WhiteLabelPage(props).Render(r.Context(), w) //nolint:errcheck
}

func toWhiteLabelChoices(choices []app.WhiteLabelBaseChoice) []storefront.WhiteLabelChoice {
	out := make([]storefront.WhiteLabelChoice, len(choices))
	for i, c := range choices {
		out[i] = storefront.WhiteLabelChoice{ID: c.ProductID.String(), Title: c.Title}
	}
	return out
}

// --- Admin: send a white-label invite to an approved wholesale customer ---

func (d *Deps) handleAdminCustomerSendWhiteLabelInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Guard up front so we don't enqueue a job that can only fail.
	var notApproved bool
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		customer, txErr := d.CustomerService.GetCustomer(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		notApproved = !customer.IsApprovedWholesale()
		return nil
	}); err != nil {
		Error(w, r, err)
		return
	}
	if notApproved {
		http.Error(w, "Customer is not an approved wholesale account.", http.StatusBadRequest)
		return
	}

	if _, err := d.RiverClient.Insert(ctx, jobs.WhiteLabelInviteArgs{CustomerID: id}, nil); err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}
