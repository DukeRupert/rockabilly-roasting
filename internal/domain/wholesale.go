package domain

import "github.com/google/uuid"

// MOQViolation describes a minimum order quantity or multiple constraint violation.
type MOQViolation struct {
	VariantID   uuid.UUID
	VariantName string
	Ordered     int
	Minimum     int
	Multiple    int // 0 = no multiple constraint
}

// ValidateWholesaleCart checks cart items against variant MOQ constraints.
// Returns nil if all items pass validation.
func ValidateWholesaleCart(items []CartItem, variants []Variant) []MOQViolation {
	variantMap := make(map[uuid.UUID]Variant, len(variants))
	for _, v := range variants {
		variantMap[v.ID] = v
	}

	var violations []MOQViolation
	for _, item := range items {
		v, ok := variantMap[item.VariantID]
		if !ok {
			continue
		}
		if v.WholesaleMinQty != nil && item.Quantity < *v.WholesaleMinQty {
			violations = append(violations, MOQViolation{
				VariantID:   v.ID,
				VariantName: v.SKU,
				Ordered:     item.Quantity,
				Minimum:     *v.WholesaleMinQty,
			})
			continue
		}
		if v.WholesaleMultiple != nil && *v.WholesaleMultiple > 0 && item.Quantity%*v.WholesaleMultiple != 0 {
			violations = append(violations, MOQViolation{
				VariantID:   v.ID,
				VariantName: v.SKU,
				Ordered:     item.Quantity,
				Multiple:    *v.WholesaleMultiple,
			})
		}
	}
	return violations
}
