package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// AttributeStore provides database access for attribute sets, keys, and product attribute values.
type AttributeStore struct{}

// NewAttributeStore creates a new AttributeStore.
func NewAttributeStore() *AttributeStore {
	return &AttributeStore{}
}

// --- Attribute Sets ---

// CreateAttributeSetParams holds the fields needed to create an attribute set.
type CreateAttributeSetParams struct {
	Name     string
	Slug     string
	Position int
}

// CreateAttributeSet inserts a new attribute set and returns it.
func (s *AttributeStore) CreateAttributeSet(ctx context.Context, tx pgx.Tx, p CreateAttributeSetParams) (*domain.AttributeSet, error) {
	row, err := sqlcgen.New(tx).CreateAttributeSet(ctx, sqlcgen.CreateAttributeSetParams{
		ID:       uuid.New(),
		Name:     p.Name,
		Slug:     p.Slug,
		Position: int32(p.Position),
	})
	if err != nil {
		return nil, fmt.Errorf("insert attribute set: %w", err)
	}
	return attributeSetFromRow(row), nil
}

// GetAttributeSetByID returns an attribute set by ID.
func (s *AttributeStore) GetAttributeSetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.AttributeSet, error) {
	row, err := sqlcgen.New(tx).GetAttributeSetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get attribute set %s: %w", id, err)
	}
	return attributeSetFromRow(row), nil
}

// ListAttributeSets returns all attribute sets ordered by position.
func (s *AttributeStore) ListAttributeSets(ctx context.Context, tx pgx.Tx) ([]domain.AttributeSet, error) {
	rows, err := sqlcgen.New(tx).ListAttributeSets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list attribute sets: %w", err)
	}
	sets := make([]domain.AttributeSet, len(rows))
	for i, r := range rows {
		sets[i] = *attributeSetFromRow(r)
	}
	return sets, nil
}

// UpdateAttributeSetParams holds the fields to update an attribute set.
type UpdateAttributeSetParams struct {
	ID       uuid.UUID
	Name     string
	Slug     string
	Position int
}

// UpdateAttributeSet updates an attribute set and returns it.
func (s *AttributeStore) UpdateAttributeSet(ctx context.Context, tx pgx.Tx, p UpdateAttributeSetParams) (*domain.AttributeSet, error) {
	row, err := sqlcgen.New(tx).UpdateAttributeSet(ctx, sqlcgen.UpdateAttributeSetParams{
		ID:       p.ID,
		Name:     p.Name,
		Slug:     p.Slug,
		Position: int32(p.Position),
	})
	if err != nil {
		return nil, fmt.Errorf("update attribute set: %w", err)
	}
	return attributeSetFromRow(row), nil
}

// DeleteAttributeSet removes an attribute set by ID.
func (s *AttributeStore) DeleteAttributeSet(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteAttributeSet(ctx, id); err != nil {
		return fmt.Errorf("delete attribute set: %w", err)
	}
	return nil
}

// --- Attribute Keys ---

// CreateAttributeKeyParams holds the fields needed to create an attribute key.
type CreateAttributeKeyParams struct {
	AttributeSetID uuid.UUID
	Name           string
	Slug           string
	ValueType      domain.AttributeValueType
	Position       int
	Filterable     bool
	Sortable       bool
}

// CreateAttributeKey inserts a new attribute key and returns it.
func (s *AttributeStore) CreateAttributeKey(ctx context.Context, tx pgx.Tx, p CreateAttributeKeyParams) (*domain.AttributeKey, error) {
	row, err := sqlcgen.New(tx).CreateAttributeKey(ctx, sqlcgen.CreateAttributeKeyParams{
		ID:             uuid.New(),
		AttributeSetID: p.AttributeSetID,
		Name:           p.Name,
		Slug:           p.Slug,
		ValueType:      sqlcgen.AttributeValueType(p.ValueType),
		Position:       int32(p.Position),
		Filterable:     p.Filterable,
		Sortable:       p.Sortable,
	})
	if err != nil {
		return nil, fmt.Errorf("insert attribute key: %w", err)
	}
	return attributeKeyFromRow(row), nil
}

// GetAttributeKeyByID returns an attribute key by ID.
func (s *AttributeStore) GetAttributeKeyByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.AttributeKey, error) {
	row, err := sqlcgen.New(tx).GetAttributeKeyByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get attribute key %s: %w", id, err)
	}
	return attributeKeyFromRow(row), nil
}

// ListAttributeKeysBySet returns all keys for an attribute set.
func (s *AttributeStore) ListAttributeKeysBySet(ctx context.Context, tx pgx.Tx, setID uuid.UUID) ([]domain.AttributeKey, error) {
	rows, err := sqlcgen.New(tx).ListAttributeKeysBySet(ctx, setID)
	if err != nil {
		return nil, fmt.Errorf("list attribute keys: %w", err)
	}
	keys := make([]domain.AttributeKey, len(rows))
	for i, r := range rows {
		keys[i] = *attributeKeyFromRow(r)
	}
	return keys, nil
}

// UpdateAttributeKeyParams holds the fields to update an attribute key.
type UpdateAttributeKeyParams struct {
	ID         uuid.UUID
	Name       string
	Slug       string
	ValueType  domain.AttributeValueType
	Position   int
	Filterable bool
	Sortable   bool
}

// UpdateAttributeKey updates an attribute key and returns it.
func (s *AttributeStore) UpdateAttributeKey(ctx context.Context, tx pgx.Tx, p UpdateAttributeKeyParams) (*domain.AttributeKey, error) {
	row, err := sqlcgen.New(tx).UpdateAttributeKey(ctx, sqlcgen.UpdateAttributeKeyParams{
		ID:         p.ID,
		Name:       p.Name,
		Slug:       p.Slug,
		ValueType:  sqlcgen.AttributeValueType(p.ValueType),
		Position:   int32(p.Position),
		Filterable: p.Filterable,
		Sortable:   p.Sortable,
	})
	if err != nil {
		return nil, fmt.Errorf("update attribute key: %w", err)
	}
	return attributeKeyFromRow(row), nil
}

// DeleteAttributeKey removes an attribute key by ID.
func (s *AttributeStore) DeleteAttributeKey(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteAttributeKey(ctx, id); err != nil {
		return fmt.Errorf("delete attribute key: %w", err)
	}
	return nil
}

// --- Product Attribute Sets ---

// AssignAttributeSetToProduct assigns an attribute set to a product.
func (s *AttributeStore) AssignAttributeSetToProduct(ctx context.Context, tx pgx.Tx, productID, setID uuid.UUID) error {
	if err := sqlcgen.New(tx).AssignAttributeSetToProduct(ctx, sqlcgen.AssignAttributeSetToProductParams{
		ProductID:      productID,
		AttributeSetID: setID,
	}); err != nil {
		return fmt.Errorf("assign attribute set to product: %w", err)
	}
	return nil
}

// RemoveAttributeSetFromProduct removes an attribute set assignment from a product.
func (s *AttributeStore) RemoveAttributeSetFromProduct(ctx context.Context, tx pgx.Tx, productID, setID uuid.UUID) error {
	if err := sqlcgen.New(tx).RemoveAttributeSetFromProduct(ctx, sqlcgen.RemoveAttributeSetFromProductParams{
		ProductID:      productID,
		AttributeSetID: setID,
	}); err != nil {
		return fmt.Errorf("remove attribute set from product: %w", err)
	}
	return nil
}

// ListProductAttributeSets returns the attribute sets assigned to a product.
func (s *AttributeStore) ListProductAttributeSets(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]domain.AttributeSet, error) {
	rows, err := sqlcgen.New(tx).ListProductAttributeSets(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("list product attribute sets: %w", err)
	}
	sets := make([]domain.AttributeSet, len(rows))
	for i, r := range rows {
		sets[i] = *attributeSetFromRow(r)
	}
	return sets, nil
}

// --- Product Attribute Values ---

// UpsertProductAttributeValue inserts or updates a product's value for an attribute key.
func (s *AttributeStore) UpsertProductAttributeValue(ctx context.Context, tx pgx.Tx, productID, keyID uuid.UUID, value *string, values []string) error {
	var jsonValues []byte
	if len(values) > 0 {
		var err error
		jsonValues, err = json.Marshal(values)
		if err != nil {
			return fmt.Errorf("marshal values: %w", err)
		}
	}
	if err := sqlcgen.New(tx).UpsertProductAttributeValue(ctx, sqlcgen.UpsertProductAttributeValueParams{
		ID:             uuid.New(),
		ProductID:      productID,
		AttributeKeyID: keyID,
		Value:          value,
		Values:         jsonValues,
	}); err != nil {
		return fmt.Errorf("upsert product attribute value: %w", err)
	}
	return nil
}

// DeleteProductAttributeValuesByProduct removes all attribute values for a product.
func (s *AttributeStore) DeleteProductAttributeValuesByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteProductAttributeValuesByProduct(ctx, productID); err != nil {
		return fmt.Errorf("delete product attribute values: %w", err)
	}
	return nil
}

// ListProductAttributeValues returns all attribute values for a product, joined with key info.
// Hand-written query because it joins across tables.
func (s *AttributeStore) ListProductAttributeValues(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]domain.ProductAttributeValue, error) {
	query := `SELECT
		ak.id        AS key_id,
		ak.name      AS key_name,
		ak.slug      AS key_slug,
		ak.value_type,
		pav.value,
		pav.values
	FROM product_attribute_values pav
	JOIN attribute_keys ak   ON ak.id = pav.attribute_key_id
	JOIN attribute_sets aset ON aset.id = ak.attribute_set_id
	WHERE pav.product_id = $1
	ORDER BY aset.position, ak.position`

	rows, err := tx.Query(ctx, query, productID)
	if err != nil {
		return nil, fmt.Errorf("list product attribute values: %w", err)
	}
	defer rows.Close()

	var result []domain.ProductAttributeValue
	for rows.Next() {
		var v domain.ProductAttributeValue
		var valueType string
		var jsonValues []byte
		if err := rows.Scan(&v.KeyID, &v.KeyName, &v.KeySlug, &valueType, &v.Value, &jsonValues); err != nil {
			return nil, fmt.Errorf("scan product attribute value: %w", err)
		}
		v.ValueType = domain.AttributeValueType(valueType)
		if len(jsonValues) > 0 {
			if err := json.Unmarshal(jsonValues, &v.Values); err != nil {
				return nil, fmt.Errorf("unmarshal values: %w", err)
			}
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// AttributeValueInput holds a single or multi value for saving.
type AttributeValueInput struct {
	Value  *string  // for single-value keys
	Values []string // for multi-value keys
}

// --- Row converters ---

func attributeSetFromRow(r sqlcgen.AttributeSet) *domain.AttributeSet {
	return &domain.AttributeSet{
		ID:        r.ID,
		Name:      r.Name,
		Slug:      r.Slug,
		Position:  int(r.Position),
		CreatedAt: r.CreatedAt,
	}
}

func attributeKeyFromRow(r sqlcgen.AttributeKey) *domain.AttributeKey {
	return &domain.AttributeKey{
		ID:             r.ID,
		AttributeSetID: r.AttributeSetID,
		Name:           r.Name,
		Slug:           r.Slug,
		ValueType:      domain.AttributeValueType(r.ValueType),
		Position:       int(r.Position),
		Filterable:     r.Filterable,
		Sortable:       r.Sortable,
	}
}
