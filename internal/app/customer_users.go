package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/sessions"
	"github.com/dukerupert/hiri/internal/store"
)

// CustomerUserService owns additional logins on a wholesale account: inviting a
// teammate, listing and revoking them, and choosing who receives the account's
// transactional mail.
//
// A customer user is a login, never an account. Every method takes the owning
// customerID and scopes by it, so this service cannot be used to reach across
// accounts even if a caller passes an id it does not own.
type CustomerUserService struct {
	customers     *store.CustomerStore
	customerUsers *store.CustomerUserStore
	sessions      *sessions.Manager
	audit         *audit.AuditWriter
	metrics       *metrics.Registry
	auth          *AuthService // populated via WithEmail; mints invite tokens
	email         EmailEnv     // populated via WithEmail; required for SendInviteEmail
}

// NewCustomerUserService creates a new CustomerUserService.
func NewCustomerUserService(
	customers *store.CustomerStore,
	customerUsers *store.CustomerUserStore,
	sessions *sessions.Manager,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *CustomerUserService {
	return &CustomerUserService{
		customers:     customers,
		customerUsers: customerUsers,
		sessions:      sessions,
		audit:         audit,
		metrics:       metrics,
	}
}

// WithEmail wires the email environment and auth service needed by
// SendInviteEmail and invite-token minting. Mirrors WhiteLabelService.WithEmail.
func (s *CustomerUserService) WithEmail(env EmailEnv, auth *AuthService) *CustomerUserService {
	s.email = env
	s.auth = auth
	return s
}

// List returns every additional login on an account.
func (s *CustomerUserService) List(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) ([]domain.CustomerUser, error) {
	return s.customerUsers.ListByCustomer(ctx, tx, customerID)
}

// Invite creates an additional login on a wholesale account and mints a
// single-use setup token. It does NOT send the email — the caller enqueues the
// send job in the same transaction, matching the magic-link and password-reset
// flows.
//
// Returns ErrNotWholesaleAccount for a retail account, and ErrCustomerUserEmailTaken
// if the address already belongs to any customers row or any other customer
// user. That first check is load-bearing, not politeness: CustomerLogin resolves
// customers.email before customer_users.email, so an invite shadowing a
// customers row would silently send the invitee into a different account.
func (s *CustomerUserService) Invite(
	ctx context.Context,
	tx pgx.Tx,
	actor Actor,
	customerID uuid.UUID,
	email, name string,
	receivesNotifications bool,
) (*domain.CustomerUser, string, error) {
	normalized := domain.NormalizeEmail(email)
	if normalized == "" {
		return nil, "", ErrCustomerUserEmailRequired
	}

	account, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrCustomerNotFound
		}
		return nil, "", fmt.Errorf("get account for invite: %w", err)
	}
	if account.AccountType != domain.AccountTypeWholesale {
		return nil, "", ErrNotWholesaleAccount
	}

	if err := s.assertEmailAvailable(ctx, tx, normalized); err != nil {
		return nil, "", err
	}

	user, err := s.customerUsers.Create(ctx, tx, store.CreateCustomerUserParams{
		CustomerID:            customerID,
		Email:                 normalized,
		Name:                  strings.TrimSpace(name),
		Role:                  domain.CustomerUserRoleMember,
		ReceivesNotifications: receivesNotifications,
	})
	if err != nil {
		return nil, "", fmt.Errorf("create customer user: %w", err)
	}

	rawToken, err := s.auth.CreateCustomerUserInviteToken(ctx, tx, user.ID)
	if err != nil {
		return nil, "", err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerUserInvited,
		ResourceType: "customer",
		ResourceID:   customerID,
		Metadata: map[string]any{
			"customer_user_id": user.ID.String(),
			"email":            user.Email,
		},
	}); err != nil {
		return nil, "", fmt.Errorf("audit customer user invited: %w", err)
	}

	return user, rawToken, nil
}

// assertEmailAvailable rejects an address already claimed by a customers row or
// another customer user.
func (s *CustomerUserService) assertEmailAvailable(ctx context.Context, tx pgx.Tx, normalizedEmail string) error {
	if _, err := s.customers.GetByEmail(ctx, tx, normalizedEmail); err == nil {
		return ErrCustomerUserEmailTaken
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check customers for invite email: %w", err)
	}

	if _, err := s.customerUsers.GetByEmail(ctx, tx, normalizedEmail); err == nil {
		return ErrCustomerUserEmailTaken
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check customer users for invite email: %w", err)
	}

	return nil
}

// ResendInvite mints a fresh setup token for an existing member and returns it
// for the caller to email. Works for a member who never accepted as well as one
// who has forgotten their password — the token table serves both.
func (s *CustomerUserService) ResendInvite(ctx context.Context, tx pgx.Tx, actor Actor, id, customerID uuid.UUID) (*domain.CustomerUser, string, error) {
	user, err := s.getScoped(ctx, tx, id, customerID)
	if err != nil {
		return nil, "", err
	}

	rawToken, err := s.auth.CreateCustomerUserInviteToken(ctx, tx, user.ID)
	if err != nil {
		return nil, "", err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerUserInviteResent,
		ResourceType: "customer",
		ResourceID:   customerID,
		Metadata: map[string]any{
			"customer_user_id": user.ID.String(),
			"email":            user.Email,
		},
	}); err != nil {
		return nil, "", fmt.Errorf("audit customer user invite resent: %w", err)
	}

	return user, rawToken, nil
}

// Revoke removes an additional login and kills its live sessions in the same
// transaction. Without the session revocation a removed teammate would keep
// portal access until their cookie happened to expire — up to 30 days on a
// remember-me session.
func (s *CustomerUserService) Revoke(ctx context.Context, tx pgx.Tx, actor Actor, id, customerID uuid.UUID) error {
	user, err := s.getScoped(ctx, tx, id, customerID)
	if err != nil {
		return err
	}

	if err := s.sessions.GetStore().RevokeAllForActor(ctx, tx, domain.SessionActorTypeCustomerUser, user.ID); err != nil {
		return fmt.Errorf("revoke customer user sessions: %w", err)
	}

	// Scoped delete — cascades the invite tokens.
	if err := s.customerUsers.Delete(ctx, tx, id, customerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCustomerUserNotFound
		}
		return fmt.Errorf("delete customer user: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerUserRevoked,
		ResourceType: "customer",
		ResourceID:   customerID,
		Metadata: map[string]any{
			"customer_user_id": user.ID.String(),
			"email":            user.Email,
		},
	}); err != nil {
		return fmt.Errorf("audit customer user revoked: %w", err)
	}

	return nil
}

// SetNotifications toggles whether a member receives the account's
// transactional mail.
func (s *CustomerUserService) SetNotifications(ctx context.Context, tx pgx.Tx, actor Actor, id, customerID uuid.UUID, enabled bool) error {
	user, err := s.getScoped(ctx, tx, id, customerID)
	if err != nil {
		return err
	}

	if err := s.customerUsers.UpdateNotifications(ctx, tx, user.ID, enabled); err != nil {
		return fmt.Errorf("update customer user notifications: %w", err)
	}

	action := audit.AuditCustomerUserNotificationsDisabled
	if enabled {
		action = audit.AuditCustomerUserNotificationsEnabled
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       action,
		ResourceType: "customer",
		ResourceID:   customerID,
		Metadata: map[string]any{
			"customer_user_id": user.ID.String(),
			"email":            user.Email,
		},
	}); err != nil {
		return fmt.Errorf("audit customer user notifications: %w", err)
	}

	return nil
}

// NotificationRecipients returns every address that should receive an account's
// transactional mail: the account's primary contact plus any member who has
// opted in. The primary is always first and always included — a wholesale
// account must never end up with no deliverable contact because the team screen
// was misconfigured.
//
// Addresses are de-duplicated, so a member who somehow shares the primary
// address is not mailed twice.
func (s *CustomerUserService) NotificationRecipients(ctx context.Context, tx pgx.Tx, customer *domain.Customer) ([]string, error) {
	return notificationRecipients(ctx, tx, s.customerUsers, customer)
}

// notificationRecipients is the single definition of "who gets this account's
// mail", shared by CustomerUserService and WholesaleService so the team screen
// and the actual sends can never disagree.
func notificationRecipients(ctx context.Context, tx pgx.Tx, users *store.CustomerUserStore, customer *domain.Customer) ([]string, error) {
	extra, err := users.ListNotified(ctx, tx, customer.ID)
	if err != nil {
		return nil, err
	}

	out := []string{customer.Email}
	seen := map[string]struct{}{domain.NormalizeEmail(customer.Email): {}}
	for _, u := range extra {
		key := domain.NormalizeEmail(u.Email)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, u.Email)
	}
	return out, nil
}

// getScoped fetches a member by id constrained to its owning account, mapping a
// miss to ErrCustomerUserNotFound so a cross-account probe is indistinguishable
// from a deleted row.
func (s *CustomerUserService) getScoped(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) (*domain.CustomerUser, error) {
	user, err := s.customerUsers.GetForCustomer(ctx, tx, id, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerUserNotFound
		}
		return nil, fmt.Errorf("get customer user: %w", err)
	}
	return user, nil
}

// SendInviteEmail renders and sends the invite / password-setup email for an
// additional login. Called from the River job so the send happens outside the
// caller's transaction.
func (s *CustomerUserService) SendInviteEmail(ctx context.Context, pool *pgxpool.Pool, customerUserID uuid.UUID, rawToken string) error {
	var (
		user    *domain.CustomerUser
		account *domain.Customer
	)
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		u, err := s.customerUsers.GetByID(ctx, tx, customerUserID)
		if err != nil {
			return fmt.Errorf("get customer user %s: %w", customerUserID, err)
		}
		user = u
		a, err := s.customers.GetByID(ctx, tx, u.CustomerID)
		if err != nil {
			return fmt.Errorf("get account %s: %w", u.CustomerID, err)
		}
		account = a
		return nil
	}); err != nil {
		return err
	}

	companyName := account.CompanyName
	if companyName == nil || *companyName == "" {
		fallback := s.email.StoreName
		companyName = &fallback
	}

	setupURL := s.email.BaseURL + "/wholesale/invite?token=" + url.QueryEscape(rawToken)

	html, text, err := s.email.Renderer.Render("customer_user_invite", emailtemplates.CustomerUserInviteData{
		Name:        user.Name,
		CompanyName: *companyName,
		SetupURL:    setupURL,
		StoreName:   s.email.StoreName,
		StoreURL:    s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("customer_user_invite", "failed").Inc()
		return fmt.Errorf("render customer user invite template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      user.Email,
		Subject: "You've been added to the " + *companyName + " wholesale account",
		HTML:    html,
		Text:    text,
		Tag:     "customer-user-invite",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("customer_user_invite", "failed").Inc()
		return fmt.Errorf("send customer user invite email: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("customer_user_invite", "sent").Inc()
	return nil
}
