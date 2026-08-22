package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// CustomerUserStore provides database access for additional logins on a
// wholesale account.
type CustomerUserStore struct{}

// NewCustomerUserStore creates a new CustomerUserStore.
func NewCustomerUserStore() *CustomerUserStore {
	return &CustomerUserStore{}
}

// CreateCustomerUserParams holds the fields needed to invite a customer user.
// No password hash — an invitee sets one by redeeming their invite token.
type CreateCustomerUserParams struct {
	CustomerID            uuid.UUID
	Email                 string
	Name                  string
	Role                  domain.CustomerUserRole
	ReceivesNotifications bool
}

// Create inserts a new customer user. Email must already be normalized by the
// caller (see domain.NormalizeEmail).
func (s *CustomerUserStore) Create(ctx context.Context, tx pgx.Tx, p CreateCustomerUserParams) (*domain.CustomerUser, error) {
	row, err := sqlcgen.New(tx).CreateCustomerUser(ctx, sqlcgen.CreateCustomerUserParams{
		ID:                    uuid.New(),
		CustomerID:            p.CustomerID,
		Email:                 p.Email,
		Name:                  p.Name,
		Role:                  string(p.Role),
		ReceivesNotifications: p.ReceivesNotifications,
	})
	if err != nil {
		return nil, fmt.Errorf("insert customer user: %w", err)
	}
	return customerUserFromRow(row), nil
}

// GetByID returns a customer user by ID (unscoped — session resolution only).
func (s *CustomerUserStore) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.CustomerUser, error) {
	row, err := sqlcgen.New(tx).GetCustomerUserByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get customer user %s: %w", id, err)
	}
	return customerUserFromRow(row), nil
}

// GetByEmail returns a customer user by email address (used for login). The
// caller must normalize the address first.
func (s *CustomerUserStore) GetByEmail(ctx context.Context, tx pgx.Tx, email string) (*domain.CustomerUser, error) {
	row, err := sqlcgen.New(tx).GetCustomerUserByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("get customer user by email: %w", err)
	}
	return customerUserFromRow(row), nil
}

// GetForCustomer returns a customer user scoped to its owning account. Use this
// anywhere the account id comes from the session and the user id comes from the
// request — it is what stops one account from touching another's members.
func (s *CustomerUserStore) GetForCustomer(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) (*domain.CustomerUser, error) {
	row, err := sqlcgen.New(tx).GetCustomerUserForCustomer(ctx, sqlcgen.GetCustomerUserForCustomerParams{
		ID:         id,
		CustomerID: customerID,
	})
	if err != nil {
		return nil, fmt.Errorf("get customer user %s for customer: %w", id, err)
	}
	return customerUserFromRow(row), nil
}

// ListByCustomer returns every additional login on an account, oldest first.
func (s *CustomerUserStore) ListByCustomer(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) ([]domain.CustomerUser, error) {
	rows, err := sqlcgen.New(tx).ListCustomerUsersByCustomer(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("list customer users: %w", err)
	}
	out := make([]domain.CustomerUser, 0, len(rows))
	for _, r := range rows {
		out = append(out, *customerUserFromRow(r))
	}
	return out, nil
}

// ListNotified returns the additional logins on an account that have opted into
// transactional mail. The account's primary contact is not included — it lives
// on the customers row.
func (s *CustomerUserStore) ListNotified(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) ([]domain.CustomerUser, error) {
	rows, err := sqlcgen.New(tx).ListNotifiedCustomerUsers(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("list notified customer users: %w", err)
	}
	out := make([]domain.CustomerUser, 0, len(rows))
	for _, r := range rows {
		out = append(out, *customerUserFromRow(r))
	}
	return out, nil
}

// UpdatePassword sets the user's password hash.
func (s *CustomerUserStore) UpdatePassword(ctx context.Context, tx pgx.Tx, id uuid.UUID, passwordHash string) error {
	if err := sqlcgen.New(tx).UpdateCustomerUserPassword(ctx, sqlcgen.UpdateCustomerUserPasswordParams{
		ID:           id,
		PasswordHash: &passwordHash,
	}); err != nil {
		return fmt.Errorf("update customer user password: %w", err)
	}
	return nil
}

// UpdateNotifications toggles whether this user receives the account's
// transactional mail.
func (s *CustomerUserStore) UpdateNotifications(ctx context.Context, tx pgx.Tx, id uuid.UUID, enabled bool) error {
	if err := sqlcgen.New(tx).UpdateCustomerUserNotifications(ctx, sqlcgen.UpdateCustomerUserNotificationsParams{
		ID:                    id,
		ReceivesNotifications: enabled,
	}); err != nil {
		return fmt.Errorf("update customer user notifications: %w", err)
	}
	return nil
}

// TouchLastLogin records a successful sign-in.
func (s *CustomerUserStore) TouchLastLogin(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).TouchCustomerUserLastLogin(ctx, id); err != nil {
		return fmt.Errorf("touch customer user last login: %w", err)
	}
	return nil
}

// Delete removes a customer user. Scoped by customerID so a caller cannot
// revoke a member of an account it does not own. Returns pgx.ErrNoRows if no
// matching row exists, so a cross-account attempt is indistinguishable from a
// missing row.
func (s *CustomerUserStore) Delete(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) error {
	n, err := sqlcgen.New(tx).DeleteCustomerUser(ctx, sqlcgen.DeleteCustomerUserParams{
		ID:         id,
		CustomerID: customerID,
	})
	if err != nil {
		return fmt.Errorf("delete customer user: %w", err)
	}
	if n == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func customerUserFromRow(r sqlcgen.CustomerUser) *domain.CustomerUser {
	u := &domain.CustomerUser{
		ID:                    r.ID,
		CustomerID:            r.CustomerID,
		Email:                 r.Email,
		PasswordHash:          r.PasswordHash,
		Name:                  r.Name,
		Role:                  domain.CustomerUserRole(r.Role),
		ReceivesNotifications: r.ReceivesNotifications,
		InvitedAt:             r.InvitedAt,
		CreatedAt:             r.CreatedAt,
		UpdatedAt:             r.UpdatedAt,
	}
	if r.LastLoginAt.Valid {
		u.LastLoginAt = &r.LastLoginAt.Time
	}
	return u
}

// UpdateAnnouncementsEnabled sets whether one invited teammate receives staff
// announcements. Acts on this row alone: several colleagues share an account's
// mailing, and an opt-out must silence only the address that clicked it.
func (s *CustomerUserStore) UpdateAnnouncementsEnabled(ctx context.Context, tx pgx.Tx, id uuid.UUID, enabled bool) error {
	if _, err := tx.Exec(ctx,
		`UPDATE customer_users SET announcements_enabled = $2, updated_at = now() WHERE id = $1`,
		id, enabled); err != nil {
		return fmt.Errorf("update customer user announcements enabled: %w", err)
	}
	return nil
}

// ListAnnouncementRecipients returns the invited teammates on an account who
// receive staff announcements. Distinct from ListNotified, which governs
// transactional mail: a teammate may want order confirmations but not notices,
// and vice versa.
func (s *CustomerUserStore) ListAnnouncementRecipients(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) ([]domain.CustomerUser, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, email, name FROM customer_users
		 WHERE customer_id = $1 AND announcements_enabled
		 ORDER BY email`, customerID)
	if err != nil {
		return nil, fmt.Errorf("list announcement customer users: %w", err)
	}
	defer rows.Close()

	out := []domain.CustomerUser{}
	for rows.Next() {
		var u domain.CustomerUser
		if err := rows.Scan(&u.ID, &u.Email, &u.Name); err != nil {
			return nil, fmt.Errorf("scan announcement customer user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
