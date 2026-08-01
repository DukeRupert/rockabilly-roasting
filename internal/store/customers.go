package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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
	AccountType     *domain.AccountType
	WholesaleStatus *domain.WholesaleStatus
	Search          string // ILIKE on name or email
	Limit           int
	Offset          int
}

// List returns customers matching the given filter (staff-only).
// This method is hand-written because sqlc cannot generate dynamic WHERE clauses.
func (s *CustomerStore) List(ctx context.Context, tx pgx.Tx, f CustomerFilter) ([]domain.Customer, error) {
	query := `SELECT id, email, email_verified, password_hash, first_name, last_name, phone,
	                 tax_exempt, tax_exempt_reason, stripe_customer_id,
	                 price_list_id, account_type, wholesale_status, company_name,
	                 website, wholesale_notes, approved_at, approved_by,
	                 payment_terms_days, billing_method,
	                 two_fa_enabled, two_fa_method,
	                 preferred_local_fulfillment, order_reminders_enabled,
	                 metadata, created_at, updated_at
	          FROM customers WHERE true`
	args := []any{}
	argN := 1

	if f.AccountType != nil {
		query += fmt.Sprintf(" AND account_type = $%d", argN)
		args = append(args, string(*f.AccountType))
		argN++
	}
	if f.WholesaleStatus != nil {
		query += fmt.Sprintf(" AND wholesale_status = $%d", argN)
		args = append(args, string(*f.WholesaleStatus))
		argN++
	}
	if f.Search != "" {
		query += fmt.Sprintf(" AND (first_name || ' ' || last_name ILIKE $%d OR email ILIKE $%d)", argN, argN)
		args = append(args, "%"+f.Search+"%")
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
		var accountType string
		var wholesaleStatus *string
		var billingMethod string
		var paymentTermsDays *int32
		var preferredLocal *string
		var metadata []byte
		var approvedAt pgtype.Timestamptz
		if err := rows.Scan(
			&c.ID, &c.Email, &c.EmailVerified, &c.PasswordHash, &c.FirstName, &c.LastName, &c.Phone,
			&c.TaxExempt, &c.TaxExemptReason, &c.StripeCustomerID,
			&c.PriceListID, &accountType, &wholesaleStatus, &c.CompanyName,
			&c.Website, &c.WholesaleNotes, &approvedAt, &c.ApprovedBy,
			&paymentTermsDays, &billingMethod,
			&c.TwoFAEnabled, &c.TwoFAMethod,
			&preferredLocal, &c.OrderRemindersEnabled,
			&metadata, &c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan customer: %w", err)
		}
		c.AccountType = domain.AccountType(accountType)
		c.BillingMethod = domain.BillingMethod(billingMethod)
		if paymentTermsDays != nil {
			v := int(*paymentTermsDays)
			c.PaymentTermsDays = &v
		}
		if wholesaleStatus != nil {
			ws := domain.WholesaleStatus(*wholesaleStatus)
			c.WholesaleStatus = &ws
		}
		if preferredLocal != nil {
			m := domain.ShippingMethod(*preferredLocal)
			c.PreferredLocalFulfillment = &m
		}
		c.ApprovedAt = timestampFromPG(approvedAt)
		c.Metadata = metadataFromJSON(metadata)
		customers = append(customers, c)
	}
	return customers, rows.Err()
}

// CountCustomers returns the number of customers matching the given filter.
func (s *CustomerStore) CountCustomers(ctx context.Context, tx pgx.Tx, f CustomerFilter) (int, error) {
	query := `SELECT COUNT(*) FROM customers WHERE true`
	args := []any{}
	argN := 1

	if f.AccountType != nil {
		query += fmt.Sprintf(" AND account_type = $%d", argN)
		args = append(args, string(*f.AccountType))
		argN++
	}
	if f.WholesaleStatus != nil {
		query += fmt.Sprintf(" AND wholesale_status = $%d", argN)
		args = append(args, string(*f.WholesaleStatus))
		argN++
	}
	if f.Search != "" {
		query += fmt.Sprintf(" AND (first_name || ' ' || last_name ILIKE $%d OR email ILIKE $%d)", argN, argN)
		args = append(args, "%"+f.Search+"%")
		argN++ //nolint:ineffassign
	}

	var count int
	if err := tx.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count customers: %w", err)
	}
	return count, nil
}

// UpdateName sets a customer's first and last name.
func (s *CustomerStore) UpdateName(ctx context.Context, tx pgx.Tx, id uuid.UUID, firstName, lastName string) (*domain.Customer, error) {
	row, err := sqlcgen.New(tx).UpdateCustomerName(ctx, sqlcgen.UpdateCustomerNameParams{
		ID:        id,
		FirstName: firstName,
		LastName:  lastName,
	})
	if err != nil {
		return nil, fmt.Errorf("update customer name: %w", err)
	}
	return customerFromRow(row), nil
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

// UpdatePhone sets a customer's phone. Pass nil to clear it.
func (s *CustomerStore) UpdatePhone(ctx context.Context, tx pgx.Tx, id uuid.UUID, phone *string) (*domain.Customer, error) {
	row, err := sqlcgen.New(tx).UpdateCustomerPhone(ctx, sqlcgen.UpdateCustomerPhoneParams{
		ID:    id,
		Phone: phone,
	})
	if err != nil {
		return nil, fmt.Errorf("update customer phone: %w", err)
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

// UpdatePriceList sets a customer's assigned price list. Pass nil to clear it.
func (s *CustomerStore) UpdatePriceList(ctx context.Context, tx pgx.Tx, id uuid.UUID, priceListID *uuid.UUID) error {
	err := sqlcgen.New(tx).UpdateCustomerPriceList(ctx, sqlcgen.UpdateCustomerPriceListParams{
		ID:          id,
		PriceListID: priceListID,
	})
	if err != nil {
		return fmt.Errorf("update customer price list: %w", err)
	}
	return nil
}

// UpdateStripeCustomerID sets the Stripe customer ID on a customer.
func (s *CustomerStore) UpdateStripeCustomerID(ctx context.Context, tx pgx.Tx, id uuid.UUID, stripeID string) (*domain.Customer, error) {
	row, err := sqlcgen.New(tx).UpdateCustomerStripeCustomerID(ctx, sqlcgen.UpdateCustomerStripeCustomerIDParams{
		ID:               id,
		StripeCustomerID: &stripeID,
	})
	if err != nil {
		return nil, fmt.Errorf("update customer stripe customer id: %w", err)
	}
	return customerFromRow(row), nil
}

// GetByStripeCustomerID returns a customer by their Stripe customer ID.
func (s *CustomerStore) GetByStripeCustomerID(ctx context.Context, tx pgx.Tx, stripeID string) (*domain.Customer, error) {
	row, err := sqlcgen.New(tx).GetCustomerByStripeCustomerID(ctx, &stripeID)
	if err != nil {
		return nil, fmt.Errorf("get customer by stripe customer id: %w", err)
	}
	return customerFromRow(row), nil
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

// GetAddressByIDAsStaff returns an address by ID (staff-only, no ownership scoping).
func (s *CustomerStore) GetAddressByIDAsStaff(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Address, error) {
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

// UpdateAddress updates an address's fields, scoped to a customer.
func (s *CustomerStore) UpdateAddress(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID, p CreateAddressParams) (*domain.Address, error) {
	row, err := sqlcgen.New(tx).UpdateAddress(ctx, sqlcgen.UpdateAddressParams{
		ID:          id,
		CustomerID:  &customerID,
		FirstName:   p.FirstName,
		LastName:    p.LastName,
		Company:     p.Company,
		Line1:       p.Line1,
		Line2:       p.Line2,
		City:        p.City,
		State:       p.State,
		PostalCode:  p.PostalCode,
		CountryCode: p.CountryCode,
	})
	if err != nil {
		return nil, fmt.Errorf("update address: %w", err)
	}
	return addressFromRow(row), nil
}

// SetDefaultAddress sets an address as the default, clearing others.
func (s *CustomerStore) SetDefaultAddress(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) error {
	q := sqlcgen.New(tx)
	if err := q.ClearDefaultAddresses(ctx, &customerID); err != nil {
		return fmt.Errorf("clear default addresses: %w", err)
	}
	if err := q.SetDefaultAddress(ctx, sqlcgen.SetDefaultAddressParams{
		ID:         id,
		CustomerID: &customerID,
	}); err != nil {
		return fmt.Errorf("set default address: %w", err)
	}
	return nil
}

// CountAddresses returns the number of addresses for a customer.
func (s *CustomerStore) CountAddresses(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (int, error) {
	count, err := sqlcgen.New(tx).CountAddresses(ctx, &customerID)
	if err != nil {
		return 0, fmt.Errorf("count addresses: %w", err)
	}
	return int(count), nil
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

// UpdatePaymentTerms sets a customer's payment terms in days.
func (s *CustomerStore) UpdatePaymentTerms(ctx context.Context, tx pgx.Tx, id uuid.UUID, days *int) error {
	var dbDays *int32
	if days != nil {
		v := int32(*days)
		dbDays = &v
	}
	err := sqlcgen.New(tx).UpdateCustomerPaymentTerms(ctx, sqlcgen.UpdateCustomerPaymentTermsParams{
		ID:               id,
		PaymentTermsDays: dbDays,
	})
	if err != nil {
		return fmt.Errorf("update customer payment terms: %w", err)
	}
	return nil
}

// UpdatePreferredLocalFulfillment sets a customer's saved fulfillment choice.
// Pass nil to clear the preference (back to "ask each time"). The DB CHECK
// constraint enforces the value is pickup, local_delivery, or shipped.
func (s *CustomerStore) UpdatePreferredLocalFulfillment(ctx context.Context, tx pgx.Tx, id uuid.UUID, method *domain.ShippingMethod) error {
	var v *string
	if method != nil {
		s := string(*method)
		v = &s
	}
	err := sqlcgen.New(tx).UpdateCustomerPreferredLocalFulfillment(ctx, sqlcgen.UpdateCustomerPreferredLocalFulfillmentParams{
		ID:                        id,
		PreferredLocalFulfillment: v,
	})
	if err != nil {
		return fmt.Errorf("update customer preferred local fulfillment: %w", err)
	}
	return nil
}

// UpdateBillingMethod sets a customer's billing method.
func (s *CustomerStore) UpdateBillingMethod(ctx context.Context, tx pgx.Tx, id uuid.UUID, method domain.BillingMethod) error {
	err := sqlcgen.New(tx).UpdateCustomerBillingMethod(ctx, sqlcgen.UpdateCustomerBillingMethodParams{
		ID:            id,
		BillingMethod: string(method),
	})
	if err != nil {
		return fmt.Errorf("update customer billing method: %w", err)
	}
	return nil
}

// --- QuickBooks sync methods ---

// SetQBCustomerID sets the QB customer ID on a customer.
func (s *CustomerStore) SetQBCustomerID(ctx context.Context, tx pgx.Tx, id uuid.UUID, qbID string) error {
	_, err := tx.Exec(ctx,
		`UPDATE customers SET qb_customer_id = $2, qb_synced_at = now(), updated_at = now() WHERE id = $1`,
		id, qbID,
	)
	if err != nil {
		return fmt.Errorf("set qb customer id: %w", err)
	}
	return nil
}

// SetQBSyncedAt updates the QB sync timestamp on a customer.
func (s *CustomerStore) SetQBSyncedAt(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx,
		`UPDATE customers SET qb_synced_at = now(), updated_at = now() WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("set qb synced at: %w", err)
	}
	return nil
}

// --- Wholesale state transitions ---

// SetWholesaleApplicationFields promotes a standard customer to a pending
// wholesale applicant and records the company details submitted with the application.
func (s *CustomerStore) SetWholesaleApplicationFields(ctx context.Context, tx pgx.Tx, id uuid.UUID, companyName string, website *string) error {
	_, err := tx.Exec(ctx,
		`UPDATE customers
		 SET account_type = 'wholesale', wholesale_status = 'pending',
		     company_name = $2, website = $3, updated_at = now()
		 WHERE id = $1`,
		id, companyName, website,
	)
	if err != nil {
		return fmt.Errorf("set wholesale application fields: %w", err)
	}
	return nil
}

// SetWholesaleApproved transitions a pending wholesale applicant to approved
// and records who approved them and when.
func (s *CustomerStore) SetWholesaleApproved(ctx context.Context, tx pgx.Tx, id uuid.UUID, approvedBy *uuid.UUID, approvedAt time.Time) error {
	_, err := tx.Exec(ctx,
		`UPDATE customers
		 SET wholesale_status = 'approved', approved_at = $2, approved_by = $3, updated_at = now()
		 WHERE id = $1`,
		id, approvedAt, approvedBy,
	)
	if err != nil {
		return fmt.Errorf("set wholesale approved: %w", err)
	}
	return nil
}

// SetWholesaleNotes updates the wholesale notes field (used for decline reasons or staff commentary).
func (s *CustomerStore) SetWholesaleNotes(ctx context.Context, tx pgx.Tx, id uuid.UUID, notes string) error {
	_, err := tx.Exec(ctx,
		`UPDATE customers SET wholesale_notes = $2, updated_at = now() WHERE id = $1`,
		id, notes,
	)
	if err != nil {
		return fmt.Errorf("set wholesale notes: %w", err)
	}
	return nil
}

// SetWholesaleDeclined moves a pending application to the declined state and
// records the decline reason in wholesale_notes.
func (s *CustomerStore) SetWholesaleDeclined(ctx context.Context, tx pgx.Tx, id uuid.UUID, notes string) error {
	_, err := tx.Exec(ctx,
		`UPDATE customers
		 SET wholesale_status = 'declined', wholesale_notes = $2, updated_at = now()
		 WHERE id = $1`,
		id, notes,
	)
	if err != nil {
		return fmt.Errorf("set wholesale declined: %w", err)
	}
	return nil
}

// SetWholesaleSuspended suspends an approved wholesale account and records
// the reason in wholesale_notes.
func (s *CustomerStore) SetWholesaleSuspended(ctx context.Context, tx pgx.Tx, id uuid.UUID, notes string) error {
	_, err := tx.Exec(ctx,
		`UPDATE customers
		 SET wholesale_status = 'suspended', wholesale_notes = $2, updated_at = now()
		 WHERE id = $1`,
		id, notes,
	)
	if err != nil {
		return fmt.Errorf("set wholesale suspended: %w", err)
	}
	return nil
}

// SetWholesaleReactivated reactivates a suspended wholesale account and clears
// the suspension notes.
func (s *CustomerStore) SetWholesaleReactivated(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx,
		`UPDATE customers
		 SET wholesale_status = 'approved', wholesale_notes = NULL, updated_at = now()
		 WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("set wholesale reactivated: %w", err)
	}
	return nil
}

// ListWholesaleWithQBCustomerID returns all wholesale customers that have a QB customer ID.
// Used for the "Re-sync all customers" admin action.
func (s *CustomerStore) ListWholesaleWithQBCustomerID(ctx context.Context, tx pgx.Tx) ([]domain.Customer, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, email, email_verified, password_hash, first_name, last_name, phone,
		        tax_exempt, tax_exempt_reason, stripe_customer_id,
		        price_list_id, account_type, wholesale_status, company_name,
		        website, wholesale_notes, approved_at, approved_by,
		        payment_terms_days, billing_method,
		        qb_customer_id, qb_synced_at,
		        two_fa_enabled, two_fa_method,
		        preferred_local_fulfillment, order_reminders_enabled,
		        metadata, created_at, updated_at
		 FROM customers
		 WHERE account_type = 'wholesale' AND qb_customer_id IS NOT NULL
		 ORDER BY company_name`)
	if err != nil {
		return nil, fmt.Errorf("list wholesale with qb id: %w", err)
	}
	defer rows.Close()

	var customers []domain.Customer
	for rows.Next() {
		var c domain.Customer
		var accountType string
		var wholesaleStatus *string
		var billingMethod string
		var paymentTermsDays *int32
		var preferredLocal *string
		var metadata []byte
		var approvedAt pgtype.Timestamptz
		var qbSyncedAt pgtype.Timestamptz
		if err := rows.Scan(
			&c.ID, &c.Email, &c.EmailVerified, &c.PasswordHash, &c.FirstName, &c.LastName, &c.Phone,
			&c.TaxExempt, &c.TaxExemptReason, &c.StripeCustomerID,
			&c.PriceListID, &accountType, &wholesaleStatus, &c.CompanyName,
			&c.Website, &c.WholesaleNotes, &approvedAt, &c.ApprovedBy,
			&paymentTermsDays, &billingMethod,
			&c.QBCustomerID, &qbSyncedAt,
			&c.TwoFAEnabled, &c.TwoFAMethod,
			&preferredLocal, &c.OrderRemindersEnabled,
			&metadata, &c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan wholesale customer: %w", err)
		}
		c.AccountType = domain.AccountType(accountType)
		c.BillingMethod = domain.BillingMethod(billingMethod)
		if paymentTermsDays != nil {
			v := int(*paymentTermsDays)
			c.PaymentTermsDays = &v
		}
		if wholesaleStatus != nil {
			ws := domain.WholesaleStatus(*wholesaleStatus)
			c.WholesaleStatus = &ws
		}
		if preferredLocal != nil {
			m := domain.ShippingMethod(*preferredLocal)
			c.PreferredLocalFulfillment = &m
		}
		c.ApprovedAt = timestampFromPG(approvedAt)
		c.QBSyncedAt = timestampFromPG(qbSyncedAt)
		c.Metadata = metadataFromJSON(metadata)
		customers = append(customers, c)
	}
	return customers, rows.Err()
}

// UpdateOrderRemindersEnabled sets whether the customer receives the weekly
// wholesale order reminder.
func (s *CustomerStore) UpdateOrderRemindersEnabled(ctx context.Context, tx pgx.Tx, id uuid.UUID, enabled bool) error {
	q := sqlcgen.New(tx)
	if err := q.UpdateCustomerOrderRemindersEnabled(ctx, sqlcgen.UpdateCustomerOrderRemindersEnabledParams{
		ID:                    id,
		OrderRemindersEnabled: enabled,
	}); err != nil {
		return fmt.Errorf("update order reminders enabled: %w", err)
	}
	return nil
}

// ListOrderReminderRecipients returns approved wholesale customers who placed
// at least one order in the trailing `since` window and have reminders enabled.
//
// Cancelled and refunded orders do not count as activity — an account whose
// only recent order was cancelled has not actually been buying, and reminding
// them to "order again" would be wrong. The window is measured against
// placed_at (when the customer ordered), never created_at, so backfilled and
// imported orders sort by real-world order date.
func (s *CustomerStore) ListOrderReminderRecipients(ctx context.Context, tx pgx.Tx, since time.Time) ([]domain.OrderReminderRecipient, error) {
	rows, err := tx.Query(ctx,
		`SELECT c.id, c.email, c.company_name, c.first_name, c.last_name,
		        max(o.placed_at) AS last_order_at
		 FROM customers c
		 JOIN orders o ON o.customer_id = c.id
		 WHERE c.account_type = 'wholesale'
		   AND c.wholesale_status = 'approved'
		   AND c.order_reminders_enabled
		   AND o.channel = 'wholesale'
		   AND o.status NOT IN ('cancelled', 'refunded')
		   AND o.placed_at >= $1
		 GROUP BY c.id, c.email, c.company_name, c.first_name, c.last_name
		 ORDER BY c.company_name NULLS LAST, c.email`, since)
	if err != nil {
		return nil, fmt.Errorf("list order reminder recipients: %w", err)
	}
	defer rows.Close()

	recipients := []domain.OrderReminderRecipient{}
	for rows.Next() {
		var r domain.OrderReminderRecipient
		if err := rows.Scan(&r.CustomerID, &r.Email, &r.CompanyName,
			&r.FirstName, &r.LastName, &r.LastOrderAt); err != nil {
			return nil, fmt.Errorf("scan order reminder recipient: %w", err)
		}
		recipients = append(recipients, r)
	}
	return recipients, rows.Err()
}

// --- Row converters ---

func customerFromRow(r sqlcgen.Customer) *domain.Customer {
	c := &domain.Customer{
		ID:                    r.ID,
		Email:                 r.Email,
		EmailVerified:         r.EmailVerified,
		PasswordHash:          r.PasswordHash,
		FirstName:             r.FirstName,
		LastName:              r.LastName,
		Phone:                 r.Phone,
		TaxExempt:             r.TaxExempt,
		TaxExemptReason:       r.TaxExemptReason,
		StripeCustomerID:      r.StripeCustomerID,
		PriceListID:           r.PriceListID,
		AccountType:           domain.AccountType(r.AccountType),
		CompanyName:           r.CompanyName,
		Website:               r.Website,
		WholesaleNotes:        r.WholesaleNotes,
		ApprovedAt:            timestampFromPG(r.ApprovedAt),
		ApprovedBy:            r.ApprovedBy,
		BillingMethod:         domain.BillingMethod(r.BillingMethod),
		TwoFAEnabled:          r.TwoFaEnabled,
		TwoFAMethod:           r.TwoFaMethod,
		OrderRemindersEnabled: r.OrderRemindersEnabled,
		Metadata:              metadataFromJSON(r.Metadata),
		CreatedAt:             r.CreatedAt,
		UpdatedAt:             r.UpdatedAt,
	}
	if r.PaymentTermsDays != nil {
		v := int(*r.PaymentTermsDays)
		c.PaymentTermsDays = &v
	}
	if r.WholesaleStatus != nil {
		ws := domain.WholesaleStatus(*r.WholesaleStatus)
		c.WholesaleStatus = &ws
	}
	if r.PreferredLocalFulfillment != nil {
		m := domain.ShippingMethod(*r.PreferredLocalFulfillment)
		c.PreferredLocalFulfillment = &m
	}
	return c
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
