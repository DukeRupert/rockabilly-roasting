package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// ShippingStore provides database access for shipping config and shipments.
type ShippingStore struct{}

// NewShippingStore creates a new ShippingStore.
func NewShippingStore() *ShippingStore {
	return &ShippingStore{}
}

// --- Shipping Config ---

// GetConfig returns the shipping configuration.
func (s *ShippingStore) GetConfig(ctx context.Context, tx pgx.Tx) (*domain.ShippingConfig, error) {
	row, err := sqlcgen.New(tx).GetShippingConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("get shipping config: %w", err)
	}
	cfg := &domain.ShippingConfig{
		FlatRateCents:              int(row.FlatRateCents),
		FreeShippingThreshold:      int32PtrToIntPtr(row.FreeShippingThreshold),
		Currency:                   row.Currency,
		LocalZipCodes:              row.LocalZipCodes,
		LocalDeliveryEnabled:       row.LocalDeliveryEnabled,
		LocalPickupEnabled:         row.LocalPickupEnabled,
		LocalPickupInstructions:    row.LocalPickupInstructions,
		LocalDeliveryWeekdays:      weekdaysFromPG(row.LocalDeliveryWeekdays),
		LocalDeliveryCutoffMinutes: int(row.LocalDeliveryCutoffMinutes),
		OriginName:                 row.OriginName,
		OriginStreet1:              row.OriginStreet1,
		OriginStreet2:              row.OriginStreet2,
		OriginCity:                 row.OriginCity,
		OriginState:                row.OriginState,
		OriginZip:                  row.OriginZip,
		OriginCountry:              row.OriginCountry,
		OriginEmail:                row.OriginEmail,
		OriginPhone:                row.OriginPhone,
		TareWeightOz:               numericToFloat64(row.TareWeightOz),
	}

	// Postponements ride along with the config rather than being fetched
	// separately by callers. NextDeliveryDate cannot give a correct answer
	// without them, and every caller of it — checkout, the wholesale portal,
	// route building, the dashboard strip, order placement — goes through this
	// one loader. Attaching them here is what stops a future caller from
	// quoting a holiday because it forgot a second query.
	postponements, err := s.ListDeliveryPostponements(ctx, tx)
	if err != nil {
		return nil, err
	}
	cfg.DeliveryPostponements = postponements

	return cfg, nil
}

// --- Delivery postponements ---

// ListDeliveryPostponements returns every recorded postponement, soonest first.
//
// Deliberately unfiltered by date: the set is tiny (a working year has a
// handful of observed holidays), and NextDeliveryDate scans backwards as well
// as forwards, so filtering to "future only" here would hide the run that moved
// out of yesterday and into today.
func (s *ShippingStore) ListDeliveryPostponements(ctx context.Context, tx pgx.Tx) ([]domain.DeliveryPostponement, error) {
	rows, err := tx.Query(ctx,
		`SELECT original_date, moved_to_date, note
		   FROM delivery_postponements
		  ORDER BY original_date`)
	if err != nil {
		return nil, fmt.Errorf("list delivery postponements: %w", err)
	}
	defer rows.Close()

	var out []domain.DeliveryPostponement
	for rows.Next() {
		var p domain.DeliveryPostponement
		if err := rows.Scan(&p.OriginalDate, &p.MovedTo, &p.Note); err != nil {
			return nil, fmt.Errorf("scan delivery postponement: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate delivery postponements: %w", err)
	}
	return out, nil
}

// UpsertDeliveryPostponement records that the run scheduled for originalDate
// happens on movedTo instead, replacing any postponement already recorded for
// that day.
//
// Upsert rather than insert because marking the same holiday twice is a
// correction, not a conflict — staff who typed the wrong Tuesday should be able
// to fix it by saying it again.
func (s *ShippingStore) UpsertDeliveryPostponement(ctx context.Context, tx pgx.Tx, originalDate, movedTo time.Time, note string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO delivery_postponements (original_date, moved_to_date, note)
		 VALUES ($1::date, $2::date, $3)
		 ON CONFLICT (original_date) DO UPDATE
		    SET moved_to_date = EXCLUDED.moved_to_date,
		        note          = EXCLUDED.note`,
		originalDate, movedTo, note,
	)
	if err != nil {
		return fmt.Errorf("upsert delivery postponement: %w", err)
	}
	return nil
}

// DeleteDeliveryPostponement removes a postponement, putting the run back on
// its scheduled day.
func (s *ShippingStore) DeleteDeliveryPostponement(ctx context.Context, tx pgx.Tx, originalDate time.Time) error {
	_, err := tx.Exec(ctx,
		`DELETE FROM delivery_postponements WHERE original_date = $1::date`,
		originalDate,
	)
	if err != nil {
		return fmt.Errorf("delete delivery postponement: %w", err)
	}
	return nil
}

// RescheduleDeliveryRun moves every order riding the run scheduled for runDate
// onto the day that run now goes out, and reports how many moved.
//
// This is what keeps the fulfillment queue and the load list honest after a run
// is postponed: those read the stored scheduled_delivery_date, not the schedule
// rule, so without it the van's own paperwork would still say Monday. Customers
// are not emailed — the confirmation they already have names the old date, and
// the shop announces a moved holiday through the Announcements composer if it
// wants to say more.
//
// Selection is by delivery_run_date, never by the promised date. A promised
// date stops identifying a run the moment two runs can share one: postpone a
// Monday onto a Thursday and its orders sit alongside Thursday's own, after
// which "everything promised Thursday" is the wrong set — restoring the Monday
// would drag Thursday's orders onto a day they never rode. The run column says
// which orders belong to which run, whatever date they currently show.
//
// Narrowed to orders the van has still to carry, and deliberately to the same
// set the load list counts (see dashboard counts in orders.go) — postponement
// exists to keep the load list honest, so moving a different set than the one
// it shows would be the bug wearing a different hat.
//
// That exclusion is what makes the day-granular postpone guard safe. The guard
// asks whether the run has gone, judged by date, so at six in the evening staff
// can still move today's run; without this filter that would rewrite the
// promised date on orders the driver delivered this morning, claiming a
// delivery on a day it did not happen. Delivered orders now stay where they
// are. Cancelled and refunded orders stay put for the same reason — nothing is
// riding for them.
//
// They keep their delivery_run_date, so they remain attached to the run for the
// record; they simply stop being re-dated by it.
func (s *ShippingStore) RescheduleDeliveryRun(ctx context.Context, tx pgx.Tx, runDate, to time.Time) (int64, error) {
	tag, err := tx.Exec(ctx,
		`UPDATE orders
		    SET scheduled_delivery_date = $2::date,
		        updated_at = now()
		  WHERE delivery_run_date = $1::date
		    AND shipping_method = 'local_delivery'
		    AND status NOT IN ('cancelled', 'refunded')
		    AND fulfillment_status IN ('unfulfilled', 'partially_fulfilled',
		                               'fulfilled', 'ready_for_pickup')`,
		runDate, to,
	)
	if err != nil {
		return 0, fmt.Errorf("reschedule delivery run: %w", err)
	}
	return tag.RowsAffected(), nil
}

// UpdateConfig updates the shipping configuration.
func (s *ShippingStore) UpdateConfig(ctx context.Context, tx pgx.Tx, cfg domain.ShippingConfig) error {
	zips := cfg.LocalZipCodes
	if zips == nil {
		zips = []string{}
	}
	err := sqlcgen.New(tx).UpdateShippingConfig(ctx, sqlcgen.UpdateShippingConfigParams{
		FlatRateCents:           int32(cfg.FlatRateCents),
		FreeShippingThreshold:   intPtrToInt32Ptr(cfg.FreeShippingThreshold),
		Currency:                cfg.Currency,
		LocalZipCodes:           zips,
		OriginName:              cfg.OriginName,
		OriginStreet1:           cfg.OriginStreet1,
		OriginStreet2:           cfg.OriginStreet2,
		OriginCity:              cfg.OriginCity,
		OriginState:             cfg.OriginState,
		OriginZip:               cfg.OriginZip,
		OriginCountry:           cfg.OriginCountry,
		OriginEmail:             cfg.OriginEmail,
		OriginPhone:             cfg.OriginPhone,
		TareWeightOz:            float64ToNumeric(cfg.TareWeightOz),
		LocalDeliveryEnabled:    cfg.LocalDeliveryEnabled,
		LocalPickupEnabled:      cfg.LocalPickupEnabled,
		LocalPickupInstructions: cfg.LocalPickupInstructions,
		// local_delivery_days is legacy free text superseded by
		// local_delivery_weekdays; the label is derived now. Written back as-is
		// so the column keeps whatever the merchant last typed until it is
		// dropped.
		LocalDeliveryDays:          cfg.DeliveryDaysLabel(),
		LocalDeliveryWeekdays:      weekdaysToPG(cfg.LocalDeliveryWeekdays),
		LocalDeliveryCutoffMinutes: int32(cfg.LocalDeliveryCutoffMinutes),
	})
	if err != nil {
		return fmt.Errorf("update shipping config: %w", err)
	}
	return nil
}

// --- Shipments ---

// CreateShipmentParams holds the fields needed to create a shipment.
// LabelURL and the box dimensions are pointer-typed because not every
// shipment source supplies them — only carrier label purchases do.
type CreateShipmentParams struct {
	OrderID        uuid.UUID
	Status         domain.ShipmentStatus
	Provider       string
	TrackingNumber string
	LabelURL       *string
	CarrierName    string
	ServiceName    string
	LabelCostCents int
	LabelCurrency  string
	WeightOz       float64
	LengthIn       *float64
	WidthIn        *float64
	HeightIn       *float64
	ShippedAt      *time.Time
	CreatedBy      uuid.UUID
	// ProviderTransactionID is the carrier's transaction handle, kept so the
	// label can later be refunded. Nil for sources that don't supply one.
	ProviderTransactionID *string
}

// CreateShipment inserts a new shipment and returns it.
func (s *ShippingStore) CreateShipment(ctx context.Context, tx pgx.Tx, p CreateShipmentParams) (*domain.Shipment, error) {
	row, err := sqlcgen.New(tx).CreateShipment(ctx, sqlcgen.CreateShipmentParams{
		ID:             uuid.New(),
		OrderID:        p.OrderID,
		Status:         string(p.Status),
		Provider:       p.Provider,
		TrackingNumber: p.TrackingNumber,
		LabelUrl:       p.LabelURL,
		CarrierName:    p.CarrierName,
		ServiceName:    p.ServiceName,
		LabelCostCents: int32(p.LabelCostCents),
		LabelCurrency:  p.LabelCurrency,
		WeightOz:       float64ToNumeric(p.WeightOz),
		LengthIn:       float64PtrToNumeric(p.LengthIn),
		WidthIn:        float64PtrToNumeric(p.WidthIn),
		HeightIn:       float64PtrToNumeric(p.HeightIn),
		ShippedAt:      timestampToPG(p.ShippedAt),
		CreatedBy:      p.CreatedBy,

		ProviderTransactionID: p.ProviderTransactionID,
	})
	if err != nil {
		return nil, fmt.Errorf("insert shipment: %w", err)
	}
	return shipmentFromRow(row), nil
}

// GetShipmentByIDAsStaff returns a shipment by ID.
func (s *ShippingStore) GetShipmentByIDAsStaff(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Shipment, error) {
	row, err := sqlcgen.New(tx).GetShipmentByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get shipment %s: %w", id, err)
	}
	return shipmentFromRow(row), nil
}

// GetShipmentByTrackingNumber returns the most recent shipment with the given
// tracking number. Used to match an inbound Shippo tracking webhook to a
// shipment. Returns pgx.ErrNoRows if no shipment carries that number.
func (s *ShippingStore) GetShipmentByTrackingNumber(ctx context.Context, tx pgx.Tx, trackingNumber string) (*domain.Shipment, error) {
	row, err := sqlcgen.New(tx).GetShipmentByTrackingNumber(ctx, trackingNumber)
	if err != nil {
		return nil, fmt.Errorf("get shipment by tracking number: %w", err)
	}
	return shipmentFromRow(row), nil
}

// ListShipmentsByOrder returns all shipments for an order.
func (s *ShippingStore) ListShipmentsByOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.Shipment, error) {
	rows, err := sqlcgen.New(tx).ListShipmentsByOrder(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list shipments: %w", err)
	}
	shipments := make([]domain.Shipment, len(rows))
	for i, r := range rows {
		shipments[i] = *shipmentFromRow(r)
	}
	return shipments, nil
}

// UpdateShipmentStatus updates a shipment's status and returns it.
func (s *ShippingStore) UpdateShipmentStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.ShipmentStatus) (*domain.Shipment, error) {
	row, err := sqlcgen.New(tx).UpdateShipmentStatus(ctx, sqlcgen.UpdateShipmentStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("update shipment status: %w", err)
	}
	return shipmentFromRow(row), nil
}

// UpdateShipmentTracking updates tracking info and returns the shipment.
func (s *ShippingStore) UpdateShipmentTracking(ctx context.Context, tx pgx.Tx, id uuid.UUID, trackingNumber, labelURL string) (*domain.Shipment, error) {
	row, err := sqlcgen.New(tx).UpdateShipmentTracking(ctx, sqlcgen.UpdateShipmentTrackingParams{
		ID:             id,
		TrackingNumber: trackingNumber,
		LabelUrl:       &labelURL,
	})
	if err != nil {
		return nil, fmt.Errorf("update shipment tracking: %w", err)
	}
	return shipmentFromRow(row), nil
}

// UpdateShipmentDelivered marks a shipment as delivered.
func (s *ShippingStore) UpdateShipmentDelivered(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).UpdateShipmentDelivered(ctx, id); err != nil {
		return fmt.Errorf("mark shipment delivered: %w", err)
	}
	return nil
}

// UpdateShipmentLabel sets the R2 key and format for a shipment's label.
func (s *ShippingStore) UpdateShipmentLabel(ctx context.Context, tx pgx.Tx, id uuid.UUID, r2Key, format string) error {
	err := sqlcgen.New(tx).UpdateShipmentLabel(ctx, sqlcgen.UpdateShipmentLabelParams{
		ID:          id,
		LabelR2Key:  &r2Key,
		LabelFormat: &format,
	})
	if err != nil {
		return fmt.Errorf("update shipment label: %w", err)
	}
	return nil
}

// GetShipmentLabelKey returns the R2 object key for a shipment's label.
func (s *ShippingStore) GetShipmentLabelKey(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*string, error) {
	key, err := sqlcgen.New(tx).GetShipmentLabelKey(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get shipment label key: %w", err)
	}
	return key, nil
}

// UpdateShipmentRefundRequested marks a shipment as having a refund in flight,
// recording the carrier's refund handle and who requested it, and returns it.
func (s *ShippingStore) UpdateShipmentRefundRequested(ctx context.Context, tx pgx.Tx, id uuid.UUID, refundID string, requestedBy *uuid.UUID) (*domain.Shipment, error) {
	row, err := sqlcgen.New(tx).UpdateShipmentRefundRequested(ctx, sqlcgen.UpdateShipmentRefundRequestedParams{
		ID:                id,
		RefundID:          &refundID,
		RefundRequestedBy: requestedBy,
	})
	if err != nil {
		return nil, fmt.Errorf("update shipment refund requested: %w", err)
	}
	return shipmentFromRow(row), nil
}

// UpdateShipmentRefundResolved settles a requested refund to its terminal
// status ('refunded' or 'failed'). The update only touches a shipment still in
// 'requested', so a replayed poll job is a safe no-op: in that case no row
// matches and ok is false (the caller treats it as already-resolved).
func (s *ShippingStore) UpdateShipmentRefundResolved(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.RefundStatus) (*domain.Shipment, bool, error) {
	row, err := sqlcgen.New(tx).UpdateShipmentRefundResolved(ctx, sqlcgen.UpdateShipmentRefundResolvedParams{
		ID:           id,
		RefundStatus: string(status),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("update shipment refund resolved: %w", err)
	}
	return shipmentFromRow(row), true, nil
}

// --- Label attempts ---

// GetLatestLabelAttempt returns the most recent non-successful BuyLabel job
// for an order, or nil if none is in flight or recently failed. A successful
// attempt is communicated via the shipment row, not here — so completed
// states are filtered out.
//
// This reads River's river_job table directly. The columns referenced (kind,
// args, state, attempt, max_attempts, errors) are stable across River
// versions; the table layout is part of River's documented contract.
func (s *ShippingStore) GetLatestLabelAttempt(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (*domain.LabelAttempt, error) {
	// The last attempt's message is extracted in SQL. river_job.errors is
	// jsonb[] — a Postgres array of jsonb, not a jsonb array — and scanning it
	// into a []byte fails outright ("cannot unmarshal object into Go value of
	// type uint8"), which took the whole order page down for any order whose
	// label purchase had recorded an error.
	const query = `
		SELECT id, state, attempt, max_attempts,
		       COALESCE(errors[array_upper(errors, 1)]->>'error', '')
		FROM river_job
		WHERE kind = 'buy_label'
		  AND (args->>'order_id') = $1
		  AND state <> 'completed'
		ORDER BY id DESC
		LIMIT 1`

	var (
		id          int64
		state       string
		attempt     int
		maxAttempts int
		lastError   string
	)
	err := tx.QueryRow(ctx, query, orderID.String()).Scan(&id, &state, &attempt, &maxAttempts, &lastError)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get latest label attempt: %w", err)
	}

	out := &domain.LabelAttempt{
		JobID:       id,
		Attempt:     attempt,
		MaxAttempts: maxAttempts,
		Status:      labelAttemptStatusFromRiverState(state),
	}
	if out.Status == domain.LabelAttemptStatusFailed {
		out.LastError = lastError
	}
	return out, nil
}

// ListOrdersWithFailedLabelAttempts returns the subset of orderIDs whose
// latest BuyLabel job ended in a terminal failure state (cancelled or
// discarded). Used by the order list to flag rows that need operator
// attention. Orders with a queued/running attempt are NOT included — those
// resolve on their own.
func (s *ShippingStore) ListOrdersWithFailedLabelAttempts(ctx context.Context, tx pgx.Tx, orderIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool)
	if len(orderIDs) == 0 {
		return out, nil
	}

	idStrs := make([]string, len(orderIDs))
	for i, id := range orderIDs {
		idStrs[i] = id.String()
	}

	const query = `
		SELECT DISTINCT ON ((args->>'order_id'))
		       (args->>'order_id') AS order_id,
		       state
		FROM river_job
		WHERE kind = 'buy_label'
		  AND (args->>'order_id') = ANY($1::text[])
		ORDER BY (args->>'order_id'), id DESC`

	rows, err := tx.Query(ctx, query, idStrs)
	if err != nil {
		return nil, fmt.Errorf("list orders with failed label attempts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var orderIDStr, state string
		if err := rows.Scan(&orderIDStr, &state); err != nil {
			return nil, fmt.Errorf("scan label attempt row: %w", err)
		}
		if state != "cancelled" && state != "discarded" {
			continue
		}
		id, parseErr := uuid.Parse(orderIDStr)
		if parseErr != nil {
			continue
		}
		out[id] = true
	}
	return out, rows.Err()
}

// failedLabelOrdersCTE is the shared body behind CountFailedLabelOrdersByChannel
// and ListFailedLabelOrders. It finds every order whose most recent buy_label
// job ended in a terminal failure (cancelled or discarded) and that still needs
// a label: the order is live, still in the pack-and-ship queue, and nothing has
// since produced a shipment for it.
//
// A queued or running attempt is deliberately not a match — those resolve on
// their own. A later successful retry isn't either, since it becomes the
// newest job for that order and its state is 'completed'. The shipments guard
// covers the remaining case: staff bought the label by hand after the job gave
// up, so the failure is already dealt with.
//
// The status and fulfillment_status predicates mirror the fulfillment list's
// "needs action" bucket exactly (see CountFulfillmentViews and
// applyFulfillmentViewFilter), because the dashboard group that reports this
// count links straight into that queue. An order counted here that the
// destination page filters out is the same class of bug as a count that
// overstates: staff click through and can't find what they were sent for.
const failedLabelOrdersCTE = `
	WITH latest_attempt AS (
		SELECT DISTINCT ON ((args->>'order_id'))
		       (args->>'order_id') AS order_id,
		       state
		FROM river_job
		WHERE kind = 'buy_label'
		ORDER BY (args->>'order_id'), id DESC
	)
	SELECT %s
	FROM orders o
	JOIN latest_attempt la ON la.order_id = o.id::text
	WHERE la.state IN ('cancelled', 'discarded')
	  AND o.status NOT IN ('cancelled', 'refunded')
	  AND NOT (o.status = 'pending' AND o.payment_status = 'awaiting')
	  AND o.fulfillment_status IN ('unfulfilled', 'partially_fulfilled', 'fulfilled', 'ready_for_pickup')
	  AND NOT EXISTS (SELECT 1 FROM shipments sh WHERE sh.order_id = o.id)`

// CountFailedLabelOrdersByChannel returns how many orders are stuck without a
// shipping label because their buy_label job gave up, split by sales channel.
//
// The split is not cosmetic. Retail and wholesale have separate fulfillment
// queues by design, so a single combined number would link to a page showing
// only part of it. Channels with nothing stuck are absent from the map rather
// than present with a zero.
func (s *ShippingStore) CountFailedLabelOrdersByChannel(ctx context.Context, tx pgx.Tx) (_ map[domain.OrderChannel]int, err error) {
	query := fmt.Sprintf(failedLabelOrdersCTE, "o.channel, COUNT(*)::int") + " GROUP BY o.channel"

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("count failed label orders by channel: %w", err)
	}
	defer rows.Close()

	out := make(map[domain.OrderChannel]int)
	for rows.Next() {
		var channel string
		var count int
		if err := rows.Scan(&channel, &count); err != nil {
			return nil, fmt.Errorf("scan failed label channel count: %w", err)
		}
		out[domain.OrderChannel(channel)] = count
	}
	return out, rows.Err()
}

// ListFailedLabelOrders returns the oldest `limit` orders of one channel stuck
// without a label. Oldest first: a label failure that nobody noticed is the one
// worth showing, and the newest ones are the likeliest to still be retrying.
func (s *ShippingStore) ListFailedLabelOrders(ctx context.Context, tx pgx.Tx, channel domain.OrderChannel, limit int) (_ []uuid.UUID, err error) {
	if limit <= 0 {
		return nil, nil
	}
	query := fmt.Sprintf(failedLabelOrdersCTE, "o.id") + " AND o.channel = $1 ORDER BY o.placed_at ASC LIMIT $2"

	rows, err := tx.Query(ctx, query, string(channel), limit)
	if err != nil {
		return nil, fmt.Errorf("list failed label orders: %w", err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan failed label order: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// labelAttemptStatusFromRiverState collapses River's seven job states into
// the two we surface in the UI. "cancelled" + "discarded" are terminal
// failures; everything else (available, scheduled, running, retryable,
// pending) is in-flight. "completed" is filtered out at the query level.
func labelAttemptStatusFromRiverState(state string) domain.LabelAttemptStatus {
	switch state {
	case "cancelled", "discarded":
		return domain.LabelAttemptStatusFailed
	default:
		return domain.LabelAttemptStatusQueued
	}
}

// --- Row converters ---

func shipmentFromRow(r sqlcgen.Shipment) *domain.Shipment {
	return &domain.Shipment{
		ID:             r.ID,
		OrderID:        r.OrderID,
		Status:         domain.ShipmentStatus(r.Status),
		Provider:       r.Provider,
		TrackingNumber: r.TrackingNumber,
		LabelURL:       r.LabelUrl,
		CarrierName:    r.CarrierName,
		ServiceName:    r.ServiceName,
		LabelCostCents: int(r.LabelCostCents),
		LabelCurrency:  r.LabelCurrency,
		WeightOz:       numericToFloat64(r.WeightOz),
		LengthIn:       numericToFloat64Ptr(r.LengthIn),
		WidthIn:        numericToFloat64Ptr(r.WidthIn),
		HeightIn:       numericToFloat64Ptr(r.HeightIn),
		CreatedBy:      r.CreatedBy,
		CreatedAt:      r.CreatedAt,
		LabelCreatedAt: timestampFromPG(r.LabelCreatedAt),
		ShippedAt:      timestampFromPG(r.ShippedAt),
		DeliveredAt:    timestampFromPG(r.DeliveredAt),
		LabelR2Key:     r.LabelR2Key,
		LabelFormat:    r.LabelFormat,

		ProviderTransactionID: r.ProviderTransactionID,
		RefundStatus:          domain.RefundStatus(r.RefundStatus),
		RefundID:              r.RefundID,
		RefundRequestedAt:     timestampFromPG(r.RefundRequestedAt),
		RefundRequestedBy:     r.RefundRequestedBy,
		RefundedAt:            timestampFromPG(r.RefundedAt),
	}
}
