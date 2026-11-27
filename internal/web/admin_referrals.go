package web

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// referralListLimit caps each table on the referrals page. Owed is worked
// down as bags go out, so the cap is a backstop; Sent is history.
const referralListLimit = 200

// handleAdminReferralList shows the free bags members have earned.
func (d *Deps) handleAdminReferralList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var props admin.ReferralListProps
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		if props.Offer, txErr = d.DiscountService.ReferralOffer(ctx, tx); txErr != nil {
			return txErr
		}
		if props.Owed, txErr = d.DiscountService.ListReferralRewards(ctx, tx, domain.ReferralRewardOwed, referralListLimit); txErr != nil {
			return txErr
		}
		props.Sent, txErr = d.DiscountService.ListReferralRewards(ctx, tx, domain.ReferralRewardSent, 50)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	props.MerchantTZ = d.MerchantTZ
	props.StaffName, props.StaffRole = staffNameRole(r)
	if IsHTMX(r) {
		admin.ReferralListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.ReferralList(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminReferralSent marks one owed bag as sent.
func (d *Deps) handleAdminReferralSent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.DiscountService.MarkReferralRewardSent(ctx, tx, id, staffActor(r))
	})
	// Already sent (a double click, or two staff at once) is where the
	// button wanted it: back to the list either way.
	if err != nil && !errors.Is(err, app.ErrReferralRewardNotOwed) {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/referrals", http.StatusSeeOther)
}

// handleAdminDiscountReferralSet makes a discount the referral offer.
func (d *Deps) handleAdminDiscountReferralSet(w http.ResponseWriter, r *http.Request) {
	d.setReferralOffer(w, r, true)
}

// handleAdminDiscountReferralClear stops a discount being the referral offer.
func (d *Deps) handleAdminDiscountReferralClear(w http.ResponseWriter, r *http.Request) {
	d.setReferralOffer(w, r, false)
}

func (d *Deps) setReferralOffer(w http.ResponseWriter, r *http.Request, on bool) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if on {
			return d.DiscountService.SetReferralOffer(ctx, tx, id, staffActor(r))
		}
		return d.DiscountService.ClearReferralOffer(ctx, tx, id, staffActor(r))
	})
	back := "/admin/discounts/" + id.String()
	switch {
	case err == nil:
		http.Redirect(w, r, back, http.StatusSeeOther)
	case errors.Is(err, app.ErrDiscountNotFound):
		http.NotFound(w, r)
	case errors.Is(err, app.ErrReferralOfferInvalid):
		http.Redirect(w, r, back+"?error="+url.QueryEscape("This discount can't be the referral offer. It needs to be once per customer, for the first subscription order, and take money off."), http.StatusSeeOther)
	default:
		Error(w, r, err)
	}
}
