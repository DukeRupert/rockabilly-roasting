package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// BoxPresetStore provides database access for box presets.
type BoxPresetStore struct{}

// NewBoxPresetStore creates a new BoxPresetStore.
func NewBoxPresetStore() *BoxPresetStore {
	return &BoxPresetStore{}
}

// List returns all box presets in display order (sort_order, then capacity).
func (s *BoxPresetStore) List(ctx context.Context, tx pgx.Tx) ([]domain.BoxPreset, error) {
	rows, err := sqlcgen.New(tx).ListBoxPresets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list box presets: %w", err)
	}
	out := make([]domain.BoxPreset, len(rows))
	for i, r := range rows {
		out[i] = boxPresetFromRow(r)
	}
	return out, nil
}

// ListByMaxWeightAsc returns presets sorted by max_weight_oz ascending —
// the order needed for SelectBoxForWeight.
func (s *BoxPresetStore) ListByMaxWeightAsc(ctx context.Context, tx pgx.Tx) ([]domain.BoxPreset, error) {
	rows, err := sqlcgen.New(tx).ListBoxPresetsByMaxWeightAsc(ctx)
	if err != nil {
		return nil, fmt.Errorf("list box presets by weight: %w", err)
	}
	out := make([]domain.BoxPreset, len(rows))
	for i, r := range rows {
		out[i] = boxPresetFromRow(r)
	}
	return out, nil
}

// GetByID returns a box preset by ID.
func (s *BoxPresetStore) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.BoxPreset, error) {
	row, err := sqlcgen.New(tx).GetBoxPresetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get box preset %s: %w", id, err)
	}
	out := boxPresetFromRow(row)
	return &out, nil
}

// CreateBoxPresetParams holds the fields needed to create a preset.
type CreateBoxPresetParams struct {
	Name        string
	LengthIn    float64
	WidthIn     float64
	HeightIn    float64
	MaxWeightOz float64
	SortOrder   int
}

// Create inserts a new box preset and returns it.
func (s *BoxPresetStore) Create(ctx context.Context, tx pgx.Tx, p CreateBoxPresetParams) (*domain.BoxPreset, error) {
	row, err := sqlcgen.New(tx).CreateBoxPreset(ctx, sqlcgen.CreateBoxPresetParams{
		ID:          uuid.New(),
		Name:        p.Name,
		LengthIn:    float64ToNumeric(p.LengthIn),
		WidthIn:     float64ToNumeric(p.WidthIn),
		HeightIn:    float64ToNumeric(p.HeightIn),
		MaxWeightOz: float64ToNumeric(p.MaxWeightOz),
		SortOrder:   int32(p.SortOrder),
	})
	if err != nil {
		return nil, fmt.Errorf("insert box preset: %w", err)
	}
	out := boxPresetFromRow(row)
	return &out, nil
}

// UpdateBoxPresetParams holds the fields needed to update a preset.
type UpdateBoxPresetParams struct {
	ID          uuid.UUID
	Name        string
	LengthIn    float64
	WidthIn     float64
	HeightIn    float64
	MaxWeightOz float64
	SortOrder   int
}

// Update updates a box preset and returns it.
func (s *BoxPresetStore) Update(ctx context.Context, tx pgx.Tx, p UpdateBoxPresetParams) (*domain.BoxPreset, error) {
	row, err := sqlcgen.New(tx).UpdateBoxPreset(ctx, sqlcgen.UpdateBoxPresetParams{
		ID:          p.ID,
		Name:        p.Name,
		LengthIn:    float64ToNumeric(p.LengthIn),
		WidthIn:     float64ToNumeric(p.WidthIn),
		HeightIn:    float64ToNumeric(p.HeightIn),
		MaxWeightOz: float64ToNumeric(p.MaxWeightOz),
		SortOrder:   int32(p.SortOrder),
	})
	if err != nil {
		return nil, fmt.Errorf("update box preset %s: %w", p.ID, err)
	}
	out := boxPresetFromRow(row)
	return &out, nil
}

// Delete removes a box preset.
func (s *BoxPresetStore) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteBoxPreset(ctx, id); err != nil {
		return fmt.Errorf("delete box preset %s: %w", id, err)
	}
	return nil
}

func boxPresetFromRow(r sqlcgen.BoxPreset) domain.BoxPreset {
	return domain.BoxPreset{
		ID:          r.ID,
		Name:        r.Name,
		LengthIn:    numericToFloat64(r.LengthIn),
		WidthIn:     numericToFloat64(r.WidthIn),
		HeightIn:    numericToFloat64(r.HeightIn),
		MaxWeightOz: numericToFloat64(r.MaxWeightOz),
		SortOrder:   int(r.SortOrder),
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}
