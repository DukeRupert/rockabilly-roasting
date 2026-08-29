package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// SettingsStore provides database access for store-level settings.
type SettingsStore struct{}

// NewSettingsStore creates a new SettingsStore.
func NewSettingsStore() *SettingsStore {
	return &SettingsStore{}
}

// GetTaxConfig returns the store's tax configuration.
func (s *SettingsStore) GetTaxConfig(ctx context.Context, tx pgx.Tx) (*domain.TaxConfig, error) {
	row, err := sqlcgen.New(tx).GetStoreSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("get store settings: %w", err)
	}
	return &domain.TaxConfig{
		Mode:  domain.TaxMode(row.TaxMode),
		Rate:  numericToFloat64(row.TaxRate),
		Label: ptrToStr(row.TaxLabel),
	}, nil
}

// UpdateTaxConfigParams holds the fields to update the tax configuration.
type UpdateTaxConfigParams struct {
	Mode  domain.TaxMode
	Rate  float64
	Label string
}

// UpdateTaxConfig updates the store's tax configuration.
func (s *SettingsStore) UpdateTaxConfig(ctx context.Context, tx pgx.Tx, p UpdateTaxConfigParams) (*domain.TaxConfig, error) {
	var labelPtr *string
	if p.Label != "" {
		labelPtr = &p.Label
	}
	row, err := sqlcgen.New(tx).UpdateTaxConfig(ctx, sqlcgen.UpdateTaxConfigParams{
		TaxMode:  string(p.Mode),
		TaxRate:  float64ToNumeric(p.Rate),
		TaxLabel: labelPtr,
	})
	if err != nil {
		return nil, fmt.Errorf("update tax config: %w", err)
	}
	return &domain.TaxConfig{
		Mode:  domain.TaxMode(row.TaxMode),
		Rate:  numericToFloat64(row.TaxRate),
		Label: ptrToStr(row.TaxLabel),
	}, nil
}

// GetDefaultWholesalePriceListID returns the store-wide default price list for
// wholesale accounts, or nil if none is configured (callers fall back to base
// prices).
func (s *SettingsStore) GetDefaultWholesalePriceListID(ctx context.Context, tx pgx.Tx) (*uuid.UUID, error) {
	row, err := sqlcgen.New(tx).GetStoreSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("get store settings: %w", err)
	}
	return row.DefaultWholesalePriceListID, nil
}

// SetDefaultWholesalePriceListID sets (or clears, when id is nil) the store-wide
// default wholesale price list.
func (s *SettingsStore) SetDefaultWholesalePriceListID(ctx context.Context, tx pgx.Tx, id *uuid.UUID) error {
	if _, err := sqlcgen.New(tx).UpdateDefaultWholesalePriceList(ctx, id); err != nil {
		return fmt.Errorf("update default wholesale price list: %w", err)
	}
	return nil
}

func ptrToStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// GetQBBillingMode returns whether the QuickBooks chain may create and send
// real invoices. A value this binary does not recognise reads as shadow: the
// column decides whether real customers get billed, and the safe direction on
// an unknown value is not to bill.
func (s *SettingsStore) GetQBBillingMode(ctx context.Context, tx pgx.Tx) (domain.QBBillingMode, error) {
	var raw string
	if err := tx.QueryRow(ctx, `SELECT qb_billing_mode FROM store_settings LIMIT 1`).Scan(&raw); err != nil {
		return domain.DefaultQBBillingMode, fmt.Errorf("get qb billing mode: %w", err)
	}
	mode := domain.QBBillingMode(raw)
	if !mode.Valid() {
		return domain.DefaultQBBillingMode, nil
	}
	return mode, nil
}

// UpdateQBBillingMode sets the QuickBooks billing mode. Callers are
// responsible for rejecting an invalid mode before reaching here; the CHECK
// constraint is the backstop.
func (s *SettingsStore) UpdateQBBillingMode(ctx context.Context, tx pgx.Tx, mode domain.QBBillingMode) error {
	if _, err := tx.Exec(ctx,
		`UPDATE store_settings SET qb_billing_mode = $1, updated_at = now() WHERE id = true`, string(mode),
	); err != nil {
		return fmt.Errorf("update qb billing mode: %w", err)
	}
	return nil
}
