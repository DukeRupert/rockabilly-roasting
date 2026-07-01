package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/shipping"
	"github.com/dukerupert/hiri/internal/store"
)

// FulfillmentService contains business logic for fulfillment and shipping.
type FulfillmentService struct {
	fulfillment   *store.FulfillmentStore
	shipments     *store.ShippingStore
	orders        *store.OrderStore
	boxPresets    *store.BoxPresetStore
	customers     *store.CustomerStore
	catalog       *store.CatalogStore
	labelProvider shipping.LabelProvider
	audit         *audit.AuditWriter
	metrics       *metrics.Registry
}

// NewFulfillmentService creates a new FulfillmentService.
func NewFulfillmentService(
	fulfillment *store.FulfillmentStore,
	shipments *store.ShippingStore,
	orders *store.OrderStore,
	boxPresets *store.BoxPresetStore,
	customers *store.CustomerStore,
	catalog *store.CatalogStore,
	labelProvider shipping.LabelProvider,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *FulfillmentService {
	return &FulfillmentService{
		fulfillment:   fulfillment,
		shipments:     shipments,
		orders:        orders,
		boxPresets:    boxPresets,
		customers:     customers,
		catalog:       catalog,
		labelProvider: labelProvider,
		audit:         audit,
		metrics:       metrics,
	}
}

// GetShipment returns a shipment by ID.
func (s *FulfillmentService) GetShipment(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Shipment, error) {
	shipment, err := s.shipments.GetShipmentByIDAsStaff(ctx, tx, id)
	if err != nil {
		return nil, fmt.Errorf("get shipment: %w", err)
	}
	return shipment, nil
}

// ListShipmentsByOrder returns all shipments for an order.
func (s *FulfillmentService) ListShipmentsByOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.Shipment, error) {
	shipments, err := s.shipments.ListShipmentsByOrder(ctx, tx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list shipments by order: %w", err)
	}
	return shipments, nil
}

// GetLatestLabelAttempt returns the most recent in-flight or failed BuyLabel
// job for an order. Returns nil if none is pending and no recent failure is
// recorded. Successful attempts are surfaced as shipment rows, not here.
func (s *FulfillmentService) GetLatestLabelAttempt(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (*domain.LabelAttempt, error) {
	attempt, err := s.shipments.GetLatestLabelAttempt(ctx, tx, orderID)
	if err != nil {
		return nil, fmt.Errorf("get latest label attempt: %w", err)
	}
	return attempt, nil
}

// ListOrdersWithFailedLabelAttempts returns the subset of the given orders
// whose latest BuyLabel job ended in failure. Used by the order list to
// flag rows that need operator attention.
func (s *FulfillmentService) ListOrdersWithFailedLabelAttempts(ctx context.Context, tx pgx.Tx, orderIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	ids, err := s.shipments.ListOrdersWithFailedLabelAttempts(ctx, tx, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("list orders with failed label attempts: %w", err)
	}
	return ids, nil
}

// CreateShipmentLabel calls the external label provider to create a shipping
// label, then persists the shipment record in the database. The external API
// call happens BEFORE the transaction — if it fails, nothing is written.
func (s *FulfillmentService) CreateShipmentLabel(
	ctx context.Context,
	tx pgx.Tx,
	req shipping.LabelRequest,
	orderID uuid.UUID,
	actor Actor,
) (*domain.Shipment, error) {
	// External API call — outside transaction scope per architecture rules.
	result, err := s.labelProvider.CreateLabel(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create label: %w", err)
	}

	labelURL := result.LabelURL
	lengthIn := req.LengthIn
	widthIn := req.WidthIn
	heightIn := req.HeightIn
	shipment, err := s.shipments.CreateShipment(ctx, tx, store.CreateShipmentParams{
		OrderID:        orderID,
		Status:         domain.ShipmentStatusLabelCreated,
		Provider:       "easypost",
		TrackingNumber: result.TrackingNumber,
		LabelURL:       &labelURL,
		CarrierName:    result.CarrierName,
		ServiceName:    result.ServiceName,
		LabelCostCents: result.RateCents,
		LabelCurrency:  result.Currency,
		WeightOz:       req.WeightOz,
		LengthIn:       &lengthIn,
		WidthIn:        &widthIn,
		HeightIn:       &heightIn,
		CreatedBy:      *actor.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("insert shipment: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditShipmentLabelCreated,
		ResourceType: "shipment",
		ResourceID:   shipment.ID,
		After:        shipment,
	}); err != nil {
		return nil, fmt.Errorf("audit shipment label created: %w", err)
	}

	return shipment, nil
}

// UpdateShipmentLabel stores the R2 key and format for a shipment's label.
// Called after the label has been fetched from EasyPost and uploaded to R2.
func (s *FulfillmentService) UpdateShipmentLabel(ctx context.Context, tx pgx.Tx, shipmentID uuid.UUID, r2Key, format string) error {
	if err := s.shipments.UpdateShipmentLabel(ctx, tx, shipmentID, r2Key, format); err != nil {
		return fmt.Errorf("update shipment label: %w", err)
	}
	return nil
}

// GetShipmentLabelKey returns the R2 object key for a shipment's label.
func (s *FulfillmentService) GetShipmentLabelKey(ctx context.Context, tx pgx.Tx, shipmentID uuid.UUID) (*string, error) {
	key, err := s.shipments.GetShipmentLabelKey(ctx, tx, shipmentID)
	if err != nil {
		return nil, fmt.Errorf("get shipment label key: %w", err)
	}
	return key, nil
}

// PrepareLabelRequest assembles a LabelRequest for an order from stored data:
// origin from shipping config, destination from the order's shipping address,
// weight from line item variants + tare, and box dimensions from the smallest
// preset that fits. Returns ErrShipmentWeightUnknown if any physical line
// item lacks a configured variant weight, and ErrNoBoxPreset if the merchant
// has no presets defined.
func (s *FulfillmentService) PrepareLabelRequest(
	ctx context.Context,
	tx pgx.Tx,
	orderID uuid.UUID,
	serviceCode string,
) (shipping.LabelRequest, error) {
	zero := shipping.LabelRequest{}

	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, orderID)
	if err != nil {
		return zero, fmt.Errorf("load order: %w", err)
	}
	cfg, err := s.shipments.GetConfig(ctx, tx)
	if err != nil {
		return zero, fmt.Errorf("load shipping config: %w", err)
	}
	addr, err := s.customers.GetAddressByIDAsStaff(ctx, tx, order.ShippingAddressID)
	if err != nil {
		return zero, fmt.Errorf("load shipping address: %w", err)
	}
	items, err := s.orders.ListLineItems(ctx, tx, order.ID)
	if err != nil {
		return zero, fmt.Errorf("list line items: %w", err)
	}

	// Filter to physical items and gather weights.
	physical := make([]domain.LineItem, 0, len(items))
	weights := make(map[uuid.UUID]*int, len(items))
	for _, item := range items {
		variant, vErr := s.catalog.GetVariantByID(ctx, tx, item.VariantID)
		if vErr != nil {
			return zero, fmt.Errorf("get variant %s: %w", item.VariantID, vErr)
		}
		inv, _ := s.fulfillment.GetInventoryItemByVariantID(ctx, tx, item.VariantID)
		if inv != nil && !inv.RequiresShipping {
			continue
		}
		physical = append(physical, item)
		weights[item.VariantID] = variant.WeightGrams
	}
	if len(physical) == 0 {
		return zero, ErrShipmentNoPhysicalItems
	}

	weightOz, err := CalculateShipmentWeightOz(physical, weights, cfg.TareWeightOz)
	if err != nil {
		return zero, err
	}

	presets, err := s.boxPresets.ListByMaxWeightAsc(ctx, tx)
	if err != nil {
		return zero, fmt.Errorf("list box presets: %w", err)
	}
	box, _ := domain.SelectBoxForWeight(presets, weightOz)
	if box == nil {
		return zero, ErrNoBoxPreset
	}

	toCountry := addr.CountryCode
	if toCountry == "" {
		toCountry = "US"
	}
	originCountry := cfg.OriginCountry
	if originCountry == "" {
		originCountry = "US"
	}

	// Customer contact populates the ship-to email/phone. Optional — Shippo
	// accepts shipments without them, and tracking emails from the carrier
	// are nice-to-have. Guest checkouts have no Customer row; the order has
	// a customer email we could thread through later if useful.
	toEmail, toPhone := "", ""
	if order.CustomerID != nil {
		c, cErr := s.customers.GetByID(ctx, tx, *order.CustomerID)
		if cErr == nil && c != nil {
			toEmail = c.Email
			if c.Phone != nil {
				toPhone = *c.Phone
			}
		}
	}

	return shipping.LabelRequest{
		FromName:    cfg.OriginName,
		FromStreet1: cfg.OriginStreet1,
		FromCity:    cfg.OriginCity,
		FromState:   cfg.OriginState,
		FromZip:     cfg.OriginZip,
		FromCountry: originCountry,
		FromEmail:   cfg.OriginEmail,
		FromPhone:   cfg.OriginPhone,
		ToName:      joinName(addr.FirstName, addr.LastName),
		ToStreet1:   addr.Line1,
		ToCity:      addr.City,
		ToState:     addr.State,
		ToZip:       addr.PostalCode,
		ToCountry:   toCountry,
		ToEmail:     toEmail,
		ToPhone:     toPhone,
		WeightOz:    weightOz,
		LengthIn:    box.LengthIn,
		WidthIn:     box.WidthIn,
		HeightIn:    box.HeightIn,
		ServiceCode: serviceCode,
		Reference:   order.Number,
	}, nil
}

// PurchaseLabel calls the configured label provider. This is an external API
// call and MUST NOT be invoked from inside a database transaction — the
// BuyLabel worker is the canonical caller and follows the two-phase pattern
// (read tx → PurchaseLabel → write tx).
func (s *FulfillmentService) PurchaseLabel(ctx context.Context, req shipping.LabelRequest) (*shipping.LabelResult, error) {
	result, err := s.labelProvider.CreateLabel(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create label: %w", err)
	}
	return result, nil
}

// FetchRates returns the purchasable shipping rates for a prepared label
// request, cheapest first. This is an external API call and MUST NOT be invoked
// from inside a database transaction — callers run PrepareLabelRequest in a read
// tx first, then call FetchRates with the tx released (same split as the
// PrepareLabelRequest → PurchaseLabel flow).
func (s *FulfillmentService) FetchRates(ctx context.Context, req shipping.LabelRequest) ([]shipping.Rate, error) {
	rates, err := s.labelProvider.GetRates(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get rates: %w", err)
	}
	return rates, nil
}

// BuyLabelRate purchases a specific rate the operator selected from FetchRates.
// Like PurchaseLabel this is an external API call and MUST NOT run inside a
// transaction. A stale rate may be rejected by the provider — callers should
// re-fetch and let the operator re-confirm on error.
func (s *FulfillmentService) BuyLabelRate(ctx context.Context, rate shipping.Rate) (*shipping.LabelResult, error) {
	result, err := s.labelProvider.BuyRate(ctx, rate)
	if err != nil {
		return nil, fmt.Errorf("buy rate: %w", err)
	}
	return result, nil
}

// PersistShipmentLabel writes a shipment record + audit entry for a label
// that was already purchased via PurchaseLabel. Caller is responsible for
// enqueueing the StoreLabelToR2 job in the same transaction.
//
// It refuses to persist a second live label for an order (ErrOrderHasActiveLabel):
// the "one active label per order" rule is enforced here, in the write tx, not
// just in the UI — a stale page or double-submit would otherwise buy a duplicate
// label. A refunded (or refund-requested) label no longer blocks, so a corrected
// label can be bought after requesting a refund on the wrong one.
func (s *FulfillmentService) PersistShipmentLabel(
	ctx context.Context,
	tx pgx.Tx,
	orderID uuid.UUID,
	req shipping.LabelRequest,
	result shipping.LabelResult,
	actor Actor,
) (*domain.Shipment, error) {
	existing, err := s.shipments.ListShipmentsByOrder(ctx, tx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list shipments: %w", err)
	}
	for _, sh := range existing {
		if sh.BlocksRebuy() {
			return nil, ErrOrderHasActiveLabel
		}
	}

	labelURL := result.LabelURL
	lengthIn := req.LengthIn
	widthIn := req.WidthIn
	heightIn := req.HeightIn
	var txnID *string
	if result.ProviderTransactionID != "" {
		txnID = &result.ProviderTransactionID
	}

	shipment, err := s.shipments.CreateShipment(ctx, tx, store.CreateShipmentParams{
		OrderID:               orderID,
		Status:                domain.ShipmentStatusLabelCreated,
		Provider:              "shippo",
		TrackingNumber:        result.TrackingNumber,
		LabelURL:              &labelURL,
		CarrierName:           result.CarrierName,
		ServiceName:           result.ServiceName,
		LabelCostCents:        result.RateCents,
		LabelCurrency:         result.Currency,
		WeightOz:              req.WeightOz,
		LengthIn:              &lengthIn,
		WidthIn:               &widthIn,
		HeightIn:              &heightIn,
		CreatedBy:             derefUUID(actor.ID),
		ProviderTransactionID: txnID,
	})
	if err != nil {
		return nil, fmt.Errorf("insert shipment: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditShipmentLabelCreated,
		ResourceType: "shipment",
		ResourceID:   shipment.ID,
		After:        shipment,
	}); err != nil {
		return nil, fmt.Errorf("audit shipment label created: %w", err)
	}
	return shipment, nil
}

// --- Label refunds ---
//
// Refunding a label follows the same two-phase pattern as buying one: load and
// validate in a read tx, call the carrier outside any tx, then write the result
// in a write tx that also enqueues the poll job. The carrier resolves refunds
// asynchronously (up to 14 days), so a River job polls for the terminal state.

// LoadRefundableShipment loads a shipment and confirms it can be refunded,
// returning its provider transaction ID. Returns ErrShipmentNotRefundable when
// the label has no transaction ID, already has a refund in flight/completed, or
// is delivered. Run in a read tx before calling the carrier.
func (s *FulfillmentService) LoadRefundableShipment(ctx context.Context, tx pgx.Tx, shipmentID uuid.UUID) (*domain.Shipment, error) {
	shipment, err := s.shipments.GetShipmentByIDAsStaff(ctx, tx, shipmentID)
	if err != nil {
		return nil, fmt.Errorf("get shipment: %w", err)
	}
	if !shipment.CanRequestRefund() {
		return nil, ErrShipmentNotRefundable
	}
	return shipment, nil
}

// RequestRefundExternal asks the carrier to refund the label. This is an
// external API call and MUST NOT run inside a transaction.
func (s *FulfillmentService) RequestRefundExternal(ctx context.Context, transactionID string) (*shipping.RefundResult, error) {
	res, err := s.labelProvider.RequestRefund(ctx, transactionID)
	if err != nil {
		return nil, fmt.Errorf("request refund: %w", err)
	}
	return res, nil
}

// PersistRefundRequest records that a refund is in flight, audits it, and
// returns the updated shipment. Callers enqueue the PollLabelRefund job in the
// same transaction so the async resolution is tracked.
func (s *FulfillmentService) PersistRefundRequest(ctx context.Context, tx pgx.Tx, shipmentID uuid.UUID, refund *shipping.RefundResult, actor Actor) (*domain.Shipment, error) {
	shipment, err := s.shipments.UpdateShipmentRefundRequested(ctx, tx, shipmentID, refund.RefundID, actor.ID)
	if err != nil {
		return nil, fmt.Errorf("mark refund requested: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditShipmentLabelRefundRequested,
		ResourceType: "shipment",
		ResourceID:   shipment.ID,
		After:        shipment,
	}); err != nil {
		return nil, fmt.Errorf("audit refund requested: %w", err)
	}
	return shipment, nil
}

// GetRefundStatus polls the carrier for the current state of a refund. External
// call — MUST NOT run inside a transaction. Thin passthrough for the poll job.
func (s *FulfillmentService) GetRefundStatus(ctx context.Context, refundID string) (*shipping.RefundResult, error) {
	res, err := s.labelProvider.GetRefund(ctx, refundID)
	if err != nil {
		return nil, fmt.Errorf("get refund: %w", err)
	}
	return res, nil
}

// ResolveRefund settles a requested refund to its terminal status. It is
// idempotent: the underlying update only touches a shipment still in
// 'requested', so a replayed poll job (or one racing a resolution) makes no
// change and records no audit entry. status must be RefundStatusRefunded or
// RefundStatusFailed. metadata is attached to the audit entry (the poll job
// passes its river_job_id); it may be nil.
func (s *FulfillmentService) ResolveRefund(ctx context.Context, tx pgx.Tx, shipmentID uuid.UUID, status domain.RefundStatus, actor Actor, metadata map[string]any) error {
	shipment, ok, err := s.shipments.UpdateShipmentRefundResolved(ctx, tx, shipmentID, status)
	if err != nil {
		return fmt.Errorf("resolve refund: %w", err)
	}
	if !ok {
		// Already resolved by a prior run — no-op, no re-audit.
		return nil
	}

	action := audit.AuditShipmentLabelRefunded
	if status == domain.RefundStatusFailed {
		action = audit.AuditShipmentLabelRefundFailed
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       action,
		ResourceType: "shipment",
		ResourceID:   shipment.ID,
		After:        shipment,
		Metadata:     metadata,
	}); err != nil {
		return fmt.Errorf("audit refund resolved: %w", err)
	}
	return nil
}

func derefUUID(p *uuid.UUID) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	return *p
}

// --- Box presets ---

// ListBoxPresets returns all box presets in display order.
func (s *FulfillmentService) ListBoxPresets(ctx context.Context, tx pgx.Tx) ([]domain.BoxPreset, error) {
	return s.boxPresets.List(ctx, tx)
}

// ListBoxPresetsForSelection returns presets sorted by capacity ascending —
// the order needed for SelectBoxForWeight.
func (s *FulfillmentService) ListBoxPresetsForSelection(ctx context.Context, tx pgx.Tx) ([]domain.BoxPreset, error) {
	return s.boxPresets.ListByMaxWeightAsc(ctx, tx)
}

// GetBoxPreset returns a box preset by ID.
func (s *FulfillmentService) GetBoxPreset(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.BoxPreset, error) {
	return s.boxPresets.GetByID(ctx, tx, id)
}

// BoxPresetInput is the validated form data for create/update.
type BoxPresetInput struct {
	Name        string
	LengthIn    float64
	WidthIn     float64
	HeightIn    float64
	MaxWeightOz float64
	SortOrder   int
}

func (in BoxPresetInput) validate() error {
	if in.Name == "" {
		return ErrBoxPresetNameRequired
	}
	if in.LengthIn <= 0 || in.WidthIn <= 0 || in.HeightIn <= 0 {
		return ErrBoxPresetDimensionsInvalid
	}
	if in.MaxWeightOz <= 0 {
		return ErrBoxPresetMaxWeightInvalid
	}
	return nil
}

// CreateBoxPreset inserts a new preset and records an audit entry.
func (s *FulfillmentService) CreateBoxPreset(ctx context.Context, tx pgx.Tx, in BoxPresetInput, actor Actor) (*domain.BoxPreset, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	preset, err := s.boxPresets.Create(ctx, tx, store.CreateBoxPresetParams{
		Name:        in.Name,
		LengthIn:    in.LengthIn,
		WidthIn:     in.WidthIn,
		HeightIn:    in.HeightIn,
		MaxWeightOz: in.MaxWeightOz,
		SortOrder:   in.SortOrder,
	})
	if err != nil {
		return nil, err
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditBoxPresetCreated,
		ResourceType: "box_preset",
		ResourceID:   preset.ID,
		After:        preset,
	}); err != nil {
		return nil, fmt.Errorf("audit box preset created: %w", err)
	}
	return preset, nil
}

// UpdateBoxPreset persists changes to a preset and records an audit entry.
func (s *FulfillmentService) UpdateBoxPreset(ctx context.Context, tx pgx.Tx, id uuid.UUID, in BoxPresetInput, actor Actor) (*domain.BoxPreset, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	after, err := s.boxPresets.Update(ctx, tx, store.UpdateBoxPresetParams{
		ID:          id,
		Name:        in.Name,
		LengthIn:    in.LengthIn,
		WidthIn:     in.WidthIn,
		HeightIn:    in.HeightIn,
		MaxWeightOz: in.MaxWeightOz,
		SortOrder:   in.SortOrder,
	})
	if err != nil {
		return nil, err
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditBoxPresetUpdated,
		ResourceType: "box_preset",
		ResourceID:   id,
		After:        after,
	}); err != nil {
		return nil, fmt.Errorf("audit box preset updated: %w", err)
	}
	return after, nil
}

// DeleteBoxPreset removes a preset and records an audit entry.
func (s *FulfillmentService) DeleteBoxPreset(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	before, err := s.boxPresets.GetByID(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := s.boxPresets.Delete(ctx, tx, id); err != nil {
		return err
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditBoxPresetDeleted,
		ResourceType: "box_preset",
		ResourceID:   id,
		After:        before,
	}); err != nil {
		return fmt.Errorf("audit box preset deleted: %w", err)
	}
	return nil
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
