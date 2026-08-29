package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
)

// EquipmentStore persists the machines a shop maintains for its customers.
// Part of the equipment service module; see docs/equipment-service-module.md.
type EquipmentStore struct{}

// NewEquipmentStore creates a new EquipmentStore.
func NewEquipmentStore() *EquipmentStore { return &EquipmentStore{} }

const equipmentColumns = `id, customer_id, address_id, category, make, model, serial_number,
	                  ownership, status, installed_on, warranty_expires_on, notes,
	                  created_at, updated_at`

func scanEquipment(row rowScanner) (*domain.Equipment, error) {
	var e domain.Equipment
	var category, ownership, status string
	if err := row.Scan(
		&e.ID, &e.CustomerID, &e.AddressID, &category, &e.Make, &e.Model, &e.SerialNumber,
		&ownership, &status, &e.InstalledOn, &e.WarrantyExpiresOn, &e.Notes,
		&e.CreatedAt, &e.UpdatedAt,
	); err != nil {
		return nil, err
	}
	e.Category = domain.EquipmentCategory(category)
	e.Ownership = domain.EquipmentOwnership(ownership)
	e.Status = domain.EquipmentStatus(status)
	return &e, nil
}

// CreateEquipmentParams is the input for registering a machine.
type CreateEquipmentParams struct {
	CustomerID        uuid.UUID
	AddressID         *uuid.UUID
	Category          domain.EquipmentCategory
	Make              string
	Model             string
	SerialNumber      string
	Ownership         domain.EquipmentOwnership
	InstalledOn       *time.Time
	WarrantyExpiresOn *time.Time
	Notes             string
}

// Create registers a machine. It always starts active — a machine is added
// because it is in service, and the two other statuses are things that happen
// to it later.
func (s *EquipmentStore) Create(ctx context.Context, tx pgx.Tx, p CreateEquipmentParams) (*domain.Equipment, error) {
	row := tx.QueryRow(ctx,
		`INSERT INTO equipment (customer_id, address_id, category, make, model, serial_number,
		                        ownership, installed_on, warranty_expires_on, notes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING `+equipmentColumns,
		p.CustomerID, p.AddressID, string(p.Category), p.Make, p.Model, p.SerialNumber,
		string(p.Ownership), p.InstalledOn, p.WarrantyExpiresOn, p.Notes)

	e, err := scanEquipment(row)
	if err != nil {
		return nil, fmt.Errorf("create equipment: %w", err)
	}
	return e, nil
}

// GetByID returns one machine, unscoped. Staff only.
// Returns pgx.ErrNoRows when it does not exist.
func (s *EquipmentStore) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Equipment, error) {
	row := tx.QueryRow(ctx, `SELECT `+equipmentColumns+` FROM equipment WHERE id = $1`, id)
	e, err := scanEquipment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, err // mapped to a sentinel by the caller
		}
		return nil, fmt.Errorf("get equipment %s: %w", id, err)
	}
	return e, nil
}

// Get returns one machine belonging to a customer.
//
// customerID is required, not optional: it is how the wholesale portal's
// ownership check is enforced at the type level, so a handler cannot forget it.
func (s *EquipmentStore) Get(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) (*domain.Equipment, error) {
	row := tx.QueryRow(ctx,
		`SELECT `+equipmentColumns+` FROM equipment WHERE id = $1 AND customer_id = $2`, id, customerID)
	e, err := scanEquipment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("get equipment %s for customer %s: %w", id, customerID, err)
	}
	return e, nil
}

// EquipmentFilter narrows the admin register. Zero value lists every machine
// that is not retired.
type EquipmentFilter struct {
	CustomerID *uuid.UUID
	Category   domain.EquipmentCategory
	Ownership  domain.EquipmentOwnership
	Status     domain.EquipmentStatus
	// IncludeRetired brings back the machines that have been taken out of
	// service. Off by default: the register is a list of what is out there now.
	IncludeRetired bool
	// Search matches make, model or serial number, case-insensitively. Serial
	// is the one people actually paste in.
	Search string
	Limit  int
}

// equipmentWhere builds the filter's WHERE clause and its arguments.
//
// Shared by List and ListWithCustomer so the two cannot drift — a register that
// hid retired machines in one view and showed them in the other would be a
// genuinely confusing bug, and the only thing keeping them honest is that they
// are the same code. prefix qualifies the columns for the joined variant.
func equipmentWhere(f EquipmentFilter, prefix string) (string, []any) {
	var where []string
	var args []any

	add := func(clause string, arg any) {
		args = append(args, arg)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	if f.CustomerID != nil {
		add(prefix+"customer_id = $%d", *f.CustomerID)
	}
	if f.Category != "" {
		add(prefix+"category = $%d", string(f.Category))
	}
	if f.Ownership != "" {
		add(prefix+"ownership = $%d", string(f.Ownership))
	}
	switch {
	case f.Status != "":
		add(prefix+"status = $%d", string(f.Status))
	case !f.IncludeRetired:
		where = append(where, prefix+"status <> 'retired'")
	}
	if f.Search != "" {
		add("("+prefix+"make ILIKE '%%' || $%d || '%%' OR "+prefix+"model ILIKE '%%' || $%[1]d || '%%' OR "+prefix+"serial_number ILIKE '%%' || $%[1]d || '%%')", f.Search)
	}

	if len(where) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(where, " AND "), args
}

// List returns machines matching the filter, newest first.
func (s *EquipmentStore) List(ctx context.Context, tx pgx.Tx, f EquipmentFilter) ([]domain.Equipment, error) {
	whereSQL, args := equipmentWhere(f, "")

	query := `SELECT ` + equipmentColumns + ` FROM equipment` + whereSQL + ` ORDER BY created_at DESC`
	if f.Limit > 0 {
		args = append(args, f.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list equipment: %w", err)
	}
	defer rows.Close()

	var out []domain.Equipment
	for rows.Next() {
		e, err := scanEquipment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan equipment: %w", err)
		}
		out = append(out, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list equipment: %w", err)
	}
	return out, nil
}

// UpdateEquipmentParams is the editable half of a machine. Status is not here:
// it moves through UpdateStatus, which is the action staff actually take.
type UpdateEquipmentParams struct {
	AddressID         *uuid.UUID
	Category          domain.EquipmentCategory
	Make              string
	Model             string
	SerialNumber      string
	Ownership         domain.EquipmentOwnership
	InstalledOn       *time.Time
	WarrantyExpiresOn *time.Time
	Notes             string
}

// Update rewrites a machine's details.
func (s *EquipmentStore) Update(ctx context.Context, tx pgx.Tx, id uuid.UUID, p UpdateEquipmentParams) (*domain.Equipment, error) {
	row := tx.QueryRow(ctx,
		`UPDATE equipment
		 SET address_id = $2, category = $3, make = $4, model = $5, serial_number = $6,
		     ownership = $7, installed_on = $8, warranty_expires_on = $9, notes = $10,
		     updated_at = now()
		 WHERE id = $1
		 RETURNING `+equipmentColumns,
		id, p.AddressID, string(p.Category), p.Make, p.Model, p.SerialNumber,
		string(p.Ownership), p.InstalledOn, p.WarrantyExpiresOn, p.Notes)

	e, err := scanEquipment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("update equipment %s: %w", id, err)
	}
	return e, nil
}

// UpdateStatus moves a machine between in service, in the shop, and retired.
func (s *EquipmentStore) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.EquipmentStatus) (*domain.Equipment, error) {
	row := tx.QueryRow(ctx,
		`UPDATE equipment SET status = $2, updated_at = now() WHERE id = $1
		 RETURNING `+equipmentColumns,
		id, string(status))

	e, err := scanEquipment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("update equipment %s status: %w", id, err)
	}
	return e, nil
}

// ListWithCustomer returns machines with their owner's display name attached,
// for the register — the one view that spans customers.
func (s *EquipmentStore) ListWithCustomer(ctx context.Context, tx pgx.Tx, f EquipmentFilter) ([]domain.EquipmentWithCustomer, error) {
	whereSQL, args := equipmentWhere(f, "e.")

	// Company where there is one, otherwise the person's name — a cafe is known
	// by the name on the sign. NULLIF catches the empty-string company that a
	// retail signup leaves behind, which COALESCE alone would happily return.
	query := `SELECT e.id, e.customer_id, e.address_id, e.category, e.make, e.model,
	                 e.serial_number, e.ownership, e.status, e.installed_on,
	                 e.warranty_expires_on, e.notes, e.created_at, e.updated_at,
	                 COALESCE(NULLIF(c.company_name, ''), TRIM(c.first_name || ' ' || c.last_name), c.email)
	          FROM equipment e
	          JOIN customers c ON c.id = e.customer_id` + whereSQL + ` ORDER BY e.created_at DESC`
	if f.Limit > 0 {
		args = append(args, f.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list equipment with customer: %w", err)
	}
	defer rows.Close()

	var out []domain.EquipmentWithCustomer
	for rows.Next() {
		var row domain.EquipmentWithCustomer
		var category, ownership, status string
		if err := rows.Scan(
			&row.ID, &row.CustomerID, &row.AddressID, &category, &row.Make, &row.Model,
			&row.SerialNumber, &ownership, &status, &row.InstalledOn,
			&row.WarrantyExpiresOn, &row.Notes, &row.CreatedAt, &row.UpdatedAt,
			&row.CustomerName,
		); err != nil {
			return nil, fmt.Errorf("scan equipment with customer: %w", err)
		}
		row.Category = domain.EquipmentCategory(category)
		row.Ownership = domain.EquipmentOwnership(ownership)
		row.Status = domain.EquipmentStatus(status)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list equipment with customer: %w", err)
	}
	return out, nil
}
