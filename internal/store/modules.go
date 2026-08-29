package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
)

// ModuleStore persists which optional feature modules this instance has
// switched on. One row per module key; see db/migrations/076_modules.sql for
// why the registry of valid keys lives in Go rather than in a CHECK.
type ModuleStore struct{}

// NewModuleStore creates a new ModuleStore.
func NewModuleStore() *ModuleStore { return &ModuleStore{} }

// EnabledSet returns the on/off answer for every module row.
//
// Rows whose key this binary does not recognize are skipped rather than
// returned: the set is consumed by nav rendering and route guards, and a key
// only a newer binary understands has nothing here to guard.
func (s *ModuleStore) EnabledSet(ctx context.Context, tx pgx.Tx) (domain.ModuleSet, error) {
	rows, err := tx.Query(ctx, `SELECT key, enabled FROM modules`)
	if err != nil {
		return nil, fmt.Errorf("list modules: %w", err)
	}
	defer rows.Close()

	set := domain.ModuleSet{}
	for rows.Next() {
		var key string
		var enabled bool
		if err := rows.Scan(&key, &enabled); err != nil {
			return nil, fmt.Errorf("scan module: %w", err)
		}
		if info, ok := domain.LookupModule(key); ok {
			set[info.Key] = enabled
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list modules: %w", err)
	}
	return set, nil
}

// List returns the stored state of every known module, in registry order, for
// the Settings screen.
//
// A known module with no row yet reads as disabled and never-changed, so a key
// added by a deploy shows up correctly before anyone has touched it.
func (s *ModuleStore) List(ctx context.Context, tx pgx.Tx) ([]domain.ModuleState, error) {
	rows, err := tx.Query(ctx,
		`SELECT m.key, m.enabled, m.updated_at, COALESCE(st.name, '')
		 FROM modules m
		 LEFT JOIN staff st ON st.id = m.enabled_by`)
	if err != nil {
		return nil, fmt.Errorf("list module states: %w", err)
	}
	defer rows.Close()

	stored := map[string]domain.ModuleState{}
	for rows.Next() {
		var key string
		var st domain.ModuleState
		if err := rows.Scan(&key, &st.Enabled, &st.ChangedAt, &st.ChangedByName); err != nil {
			return nil, fmt.Errorf("scan module state: %w", err)
		}
		st.Key = domain.Module(key)
		stored[key] = st
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list module states: %w", err)
	}

	registry := domain.ModuleRegistry()
	out := make([]domain.ModuleState, 0, len(registry))
	for _, info := range registry {
		if st, ok := stored[string(info.Key)]; ok {
			out = append(out, st)
			continue
		}
		out = append(out, domain.ModuleState{Key: info.Key})
	}
	return out, nil
}

// SetEnabled switches a module on or off, creating the row if a deploy added
// the key after the migration that seeded the table.
func (s *ModuleStore) SetEnabled(ctx context.Context, tx pgx.Tx, key domain.Module, enabled bool, staffID *uuid.UUID) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO modules (key, enabled, enabled_at, enabled_by, updated_at)
		 VALUES ($1, $2, now(), $3, now())
		 ON CONFLICT (key) DO UPDATE
		 SET enabled = EXCLUDED.enabled,
		     enabled_at = EXCLUDED.enabled_at,
		     enabled_by = EXCLUDED.enabled_by,
		     updated_at = now()`,
		string(key), enabled, staffID)
	if err != nil {
		return fmt.Errorf("set module %s: %w", key, err)
	}
	return nil
}
