package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/pirateship"
	"github.com/dukerupert/hiri/internal/store"
)

// ShippingExportService builds Pirate-Ship-compatible CSV exports of orders
// that are ready to ship. Read-only orchestration over orders, addresses,
// line items, variants, and inventory.
type ShippingExportService struct {
	orders      *store.OrderStore
	customers   *store.CustomerStore
	catalog     *store.CatalogStore
	fulfillment *store.FulfillmentStore
	shipping    *store.ShippingStore
}

// NewShippingExportService wires the read-side stores the export needs.
func NewShippingExportService(
	orders *store.OrderStore,
	customers *store.CustomerStore,
	catalog *store.CatalogStore,
	fulfillment *store.FulfillmentStore,
	shipping *store.ShippingStore,
) *ShippingExportService {
	return &ShippingExportService{
		orders:      orders,
		customers:   customers,
		catalog:     catalog,
		fulfillment: fulfillment,
		shipping:    shipping,
	}
}

// SkippedOrder reports an order that was excluded from the export so the
// admin UI can surface it (typically: missing variant weight).
type SkippedOrder struct {
	Number string
	Reason string
}

// BuildPirateShipCSV loads ready-to-ship orders and returns the encoded CSV
// alongside any orders that were skipped. The default selection is
// `fulfillment_status = 'unfulfilled' AND payment_status IN ('captured',
// 'authorized')`. If explicitOrderIDs is non-empty, those orders are loaded
// directly and the status filter is ignored.
func (s *ShippingExportService) BuildPirateShipCSV(
	ctx context.Context,
	tx pgx.Tx,
	explicitOrderIDs []uuid.UUID,
) ([]byte, []SkippedOrder, error) {
	orders, err := s.loadCandidateOrders(ctx, tx, explicitOrderIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("load orders: %w", err)
	}

	cfg, err := s.shipping.GetConfig(ctx, tx)
	if err != nil {
		return nil, nil, fmt.Errorf("load shipping config: %w", err)
	}

	// Caches scoped to this export pass so the same variant or inventory
	// item across multiple orders only hits the DB once.
	variantCache := map[uuid.UUID]*domain.Variant{}
	inventoryCache := map[uuid.UUID]*domain.InventoryItem{}

	rows := make([]pirateship.ExportRow, 0, len(orders))
	skipped := make([]SkippedOrder, 0)

	for _, order := range orders {
		row, skip, err := s.buildRow(ctx, tx, order, cfg.TareWeightOz, variantCache, inventoryCache)
		if err != nil {
			return nil, nil, fmt.Errorf("build row for order %s: %w", order.Number, err)
		}
		if skip != nil {
			skipped = append(skipped, *skip)
			continue
		}
		rows = append(rows, row)
	}

	csvBytes, err := pirateship.Encode(rows)
	if err != nil {
		return nil, nil, fmt.Errorf("encode csv: %w", err)
	}
	return csvBytes, skipped, nil
}

// loadCandidateOrders fetches the order set targeted by the export. Either
// explicit IDs (each loaded individually — small lists assumed) or the
// status-based ready-to-ship filter.
func (s *ShippingExportService) loadCandidateOrders(
	ctx context.Context,
	tx pgx.Tx,
	explicitOrderIDs []uuid.UUID,
) ([]domain.Order, error) {
	if len(explicitOrderIDs) > 0 {
		out := make([]domain.Order, 0, len(explicitOrderIDs))
		for _, id := range explicitOrderIDs {
			o, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					continue // silently drop unknown IDs
				}
				return nil, fmt.Errorf("get order %s: %w", id, err)
			}
			out = append(out, *o)
		}
		return out, nil
	}

	unfulfilled := domain.FulfillmentStatusUnfulfilled
	return s.orders.ListOrders(ctx, tx, store.OrderFilter{
		FulfillmentStatuses: []domain.FulfillmentStatus{unfulfilled},
		PaymentStatuses: []domain.PaymentStatus{
			domain.PaymentStatusCaptured,
			domain.PaymentStatusAuthorized,
		},
		Limit: 1000, // cap; single-merchant volume is far below this
	})
}

// buildRow assembles one ExportRow. Returns (row, nil, nil) on success or
// (zero, &SkippedOrder, nil) on a recoverable skip (e.g. missing weight).
// Any other error is fatal for the whole export.
func (s *ShippingExportService) buildRow(
	ctx context.Context,
	tx pgx.Tx,
	order domain.Order,
	tareOz float64,
	variantCache map[uuid.UUID]*domain.Variant,
	inventoryCache map[uuid.UUID]*domain.InventoryItem,
) (pirateship.ExportRow, *SkippedOrder, error) {
	addr, err := s.customers.GetAddressByIDAsStaff(ctx, tx, order.ShippingAddressID)
	if err != nil {
		return pirateship.ExportRow{}, &SkippedOrder{Number: order.Number, Reason: "shipping address not found"}, nil
	}

	email := ""
	if order.CustomerID != nil {
		c, cErr := s.customers.GetByID(ctx, tx, *order.CustomerID)
		if cErr == nil && c != nil {
			email = c.Email
		}
	}

	items, err := s.orders.ListLineItems(ctx, tx, order.ID)
	if err != nil {
		return pirateship.ExportRow{}, nil, fmt.Errorf("list line items: %w", err)
	}

	// Filter to physical items and gather SKUs + weights for the calc.
	physical := make([]domain.LineItem, 0, len(items))
	weights := make(map[uuid.UUID]*int, len(items))
	skus := make([]string, 0, len(items))
	for _, item := range items {
		variant, vErr := s.cachedVariant(ctx, tx, item.VariantID, variantCache)
		if vErr != nil {
			return pirateship.ExportRow{}, nil, fmt.Errorf("get variant %s: %w", item.VariantID, vErr)
		}
		// Treat a missing inventory item as physical (default for products
		// pre-inventory-tracking). Only an explicit RequiresShipping=false
		// removes the line from the shipment.
		inv, _ := s.cachedInventory(ctx, tx, item.VariantID, inventoryCache)
		if inv != nil && !inv.RequiresShipping {
			continue
		}
		physical = append(physical, item)
		weights[item.VariantID] = variant.WeightGrams
		if variant.SKU != "" {
			skus = append(skus, variant.SKU)
		}
	}

	weightOz, err := CalculateShipmentWeightOz(physical, weights, tareOz)
	if err != nil {
		if errors.Is(err, ErrShipmentWeightUnknown) {
			return pirateship.ExportRow{}, &SkippedOrder{
				Number: order.Number,
				Reason: "missing variant weight",
			}, nil
		}
		return pirateship.ExportRow{}, nil, fmt.Errorf("calculate weight: %w", err)
	}

	// All-digital orders: nothing physical to ship, nothing for Pirate Ship
	// to print. Skip rather than emit a zero-content row.
	if len(physical) == 0 {
		return pirateship.ExportRow{}, &SkippedOrder{
			Number: order.Number,
			Reason: "no shippable items",
		}, nil
	}

	row := pirateship.ExportRow{
		OrderID:   order.Number,
		Name:      joinName(addr.FirstName, addr.LastName),
		Address:   addr.Line1,
		City:      addr.City,
		State:     addr.State,
		Zipcode:   addr.PostalCode,
		Country:   defaultCountry(addr.CountryCode),
		Email:     email,
		WeightOz:  weightOz,
		ItemsLine: joinSKUs(skus),
	}
	if addr.Company != nil {
		row.Company = *addr.Company
	}
	if addr.Line2 != nil {
		row.Address2 = *addr.Line2
	}
	return row, nil, nil
}

func (s *ShippingExportService) cachedVariant(
	ctx context.Context,
	tx pgx.Tx,
	id uuid.UUID,
	cache map[uuid.UUID]*domain.Variant,
) (*domain.Variant, error) {
	if v, ok := cache[id]; ok {
		return v, nil
	}
	v, err := s.catalog.GetVariantByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	cache[id] = v
	return v, nil
}

func (s *ShippingExportService) cachedInventory(
	ctx context.Context,
	tx pgx.Tx,
	variantID uuid.UUID,
	cache map[uuid.UUID]*domain.InventoryItem,
) (*domain.InventoryItem, error) {
	if v, ok := cache[variantID]; ok {
		return v, nil
	}
	v, err := s.fulfillment.GetInventoryItemByVariantID(ctx, tx, variantID)
	if err != nil {
		// Cache the miss as nil so we don't keep retrying within one export.
		if errors.Is(err, pgx.ErrNoRows) {
			cache[variantID] = nil
			return nil, nil
		}
		return nil, err
	}
	cache[variantID] = v
	return v, nil
}

func joinName(first, last string) string {
	if first == "" {
		return last
	}
	if last == "" {
		return first
	}
	return first + " " + last
}

func defaultCountry(code string) string {
	if code == "" {
		return "US"
	}
	return code
}

// joinSKUs builds the Items column. Pirate Ship treats this as a "Rubber
// Stamp" hint, used to label the box. Comma-joined to keep the column
// human-readable; encoding/csv quotes it for us.
func joinSKUs(skus []string) string {
	if len(skus) == 0 {
		return ""
	}
	out := skus[0]
	for _, s := range skus[1:] {
		out += ", " + s
	}
	return out
}
