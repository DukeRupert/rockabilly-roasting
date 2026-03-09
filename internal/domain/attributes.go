package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// AttributeValueType controls storage shape and UI rendering.
type AttributeValueType string

const (
	AttributeValueTypeSingle AttributeValueType = "single"
	AttributeValueTypeMulti  AttributeValueType = "multi"
)

// AttributeSet is a named group of attribute keys (e.g. "Coffee Profile").
type AttributeSet struct {
	ID        uuid.UUID
	Name      string
	Slug      string
	Position  int
	CreatedAt time.Time
	Keys      []AttributeKey // populated on full fetch
}

// AttributeKey is an individual attribute within a set (e.g. "Tasting Notes").
type AttributeKey struct {
	ID             uuid.UUID
	AttributeSetID uuid.UUID
	Name           string
	Slug           string
	ValueType      AttributeValueType
	Position       int
	Filterable     bool
	Sortable       bool
}

// ProductAttributeValue holds a product's value for a single attribute key.
type ProductAttributeValue struct {
	KeyID     uuid.UUID
	KeyName   string
	KeySlug   string
	ValueType AttributeValueType
	Value     *string  // single
	Values    []string // multi
}

// DisplayValue returns a display-ready string for use in templates.
func (v ProductAttributeValue) DisplayValue() string {
	if len(v.Values) > 0 {
		return strings.Join(v.Values, ", ")
	}
	if v.Value != nil {
		return *v.Value
	}
	return ""
}
