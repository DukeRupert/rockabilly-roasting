package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// dealersChoiceLines maps the service's pick plans onto the order page's view
// type, borrowing the product title and option label the line items table
// already resolved.
func dealersChoiceLines(plans []app.LinePickPlan, items []admin.EnrichedLineItem) []admin.DealersChoiceLine {
	if len(plans) == 0 {
		return nil
	}
	byID := make(map[uuid.UUID]admin.EnrichedLineItem, len(items))
	for _, it := range items {
		byID[it.LineItem.ID] = it
	}
	out := make([]admin.DealersChoiceLine, 0, len(plans))
	for _, p := range plans {
		it := byID[p.LineItemID]
		bags := make([]admin.DealersChoiceBag, p.Required)
		for i := range bags {
			bags[i] = admin.DealersChoiceBag{Selected: p.Selected[i], Suggested: p.Suggested[i]}
		}
		out = append(out, admin.DealersChoiceLine{
			LineItemID:      p.LineItemID,
			ProductTitle:    it.ProductTitle,
			OptionLabel:     it.CurrentLabel,
			Quantity:        it.LineItem.Quantity,
			Required:        p.Required,
			ExcludeDecaf:    p.Rule.ExcludeDecaf,
			Bags:            bags,
			Options:         p.Options,
			Confirmed:       p.Confirmed,
			AvoidedPrevious: p.AvoidedPrevious,
		})
	}
	return out
}

// handleAdminOrderPicksSave records the coffees packed in one dealer's choice
// line. The form posts one "pick" field per bag, in bag order.
func (d *Deps) handleAdminOrderPicksSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	orderID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lineItemID, err := uuid.Parse(r.PathValue("lineItemID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	raw := r.PostForm["pick"]
	productIDs := make([]uuid.UUID, 0, len(raw))
	for _, v := range raw {
		id, perr := uuid.Parse(strings.TrimSpace(v))
		if perr != nil {
			Error(w, r, app.ErrInvalidPicks)
			return
		}
		productIDs = append(productIDs, id)
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.DealersChoiceService.SavePicks(ctx, tx, orderID, lineItemID, productIDs, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/orders/"+orderID.String()+"?flash="+url.QueryEscape("Dealer's choice picks saved")+"#dealers-choice", http.StatusSeeOther)
}

// handleAdminVariantPickRule sets or clears a variant's dealer's choice rule
// from the product's Variants tab. A blank or zero bag count clears it.
func (d *Deps) handleAdminVariantPickRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	variantID, err := uuid.Parse(r.PathValue("variantID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	count := 0
	if v := strings.TrimSpace(r.PostFormValue("pick_count")); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil {
			Error(w, r, app.ErrInvalidPickCount)
			return
		}
		count = n
	}
	excludeDecaf := r.PostFormValue("exclude_decaf") == "on"

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.DealersChoiceService.SetPickRule(ctx, tx, productID, variantID, count, excludeDecaf, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?tab=variants&flash=%s", productID, url.QueryEscape("Dealer's choice setup saved")), http.StatusSeeOther)
}

// applyPackingSlipPicks writes each dealer's choice line's coffees onto the
// packing slip rows: the saved picks, or Hiri's suggestions marked as such so
// a slip printed before packing still tells the packer what to grab.
func applyPackingSlipPicks(plans []app.LinePickPlan, items []admin.EnrichedLineItem) {
	for _, p := range plans {
		titles := make(map[uuid.UUID]string, len(p.Options))
		for _, o := range p.Options {
			titles[o.ProductID] = o.Title
		}
		for i := range items {
			if items[i].LineItem.ID != p.LineItemID {
				continue
			}
			names := make([]string, 0, len(p.Selected))
			for _, id := range p.Selected {
				if t, ok := titles[id]; ok {
					names = append(names, t)
				}
			}
			items[i].Picks = names
			items[i].PicksSuggested = !p.Confirmed
		}
	}
}

// dealersChoiceOffer summarizes the pick rules a storefront customer can buy
// (see DealersChoiceService.ListOfferedRules): the distinct bag counts,
// smallest first, and whether the customer gets a decaf choice, which needs
// at least one variant with decaf and one without.
func dealersChoiceOffer(rules []domain.PickRule) (sizes []int, decafChoice bool) {
	var withDecaf, withoutDecaf bool
	for _, r := range rules {
		if !slices.Contains(sizes, r.PickCount) {
			sizes = append(sizes, r.PickCount)
		}
		if r.ExcludeDecaf {
			withoutDecaf = true
		} else {
			withDecaf = true
		}
	}
	slices.Sort(sizes)
	return sizes, withDecaf && withoutDecaf
}

// hasGrindOption reports whether a product sells a grind choice, so the
// storefront only tells customers to pick one when there is one to pick.
func hasGrindOption(options []storefront.OptionWithValues) bool {
	for _, o := range options {
		if strings.Contains(strings.ToLower(o.Option.Name), "grind") && len(o.Values) > 0 {
			return true
		}
	}
	return false
}
