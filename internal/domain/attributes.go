package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// AttributeValueType controls storage shape and UI rendering.
type AttributeValueType string

const (
	AttributeValueTypeText      AttributeValueType = "text"
	AttributeValueTypeEnum      AttributeValueType = "enum"
	AttributeValueTypeMultiText AttributeValueType = "multi_text"
	AttributeValueTypeMultiEnum AttributeValueType = "multi_enum"
	AttributeValueTypeBoolean   AttributeValueType = "boolean"
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
	AllowedValues  []string // predefined choices for enum/multi_enum types
}

// IsMultiType returns true for value types that store multiple values.
func (k AttributeKey) IsMultiType() bool {
	return k.ValueType == AttributeValueTypeMultiText || k.ValueType == AttributeValueTypeMultiEnum
}

// IsEnumType returns true for value types that constrain values to AllowedValues.
func (k AttributeKey) IsEnumType() bool {
	return k.ValueType == AttributeValueTypeEnum || k.ValueType == AttributeValueTypeMultiEnum
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
	if v.ValueType == AttributeValueTypeBoolean {
		if v.BoolValue() {
			return "Yes"
		}
		return "No"
	}
	if len(v.Values) > 0 {
		return strings.Join(v.Values, ", ")
	}
	if v.Value != nil {
		return *v.Value
	}
	return ""
}

// BoolValue returns true if the stored value is "true".
func (v ProductAttributeValue) BoolValue() bool {
	return v.Value != nil && *v.Value == "true"
}
