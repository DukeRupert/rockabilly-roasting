package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// CustomerService contains business logic for customer management.
type CustomerService struct {
	customers *store.CustomerStore
	audit     *audit.AuditWriter
	metrics   *metrics.Registry
}

// NewCustomerService creates a new CustomerService.
func NewCustomerService(customers *store.CustomerStore, audit *audit.AuditWriter, metrics *metrics.Registry) *CustomerService {
	return &CustomerService{
		customers: customers,
		audit:     audit,
		metrics:   metrics,
	}
}

// --- Self-service methods (no audit) ---

// GetCustomer returns a customer by ID.
func (s *CustomerService) GetCustomer(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Customer, error) {
	c, err := s.customers.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer: %w", err)
	}
	return c, nil
}

// GetCustomerByEmail returns a customer by email.
func (s *CustomerService) GetCustomerByEmail(ctx context.Context, tx pgx.Tx, email string) (*domain.Customer, error) {
	c, err := s.customers.GetByEmail(ctx, tx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer by email: %w", err)
	}
	return c, nil
}

// UpdateName updates a customer's first and last name.
func (s *CustomerService) UpdateName(ctx context.Context, tx pgx.Tx, id uuid.UUID, firstName, lastName string) (*domain.Customer, error) {
	c, err := s.customers.UpdateName(ctx, tx, id, firstName, lastName)
	if err != nil {
		return nil, fmt.Errorf("update name: %w", err)
	}
	return c, nil
}

// UpdateEmail updates a customer's email after checking uniqueness.
func (s *CustomerService) UpdateEmail(ctx context.Context, tx pgx.Tx, id uuid.UUID, email string) (*domain.Customer, error) {
	existing, err := s.customers.GetByEmail(ctx, tx, email)
	if err == nil && existing.ID != id {
		return nil, ErrEmailAlreadyExists
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check email uniqueness: %w", err)
	}

	c, err := s.customers.UpdateEmail(ctx, tx, id, email)
	if err != nil {
		return nil, fmt.Errorf("update email: %w", err)
	}
	return c, nil
}

// UpdatePhone updates a customer's phone number. Pass nil (or empty after
// trimming) to clear it. Records a customer.phone_updated audit entry on every
// successful update — including self-service edits, unlike UpdateName /
// UpdateEmail. Phone gates outbound order notifications, so a clear trail of
// who set the destination number is worth the extra audit row.
func (s *CustomerService) UpdatePhone(ctx context.Context, tx pgx.Tx, id uuid.UUID, phone *string, actor Actor) (*domain.Customer, error) {
	c, err := s.customers.UpdatePhone(ctx, tx, id, phone)
	if err != nil {
		return nil, fmt.Errorf("update phone: %w", err)
	}
	after := map[string]any{"phone": nil}
	if phone != nil {
		after["phone"] = *phone
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerPhoneUpdated,
		ResourceType: "customer",
		ResourceID:   id,
		After:        after,
	}); err != nil {
		return nil, fmt.Errorf("record phone update audit: %w", err)
	}
	return c, nil
}

// UpdatePreferredLocalFulfillmentSelf is the customer-initiated path (no
// audit). The audited staff path is UpdatePreferredLocalFulfillment, below.
// Pass nil to clear the preference back to "ask each time".
func (s *CustomerService) UpdatePreferredLocalFulfillmentSelf(ctx context.Context, tx pgx.Tx, id uuid.UUID, method *domain.ShippingMethod) error {
	if method != nil {
		if *method != domain.ShippingMethodPickup && *method != domain.ShippingMethodLocalDelivery {
			return fmt.Errorf("invalid local fulfillment method: %s", *method)
		}
	}
	if err := s.customers.UpdatePreferredLocalFulfillment(ctx, tx, id, method); err != nil {
		return fmt.Errorf("update preferred local fulfillment: %w", err)
	}
	return nil
}

// VerifyEmail marks a customer's email as verified.
func (s *CustomerService) VerifyEmail(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := s.customers.UpdateEmailVerified(ctx, tx, id, true); err != nil {
		return fmt.Errorf("verify email: %w", err)
	}
	return nil
}

// CreateRetail creates a retail customer with the given email and name (no password).
// Phone is optional; it's persisted on creation so pickup customers can be
// reached when their order is ready, but the checkout form leaves it blank
// for callers who just want a guest account.
func (s *CustomerService) CreateRetail(ctx context.Context, tx pgx.Tx, email, firstName, lastName string, phone *string) (*domain.Customer, error) {
	c, err := s.customers.Create(ctx, tx, store.CreateCustomerParams{
		Email:     email,
		FirstName: firstName,
		LastName:  lastName,
		Phone:     phone,
	})
	if err != nil {
		return nil, fmt.Errorf("create guest customer: %w", err)
	}
	return c, nil
}

// GetAddressByIDAsStaff returns an address by ID (no customer scoping).
func (s *CustomerService) GetAddressByIDAsStaff(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Address, error) {
	addr, err := s.customers.GetAddressByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAddressNotFound
		}
		return nil, fmt.Errorf("get address by id: %w", err)
	}
	return addr, nil
}

// --- Staff-initiated methods (audited) ---

// UpdateCustomerGroup updates a customer's group and records an audit entry.
func (s *CustomerService) UpdateCustomerGroup(ctx context.Context, tx pgx.Tx, id uuid.UUID, groupID *uuid.UUID, actor Actor) error {
	if err := s.customers.UpdateCustomerGroup(ctx, tx, id, groupID); err != nil {
		return fmt.Errorf("update customer group: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerGroupChanged,
		ResourceType: "customer",
		ResourceID:   id,
		After:        map[string]any{"customer_group_id": groupID},
	}); err != nil {
		return fmt.Errorf("audit customer group changed: %w", err)
	}

	return nil
}

// GrantTaxExemption grants tax exemption to a customer and records an audit entry.
func (s *CustomerService) GrantTaxExemption(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason string, actor Actor) error {
	if err := s.customers.UpdateTaxExempt(ctx, tx, id, true, &reason); err != nil {
		return fmt.Errorf("grant tax exemption: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerTaxExemptionGranted,
		ResourceType: "customer",
		ResourceID:   id,
		After:        map[string]any{"tax_exempt": true, "reason": reason},
	}); err != nil {
		return fmt.Errorf("audit tax exemption granted: %w", err)
	}

	return nil
}

// RevokeTaxExemption revokes tax exemption from a customer and records an audit entry.
func (s *CustomerService) RevokeTaxExemption(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	if err := s.customers.UpdateTaxExempt(ctx, tx, id, false, nil); err != nil {
		return fmt.Errorf("revoke tax exemption: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerTaxExemptionRevoked,
		ResourceType: "customer",
		ResourceID:   id,
		After:        map[string]any{"tax_exempt": false},
	}); err != nil {
		return fmt.Errorf("audit tax exemption revoked: %w", err)
	}

	return nil
}

// ListCustomers returns customers matching the given filter.
func (s *CustomerService) ListCustomers(ctx context.Context, tx pgx.Tx, f store.CustomerFilter) ([]domain.Customer, error) {
	customers, err := s.customers.List(ctx, tx, f)
	if err != nil {
		return nil, fmt.Errorf("list customers: %w", err)
	}
	return customers, nil
}

// CountCustomers returns the number of customers matching the given filter.
func (s *CustomerService) CountCustomers(ctx context.Context, tx pgx.Tx, f store.CustomerFilter) (int, error) {
	count, err := s.customers.CountCustomers(ctx, tx, f)
	if err != nil {
		return 0, fmt.Errorf("count customers: %w", err)
	}
	return count, nil
}

// CountAddresses returns the number of addresses for a customer.
func (s *CustomerService) CountAddresses(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (int, error) {
	count, err := s.customers.CountAddresses(ctx, tx, customerID)
	if err != nil {
		return 0, fmt.Errorf("count addresses: %w", err)
	}
	return count, nil
}

// LinkStripeCustomerID records the Stripe customer ID on a customer record.
// This is system-initiated (after a successful payment intent) and is audited
// with a SystemActor so the linkage is traceable.
func (s *CustomerService) LinkStripeCustomerID(ctx context.Context, tx pgx.Tx, id uuid.UUID, stripeCustomerID string) error {
	if _, err := s.customers.UpdateStripeCustomerID(ctx, tx, id, stripeCustomerID); err != nil {
		return fmt.Errorf("link stripe customer id: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    domain.AuditActorTypeSystem,
		ActorName:    "stripe_checkout",
		Action:       audit.AuditCustomerStripeIDLinked,
		ResourceType: "customer",
		ResourceID:   id,
		After:        map[string]any{"stripe_customer_id": stripeCustomerID},
	}); err != nil {
		return fmt.Errorf("audit stripe id linked: %w", err)
	}
	return nil
}

// UpdatePaymentTerms sets a customer's NET payment terms (days) and records
// an audit entry. Pass nil to clear the terms.
func (s *CustomerService) UpdatePaymentTerms(ctx context.Context, tx pgx.Tx, id uuid.UUID, days *int, actor Actor) error {
	if err := s.customers.UpdatePaymentTerms(ctx, tx, id, days); err != nil {
		return fmt.Errorf("update payment terms: %w", err)
	}

	after := map[string]any{"payment_terms_days": nil}
	if days != nil {
		after["payment_terms_days"] = *days
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerPaymentTermsUpdated,
		ResourceType: "customer",
		ResourceID:   id,
		After:        after,
	}); err != nil {
		return fmt.Errorf("audit payment terms updated: %w", err)
	}
	return nil
}

// UpdateBillingMethod sets a customer's billing method and records an audit entry.
func (s *CustomerService) UpdateBillingMethod(ctx context.Context, tx pgx.Tx, id uuid.UUID, method domain.BillingMethod, actor Actor) error {
	if err := s.customers.UpdateBillingMethod(ctx, tx, id, method); err != nil {
		return fmt.Errorf("update billing method: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerBillingMethodUpdated,
		ResourceType: "customer",
		ResourceID:   id,
		After:        map[string]any{"billing_method": string(method)},
	}); err != nil {
		return fmt.Errorf("audit billing method updated: %w", err)
	}
	return nil
}

// UpdatePreferredLocalFulfillment is the staff-initiated path: same write as
// the self-service variant but additionally records an audit entry. Pass nil
// to clear the preference back to "ask each time".
func (s *CustomerService) UpdatePreferredLocalFulfillment(ctx context.Context, tx pgx.Tx, id uuid.UUID, method *domain.ShippingMethod, actor Actor) error {
	if method != nil {
		if *method != domain.ShippingMethodPickup && *method != domain.ShippingMethodLocalDelivery {
			return fmt.Errorf("invalid local fulfillment method: %s", *method)
		}
	}
	if err := s.customers.UpdatePreferredLocalFulfillment(ctx, tx, id, method); err != nil {
		return fmt.Errorf("update preferred local fulfillment: %w", err)
	}

	after := map[string]any{"preferred_local_fulfillment": nil}
	if method != nil {
		after["preferred_local_fulfillment"] = string(*method)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerLocalFulfillmentUpdated,
		ResourceType: "customer",
		ResourceID:   id,
		After:        after,
	}); err != nil {
		return fmt.Errorf("audit preferred local fulfillment updated: %w", err)
	}
	return nil
}

// --- Address methods ---

// CreateAddress creates a new address and records an audit entry.
func (s *CustomerService) CreateAddress(ctx context.Context, tx pgx.Tx, p store.CreateAddressParams, actor Actor) (*domain.Address, error) {
	addr, err := s.customers.CreateAddress(ctx, tx, p)
	if err != nil {
		return nil, fmt.Errorf("create address: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerAddressAdded,
		ResourceType: "address",
		ResourceID:   addr.ID,
		After:        addr,
	}); err != nil {
		return nil, fmt.Errorf("audit address created: %w", err)
	}

	return addr, nil
}

// GetAddress returns an address by ID, scoped to a customer.
func (s *CustomerService) GetAddress(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) (*domain.Address, error) {
	addr, err := s.customers.GetAddress(ctx, tx, id, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAddressNotFound
		}
		return nil, fmt.Errorf("get address: %w", err)
	}
	return addr, nil
}

// ListAddresses returns all addresses for a customer.
func (s *CustomerService) ListAddresses(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) ([]domain.Address, error) {
	addrs, err := s.customers.ListAddresses(ctx, tx, customerID)
	if err != nil {
		return nil, fmt.Errorf("list addresses: %w", err)
	}
	return addrs, nil
}

// UpdateAddress updates an address's fields, scoped to a customer, and records an audit entry.
func (s *CustomerService) UpdateAddress(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID, p store.CreateAddressParams, actor Actor) (*domain.Address, error) {
	addr, err := s.customers.UpdateAddress(ctx, tx, id, customerID, p)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAddressNotFound
		}
		return nil, fmt.Errorf("update address: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerAddressUpdated,
		ResourceType: "address",
		ResourceID:   id,
		After:        addr,
	}); err != nil {
		return nil, fmt.Errorf("audit address updated: %w", err)
	}

	return addr, nil
}

// SetDefaultAddress sets an address as the default for a customer.
func (s *CustomerService) SetDefaultAddress(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) error {
	if err := s.customers.SetDefaultAddress(ctx, tx, id, customerID); err != nil {
		return fmt.Errorf("set default address: %w", err)
	}
	return nil
}

// DeleteAddress removes an address, scoped to a customer, and records an audit entry.
func (s *CustomerService) DeleteAddress(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID, actor Actor) error {
	if err := s.customers.DeleteAddress(ctx, tx, id, customerID); err != nil {
		return fmt.Errorf("delete address: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerAddressDeleted,
		ResourceType: "address",
		ResourceID:   id,
	}); err != nil {
		return fmt.Errorf("audit address deleted: %w", err)
	}

	return nil
}
