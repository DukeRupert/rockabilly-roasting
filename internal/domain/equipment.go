package domain

import (
	"time"

	"github.com/google/uuid"
)

// Equipment — the machines a roaster maintains for its wholesale customers.
// Part of the equipment service module; see docs/equipment-service-module.md.

// EquipmentCategory is what kind of machine this is. The list is deliberately
// short: it exists to filter a register and to label a row, not to be a
// taxonomy of cafe hardware.
type EquipmentCategory string

const (
	EquipmentCategoryEspressoMachine EquipmentCategory = "espresso_machine"
	EquipmentCategoryGrinder         EquipmentCategory = "grinder"
	EquipmentCategoryBrewer          EquipmentCategory = "brewer"
	EquipmentCategoryWater           EquipmentCategory = "water"
	EquipmentCategoryOther           EquipmentCategory = "other"
)

// EquipmentCategories lists the categories in the order forms should offer
// them — commonest first, "other" last.
func EquipmentCategories() []EquipmentCategory {
	return []EquipmentCategory{
		EquipmentCategoryEspressoMachine,
		EquipmentCategoryGrinder,
		EquipmentCategoryBrewer,
		EquipmentCategoryWater,
		EquipmentCategoryOther,
	}
}

// Label is the human name for a category.
func (c EquipmentCategory) Label() string {
	switch c {
	case EquipmentCategoryEspressoMachine:
		return "Espresso machine"
	case EquipmentCategoryGrinder:
		return "Grinder"
	case EquipmentCategoryBrewer:
		return "Brewer"
	case EquipmentCategoryWater:
		return "Water treatment"
	case EquipmentCategoryOther:
		return "Other"
	}
	return string(c)
}

// Valid reports whether c is one of the known categories. The database has the
// same list as a CHECK; this catches a bad value before the round trip.
func (c EquipmentCategory) Valid() bool {
	switch c {
	case EquipmentCategoryEspressoMachine, EquipmentCategoryGrinder,
		EquipmentCategoryBrewer, EquipmentCategoryWater, EquipmentCategoryOther:
		return true
	}
	return false
}

// EquipmentOwnership is who owns the machine. It decides who pays for a repair,
// and a loaner is an asset the roaster needs to be able to list.
type EquipmentOwnership string

const (
	// EquipmentOwnershipCustomer — the cafe bought it; the roaster just services it.
	EquipmentOwnershipCustomer EquipmentOwnership = "customer"
	// EquipmentOwnershipLoaner — the roaster owns it and placed it, usually
	// against a volume commitment. Still the roaster's asset.
	EquipmentOwnershipLoaner EquipmentOwnership = "loaner"
	// EquipmentOwnershipLeased — the cafe leases it from a third party, which
	// means a repair may be somebody else's to make.
	EquipmentOwnershipLeased EquipmentOwnership = "leased"
)

// EquipmentOwnerships lists the ownership options in form order.
func EquipmentOwnerships() []EquipmentOwnership {
	return []EquipmentOwnership{
		EquipmentOwnershipCustomer,
		EquipmentOwnershipLoaner,
		EquipmentOwnershipLeased,
	}
}

// Label is the human name for an ownership.
func (o EquipmentOwnership) Label() string {
	switch o {
	case EquipmentOwnershipCustomer:
		return "Customer owned"
	case EquipmentOwnershipLoaner:
		return "Our loaner"
	case EquipmentOwnershipLeased:
		return "Leased"
	}
	return string(o)
}

// Valid reports whether o is a known ownership.
func (o EquipmentOwnership) Valid() bool {
	switch o {
	case EquipmentOwnershipCustomer, EquipmentOwnershipLoaner, EquipmentOwnershipLeased:
		return true
	}
	return false
}

// EquipmentStatus is where the machine is.
type EquipmentStatus string

const (
	// EquipmentStatusActive — in service at the customer's site.
	EquipmentStatusActive EquipmentStatus = "active"
	// EquipmentStatusInShop — taken away for work. Temporary, and distinct from
	// retired: the machine is coming back.
	EquipmentStatusInShop EquipmentStatus = "in_shop"
	// EquipmentStatusRetired — permanently out of service. Never deleted: the
	// repair history that justified replacing it hangs off this row.
	EquipmentStatusRetired EquipmentStatus = "retired"
)

// Label is the human name for a status.
func (s EquipmentStatus) Label() string {
	switch s {
	case EquipmentStatusActive:
		return "In service"
	case EquipmentStatusInShop:
		return "In the shop"
	case EquipmentStatusRetired:
		return "Retired"
	}
	return string(s)
}

// Valid reports whether s is a known status.
func (s EquipmentStatus) Valid() bool {
	switch s {
	case EquipmentStatusActive, EquipmentStatusInShop, EquipmentStatusRetired:
		return true
	}
	return false
}

// Equipment is one machine on a customer's site.
type Equipment struct {
	ID         uuid.UUID
	CustomerID uuid.UUID
	// AddressID is which of the customer's locations it sits at. Nil when the
	// customer has one site, or when the address it was recorded against has
	// since been removed.
	AddressID         *uuid.UUID
	Category          EquipmentCategory
	Make              string
	Model             string
	SerialNumber      string
	Ownership         EquipmentOwnership
	Status            EquipmentStatus
	InstalledOn       *time.Time
	WarrantyExpiresOn *time.Time
	Notes             string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Description is the machine in one line — "La Marzocco Linea PB", or just the
// make when no model was recorded. Used wherever a ticket has to name the
// machine it is against.
func (e Equipment) Description() string {
	if e.Model == "" {
		return e.Make
	}
	return e.Make + " " + e.Model
}

// UnderWarranty reports whether the machine's warranty still covers it on the
// given day. False when no warranty date was recorded — an unknown warranty is
// not a warranty, and quoting a repair as covered when it is not is worse than
// the reverse.
func (e Equipment) UnderWarranty(on time.Time) bool {
	if e.WarrantyExpiresOn == nil {
		return false
	}
	return !on.After(*e.WarrantyExpiresOn)
}
