package domain

import (
	"time"

	"github.com/google/uuid"
)

// PriceListType represents the type of a price list.
type PriceListType string

const (
	PriceListTypeSale     PriceListType = "sale"
	PriceListTypeOverride PriceListType = "override"
)

// PriceListStatus represents the lifecycle state of a price list.
type PriceListStatus string

const (
	PriceListStatusDraft   PriceListStatus = "draft"
	PriceListStatusActive  PriceListStatus = "active"
	PriceListStatusExpired PriceListStatus = "expired"
)

// PriceSet groups prices for a variant.
type PriceSet struct {
	ID        uuid.UUID
	VariantID uuid.UUID
}

// Price represents a single price entry within a price set.
type Price struct {
	ID              uuid.UUID
	PriceSetID      uuid.UUID
	Amount          int
	CurrencyCode    string
	MinQuantity     *int
	MaxQuantity     *int
	PriceListID     *uuid.UUID
	StartsAt        *time.Time
	EndsAt          *time.Time
}

// PriceList groups promotional or override prices.
type PriceList struct {
	ID       uuid.UUID
	Name     string
	Type     PriceListType
	Status   PriceListStatus
	StartsAt *time.Time
	EndsAt   *time.Time
}
