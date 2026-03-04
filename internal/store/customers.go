package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// CustomerStore provides database access for customers and addresses.
type CustomerStore struct{}

// NewCustomerStore creates a new CustomerStore.
func NewCustomerStore() *CustomerStore {
	return &CustomerStore{}
}

// --- Customer CRUD ---

// CreateCustomerParams holds the fields needed to create a customer.
type CreateCustomerParams struct {
	Email        string
	PasswordHash *string
	FirstName    string
	LastName     string
	Phone        *string
	IsGuest      bool
}

// Create inserts a new customer and returns it.
func (s *CustomerStore) Create(ctx context.Context, tx pgx.Tx, p CreateCustomerParams) (*domain.Customer, error) {
	row, err := sqlcgen.New(tx).CreateCustomer(ctx, sqlcgen.CreateCustomerParams{
		ID:           uuid.New(),
		Email:        p.Email,
		PasswordHash: p.PasswordHash,
		FirstName:    p.FirstName,
		LastName:     p.LastName,
		Phone:        p.Phone,
		IsGuest:      p.IsGuest,
	})
	if err != nil {
		return nil, fmt.Errorf("insert customer: %w", err)
	}
	return customerFromRow(row), nil
}

// GetByID returns a customer by ID (staff-only, no ownership scoping).
func (s *CustomerStore) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Customer, error) {
	row, err := sqlcgen.New(tx).GetCustomerByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get customer %s: %w", id, err)
	}
	return customerFromRow(row), nil
}

// GetByEmail returns a customer by email address (used for login).
func (s *CustomerStore) GetByEmail(ctx context.Context, tx pgx.Tx, email string) (*domain.Customer, error) {
	row, err := sqlcgen.New(tx).GetCustomerByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("get customer by email: %w", err)
	}
	return customerFromRow(row), nil
}

// CustomerFilter holds optional filters for listing customers.
type CustomerFilter struct {
	CustomerGroupID *uuid.UUID
	IsGuest         *bool
	Limit           int
	Offset          int
}

// List returns customers matching the given filter (staff-only).
// This method is hand-written because sqlc cannot generate dynamic WHERE clauses.
func (s *CustomerStore) List(ctx context.Context, tx pgx.Tx, f CustomerFilter) ([]domain.Customer, error) {
	query := `SELECT id, email, email_verified, password_hash, first_name, last_name, phone,
	                 is_guest, tax_exempt, tax_exempt_reason, customer_group_id, metadata,
	                 created_at, updated_at
	          FROM customers WHERE true`
	args := []any{}
	argN := 1

	if f.CustomerGroupID != nil {
		query += fmt.Sprintf(" AND customer_group_id = $%d", argN)
		args = append(args, *f.CustomerGroupID)
		argN++
	}
	if f.IsGuest != nil {
		query += fmt.Sprintf(" AND is_guest = $%d", argN)
		args = append(args, *f.IsGuest)
		argN++
	}

	query += " ORDER BY created_at DESC"

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	query += fmt.Sprintf(" LIMIT $%d", argN)
	args = append(args, limit)
	argN++

	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, f.Offset)
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list customers: %w", err)
	}
	defer rows.Close()

	var customers []domain.Customer
	for rows.Next() {
		var c domain.Customer
		if err := rows.Scan(
			&c.ID, &c.Email, &c.EmailVerified, &c.PasswordHash, &c.FirstName, &c.LastName, &c.Phone,
			&c.IsGuest, &c.TaxExempt, &c.TaxExemptReason, &c.CustomerGroupID, &c.Metadata,
			&c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan customer: %w", err)
		}
		customers = append(customers, c)
	}
	return customers, rows.Err()
}

// UpdateEmail sets a customer's email and resets email_verified to false.
func (s *CustomerStore) UpdateEmail(ctx context.Context, tx pgx.Tx, id uuid.UUID, email string) (*domain.Customer, error) {
	row, err := sqlcgen.New(tx).UpdateCustomerEmail(ctx, sqlcgen.UpdateCustomerEmailParams{
		ID:    id,
		Email: email,
	})
	if err != nil {
		return nil, fmt.Errorf("update customer email: %w", err)
	}
	return customerFromRow(row), nil
}

// UpdatePassword sets a customer's password hash.
func (s *CustomerStore) UpdatePassword(ctx context.Context, tx pgx.Tx, id uuid.UUID, hash string) error {
	err := sqlcgen.New(tx).UpdateCustomerPassword(ctx, sqlcgen.UpdateCustomerPasswordParams{
		ID:           id,
		PasswordHash: &hash,
	})
	if err != nil {
		return fmt.Errorf("update customer password: %w", err)
	}
	return nil
}

// UpdateEmailVerified marks a customer's email as verified.
func (s *CustomerStore) UpdateEmailVerified(ctx context.Context, tx pgx.Tx, id uuid.UUID, verified bool) error {
	err := sqlcgen.New(tx).UpdateCustomerEmailVerified(ctx, sqlcgen.UpdateCustomerEmailVerifiedParams{
		ID:            id,
		EmailVerified: verified,
	})
	if err != nil {
		return fmt.Errorf("update customer email_verified: %w", err)
	}
	return nil
}

// UpdateTaxExempt sets a customer's tax exemption status.
func (s *CustomerStore) UpdateTaxExempt(ctx context.Context, tx pgx.Tx, id uuid.UUID, exempt bool, reason *string) error {
	err := sqlcgen.New(tx).UpdateCustomerTaxExempt(ctx, sqlcgen.UpdateCustomerTaxExemptParams{
		ID:              id,
		TaxExempt:       exempt,
		TaxExemptReason: reason,
	})
	if err != nil {
		return fmt.Errorf("update customer tax_exempt: %w", err)
	}
	return nil
}

// UpdateCustomerGroup sets a customer's customer group.
func (s *CustomerStore) UpdateCustomerGroup(ctx context.Context, tx pgx.Tx, id uuid.UUID, groupID *uuid.UUID) error {
	err := sqlcgen.New(tx).UpdateCustomerGroup(ctx, sqlcgen.UpdateCustomerGroupParams{
		ID:              id,
		CustomerGroupID: groupID,
	})
	if err != nil {
		return fmt.Errorf("update customer group: %w", err)
	}
	return nil
}

// Delete hard-deletes a customer by ID.
func (s *CustomerStore) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteCustomer(ctx, id); err != nil {
		return fmt.Errorf("delete customer: %w", err)
	}
	return nil
}

// --- Address CRUD ---

// CreateAddressParams holds the fields needed to create an address.
type CreateAddressParams struct {
	CustomerID  *uuid.UUID
	FirstName   string
	LastName    string
	Company     *string
	Line1       string
	Line2       *string
	City        string
	State       string
	PostalCode  string
	CountryCode string
	IsDefault   bool
}

// CreateAddress inserts a new address.
func (s *CustomerStore) CreateAddress(ctx context.Context, tx pgx.Tx, p CreateAddressParams) (*domain.Address, error) {
	row, err := sqlcgen.New(tx).CreateAddress(ctx, sqlcgen.CreateAddressParams{
		ID:          uuid.New(),
		CustomerID:  p.CustomerID,
		FirstName:   p.FirstName,
		LastName:    p.LastName,
		Company:     p.Company,
		Line1:       p.Line1,
		Line2:       p.Line2,
		City:        p.City,
		State:       p.State,
		PostalCode:  p.PostalCode,
		CountryCode: p.CountryCode,
		IsDefault:   p.IsDefault,
	})
	if err != nil {
		return nil, fmt.Errorf("insert address: %w", err)
	}
	return addressFromRow(row), nil
}

// GetAddress returns an address by ID, scoped to a customer (storefront).
func (s *CustomerStore) GetAddress(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) (*domain.Address, error) {
	row, err := sqlcgen.New(tx).GetAddress(ctx, sqlcgen.GetAddressParams{
		ID:         id,
		CustomerID: &customerID,
	})
	if err != nil {
		return nil, fmt.Errorf("get address %s: %w", id, err)
	}
	return addressFromRow(row), nil
}

// GetAddressByID returns an address by ID (staff-only, no ownership scoping).
func (s *CustomerStore) GetAddressByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Address, error) {
	row, err := sqlcgen.New(tx).GetAddressByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get address %s: %w", id, err)
	}
	return addressFromRow(row), nil
}

// ListAddresses returns all addresses for a customer.
func (s *CustomerStore) ListAddresses(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) ([]domain.Address, error) {
	rows, err := sqlcgen.New(tx).ListAddresses(ctx, &customerID)
	if err != nil {
		return nil, fmt.Errorf("list addresses: %w", err)
	}
	addresses := make([]domain.Address, len(rows))
	for i, r := range rows {
		addresses[i] = *addressFromRow(r)
	}
	return addresses, nil
}

// DeleteAddress removes an address, scoped to a customer.
func (s *CustomerStore) DeleteAddress(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) error {
	err := sqlcgen.New(tx).DeleteAddress(ctx, sqlcgen.DeleteAddressParams{
		ID:         id,
		CustomerID: &customerID,
	})
	if err != nil {
		return fmt.Errorf("delete address: %w", err)
	}
	return nil
}

// --- Row converters ---

func customerFromRow(r sqlcgen.Customer) *domain.Customer {
	return &domain.Customer{
		ID:              r.ID,
		Email:           r.Email,
		EmailVerified:   r.EmailVerified,
		PasswordHash:    r.PasswordHash,
		FirstName:       r.FirstName,
		LastName:        r.LastName,
		Phone:           r.Phone,
		IsGuest:         r.IsGuest,
		TaxExempt:       r.TaxExempt,
		TaxExemptReason: r.TaxExemptReason,
		CustomerGroupID: r.CustomerGroupID,
		Metadata:        metadataFromJSON(r.Metadata),
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

func addressFromRow(r sqlcgen.Address) *domain.Address {
	return &domain.Address{
		ID:          r.ID,
		CustomerID:  r.CustomerID,
		FirstName:   r.FirstName,
		LastName:    r.LastName,
		Company:     r.Company,
		Line1:       r.Line1,
		Line2:       r.Line2,
		City:        r.City,
		State:       r.State,
		PostalCode:  r.PostalCode,
		CountryCode: r.CountryCode,
		IsDefault:   r.IsDefault,
	}
}

