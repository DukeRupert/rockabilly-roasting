package store

// DiscountStore provides database access for discounts and coupon codes.
type DiscountStore struct{}

// NewDiscountStore creates a new DiscountStore.
func NewDiscountStore() *DiscountStore {
	return &DiscountStore{}
}
