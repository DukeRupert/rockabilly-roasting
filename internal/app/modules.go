package app

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
)

// ModuleService answers "is this optional section of the app switched on", and
// owns the toggle.
//
// The answer is needed on every admin request (the sidebar), on every route in
// a module's section, and inside job workers — so it is cached in memory rather
// than read per request. That is safe *because Hiri runs as a single process*:
// the HTTP server and the River workers are one binary, so the toggle and every
// reader share this one struct. If the fleet ever runs two replicas of an
// instance, delete the cache and read the row per request — it is one indexed
// lookup on a table with a handful of rows, and a stale replica would otherwise
// serve a section the merchant just turned off.
type ModuleService struct {
	modules *store.ModuleStore
	audit   *audit.AuditWriter
	// enabled holds a domain.ModuleSet. Zero value (never refreshed, or a
	// failed refresh) reports every module disabled, which hides a section
	// rather than exposing a half-wired one.
	enabled atomic.Value
}

// NewModuleService creates a new ModuleService. Call Refresh once at startup —
// until then every module reads as disabled.
func NewModuleService(modules *store.ModuleStore, auditWriter *audit.AuditWriter) *ModuleService {
	return &ModuleService{modules: modules, audit: auditWriter}
}

// Refresh reloads the enabled set from the database into the cache. Called once
// at boot and again after every toggle, outside the transaction that made the
// change — the same rule metrics follow, so nothing is published from a
// transaction that may still roll back.
func (s *ModuleService) Refresh(ctx context.Context, tx pgx.Tx) error {
	set, err := s.modules.EnabledSet(ctx, tx)
	if err != nil {
		return fmt.Errorf("refresh modules: %w", err)
	}
	s.enabled.Store(set)
	return nil
}

// Enabled reports whether a module is switched on. Cheap enough to call in
// middleware and in every job worker — it touches no database.
func (s *ModuleService) Enabled(m domain.Module) bool {
	return s.Set().Enabled(m)
}

// Set returns a copy of the enabled set, for handing to a request context.
// A copy, not the cached map, so a caller cannot mutate what every other
// request is reading.
func (s *ModuleService) Set() domain.ModuleSet {
	cached, _ := s.enabled.Load().(domain.ModuleSet)
	out := make(domain.ModuleSet, len(cached))
	for k, v := range cached {
		out[k] = v
	}
	return out
}

// List returns the stored state of every known module for the Settings screen.
func (s *ModuleService) List(ctx context.Context, tx pgx.Tx) ([]domain.ModuleState, error) {
	states, err := s.modules.List(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("list modules: %w", err)
	}
	return states, nil
}

// SetEnabled switches a module on or off and records who did it.
//
// It deliberately does not touch the cache: the caller refreshes after the
// transaction commits. Returns the registry entry so the caller can name the
// module in a flash message without looking it up again.
func (s *ModuleService) SetEnabled(ctx context.Context, tx pgx.Tx, key string, enabled bool, actor Actor) (domain.ModuleInfo, error) {
	info, ok := domain.LookupModule(key)
	if !ok {
		return domain.ModuleInfo{}, fmt.Errorf("%w: %s", ErrUnknownModule, key)
	}

	if err := s.modules.SetEnabled(ctx, tx, info.Key, enabled, actor.ID); err != nil {
		return domain.ModuleInfo{}, err
	}

	action := audit.AuditModuleDisabled
	if enabled {
		action = audit.AuditModuleEnabled
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       action,
		ResourceType: "module",
		// Modules are keyed by name, not by id — the same singleton shape as
		// the checkout and pricing settings records.
		ResourceID: uuid.Nil,
		Metadata: map[string]any{
			"module": string(info.Key),
			"name":   info.Name,
		},
	}); err != nil {
		return domain.ModuleInfo{}, fmt.Errorf("audit module toggle: %w", err)
	}

	return info, nil
}
