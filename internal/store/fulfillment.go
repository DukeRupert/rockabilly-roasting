package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// FulfillmentStore provides database access for fulfillments, inventory, and stock levels.
type FulfillmentStore struct{}

// NewFulfillmentStore creates a new FulfillmentStore.
func NewFulfillmentStore() *FulfillmentStore {
	return &FulfillmentStore{}
}

// --- Fulfillments ---

// CreateFulfillmentParams holds the fields needed to create a fulfillment.
type CreateFulfillmentParams struct {
	OrderID        uuid.UUID
	LocationID     uuid.UUID
	Status         domain.FulfillmentItemStatus
	TrackingNumber *string
	TrackingURL    *string
	Provider       *string
	Metadata       map[string]any
}

// CreateFulfillment inserts a new fulfillment and returns it.
func (s *FulfillmentStore) CreateFulfillment(ctx context.Context, tx pgx.Tx, p CreateFulfillmentParams) (*domain.Fulfillment, error) {
	row, err := sqlcgen.New(tx).CreateFulfillment(ctx, sqlcgen.CreateFulfillmentParams{
		ID:             uuid.New(),
		OrderID:        p.OrderID,
		LocationID:     p.LocationID,
		Status:         string(p.Status),
		TrackingNumber: p.TrackingNumber,
		TrackingUrl:    p.TrackingURL,
		Provider:       p.Provider,
		Metadata:       metadataToJSON(p.Metadata),
	})
	if err != nil {
		return nil, fmt.Errorf("insert fulfillment: %w", err)
	}
	return fulfillmentFromRow(row), nil
}

// GetFulfillmentByID returns a fulfillment by ID.
func (s *FulfillmentStore) GetFulfillmentByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Fulfillment, error) {
	row, err := sqlcgen.New(tx).GetFulfillmentByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get fulfillment %s: %w", id, err)
	}
	return fulfillmentFromRow(row), nil
}

// ListFulfillmentsByOrder returns all fulfillments for an order.
func (s *FulfillmentStore) ListFulfillmentsByOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.Fulfillment, error) {
	rows, err := sqlcgen.New(tx).ListFulfillmentsByOrder(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list fulfillments: %w", err)
	}
	fulfillments := make([]domain.Fulfillment, len(rows))
	for i, r := range rows {
		fulfillments[i] = *fulfillmentFromRow(r)
	}
	return fulfillments, nil
}

// UpdateFulfillmentStatus updates a fulfillment's status and returns it.
func (s *FulfillmentStore) UpdateFulfillmentStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.FulfillmentItemStatus) (*domain.Fulfillment, error) {
	row, err := sqlcgen.New(tx).UpdateFulfillmentStatus(ctx, sqlcgen.UpdateFulfillmentStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("update fulfillment status: %w", err)
	}
	return fulfillmentFromRow(row), nil
}

// UpdateFulfillmentTracking updates tracking info and returns the fulfillment.
func (s *FulfillmentStore) UpdateFulfillmentTracking(ctx context.Context, tx pgx.Tx, id uuid.UUID, trackingNumber, trackingURL, provider *string) (*domain.Fulfillment, error) {
	row, err := sqlcgen.New(tx).UpdateFulfillmentTracking(ctx, sqlcgen.UpdateFulfillmentTrackingParams{
		ID:             id,
		TrackingNumber: trackingNumber,
		TrackingUrl:    trackingURL,
		Provider:       provider,
	})
	if err != nil {
		return nil, fmt.Errorf("update fulfillment tracking: %w", err)
	}
	return fulfillmentFromRow(row), nil
}

// --- Fulfillment Items ---

// CreateFulfillmentItem inserts a new fulfillment item and returns it.
func (s *FulfillmentStore) CreateFulfillmentItem(ctx context.Context, tx pgx.Tx, fulfillmentID, lineItemID uuid.UUID, quantity int) (*domain.FulfillmentItem, error) {
	row, err := sqlcgen.New(tx).CreateFulfillmentItem(ctx, sqlcgen.CreateFulfillmentItemParams{
		ID:            uuid.New(),
		FulfillmentID: fulfillmentID,
		LineItemID:    lineItemID,
		Quantity:      int32(quantity),
	})
	if err != nil {
		return nil, fmt.Errorf("insert fulfillment item: %w", err)
	}
	return fulfillmentItemFromRow(row), nil
}

// ListFulfillmentItems returns all items for a fulfillment.
func (s *FulfillmentStore) ListFulfillmentItems(ctx context.Context, tx pgx.Tx, fulfillmentID uuid.UUID) ([]domain.FulfillmentItem, error) {
	rows, err := sqlcgen.New(tx).ListFulfillmentItemsByFulfillment(ctx, fulfillmentID)
	if err != nil {
		return nil, fmt.Errorf("list fulfillment items: %w", err)
	}
	items := make([]domain.FulfillmentItem, len(rows))
	for i, r := range rows {
		items[i] = *fulfillmentItemFromRow(r)
	}
	return items, nil
}

// --- Stock Locations ---

// CreateStockLocation inserts a new stock location and returns it.
func (s *FulfillmentStore) CreateStockLocation(ctx context.Context, tx pgx.Tx, name string, addressID *uuid.UUID, isActive bool) (*domain.StockLocation, error) {
	row, err := sqlcgen.New(tx).CreateStockLocation(ctx, sqlcgen.CreateStockLocationParams{
		ID:        uuid.New(),
		Name:      name,
		AddressID: addressID,
		IsActive:  isActive,
	})
	if err != nil {
		return nil, fmt.Errorf("insert stock location: %w", err)
	}
	return stockLocationFromRow(row), nil
}

// GetStockLocationByID returns a stock location by ID.
func (s *FulfillmentStore) GetStockLocationByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.StockLocation, error) {
	row, err := sqlcgen.New(tx).GetStockLocationByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get stock location %s: %w", id, err)
	}
	return stockLocationFromRow(row), nil
}

// ListStockLocations returns all stock locations.
func (s *FulfillmentStore) ListStockLocations(ctx context.Context, tx pgx.Tx) ([]domain.StockLocation, error) {
	rows, err := sqlcgen.New(tx).ListStockLocations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list stock locations: %w", err)
	}
	locations := make([]domain.StockLocation, len(rows))
	for i, r := range rows {
		locations[i] = *stockLocationFromRow(r)
	}
	return locations, nil
}

// UpdateStockLocationActive sets the active status of a stock location.
func (s *FulfillmentStore) UpdateStockLocationActive(ctx context.Context, tx pgx.Tx, id uuid.UUID, isActive bool) error {
	err := sqlcgen.New(tx).UpdateStockLocationActive(ctx, sqlcgen.UpdateStockLocationActiveParams{
		ID:       id,
		IsActive: isActive,
	})
	if err != nil {
		return fmt.Errorf("update stock location active: %w", err)
	}
	return nil
}

// --- Inventory Items ---

// CreateInventoryItem inserts a new inventory item and returns it.
func (s *FulfillmentStore) CreateInventoryItem(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, trackInventory, requiresShipping bool) (*domain.InventoryItem, error) {
	row, err := sqlcgen.New(tx).CreateInventoryItem(ctx, sqlcgen.CreateInventoryItemParams{
		ID:               uuid.New(),
		VariantID:        variantID,
		TrackInventory:   trackInventory,
		RequiresShipping: requiresShipping,
	})
	if err != nil {
		return nil, fmt.Errorf("insert inventory item: %w", err)
	}
	return inventoryItemFromRow(row), nil
}

// GetInventoryItemByID returns an inventory item by ID.
func (s *FulfillmentStore) GetInventoryItemByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.InventoryItem, error) {
	row, err := sqlcgen.New(tx).GetInventoryItemByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get inventory item %s: %w", id, err)
	}
	return inventoryItemFromRow(row), nil
}

// GetInventoryItemByVariantID returns an inventory item by variant ID.
func (s *FulfillmentStore) GetInventoryItemByVariantID(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) (*domain.InventoryItem, error) {
	row, err := sqlcgen.New(tx).GetInventoryItemByVariantID(ctx, variantID)
	if err != nil {
		return nil, fmt.Errorf("get inventory item by variant %s: %w", variantID, err)
	}
	return inventoryItemFromRow(row), nil
}

// --- Stock Levels ---

// CreateStockLevel inserts a new stock level and returns it.
func (s *FulfillmentStore) CreateStockLevel(ctx context.Context, tx pgx.Tx, inventoryItemID, locationID uuid.UUID, quantityOnHand, quantityReserved int) (*domain.StockLevel, error) {
	row, err := sqlcgen.New(tx).CreateStockLevel(ctx, sqlcgen.CreateStockLevelParams{
		ID:               uuid.New(),
		InventoryItemID:  inventoryItemID,
		LocationID:       locationID,
		QuantityOnHand:   int32(quantityOnHand),
		QuantityReserved: int32(quantityReserved),
	})
	if err != nil {
		return nil, fmt.Errorf("insert stock level: %w", err)
	}
	return stockLevelFromRow(row), nil
}

// GetStockLevel returns a stock level by inventory item and location.
func (s *FulfillmentStore) GetStockLevel(ctx context.Context, tx pgx.Tx, inventoryItemID, locationID uuid.UUID) (*domain.StockLevel, error) {
	row, err := sqlcgen.New(tx).GetStockLevelByInventoryAndLocation(ctx, sqlcgen.GetStockLevelByInventoryAndLocationParams{
		InventoryItemID: inventoryItemID,
		LocationID:      locationID,
	})
	if err != nil {
		return nil, fmt.Errorf("get stock level: %w", err)
	}
	return stockLevelFromRow(row), nil
}

// ListStockLevels returns all stock levels for an inventory item.
func (s *FulfillmentStore) ListStockLevels(ctx context.Context, tx pgx.Tx, inventoryItemID uuid.UUID) ([]domain.StockLevel, error) {
	rows, err := sqlcgen.New(tx).ListStockLevelsByInventory(ctx, inventoryItemID)
	if err != nil {
		return nil, fmt.Errorf("list stock levels: %w", err)
	}
	levels := make([]domain.StockLevel, len(rows))
	for i, r := range rows {
		levels[i] = *stockLevelFromRow(r)
	}
	return levels, nil
}

// AdjustStockQuantity adjusts the on-hand quantity by a delta and returns the updated level.
func (s *FulfillmentStore) AdjustStockQuantity(ctx context.Context, tx pgx.Tx, id uuid.UUID, delta int) (*domain.StockLevel, error) {
	row, err := sqlcgen.New(tx).AdjustStockQuantity(ctx, sqlcgen.AdjustStockQuantityParams{
		ID:    id,
		Delta: int32(delta),
	})
	if err != nil {
		return nil, fmt.Errorf("adjust stock quantity: %w", err)
	}
	return stockLevelFromRow(row), nil
}

// ReserveStock increases the reserved quantity and returns the updated level.
func (s *FulfillmentStore) ReserveStock(ctx context.Context, tx pgx.Tx, id uuid.UUID, delta int) (*domain.StockLevel, error) {
	row, err := sqlcgen.New(tx).ReserveStock(ctx, sqlcgen.ReserveStockParams{
		ID:    id,
		Delta: int32(delta),
	})
	if err != nil {
		return nil, fmt.Errorf("reserve stock: %w", err)
	}
	return stockLevelFromRow(row), nil
}

// ReleaseReservation decreases the reserved quantity and returns the updated level.
func (s *FulfillmentStore) ReleaseReservation(ctx context.Context, tx pgx.Tx, id uuid.UUID, delta int) (*domain.StockLevel, error) {
	row, err := sqlcgen.New(tx).ReleaseReservation(ctx, sqlcgen.ReleaseReservationParams{
		ID:    id,
		Delta: int32(delta),
	})
	if err != nil {
		return nil, fmt.Errorf("release reservation: %w", err)
	}
	return stockLevelFromRow(row), nil
}

// --- Row converters ---

func fulfillmentFromRow(r sqlcgen.Fulfillment) *domain.Fulfillment {
	return &domain.Fulfillment{
		ID:             r.ID,
		OrderID:        r.OrderID,
		LocationID:     r.LocationID,
		Status:         domain.FulfillmentItemStatus(r.Status),
		TrackingNumber: r.TrackingNumber,
		TrackingURL:    r.TrackingUrl,
		Provider:       r.Provider,
		ShippedAt:      timestampFromPG(r.ShippedAt),
		DeliveredAt:    timestampFromPG(r.DeliveredAt),
		Metadata:       metadataFromJSON(r.Metadata),
	}
}

func fulfillmentItemFromRow(r sqlcgen.FulfillmentItem) *domain.FulfillmentItem {
	return &domain.FulfillmentItem{
		ID:            r.ID,
		FulfillmentID: r.FulfillmentID,
		LineItemID:    r.LineItemID,
		Quantity:      int(r.Quantity),
	}
}

func stockLocationFromRow(r sqlcgen.StockLocation) *domain.StockLocation {
	return &domain.StockLocation{
		ID:        r.ID,
		Name:      r.Name,
		AddressID: r.AddressID,
		IsActive:  r.IsActive,
	}
}

func inventoryItemFromRow(r sqlcgen.InventoryItem) *domain.InventoryItem {
	return &domain.InventoryItem{
		ID:               r.ID,
		VariantID:        r.VariantID,
		TrackInventory:   r.TrackInventory,
		RequiresShipping: r.RequiresShipping,
	}
}

func stockLevelFromRow(r sqlcgen.StockLevel) *domain.StockLevel {
	return &domain.StockLevel{
		ID:                r.ID,
		InventoryItemID:   r.InventoryItemID,
		LocationID:        r.LocationID,
		QuantityOnHand:    int(r.QuantityOnHand),
		QuantityReserved:  int(r.QuantityReserved),
		QuantityAvailable: int32ToInt(r.QuantityAvailable),
	}
}
