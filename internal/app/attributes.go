package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// AttributeService contains business logic for product attributes.
type AttributeService struct {
	attributes *store.AttributeStore
	audit      *audit.AuditWriter
	metrics    *metrics.Registry
}

// NewAttributeService creates a new AttributeService.
func NewAttributeService(attributes *store.AttributeStore, audit *audit.AuditWriter, metrics *metrics.Registry) *AttributeService {
	return &AttributeService{
		attributes: attributes,
		audit:      audit,
		metrics:    metrics,
	}
}

// --- Attribute Sets ---

// CreateAttributeSet creates a new attribute set and records an audit entry.
func (s *AttributeService) CreateAttributeSet(ctx context.Context, tx pgx.Tx, p store.CreateAttributeSetParams, actor Actor) (*domain.AttributeSet, error) {
	set, err := s.attributes.CreateAttributeSet(ctx, tx, p)
	if err != nil {
		return nil, fmt.Errorf("create attribute set: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditAttributeSetCreated,
		ResourceType: "attribute_set",
		ResourceID:   set.ID,
		After:        set,
	}); err != nil {
		return nil, fmt.Errorf("audit attribute set created: %w", err)
	}

	return set, nil
}

// GetAttributeSet returns an attribute set by ID.
func (s *AttributeService) GetAttributeSet(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.AttributeSet, error) {
	set, err := s.attributes.GetAttributeSetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAttributeSetNotFound
		}
		return nil, fmt.Errorf("get attribute set: %w", err)
	}
	return set, nil
}

// ListAttributeSets returns all attribute sets.
func (s *AttributeService) ListAttributeSets(ctx context.Context, tx pgx.Tx) ([]domain.AttributeSet, error) {
	sets, err := s.attributes.ListAttributeSets(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("list attribute sets: %w", err)
	}
	return sets, nil
}

// UpdateAttributeSet updates an attribute set and records an audit entry.
func (s *AttributeService) UpdateAttributeSet(ctx context.Context, tx pgx.Tx, p store.UpdateAttributeSetParams, actor Actor) (*domain.AttributeSet, error) {
	set, err := s.attributes.UpdateAttributeSet(ctx, tx, p)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAttributeSetNotFound
		}
		return nil, fmt.Errorf("update attribute set: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditAttributeSetUpdated,
		ResourceType: "attribute_set",
		ResourceID:   set.ID,
		After:        set,
	}); err != nil {
		return nil, fmt.Errorf("audit attribute set updated: %w", err)
	}

	return set, nil
}

// DeleteAttributeSet removes an attribute set and records an audit entry.
func (s *AttributeService) DeleteAttributeSet(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	if err := s.attributes.DeleteAttributeSet(ctx, tx, id); err != nil {
		return fmt.Errorf("delete attribute set: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditAttributeSetDeleted,
		ResourceType: "attribute_set",
		ResourceID:   id,
	}); err != nil {
		return fmt.Errorf("audit attribute set deleted: %w", err)
	}

	return nil
}

// --- Attribute Keys ---

// CreateAttributeKey creates a new attribute key within a set and records an audit entry.
func (s *AttributeService) CreateAttributeKey(ctx context.Context, tx pgx.Tx, p store.CreateAttributeKeyParams, actor Actor) (*domain.AttributeKey, error) {
	if err := validateAllowedValues(p.ValueType, p.AllowedValues); err != nil {
		return nil, err
	}
	key, err := s.attributes.CreateAttributeKey(ctx, tx, p)
	if err != nil {
		return nil, fmt.Errorf("create attribute key: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditAttributeKeyCreated,
		ResourceType: "attribute_key",
		ResourceID:   key.ID,
		After:        key,
	}); err != nil {
		return nil, fmt.Errorf("audit attribute key created: %w", err)
	}

	return key, nil
}

// GetAttributeKey returns an attribute key by ID.
func (s *AttributeService) GetAttributeKey(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.AttributeKey, error) {
	key, err := s.attributes.GetAttributeKeyByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAttributeKeyNotFound
		}
		return nil, fmt.Errorf("get attribute key: %w", err)
	}
	return key, nil
}

// ListAttributeKeys returns all keys for an attribute set.
func (s *AttributeService) ListAttributeKeys(ctx context.Context, tx pgx.Tx, setID uuid.UUID) ([]domain.AttributeKey, error) {
	keys, err := s.attributes.ListAttributeKeysBySet(ctx, tx, setID)
	if err != nil {
		return nil, fmt.Errorf("list attribute keys: %w", err)
	}
	return keys, nil
}

// UpdateAttributeKey updates an attribute key and records an audit entry.
func (s *AttributeService) UpdateAttributeKey(ctx context.Context, tx pgx.Tx, p store.UpdateAttributeKeyParams, actor Actor) (*domain.AttributeKey, error) {
	if err := validateAllowedValues(p.ValueType, p.AllowedValues); err != nil {
		return nil, err
	}
	key, err := s.attributes.UpdateAttributeKey(ctx, tx, p)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAttributeKeyNotFound
		}
		return nil, fmt.Errorf("update attribute key: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditAttributeKeyUpdated,
		ResourceType: "attribute_key",
		ResourceID:   key.ID,
		After:        key,
	}); err != nil {
		return nil, fmt.Errorf("audit attribute key updated: %w", err)
	}

	return key, nil
}

// DeleteAttributeKey removes an attribute key and records an audit entry.
func (s *AttributeService) DeleteAttributeKey(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	if err := s.attributes.DeleteAttributeKey(ctx, tx, id); err != nil {
		return fmt.Errorf("delete attribute key: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditAttributeKeyDeleted,
		ResourceType: "attribute_key",
		ResourceID:   id,
	}); err != nil {
		return fmt.Errorf("audit attribute key deleted: %w", err)
	}

	return nil
}

// --- Product Attribute Sets ---

// AssignAttributeSetToProduct assigns an attribute set to a product.
func (s *AttributeService) AssignAttributeSetToProduct(ctx context.Context, tx pgx.Tx, productID, setID uuid.UUID) error {
	if err := s.attributes.AssignAttributeSetToProduct(ctx, tx, productID, setID); err != nil {
		return fmt.Errorf("assign attribute set: %w", err)
	}
	return nil
}

// RemoveAttributeSetFromProduct removes an attribute set assignment from a product.
func (s *AttributeService) RemoveAttributeSetFromProduct(ctx context.Context, tx pgx.Tx, productID, setID uuid.UUID) error {
	if err := s.attributes.RemoveAttributeSetFromProduct(ctx, tx, productID, setID); err != nil {
		return fmt.Errorf("remove attribute set: %w", err)
	}
	return nil
}

// ListProductAttributeSets returns the attribute sets assigned to a product.
func (s *AttributeService) ListProductAttributeSets(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]domain.AttributeSet, error) {
	sets, err := s.attributes.ListProductAttributeSets(ctx, tx, productID)
	if err != nil {
		return nil, fmt.Errorf("list product attribute sets: %w", err)
	}
	return sets, nil
}

// --- Product Attribute Values ---

// SaveProductAttributes saves all attribute values for a product (bulk upsert) and records an audit entry.
func (s *AttributeService) SaveProductAttributes(ctx context.Context, tx pgx.Tx, productID uuid.UUID, values map[uuid.UUID]store.AttributeValueInput, actor Actor) error {
	// Validate values against their keys
	for keyID, input := range values {
		key, err := s.attributes.GetAttributeKeyByID(ctx, tx, keyID)
		if err != nil {
			return fmt.Errorf("get attribute key for validation: %w", err)
		}
		if err := validateAttributeValue(key, input); err != nil {
			return err
		}
	}

	// Delete existing values and re-insert to handle removals
	if err := s.attributes.DeleteProductAttributeValuesByProduct(ctx, tx, productID); err != nil {
		return fmt.Errorf("clear product attributes: %w", err)
	}

	for keyID, input := range values {
		if err := s.attributes.UpsertProductAttributeValue(ctx, tx, productID, keyID, input.Value, input.Values); err != nil {
			return fmt.Errorf("upsert attribute value: %w", err)
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditProductAttributesUpdated,
		ResourceType: "product",
		ResourceID:   productID,
		Metadata:     map[string]any{"key_count": len(values)},
	}); err != nil {
		return fmt.Errorf("audit product attributes updated: %w", err)
	}

	return nil
}

// ListProductAttributeValues returns all attribute values for a product with key info.
func (s *AttributeService) ListProductAttributeValues(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]domain.ProductAttributeValue, error) {
	vals, err := s.attributes.ListProductAttributeValues(ctx, tx, productID)
	if err != nil {
		return nil, fmt.Errorf("list product attribute values: %w", err)
	}
	return vals, nil
}

// validateAllowedValues ensures enum types have allowed values and other types do not.
func validateAllowedValues(vt domain.AttributeValueType, allowed []string) error {
	isEnum := vt == domain.AttributeValueTypeEnum || vt == domain.AttributeValueTypeMultiEnum
	if isEnum && len(allowed) == 0 {
		return ErrAttributeAllowedValuesRequired
	}
	return nil
}

// validateAttributeValue checks that a value input is valid for the given key.
func validateAttributeValue(key *domain.AttributeKey, input store.AttributeValueInput) error {
	switch key.ValueType {
	case domain.AttributeValueTypeBoolean:
		if input.Value != nil && *input.Value != "true" && *input.Value != "false" {
			return fmt.Errorf("%w: boolean must be true or false", ErrAttributeValueNotAllowed)
		}
	case domain.AttributeValueTypeEnum:
		if input.Value != nil && len(key.AllowedValues) > 0 {
			if !slices.Contains(key.AllowedValues, *input.Value) {
				return fmt.Errorf("%w: %q is not allowed for %s", ErrAttributeValueNotAllowed, *input.Value, key.Name)
			}
		}
	case domain.AttributeValueTypeMultiEnum:
		if len(key.AllowedValues) > 0 {
			for _, v := range input.Values {
				if !slices.Contains(key.AllowedValues, v) {
					return fmt.Errorf("%w: %q is not allowed for %s", ErrAttributeValueNotAllowed, v, key.Name)
				}
			}
		}
	}
	return nil
}
