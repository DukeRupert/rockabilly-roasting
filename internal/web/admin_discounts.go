package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
		// Toast only — nothing else is rendered, so the main swap must be
		// cancelled or the empty remainder would blank the swap target.
		w.Header().Set("HX-Reswap", "none")
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

	http.Redirect(w, r, discountReturnPath(r, id), http.StatusSeeOther)
}

// discountReturnPath keeps the activate/deactivate buttons on whichever page
// they were pressed. The value is never taken from the request as a URL — the
// form only says "detail", and the path is built here — so this cannot be
// pushed into an open redirect.
func discountReturnPath(r *http.Request, id uuid.UUID) string {
	if r.FormValue("return_to") == "detail" {
		return "/admin/discounts/" + id.String()
	}
	return "/admin/discounts"
}

// handleAdminDiscountShow renders one discount: its coupon codes and their
// redemptions, the edit form, and its history.
func (d *Deps) handleAdminDiscountShow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	props, err := d.discountShowProps(ctx, r, id)
	if err != nil {
		if errors.Is(err, app.ErrDiscountNotFound) {
			http.NotFound(w, r)
			return
		}
		Error(w, r, err)
		return
	}
	props.Error = r.URL.Query().Get("error")

	if IsHTMX(r) {
		admin.DiscountShowContent(*props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.DiscountShow(*props).Render(ctx, w) //nolint:errcheck
}

// discountShowProps gathers everything the detail page renders.
func (d *Deps) discountShowProps(ctx context.Context, r *http.Request, id uuid.UUID) (*admin.DiscountShowProps, error) {
	var discount *domain.Discount
	var codes []domain.CouponCode
	var activity []domain.AuditEntry

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		discount, txErr = d.DiscountService.GetDiscount(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		codes, txErr = d.DiscountService.ListCouponCodes(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		activity, txErr = d.AuditQueryService.ListByResource(ctx, tx, "discount", id)
		return txErr
	})
	if err != nil {
		return nil, err
	}

	name, role := staffNameRole(r)
	return &admin.DiscountShowProps{
		Discount:   *discount,
		Codes:      codes,
		Activity:   activity,
		MerchantTZ: d.MerchantTZ,
		StaffName:  name,
		StaffRole:  role,
	}, nil
}

// handleAdminDiscountUpdate saves edits to an existing discount. Until this
// existed every rule on a discount was write-once: a wrong minimum order meant
// deactivating it and issuing a fresh code.
func (d *Deps) handleAdminDiscountUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	fail := func(msg string) {
		http.Redirect(w, r, "/admin/discounts/"+id.String()+"?error="+url.QueryEscape(msg), http.StatusSeeOther)
	}

	discountType := domain.DiscountType(r.FormValue("type"))
	var value int
	switch discountType {
	case domain.DiscountTypePercentage:
		v, convErr := strconv.Atoi(strings.TrimSpace(r.FormValue("value")))
		if convErr != nil || v < 1 || v > 100 {
			fail("Percentage must be 1-100.")
			return
		}
		value = v
	case domain.DiscountTypeFixedAmount:
		cents, ok := parseDollars(r.FormValue("value"))
		if !ok || cents < 1 {
			fail("Amount must be a positive dollar value.")
			return
		}
		value = cents
	default:
		fail("Pick a discount type.")
		return
	}

	var minimumOrderCents *int
	if raw := strings.TrimSpace(r.FormValue("minimum_order")); raw != "" {
		cents, ok := parseDollars(raw)
		if !ok {
			fail("Minimum order must be a dollar value.")
			return
		}
		if cents > 0 {
			minimumOrderCents = &cents
		}
	}

	startsAt, ok := parseMerchantDay(r.FormValue("starts_at"), d.MerchantTZ, false)
	if !ok {
		fail("Start date must be a valid date.")
		return
	}
	// The code works through the end of the chosen day, so store the start of
	// the next one — the same convention the create form uses.
	expiresAt, ok := parseMerchantDay(r.FormValue("expires_at"), d.MerchantTZ, true)
	if !ok {
		fail("Expiration date must be a valid date.")
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.DiscountService.Update(ctx, tx, id, app.EditDiscountParams{
			Name:              r.FormValue("name"),
			Type:              discountType,
			Value:             value,
			MinimumOrderCents: minimumOrderCents,
			StartsAt:          startsAt,
			ExpiresAt:         expiresAt,
		}, staffActor(r))
		return txErr
	})
	if err != nil {
		switch {
		case errors.Is(err, app.ErrDiscountNotFound):
			http.NotFound(w, r)
		case errors.Is(err, app.ErrDiscountInvalid):
			fail("Those settings are not valid. Check the value and the dates.")
		default:
			Error(w, r, err)
		}
		return
	}

	http.Redirect(w, r, "/admin/discounts/"+id.String(), http.StatusSeeOther)
}

// parseMerchantDay reads a yyyy-mm-dd form field in merchant time. Blank yields
// nil. exclusiveEnd steps to the start of the following day, for fields that
// mean "valid through this day".
func parseMerchantDay(raw string, loc *time.Location, exclusiveEnd bool) (*time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	day, err := time.ParseInLocation("2006-01-02", raw, loc)
	if err != nil {
		return nil, false
	}
	if exclusiveEnd {
		day = day.AddDate(0, 0, 1)
	}
	return &day, true
}
