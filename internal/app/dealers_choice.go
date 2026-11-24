package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
)

// DealersChoiceService runs dealer's choice packs: which variants are packs
// (pick rules), what the crew should pack (suggestions), and what they did
// pack (confirmed picks).
type DealersChoiceService struct {
	picks   *store.PickStore
	orders  *store.OrderStore
	catalog *store.CatalogStore
	audit   *audit.AuditWriter
}

// NewDealersChoiceService creates a DealersChoiceService.
func NewDealersChoiceService(picks *store.PickStore, orders *store.OrderStore, catalog *store.CatalogStore, audit *audit.AuditWriter) *DealersChoiceService {
	return &DealersChoiceService{picks: picks, orders: orders, catalog: catalog, audit: audit}
}

// LinePickPlan is everything the order page needs to draw one dealer's choice
// line: how many bags, what is saved, what Hiri suggests, and what staff may
// choose from.
type LinePickPlan struct {
	LineItemID uuid.UUID
	Rule       domain.PickRule
	// Required is the number of bags on the line: the rule's count times the
	// quantity ordered.
	Required int
	// Selected has Required entries: the saved pick at each position when the
	// line is confirmed, otherwise Hiri's suggestion. uuid.Nil means nothing
	// to suggest (an empty pool).
	Selected []uuid.UUID
	// Suggested is Hiri's suggestion for every position, kept separately so
	// the page can mark where staff overrode it.
	Suggested []uuid.UUID
	// Options are the coffees staff may choose, sorted by title. A saved pick
	// whose coffee has since left the pool is appended so it still renders.
	Options []domain.PickCandidate
	// Confirmed reports that staff saved a full set of picks for the line.
	Confirmed bool
	// AvoidedPrevious reports that the customer had an earlier dealer's
	// choice order, so suggestions steered away from what it held.
	AvoidedPrevious bool
	// Previous is what went out in that earlier order, so staff can check
	// they aren't sending the same box again. Nil when there is none.
	Previous *domain.PreviousPicks
}

// PlanOrderPicks builds a LinePickPlan for every dealer's choice line in the
// order. Lines that are not packs are skipped; an order with none returns nil.
func (s *DealersChoiceService) PlanOrderPicks(ctx context.Context, tx pgx.Tx, order *domain.Order, items []domain.LineItem) ([]LinePickPlan, error) {
	rules, err := s.rulesForItems(ctx, tx, items)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, nil
	}

	pool, err := s.picks.ListCandidates(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("plan picks: %w", err)
	}
	lineIDs := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		lineIDs = append(lineIDs, it.ID)
	}
	saved, err := s.picks.ListPicks(ctx, tx, lineIDs)
	if err != nil {
		return nil, fmt.Errorf("plan picks: %w", err)
	}

	avoid := map[uuid.UUID]bool{}
	var previous *domain.PreviousPicks
	if order.CustomerID != nil {
		previous, err = s.picks.GetPreviousPicks(ctx, tx, *order.CustomerID, order.ID)
		if err != nil {
			return nil, fmt.Errorf("plan picks: %w", err)
		}
		if previous != nil {
			for _, id := range previous.ProductIDs {
				avoid[id] = true
			}
		}
	}

	// Titles for saved picks that are no longer candidates (archived coffee,
	// sold out of 12oz), so they still show by name rather than vanish.
	inPool := make(map[uuid.UUID]bool, len(pool))
	for _, c := range pool {
		inPool[c.ProductID] = true
	}
	var stray []uuid.UUID
	for _, ps := range saved {
		for _, p := range ps {
			if !inPool[p.ProductID] {
				stray = append(stray, p.ProductID)
			}
		}
	}
	strayTitles, err := s.picks.ProductTitles(ctx, tx, stray)
	if err != nil {
		return nil, fmt.Errorf("plan picks: %w", err)
	}

	var plans []LinePickPlan
	usedInOrder := map[uuid.UUID]bool{}
	for _, it := range items {
		rule, ok := rules[it.VariantID]
		if !ok {
			continue
		}
		required := rule.PickCount * it.Quantity
		options := domain.FilterPickCandidates(pool, rule)
		// Coffees already suggested for an earlier pack on this order are
		// held back like last box's, so two packs in one order spread across
		// the shelf instead of each drawing the same favorites.
		lineAvoid := make(map[uuid.UUID]bool, len(avoid)+len(usedInOrder))
		for id := range avoid {
			lineAvoid[id] = true
		}
		for id := range usedInOrder {
			lineAvoid[id] = true
		}
		suggested := domain.SuggestPicks(it.ID, required, options, lineAvoid)
		for _, id := range suggested {
			usedInOrder[id] = true
		}
		if suggested == nil {
			suggested = make([]uuid.UUID, required)
		}

		lineSaved := saved[it.ID]
		confirmed := required > 0 && len(lineSaved) == required
		selected := make([]uuid.UUID, required)
		copy(selected, suggested)
		if confirmed {
			for _, p := range lineSaved {
				selected[p.Position] = p.ProductID
			}
		}

		listed := make(map[uuid.UUID]bool, len(options))
		for _, o := range options {
			listed[o.ProductID] = true
		}
		for _, p := range lineSaved {
			if listed[p.ProductID] {
				continue
			}
			listed[p.ProductID] = true
			// Either a coffee that left the pool, or a decaf saved before the
			// variant excluded decaf; the pool has the title for the latter.
			title, ok := strayTitles[p.ProductID]
			if !ok {
				for _, c := range pool {
					if c.ProductID == p.ProductID {
						title = c.Title
					}
				}
			}
			options = append(options, domain.PickCandidate{ProductID: p.ProductID, Title: title})
		}

		plans = append(plans, LinePickPlan{
			LineItemID:      it.ID,
			Rule:            rule,
			Required:        required,
			Selected:        selected,
			Suggested:       suggested,
			Options:         options,
			Confirmed:       confirmed,
			AvoidedPrevious: len(avoid) > 0,
			Previous:        previous,
		})
	}
	return plans, nil
}

// SavePicks records the coffees staff packed for one dealer's choice line,
// replacing anything saved before. productIDs must have exactly one entry per
// bag, each a current candidate for the line (decaf excluded where the variant
// says so) or a coffee already saved on the line.
func (s *DealersChoiceService) SavePicks(ctx context.Context, tx pgx.Tx, orderID, lineItemID uuid.UUID, productIDs []uuid.UUID, actor Actor) error {
	if _, err := s.orders.GetOrderByIDAsStaff(ctx, tx, orderID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOrderNotFound
		}
		return fmt.Errorf("save picks: get order: %w", err)
	}
	li, err := s.orders.GetLineItem(ctx, tx, lineItemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLineItemNotFound
		}
		return fmt.Errorf("save picks: get line item: %w", err)
	}
	if li.OrderID != orderID {
		return ErrLineItemNotInOrder
	}
	rules, err := s.picks.ListRules(ctx, tx, []uuid.UUID{li.VariantID})
	if err != nil {
		return fmt.Errorf("save picks: %w", err)
	}
	rule, ok := rules[li.VariantID]
	if !ok {
		return ErrNotDealersChoice
	}
	required := rule.PickCount * li.Quantity
	if len(productIDs) != required {
		return fmt.Errorf("got %d picks for %d bags: %w", len(productIDs), required, ErrInvalidPicks)
	}

	pool, err := s.picks.ListCandidates(ctx, tx)
	if err != nil {
		return fmt.Errorf("save picks: %w", err)
	}
	allowed := map[uuid.UUID]string{}
	for _, c := range domain.FilterPickCandidates(pool, rule) {
		allowed[c.ProductID] = c.Title
	}
	prior, err := s.picks.ListPicks(ctx, tx, []uuid.UUID{lineItemID})
	if err != nil {
		return fmt.Errorf("save picks: %w", err)
	}
	before := make([]uuid.UUID, 0, len(prior[lineItemID]))
	for _, p := range prior[lineItemID] {
		before = append(before, p.ProductID)
		if _, ok := allowed[p.ProductID]; !ok {
			allowed[p.ProductID] = ""
		}
	}
	for _, id := range productIDs {
		if _, ok := allowed[id]; !ok {
			return ErrInvalidPicks
		}
	}

	if err := s.picks.ReplacePicks(ctx, tx, lineItemID, productIDs); err != nil {
		return fmt.Errorf("save picks: %w", err)
	}

	titles := make([]string, len(productIDs))
	for i, id := range productIDs {
		titles[i] = allowed[id]
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderDealersChoicePicked,
		ResourceType: "order",
		ResourceID:   orderID,
		Metadata: map[string]any{
			"line_item_id": lineItemID,
			"product_ids":  productIDs,
			"titles":       titles,
			"previous":     before,
		},
	}); err != nil {
		return fmt.Errorf("audit dealer's choice picks: %w", err)
	}
	return nil
}

// ListRulesByProduct returns the pick rules for a product's variants, keyed by
// variant ID.
func (s *DealersChoiceService) ListRulesByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID) (map[uuid.UUID]domain.PickRule, error) {
	return s.picks.ListRulesByProduct(ctx, tx, productID)
}

// ListOfferedRules returns the pick rules on a product's live, retail-available
// variants: the pack sizes a storefront customer can buy right now.
func (s *DealersChoiceService) ListOfferedRules(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]domain.PickRule, error) {
	return s.picks.ListOfferedRulesByProduct(ctx, tx, productID)
}

// SetPickRule makes a variant a dealer's choice pack of count bags, or turns it
// back into an ordinary variant when count is zero.
func (s *DealersChoiceService) SetPickRule(ctx context.Context, tx pgx.Tx, productID, variantID uuid.UUID, count int, excludeDecaf bool, actor Actor) error {
	if count < 0 || count > domain.MaxPickCount {
		return ErrInvalidPickCount
	}
	v, err := s.catalog.GetVariantByID(ctx, tx, variantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVariantNotFound
		}
		return fmt.Errorf("set pick rule: get variant: %w", err)
	}
	if v.ProductID != productID {
		return ErrVariantNotFound
	}

	rule := domain.PickRule{VariantID: variantID, PickCount: count, ExcludeDecaf: excludeDecaf}
	if count == 0 {
		err = s.picks.DeleteRule(ctx, tx, variantID)
	} else {
		err = s.picks.UpsertRule(ctx, tx, rule)
	}
	if err != nil {
		return fmt.Errorf("set pick rule: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditVariantPickRuleUpdated,
		ResourceType: "product",
		ResourceID:   productID,
		After:        rule,
		Metadata: map[string]any{
			"variant_id":  variantID,
			"variant_sku": v.SKU,
		},
	}); err != nil {
		return fmt.Errorf("audit pick rule: %w", err)
	}
	return nil
}

func (s *DealersChoiceService) rulesForItems(ctx context.Context, tx pgx.Tx, items []domain.LineItem) (map[uuid.UUID]domain.PickRule, error) {
	return rulesForItems(ctx, tx, s.picks, items)
}

func rulesForItems(ctx context.Context, tx pgx.Tx, picks *store.PickStore, items []domain.LineItem) (map[uuid.UUID]domain.PickRule, error) {
	ids := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.VariantID)
	}
	rules, err := picks.ListRules(ctx, tx, ids)
	if err != nil {
		return nil, fmt.Errorf("load pick rules: %w", err)
	}
	return rules, nil
}

// requirePicksConfirmed refuses with ErrDealersChoicePicksMissing when any
// dealer's choice line in items lacks a full set of saved picks. It guards
// fulfill, label purchase, ready-for-pickup, out-for-delivery, and route
// activation, so a box never leaves without a record of what is in it. The
// driver's Delivered tap is deliberately not guarded: route activation already
// checked every stop, and refusing at the customer's door helps no one.
func requirePicksConfirmed(ctx context.Context, tx pgx.Tx, picks *store.PickStore, items []domain.LineItem) error {
	rules, err := rulesForItems(ctx, tx, picks, items)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}
	var packLines []uuid.UUID
	for _, it := range items {
		if _, ok := rules[it.VariantID]; ok {
			packLines = append(packLines, it.ID)
		}
	}
	saved, err := picks.ListPicks(ctx, tx, packLines)
	if err != nil {
		return fmt.Errorf("check picks: %w", err)
	}
	for _, it := range items {
		rule, ok := rules[it.VariantID]
		if !ok {
			continue
		}
		if len(saved[it.ID]) != rule.PickCount*it.Quantity {
			return ErrDealersChoicePicksMissing
		}
	}
	return nil
}
