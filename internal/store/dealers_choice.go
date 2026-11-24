package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
)

// PickStore provides database access for dealer's choice packs: the pick rules
// that mark a variant as a pack, and the coffees staff picked for each line.
type PickStore struct {
	metrics QueryRecorder
}

// NewPickStore creates a new PickStore. Pass nil for metrics to disable query
// timing instrumentation.
func NewPickStore(metrics QueryRecorder) *PickStore {
	return &PickStore{metrics: metrics}
}

// ListRules returns the pick rules for the given variants, keyed by variant ID.
// Variants without a rule are absent from the map.
func (s *PickStore) ListRules(ctx context.Context, tx pgx.Tx, variantIDs []uuid.UUID) (_ map[uuid.UUID]domain.PickRule, err error) {
	defer trackQuery(s.metrics, "variant_pick_rules.list", time.Now(), &err)
	out := make(map[uuid.UUID]domain.PickRule, len(variantIDs))
	if len(variantIDs) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx,
		`SELECT variant_id, pick_count, exclude_decaf FROM variant_pick_rules WHERE variant_id = ANY($1)`,
		variantIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("list pick rules: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r domain.PickRule
		if err := rows.Scan(&r.VariantID, &r.PickCount, &r.ExcludeDecaf); err != nil {
			return nil, fmt.Errorf("scan pick rule: %w", err)
		}
		out[r.VariantID] = r
	}
	return out, rows.Err()
}

// ListRulesByProduct returns the pick rules for every variant of a product.
func (s *PickStore) ListRulesByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID) (_ map[uuid.UUID]domain.PickRule, err error) {
	defer trackQuery(s.metrics, "variant_pick_rules.list_by_product", time.Now(), &err)
	rows, err := tx.Query(ctx,
		`SELECT r.variant_id, r.pick_count, r.exclude_decaf
		   FROM variant_pick_rules r
		   JOIN variants v ON v.id = r.variant_id
		  WHERE v.product_id = $1`,
		productID,
	)
	if err != nil {
		return nil, fmt.Errorf("list pick rules by product: %w", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID]domain.PickRule)
	for rows.Next() {
		var r domain.PickRule
		if err := rows.Scan(&r.VariantID, &r.PickCount, &r.ExcludeDecaf); err != nil {
			return nil, fmt.Errorf("scan pick rule: %w", err)
		}
		out[r.VariantID] = r
	}
	return out, rows.Err()
}

// UpsertRule creates or replaces a variant's pick rule.
func (s *PickStore) UpsertRule(ctx context.Context, tx pgx.Tx, r domain.PickRule) (err error) {
	defer trackQuery(s.metrics, "variant_pick_rules.upsert", time.Now(), &err)
	_, err = tx.Exec(ctx,
		`INSERT INTO variant_pick_rules (variant_id, pick_count, exclude_decaf)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (variant_id) DO UPDATE
		    SET pick_count = EXCLUDED.pick_count,
		        exclude_decaf = EXCLUDED.exclude_decaf,
		        updated_at = now()`,
		r.VariantID, r.PickCount, r.ExcludeDecaf,
	)
	if err != nil {
		return fmt.Errorf("upsert pick rule: %w", err)
	}
	return nil
}

// DeleteRule removes a variant's pick rule, turning it back into an ordinary
// variant. Deleting a rule that does not exist is not an error.
func (s *PickStore) DeleteRule(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "variant_pick_rules.delete", time.Now(), &err)
	if _, err = tx.Exec(ctx, `DELETE FROM variant_pick_rules WHERE variant_id = $1`, variantID); err != nil {
		return fmt.Errorf("delete pick rule: %w", err)
	}
	return nil
}

// ListPicks returns the confirmed picks for the given line items, keyed by
// line item ID and ordered by position.
func (s *PickStore) ListPicks(ctx context.Context, tx pgx.Tx, lineItemIDs []uuid.UUID) (_ map[uuid.UUID][]domain.LineItemPick, err error) {
	defer trackQuery(s.metrics, "line_item_picks.list", time.Now(), &err)
	out := make(map[uuid.UUID][]domain.LineItemPick, len(lineItemIDs))
	if len(lineItemIDs) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx,
		`SELECT line_item_id, position, product_id, picked_at
		   FROM line_item_picks
		  WHERE line_item_id = ANY($1)
		  ORDER BY line_item_id, position`,
		lineItemIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("list picks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p domain.LineItemPick
		if err := rows.Scan(&p.LineItemID, &p.Position, &p.ProductID, &p.PickedAt); err != nil {
			return nil, fmt.Errorf("scan pick: %w", err)
		}
		out[p.LineItemID] = append(out[p.LineItemID], p)
	}
	return out, rows.Err()
}

// ReplacePicks overwrites a line item's picks with productIDs, position by
// position.
func (s *PickStore) ReplacePicks(ctx context.Context, tx pgx.Tx, lineItemID uuid.UUID, productIDs []uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "line_item_picks.replace", time.Now(), &err)
	if _, err = tx.Exec(ctx, `DELETE FROM line_item_picks WHERE line_item_id = $1`, lineItemID); err != nil {
		return fmt.Errorf("clear picks: %w", err)
	}
	if len(productIDs) == 0 {
		return nil
	}
	positions := make([]int32, len(productIDs))
	for i := range positions {
		positions[i] = int32(i)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO line_item_picks (line_item_id, position, product_id)
		 SELECT $1, p.position, p.product_id
		   FROM unnest($2::int[], $3::uuid[]) AS p(position, product_id)`,
		lineItemID, positions, productIDs,
	)
	if err != nil {
		return fmt.Errorf("insert picks: %w", err)
	}
	return nil
}

// ListCandidates returns every coffee that can go in a dealer's choice box: an
// active, public product sold in a 12oz bag on the retail channel, that is not
// itself a dealer's choice pack. IsDecaf comes from the product's
// caffeine-level attribute, the same one the storefront's decaf filter reads.
//
// "Sold in a 12oz bag" means a live, retail-available variant with an option
// value reading 12oz (any spacing or case, optional trailing dot). A coffee
// only sold by the pound is left out, since the pack promises 12oz bags.
func (s *PickStore) ListCandidates(ctx context.Context, tx pgx.Tx) (_ []domain.PickCandidate, err error) {
	defer trackQuery(s.metrics, "line_item_picks.list_candidates", time.Now(), &err)
	rows, err := tx.Query(ctx, `
		SELECT p.id, p.title,
		       EXISTS (
		           SELECT 1
		             FROM product_attribute_values pav
		             JOIN attribute_keys ak ON ak.id = pav.attribute_key_id
		            WHERE pav.product_id = p.id
		              AND ak.slug = 'caffeine-level'
		              AND lower(pav.value) = 'decaf'
		       ) AS is_decaf
		  FROM products p
		 WHERE p.status = 'active'
		   AND p.visibility = 'public'
		   AND EXISTS (
		       SELECT 1
		         FROM variants v
		         JOIN variant_option_values vov ON vov.variant_id = v.id
		         JOIN product_option_values pov ON pov.id = vov.product_option_value_id
		        WHERE v.product_id = p.id
		          AND v.archived_at IS NULL
		          AND v.retail_available
		          AND pov.value ~* '^\s*12\s*oz\.?\s*$'
		   )
		   AND NOT EXISTS (
		       SELECT 1
		         FROM variants v
		         JOIN variant_pick_rules r ON r.variant_id = v.id
		        WHERE v.product_id = p.id
		   )
		 ORDER BY lower(p.title), p.id`)
	if err != nil {
		return nil, fmt.Errorf("list pick candidates: %w", err)
	}
	defer rows.Close()
	var out []domain.PickCandidate
	for rows.Next() {
		var c domain.PickCandidate
		if err := rows.Scan(&c.ProductID, &c.Title, &c.IsDecaf); err != nil {
			return nil, fmt.Errorf("scan pick candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetPreviousPicks returns the coffees a customer was sent in their most
// recent other order that has confirmed picks, or nil when there is none.
// Cancelled and refunded orders do not count: the customer never drank those.
func (s *PickStore) GetPreviousPicks(ctx context.Context, tx pgx.Tx, customerID, excludeOrderID uuid.UUID) (_ *domain.PreviousPicks, err error) {
	defer trackQuery(s.metrics, "line_item_picks.get_previous", time.Now(), &err)
	rows, err := tx.Query(ctx, `
		WITH last_order AS (
		    SELECT o.id, o.number, o.placed_at
		      FROM orders o
		     WHERE o.customer_id = $1
		       AND o.id <> $2
		       AND o.status NOT IN ('cancelled', 'refunded')
		       AND EXISTS (
		           SELECT 1 FROM line_items li
		             JOIN line_item_picks lp ON lp.line_item_id = li.id
		            WHERE li.order_id = o.id)
		     ORDER BY o.placed_at DESC, o.id DESC
		     LIMIT 1
		)
		SELECT lo.id, lo.number, lo.placed_at, p.id, p.title
		  FROM last_order lo
		  JOIN line_items li ON li.order_id = lo.id
		  JOIN line_item_picks lp ON lp.line_item_id = li.id
		  JOIN products p ON p.id = lp.product_id
		 ORDER BY li.id, lp.position`,
		customerID, excludeOrderID,
	)
	if err != nil {
		return nil, fmt.Errorf("get previous picks: %w", err)
	}
	defer rows.Close()
	var out *domain.PreviousPicks
	for rows.Next() {
		var prev domain.PreviousPicks
		var id uuid.UUID
		var title string
		if err := rows.Scan(&prev.OrderID, &prev.OrderNumber, &prev.PlacedAt, &id, &title); err != nil {
			return nil, fmt.Errorf("scan previous pick: %w", err)
		}
		if out == nil {
			out = &prev
		}
		out.ProductIDs = append(out.ProductIDs, id)
		out.Titles = append(out.Titles, title)
	}
	return out, rows.Err()
}

// ProductTitles returns the titles of the given products, keyed by ID. Used to
// label saved picks whose coffee has since left the candidate pool.
func (s *PickStore) ProductTitles(ctx context.Context, tx pgx.Tx, productIDs []uuid.UUID) (_ map[uuid.UUID]string, err error) {
	defer trackQuery(s.metrics, "line_item_picks.product_titles", time.Now(), &err)
	out := make(map[uuid.UUID]string, len(productIDs))
	if len(productIDs) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT id, title FROM products WHERE id = ANY($1)`, productIDs)
	if err != nil {
		return nil, fmt.Errorf("product titles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, fmt.Errorf("scan product title: %w", err)
		}
		out[id] = title
	}
	return out, rows.Err()
}
