package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Where a line's price is decided, for every order this shop places.
//
// It used to be decided twice. Both checkout endpoints resolved a unit price,
// applied a plan discount, summed the lines and quoted
// that total to Stripe — and then handed the same lines to PlaceOrder, which
// summed the prices it was given and wrote *that* as the order's subtotal. Two
// independent calculations, agreeing by convention: whichever one a change
// missed, the customer was charged one number and the order recorded the other,
// and nothing anywhere compared them.
//
// So the arithmetic lives here, once. A caller prices its lines, quotes the
// payment provider from the result, and hands the lines to PlaceOrder, which
// prices them again through this same function and refuses if the answer moved.
// The two cannot disagree, because there is only one of them.
//
// A consequence worth having on its own: PlaceOrderParams no longer decides what
// anything costs. It is built from a request body, and a price that arrives in
// it is now checked against the catalog rather than believed.

// OrderLine is one line of an order before it has a price: what the customer
// asked for, not what it costs.
type OrderLine struct {
	VariantID uuid.UUID
	// Quantity also picks the volume rung: a line is priced at the quantity it
	// holds, exactly as the cart priced it, or the two would disagree for every
	// tiered variant and PlaceOrder would refuse the order as moved.
	Quantity int
	// PlanDiscountPct is this line's subscription plan's percentage off, applied
	// to the resolved unit price. Zero for retail.
	//
	// A property of the line, not the order: one signup can carry a weekly line
	// and a monthly line, and the two plans take different percentages.
	//
	// Declared by the caller rather than derived here because a signup's plan is
	// not yet attached to anything this function could read it from: the
	// subscription record is created by the webhook, after the order exists.
	PlanDiscountPct int
}

// PriceLinesParams asks what a set of lines costs for one customer.
type PriceLinesParams struct {
	CustomerID   uuid.UUID
	CurrencyCode string
	Lines        []OrderLine
	// BasePrice prices every line from the variant's base price, passing over
	// the customer's price list and volume rungs. A subscription is priced this
	// way because every renewal is: RenewalService charges GetBasePrice less the
	// plan's discount. A signup resolved through the customer's list would charge
	// the first box one number and every box after it another, and nothing would
	// compare the two. Retail checkout is priced this way because the retail
	// cart is: CartService.AddItem prices at base and the checkout page shows
	// those numbers, so the charge has to match them. Set by the handler, never
	// from a request. The wholesale cart is the path that resolves through the
	// customer's list, and it reprices itself before anything is summed.
	BasePrice bool
}

// PricedLines is what those lines cost. The Items carry resolved prices and are
// what PlaceOrder takes.
type PricedLines struct {
	Items    []CartItem
	Subtotal int
}

// PriceLines resolves what each line costs and what they add up to.
//
// Prices come from the pricing service, so a customer on a price list gets
// theirs, at the volume rung the line's quantity reaches; a plan discount comes
// off that, at each line's own percentage. Nothing here reads a price off the
// request.
func (s *CheckoutService) PriceLines(ctx context.Context, tx pgx.Tx, p PriceLinesParams) (*PricedLines, error) {
	if s.pricing == nil {
		// A binary that cannot price a line must not place an order at a price
		// it guessed.
		return nil, ErrPricingUnavailable
	}
	if len(p.Lines) == 0 {
		return nil, ErrCartEmpty
	}

	out := &PricedLines{Items: make([]CartItem, len(p.Lines))}
	for i, line := range p.Lines {
		if line.Quantity <= 0 {
			return nil, ErrInvalidQuantity
		}

		unit, err := s.lineUnitPrice(ctx, tx, p, line)
		if err != nil {
			return nil, fmt.Errorf("resolve price for variant %s: %w", line.VariantID, err)
		}

		if line.PlanDiscountPct > 0 {
			unit -= unit * line.PlanDiscountPct / 100
		}

		if unit < 0 {
			// Unreachable from a catalog price and a percentage, and worth
			// saying so rather than writing a credit onto an order.
			return nil, ErrInvalidPrice
		}

		out.Items[i] = CartItem{
			VariantID:       line.VariantID,
			Quantity:        line.Quantity,
			UnitPrice:       unit,
			PlanDiscountPct: line.PlanDiscountPct,
		}
		out.Subtotal += unit * line.Quantity
	}
	return out, nil
}

// lineUnitPrice is one unit of a line before any plan discount: the base price
// for a subscription, otherwise the customer's price at the line's quantity.
func (s *CheckoutService) lineUnitPrice(ctx context.Context, tx pgx.Tx, p PriceLinesParams, line OrderLine) (int, error) {
	if p.BasePrice {
		price, err := s.pricing.GetBasePrice(ctx, tx, line.VariantID, p.CurrencyCode)
		if err != nil {
			return 0, err
		}
		return price.Amount, nil
	}
	resolved, err := s.pricing.ResolveForCustomer(ctx, tx, line.VariantID, p.CustomerID, line.Quantity, p.CurrencyCode)
	if err != nil {
		return 0, err
	}
	return int(resolved), nil
}

// linesOf drops the prices from priced items, so PlaceOrder can ask PriceLines
// the same question its caller did.
func linesOf(items []CartItem) []OrderLine {
	lines := make([]OrderLine, len(items))
	for i, it := range items {
		lines[i] = OrderLine{
			VariantID:       it.VariantID,
			Quantity:        it.Quantity,
			PlanDiscountPct: it.PlanDiscountPct,
		}
	}
	return lines
}

// refusePricesThatMoved re-prices the lines it was handed and refuses if any
// price or the subtotal differs from what the caller passed.
//
// This is the seam the whole file exists for. The caller quoted the payment
// provider from PriceLines; if pricing the same lines again here gives a
// different answer, then the amount being charged and the order about to be
// written have already diverged, and the only safe move is to place no order.
// A stale checkout page is the ordinary cause: the catalog moved between the
// quote and the confirm.
func (s *CheckoutService) refusePricesThatMoved(ctx context.Context, tx pgx.Tx, p PlaceOrderParams) (int, error) {
	priced, err := s.PriceLines(ctx, tx, PriceLinesParams{
		CustomerID:   p.CustomerID,
		CurrencyCode: p.CurrencyCode,
		Lines:        linesOf(p.Items),
		BasePrice:    p.BasePrice,
	})
	if err != nil {
		return 0, err
	}
	for i, want := range priced.Items {
		if p.Items[i].UnitPrice != want.UnitPrice {
			return 0, fmt.Errorf("line %d priced at %d, catalog says %d: %w",
				i, p.Items[i].UnitPrice, want.UnitPrice, ErrPriceMoved)
		}
	}
	return priced.Subtotal, nil
}
