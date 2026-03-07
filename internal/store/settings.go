package store

import (
	"context"
	"fmt"

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

func ptrToStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
