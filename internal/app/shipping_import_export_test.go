package app

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/platform/pirateship"
)

// Test-only exports for shipping_import.go. Build tag is the file's `_test.go`
// suffix — these symbols are only compiled into the test binary, so the
// production API stays clean.

// RecordPirateShipTrackingInTxForTest invokes the per-row import logic
// against the caller's transaction. Tests use this to drive the path that
// the public RecordPirateShipTracking would otherwise wrap in its own
// pool-acquired transaction (which would commit outside the test's
// rollback-on-cleanup tx).
func (s *ShippingImportService) RecordPirateShipTrackingInTxForTest(
	ctx context.Context,
	tx pgx.Tx,
	row pirateship.TrackingRow,
	actor Actor,
	res *ImportResult,
) error {
	return s.recordPirateShipTrackingInTx(ctx, tx, row, actor, res)
}

// ErrImportSkipForTest is the errSkip sentinel exposed for assertions.
var ErrImportSkipForTest = errSkip

// PreflightSkipReasonForTest exposes the preflight skip helper.
var PreflightSkipReasonForTest = preflightSkipReason

// CanImportTrackingForForTest exposes the fulfillment-status guard.
var CanImportTrackingForForTest = canImportTrackingFor
