package domain

import (
	"context"
	"time"
)

// Optional feature modules.
//
// A module is a whole section of the product that only some merchants want.
// Hiri is single-tenant, so enablement is a property of the instance: turning
// one off removes its routes and its sidebar row entirely rather than hiding
// controls from particular staff. Disabling never destroys data — the tables
// stay, and switching back on restores the section as it was.

// Module is a feature-module key. Values are stable strings: they are stored in
// the modules table and read back by name, so renaming one is a migration.
type Module string

const (
	// ModuleEquipmentService covers the equipment register, service tickets,
	// parts and labour tracking for roasters who maintain their customers'
	// espresso machines. See docs/equipment-service-module.md.
	ModuleEquipmentService Module = "equipment_service"
)

// ModuleInfo is the registry entry for one module: everything the Settings
// screen needs to describe it, and nothing the rest of the app needs to know.
type ModuleInfo struct {
	Key Module
	// Name is the label used in Settings and (where it applies) as the sidebar
	// row this module adds.
	Name string
	// Summary is the one-line "what is this" shown beside the toggle.
	Summary string
	// Detail says what actually changes when it is switched on, in the terms a
	// staff member would notice — a new nav row, a new customer-facing form.
	Detail string
}

// moduleRegistry is the list of modules this binary knows about, in the order
// Settings shows them. A key in the database that is missing here is ignored,
// which is what makes a rolling deploy safe in both directions.
var moduleRegistry = []ModuleInfo{
	{
		Key:     ModuleEquipmentService,
		Name:    "Equipment service",
		Summary: "Track the espresso machines and grinders you maintain for wholesale customers.",
		Detail:  "Adds a Service section to the sidebar — an equipment register, service tickets, parts and hours — and lets wholesale customers report a broken machine from their account. Turning it off hides all of it; nothing is deleted.",
	},
}

// ModuleRegistry returns the known modules in display order.
func ModuleRegistry() []ModuleInfo {
	out := make([]ModuleInfo, len(moduleRegistry))
	copy(out, moduleRegistry)
	return out
}

// LookupModule returns the registry entry for a key, and whether it is one this
// binary knows about. Callers that accept a key from a request must check it —
// an unknown key is a bad request, not a module to create.
func LookupModule(key string) (ModuleInfo, bool) {
	for _, m := range moduleRegistry {
		if string(m.Key) == key {
			return m, true
		}
	}
	return ModuleInfo{}, false
}

// ModuleState is one module's stored state, for the Settings list.
type ModuleState struct {
	Key       Module
	Enabled   bool
	ChangedAt *time.Time
	// ChangedByName is the staff member who last toggled it, blank when the
	// row has never been touched or the staff account has since been removed.
	ChangedByName string
}

// ModuleSet is the enabled/disabled answer for every known module, as carried
// on a request context and cached in the app layer.
//
// The zero value — a nil map — reports everything disabled. That is the safe
// default in both directions: a page rendered outside the middleware (or in a
// test) shows no module nav, and a failed read hides a section rather than
// exposing a half-wired one.
type ModuleSet map[Module]bool

// Enabled reports whether m is switched on.
func (s ModuleSet) Enabled(m Module) bool { return s[m] }

type moduleSetKey struct{}

// WithModules returns a context carrying the instance's enabled modules.
func WithModules(ctx context.Context, s ModuleSet) context.Context {
	return context.WithValue(ctx, moduleSetKey{}, s)
}

// ModulesFrom returns the enabled modules carried by ctx, or an empty set.
//
// This rides in the context rather than in every page's props for the same
// reason AdminBadges does: the sidebar renders on every admin page, and
// threading a module set through thirty prop structs would couple every handler
// to work it has nothing to do with.
func ModulesFrom(ctx context.Context) ModuleSet {
	s, _ := ctx.Value(moduleSetKey{}).(ModuleSet)
	return s
}
