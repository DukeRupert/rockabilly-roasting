package storefront

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/dukerupert/hiri/internal/domain"
)

// The account page's "Change" form on a subscription and "Add a coffee" form
// on a box. Both offer what the /subscribe picker offers, so a coffee a
// customer could sign up for is one they can switch to or add, and nothing
// else.

// SubscriptionChoice is one coffee a customer may put on subscription: a
// subscribable, priced retail variant.
type SubscriptionChoice struct {
	VariantID    uuid.UUID
	ProductID    uuid.UUID
	ProductTitle string
	VariantLabel string
	// BasePrice is in cents, before a plan's discount: what a renewal starts
	// from.
	BasePrice int
}

// Name is the coffee as the picker lists it: "Switchblade Espresso — Whole
// Bean · 12oz", or the title alone for a product with no options.
func (c SubscriptionChoice) Name() string {
	if c.VariantLabel == "" {
		return c.ProductTitle
	}
	return c.ProductTitle + " — " + c.VariantLabel
}

// maxBoxItems is the most a box may hold, the service's cap (and a signup's).
const maxBoxItems = 10

// subscriptionQuantities are the quantities a customer may choose, the
// service's bounds.
func subscriptionQuantities() []int {
	return []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
}

// canChange reports whether a subscription shows the Change form. Mirrors
// the service: active and paused only. A past-due one has a charge to settle
// first, and its card says so.
func canChange(sub domain.Subscription) bool {
	return sub.Status == domain.SubscriptionStatusActive || sub.Status == domain.SubscriptionStatusPaused
}

// grindChoices are the variants of this row's coffee the customer can switch
// to, its current one first. The current one is kept even when it is no longer
// on sale (archived, or retail taken off it), so the form opens on what they
// have and saving it changes nothing.
func grindChoices(row AccountSubscriptionRow, all []SubscriptionChoice) []SubscriptionChoice {
	current := SubscriptionChoice{VariantID: row.Subscription.VariantID}
	if row.Product != nil {
		current.ProductID = row.Product.ID
		current.ProductTitle = row.Product.Title
	}
	if row.BasePrice != nil {
		current.BasePrice = *row.BasePrice
	}
	current.VariantLabel = row.VariantLabel
	out := []SubscriptionChoice{current}
	for _, c := range all {
		if c.ProductID == current.ProductID && c.VariantID != current.VariantID {
			out = append(out, c)
		}
	}
	return out
}

// planChoices are the plans a row can move to, its current one first even
// when it is no longer offered, for the same reason as grindChoices.
func planChoices(row AccountSubscriptionRow, active []domain.SubscriptionPlan) []domain.SubscriptionPlan {
	var out []domain.SubscriptionPlan
	if row.Plan != nil {
		out = append(out, *row.Plan)
	}
	for _, p := range active {
		if row.Plan == nil || p.ID != row.Plan.ID {
			out = append(out, p)
		}
	}
	return out
}

// boxPlanID is the plan the Add form opens on: the box's first member's, so
// an added coffee keeps the box's rhythm unless the customer picks another.
func boxPlanID(box AccountSubscriptionBox) uuid.UUID {
	if len(box.Rows) == 0 {
		return uuid.Nil
	}
	return box.Rows[0].Subscription.PlanID
}

// discountedUnit is a unit price after a plan's discount, the arithmetic a
// renewal charges by (RenewalService) and the account page shows: integer
// cents, the discount rounded down.
func discountedUnit(base, discountPct int) int {
	return base - (base*discountPct)/100
}

// lineTotal is what one subscription line costs per shipment, before shipping
// and tax.
func lineTotal(base, discountPct, quantity int) int {
	return discountedUnit(base, discountPct) * quantity
}

// priceExpr is the Alpine expression the Change and Add forms show their price
// with as the selects move. The same arithmetic as lineTotal: an integer
// discount rounded down, then times the quantity.
const priceExpr = "'$' + ((qty * (unit - Math.floor(unit * disc / 100))) / 100).toFixed(2)"

// priceState is the Alpine state a price line starts from.
func priceState(base, discountPct, quantity int) string {
	return fmt.Sprintf("{ unit: %d, disc: %d, qty: %d }", base, discountPct, quantity)
}

// subscriptionOptions is what the Change and Add forms offer, passed from the
// page to its boxes and cards.
type subscriptionOptions struct {
	Choices []SubscriptionChoice
	Plans   []domain.SubscriptionPlan
}

func (p AccountSubscriptionsProps) options() subscriptionOptions {
	return subscriptionOptions{Choices: p.Choices, Plans: p.Plans}
}

// rowPrice is the row's base price, discount and quantity, the Change form's
// starting point. A row without a price (none on file) starts at zero, and
// the form then shows no price line.
func rowPrice(row AccountSubscriptionRow) (base, discountPct, quantity int) {
	if row.BasePrice != nil {
		base = *row.BasePrice
	}
	if row.Plan != nil {
		discountPct = row.Plan.DiscountPct
	}
	return base, discountPct, row.Subscription.Quantity
}

// boxPlanDiscount is the discount of the plan the Add form opens on.
func boxPlanDiscount(box AccountSubscriptionBox) int {
	if len(box.Rows) == 0 || box.Rows[0].Plan == nil {
		return 0
	}
	return box.Rows[0].Plan.DiscountPct
}

// grindName is a variant as the Change form's grind select lists it.
func grindName(c SubscriptionChoice) string {
	if c.VariantLabel == "" {
		return "As it is now"
	}
	return c.VariantLabel
}

func idAttr(prefix string, id uuid.UUID) string { return prefix + "-" + id.String() }
