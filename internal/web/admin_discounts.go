package web

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
	"github.com/dukerupert/hiri/internal/ui/components/toast"
)

func (d *Deps) handleAdminDiscountList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	activeFilter := r.URL.Query().Get("active")
	pageStr := r.URL.Query().Get("page")

	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	perPage := 25
	filter := store.DiscountFilter{
		Limit:  perPage + 1,
		Offset: (page - 1) * perPage,
	}

	if activeFilter != "" {
		active := activeFilter == "true"
		filter.Active = &active
	}

	var discounts []domain.Discount
	var hasMore bool
	codes := map[uuid.UUID][]domain.CouponCode{}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		discounts, txErr = d.DiscountService.ListDiscounts(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		hasMore = len(discounts) > perPage
		if hasMore {
			discounts = discounts[:perPage]
		}
		for _, disc := range discounts {
			cc, ccErr := d.DiscountService.ListCouponCodes(ctx, tx, disc.ID)
			if ccErr != nil {
				return ccErr
			}
			codes[disc.ID] = cc
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.DiscountListProps{
		Discounts:    discounts,
		Codes:        codes,
		ActiveFilter: activeFilter,
		Page:         page,
		PerPage:      perPage,
		HasMore:      hasMore,
		MerchantTZ:   d.MerchantTZ,
		StaffName:    name,
		StaffRole:    role,
	}

	if IsHTMX(r) {
		admin.DiscountListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.DiscountList(props).Render(ctx, w) //nolint:errcheck
}

func renderDiscountError(w http.ResponseWriter, r *http.Request, msg string) {
	if IsHTMX(r) {
		w.WriteHeader(http.StatusOK)
		toast.Toast(toast.VariantError, msg).Render(r.Context(), w) //nolint:errcheck
		return
	}
	http.Error(w, msg, http.StatusBadRequest)
}

// handleAdminDiscountCreate creates a discount with its coupon code.
func (d *Deps) handleAdminDiscountCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	name := r.FormValue("name")
	code := r.FormValue("code")
	discountType := domain.DiscountType(r.FormValue("type"))
	if name == "" || code == "" {
		renderDiscountError(w, r, "Name and code are required.")
		return
	}

	var value int
	switch discountType {
	case domain.DiscountTypePercentage:
		v, err := strconv.Atoi(r.FormValue("value"))
		if err != nil || v < 1 || v > 100 {
			renderDiscountError(w, r, "Percentage must be 1-100.")
			return
		}
		value = v
	case domain.DiscountTypeFixedAmount:
		cents, ok := parseDollars(r.FormValue("value"))
		if !ok || cents < 1 {
			renderDiscountError(w, r, "Amount must be a positive dollar value.")
			return
		}
		value = cents
	default:
		renderDiscountError(w, r, "Pick a discount type.")
		return
	}

	var minimumOrderCents *int
	if raw := r.FormValue("minimum_order"); raw != "" {
		cents, ok := parseDollars(raw)
		if !ok {
			renderDiscountError(w, r, "Minimum order must be a dollar value.")
			return
		}
		if cents > 0 {
			minimumOrderCents = &cents
		}
	}

	var expiresAt *time.Time
	if raw := r.FormValue("expires_at"); raw != "" {
		day, err := time.ParseInLocation("2006-01-02", raw, d.MerchantTZ)
		if err != nil {
			renderDiscountError(w, r, "Expiration date must be a valid date.")
			return
		}
		// The code works through the end of the chosen day, merchant time.
		end := day.AddDate(0, 0, 1)
		expiresAt = &end
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.DiscountService.CreateWithCode(ctx, tx, app.CreateDiscountParams{
			Name:              name,
			Type:              discountType,
			Value:             value,
			MinimumOrderCents: minimumOrderCents,
			ExpiresAt:         expiresAt,
			Code:              code,
		}, staffActor(r))
		return txErr
	})
	if err != nil {
		switch {
		case errors.Is(err, app.ErrCouponCodeExists):
			renderDiscountError(w, r, "That coupon code is already in use.")
		case errors.Is(err, app.ErrDiscountInvalid):
			renderDiscountError(w, r, "Discount fields are invalid.")
		default:
			Error(w, r, err)
		}
		return
	}

	http.Redirect(w, r, "/admin/discounts", http.StatusSeeOther)
}

// handleAdminDiscountDeactivate turns a discount off.
func (d *Deps) handleAdminDiscountDeactivate(w http.ResponseWriter, r *http.Request) {
	d.setDiscountActive(w, r, false)
}

// handleAdminDiscountActivate turns a discount back on.
func (d *Deps) handleAdminDiscountActivate(w http.ResponseWriter, r *http.Request) {
	d.setDiscountActive(w, r, true)
}

func (d *Deps) setDiscountActive(w http.ResponseWriter, r *http.Request, active bool) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.DiscountService.SetActive(ctx, tx, id, active, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/discounts", http.StatusSeeOther)
}
