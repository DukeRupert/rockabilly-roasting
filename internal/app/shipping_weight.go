package app

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/dukerupert/hiri/internal/domain"
)

// gramsPerOunce is the conversion factor used by CalculateShipmentWeightOz.
// We use the international avoirdupois ounce (28.349523125 g/oz). The full
// precision avoids drift on multi-pound orders compared to the common
// rounded value of 28.35.
const gramsPerOunce = 28.349523125

// CalculateShipmentWeightOz returns the total shipped weight in ounces for a
// set of line items, using per-variant weights in grams plus a packaging tare.
//
// Inputs:
//   - items: line items that should contribute weight. Callers are responsible
//     for filtering out non-shipping (digital) line items before calling.
//   - weightGramsByVariant: variant ID → weight in grams. A nil entry means
//     the variant has no configured weight; if any included item maps to a
//     nil entry, ErrShipmentWeightUnknown is returned.
//   - tareOz: packaging weight in ounces, added to the final total.
//
// An empty items slice with a non-zero tare returns just the tare. Quantity
// must be positive; non-positive quantities return an error to avoid silently
// shipping a zero-weight box.
func CalculateShipmentWeightOz(
	items []domain.LineItem,
	weightGramsByVariant map[uuid.UUID]*int,
	tareOz float64,
) (float64, error) {
	totalGrams := 0
	for _, item := range items {
		if item.Quantity <= 0 {
			return 0, fmt.Errorf("calculate shipment weight: line item %s has non-positive quantity %d", item.ID, item.Quantity)
		}
		grams, ok := weightGramsByVariant[item.VariantID]
		if !ok || grams == nil {
			return 0, fmt.Errorf("variant %s: %w", item.VariantID, ErrShipmentWeightUnknown)
		}
		totalGrams += *grams * item.Quantity
	}
	return float64(totalGrams)/gramsPerOunce + tareOz, nil
}
