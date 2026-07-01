package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/sessions"
	"github.com/dukerupert/hiri/internal/store"
)

// MagicLinkDuration is how long a magic link token is valid.
const MagicLinkDuration = 15 * time.Minute

// SetupTokenDuration is how long a wholesale password setup link is valid.
const SetupTokenDuration = 72 * time.Hour

// WhiteLabelInviteDuration is how long a white-label onboarding invite link is
// valid. Unlike setup tokens, an invite is reusable until it expires — a client
// can submit multiple custom-label products from the same link — so it gets a
// longer window.
const WhiteLabelInviteDuration = 30 * 24 * time.Hour

// MagicLinkSessionDuration is the session lifetime for magic link logins.
const MagicLinkSessionDuration = 30 * 24 * time.Hour

// AuthService contains business logic for authentication and session management.
type AuthService struct {
	staff        *store.StaffStore
	customers    *store.CustomerStore
	magicLinks   *store.MagicLinkStore
	staffInvites *store.StaffInviteTokenStore
	sessions     *sessions.Manager
	audit        *audit.AuditWriter
	metrics      *metrics.Registry
	email        EmailEnv // populated via WithEmail; required for SendMagicLink
}

// NewAuthService creates a new AuthService.
func NewAuthService(
	staff *store.StaffStore,
	customers *store.CustomerStore,
	magicLinks *store.MagicLinkStore,
	staffInvites *store.StaffInviteTokenStore,
	sessions *sessions.Manager,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *AuthService {
	return &AuthService{
		staff:        staff,
		customers:    customers,
		magicLinks:   magicLinks,
		staffInvites: staffInvites,
		sessions:     sessions,
		audit:        audit,
		metrics:      metrics,
	}
}

// WithEmail attaches email-send environment. Required before calling Send*
// methods; safe to call at wiring time in main.
func (s *AuthService) WithEmail(env EmailEnv) *AuthService {
	s.email = env
	return s
}

// StaffLogin authenticates a staff member and creates a session.
func (s *AuthService) StaffLogin(ctx context.Context, tx pgx.Tx, email, password string, ipAddress, userAgent *string) (*domain.Session, string, error) {
	staff, err := s.staff.GetByEmail(ctx, tx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrInvalidCredentials
		}
		return nil, "", fmt.Errorf("staff login lookup: %w", err)
	}

	if !staff.IsActive {
		return nil, "", ErrStaffInactive
	}

	if err := bcrypt.CompareHashAndPassword([]byte(staff.PasswordHash), []byte(password)); err != nil {
		return nil, "", ErrInvalidCredentials
	}

	expiresAt := time.Now().Add(sessions.StaffSessionDuration)
	session, rawToken, err := s.sessions.GetStore().Create(ctx, tx, domain.SessionActorTypeStaff, staff.ID, expiresAt, ipAddress, userAgent)
	if err != nil {
		return nil, "", fmt.Errorf("create staff session: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    domain.AuditActorTypeStaff,
		ActorID:      &staff.ID,
		ActorName:    staff.Name,
		Action:       audit.AuditStaffLogin,
		ResourceType: "session",
		ResourceID:   session.ID,
	}); err != nil {
		return nil, "", fmt.Errorf("audit staff login: %w", err)
	}

	return session, rawToken, nil
}

// Logout revokes a session.
func (s *AuthService) Logout(ctx context.Context, tx pgx.Tx, sessionID, actorID uuid.UUID, actorName string, actorType domain.AuditActorType) error {
	if err := s.sessions.GetStore().Revoke(ctx, tx, sessionID); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actorType,
		ActorID:      &actorID,
		ActorName:    actorName,
		Action:       audit.AuditStaffLogout,
		ResourceType: "session",
		ResourceID:   sessionID,
	}); err != nil {
		return fmt.Errorf("audit logout: %w", err)
	}

	return nil
}

// GetStaffByID returns a staff member by ID.
func (s *AuthService) GetStaffByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Staff, error) {
	staff, err := s.staff.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrStaffNotFound
		}
		return nil, fmt.Errorf("get staff: %w", err)
	}
	return staff, nil
}

// ValidateSession looks up a session by raw token and returns it if valid.
func (s *AuthService) ValidateSession(ctx context.Context, tx pgx.Tx, rawToken string) (*domain.Session, error) {
	session, err := s.sessions.GetStore().GetByToken(ctx, tx, rawToken)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSessionExpired
		}
		return nil, fmt.Errorf("validate session: %w", err)
	}

	if time.Now().After(session.ExpiresAt) {
		return nil, ErrSessionExpired
	}

	return session, nil
}

// CustomerLogin authenticates a customer and creates a session.
func (s *AuthService) CustomerLogin(ctx context.Context, tx pgx.Tx, email, password string, rememberMe bool, ipAddress, userAgent *string) (*domain.Session, string, error) {
	customer, err := s.customers.GetByEmail(ctx, tx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrInvalidCredentials
		}
		return nil, "", fmt.Errorf("customer login lookup: %w", err)
	}

	if customer.PasswordHash == nil {
		return nil, "", ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(*customer.PasswordHash), []byte(password)); err != nil {
		return nil, "", ErrInvalidCredentials
	}

	return s.CreateCustomerSession(ctx, tx, customer.ID, customer.Email, "password", rememberMe, ipAddress, userAgent)
}

// CreateCustomerSession mints a customer session and records the appropriate
// login audit entry. This is the SOLE entry point for creating customer sessions —
// CustomerLogin, RedeemMagicLink, and any future OAuth login route through this.
//
// The audit entry uses Action=AuditCustomerLogin. Caller passes method which is
// recorded in audit metadata as {"method": method} (e.g. "password",
// "magic_link", "oauth"). actorName should be the customer email when known,
// otherwise empty string.
func (s *AuthService) CreateCustomerSession(
	ctx context.Context,
	tx pgx.Tx,
	customerID uuid.UUID,
	actorName string,
	method string,
	rememberMe bool,
	ipAddress, userAgent *string,
) (*domain.Session, string, error) {
	var duration time.Duration
	switch {
	case method == "magic_link":
		duration = MagicLinkSessionDuration
	case rememberMe:
		duration = sessions.CustomerRememberMeDuration
	default:
		duration = sessions.CustomerSessionDuration
	}

	expiresAt := time.Now().Add(duration)
	session, rawToken, err := s.sessions.GetStore().Create(ctx, tx, domain.SessionActorTypeCustomer, customerID, expiresAt, ipAddress, userAgent)
	if err != nil {
		return nil, "", fmt.Errorf("create customer session: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    domain.AuditActorTypeCustomer,
		ActorID:      &customerID,
		ActorName:    actorName,
		Action:       audit.AuditCustomerLogin,
		ResourceType: "session",
		ResourceID:   session.ID,
		Metadata:     map[string]any{"method": method},
	}); err != nil {
		return nil, "", fmt.Errorf("audit customer login: %w", err)
	}

	return session, rawToken, nil
}

// GetCustomerByID returns a customer by ID.
func (s *AuthService) GetCustomerByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Customer, error) {
	customer, err := s.customers.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer: %w", err)
	}
	return customer, nil
}

// --- Magic link authentication ---

// CreateMagicLinkToken generates a magic link token for a customer.
// Returns the raw token (for the email link) and the stored record.
// The raw token is NOT stored — only its SHA-256 hash is persisted.
func (s *AuthService) CreateMagicLinkToken(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (rawToken string, err error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate magic link token: %w", err)
	}
	rawToken = hex.EncodeToString(tokenBytes)

	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	expiresAt := time.Now().Add(MagicLinkDuration)
	_, err = s.magicLinks.Create(ctx, tx, customerID, tokenHash, store.MagicLinkPurposeDefault, expiresAt)
	if err != nil {
		return "", fmt.Errorf("store magic link token: %w", err)
	}

	return rawToken, nil
}

// RedeemMagicLink validates a magic link token and creates a session.
// The token is single-use and expires after MagicLinkDuration.
// If the customer's email is not yet verified, it is flipped to verified in the same transaction.
func (s *AuthService) RedeemMagicLink(ctx context.Context, tx pgx.Tx, rawToken string, ipAddress, userAgent *string) (*domain.Session, string, error) {
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	token, err := s.magicLinks.Redeem(ctx, tx, tokenHash, store.MagicLinkPurposeDefault)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrMagicLinkExpired
		}
		return nil, "", fmt.Errorf("redeem magic link: %w", err)
	}

	session, sessionToken, err := s.CreateCustomerSession(ctx, tx, token.CustomerID, "", "magic_link", false, ipAddress, userAgent)
	if err != nil {
		return nil, "", err
	}

	// Flip email_verified if not already set — idempotent.
	customer, err := s.customers.GetByID(ctx, tx, token.CustomerID)
	if err != nil {
		return nil, "", fmt.Errorf("get customer for email verification: %w", err)
	}
	if !customer.EmailVerified {
		if err := s.customers.UpdateEmailVerified(ctx, tx, token.CustomerID, true); err != nil {
			return nil, "", fmt.Errorf("update email verified: %w", err)
		}
		if err := s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "magic_link_redeem",
			Action:       audit.AuditCustomerEmailVerified,
			ResourceType: "customer",
			ResourceID:   token.CustomerID,
		}); err != nil {
			return nil, "", fmt.Errorf("audit email verified: %w", err)
		}
	}

	return session, sessionToken, nil
}

// CreateSetupToken generates a password-setup token for a wholesale customer.
// Uses the same magic_link_tokens table with a longer expiry.
func (s *AuthService) CreateSetupToken(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (rawToken string, err error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate setup token: %w", err)
	}
	rawToken = hex.EncodeToString(tokenBytes)

	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	expiresAt := time.Now().Add(SetupTokenDuration)
	_, err = s.magicLinks.Create(ctx, tx, customerID, tokenHash, store.MagicLinkPurposeDefault, expiresAt)
	if err != nil {
		return "", fmt.Errorf("store setup token: %w", err)
	}

	return rawToken, nil
}

// CreateWhiteLabelInviteToken generates a reusable white-label onboarding invite
// token for a customer. Returns the raw token (for the email link). The token is
// valid until it expires and may be used to submit multiple products (the flow
// validates it via LookupWhiteLabelInvite rather than consuming it). It has its
// own purpose so it can only be used by the white-label flow, never by the
// password-setup route.
func (s *AuthService) CreateWhiteLabelInviteToken(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (rawToken string, err error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate white-label invite token: %w", err)
	}
	rawToken = hex.EncodeToString(tokenBytes)

	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	expiresAt := time.Now().Add(WhiteLabelInviteDuration)
	_, err = s.magicLinks.Create(ctx, tx, customerID, tokenHash, store.MagicLinkPurposeWhiteLabelInvite, expiresAt)
	if err != nil {
		return "", fmt.Errorf("store white-label invite token: %w", err)
	}

	return rawToken, nil
}

// LookupWhiteLabelInvite validates an invite token without consuming it and
// returns the invited customer's ID. Use this to render the onboarding form.
// Returns ErrWhiteLabelInviteInvalid if the token is missing, expired, used, or
// of the wrong purpose.
func (s *AuthService) LookupWhiteLabelInvite(ctx context.Context, tx pgx.Tx, rawToken string) (uuid.UUID, error) {
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	token, err := s.magicLinks.Lookup(ctx, tx, tokenHash, store.MagicLinkPurposeWhiteLabelInvite)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrWhiteLabelInviteInvalid
		}
		return uuid.Nil, fmt.Errorf("lookup white-label invite: %w", err)
	}
	return token.CustomerID, nil
}

// RedeemWhiteLabelInvite consumes a white-label invite token (single-use) and
// returns the invited customer's ID. Returns ErrWhiteLabelInviteInvalid if the
// token is missing, expired, already used, or of the wrong purpose.
func (s *AuthService) RedeemWhiteLabelInvite(ctx context.Context, tx pgx.Tx, rawToken string) (uuid.UUID, error) {
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	token, err := s.magicLinks.Redeem(ctx, tx, tokenHash, store.MagicLinkPurposeWhiteLabelInvite)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrWhiteLabelInviteInvalid
		}
		return uuid.Nil, fmt.Errorf("redeem white-label invite: %w", err)
	}
	return token.CustomerID, nil
}

// CreateStaffInviteToken mints a single-use staff invite / password-setup token
// and returns the raw token for the email link. Staff tokens live in their own
// table (staff_invite_tokens) because magic_link_tokens is FK-locked to
// customers. Mirrors CreateSetupToken but binds to a staff row.
func (s *AuthService) CreateStaffInviteToken(ctx context.Context, tx pgx.Tx, staffID uuid.UUID) (rawToken string, err error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate staff invite token: %w", err)
	}
	rawToken = hex.EncodeToString(tokenBytes)

	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	expiresAt := time.Now().Add(StaffInviteDuration)
	if _, err := s.staffInvites.Create(ctx, tx, staffID, tokenHash, expiresAt); err != nil {
		return "", fmt.Errorf("store staff invite token: %w", err)
	}

	return rawToken, nil
}

// SetStaffPasswordWithToken validates a staff invite token and sets the staff
// member's password. The token is single-use. Used by the public /staff/setup
// page for both first-time setup and admin-triggered password resets. Returns
// ErrStaffInviteInvalid if the token is missing, expired, or already used, and
// ErrPasswordTooShort if the password is under 10 characters.
func (s *AuthService) SetStaffPasswordWithToken(ctx context.Context, tx pgx.Tx, rawToken, password string) (*domain.Staff, error) {
	if len(password) < 10 {
		return nil, ErrPasswordTooShort
	}

	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	token, err := s.staffInvites.Redeem(ctx, tx, tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrStaffInviteInvalid
		}
		return nil, fmt.Errorf("redeem staff invite token: %w", err)
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	if err := s.staff.UpdatePassword(ctx, tx, token.StaffID, string(passwordHash)); err != nil {
		return nil, fmt.Errorf("update staff password: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    domain.AuditActorTypeSystem,
		ActorName:    "staff_invite_redeem",
		Action:       audit.AuditStaffPasswordSet,
		ResourceType: "staff",
		ResourceID:   token.StaffID,
	}); err != nil {
		return nil, fmt.Errorf("audit staff password set: %w", err)
	}

	staff, err := s.staff.GetByID(ctx, tx, token.StaffID)
	if err != nil {
		return nil, fmt.Errorf("get staff after password set: %w", err)
	}
	return staff, nil
}

// SetPasswordWithToken validates a setup token and sets the customer's password.
// The token is single-use. If the customer's email is not yet verified, it is
// flipped to verified in the same transaction.
func (s *AuthService) SetPasswordWithToken(ctx context.Context, tx pgx.Tx, rawToken, password string) (*domain.Customer, error) {
	if len(password) < 10 {
		return nil, ErrPasswordTooShort
	}

	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	token, err := s.magicLinks.Redeem(ctx, tx, tokenHash, store.MagicLinkPurposeDefault)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSetupTokenExpired
		}
		return nil, fmt.Errorf("redeem setup token: %w", err)
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	if err := s.customers.UpdatePassword(ctx, tx, token.CustomerID, string(passwordHash)); err != nil {
		return nil, fmt.Errorf("update password: %w", err)
	}

	customer, err := s.customers.GetByID(ctx, tx, token.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("get customer: %w", err)
	}

	// Flip email_verified if not already set — idempotent.
	if !customer.EmailVerified {
		if err := s.customers.UpdateEmailVerified(ctx, tx, token.CustomerID, true); err != nil {
			return nil, fmt.Errorf("update email verified: %w", err)
		}
		if err := s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "setup_token_redeem",
			Action:       audit.AuditCustomerEmailVerified,
			ResourceType: "customer",
			ResourceID:   token.CustomerID,
		}); err != nil {
			return nil, fmt.Errorf("audit email verified: %w", err)
		}
		// Re-fetch so the returned customer reflects the verified state.
		customer, err = s.customers.GetByID(ctx, tx, token.CustomerID)
		if err != nil {
			return nil, fmt.Errorf("get customer after email verification: %w", err)
		}
	}

	return customer, nil
}

// SetPassword sets a customer's password without requiring the current one.
// CALLER MUST verify the request is from an authenticated session for this
// customer — the service does not re-check identity.
//
// Returns ErrPasswordTooShort if newPassword is fewer than 10 characters.
// Records AuditCustomerPasswordSet with the provided actor.
func (s *AuthService) SetPassword(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, newPassword string, actor Actor) error {
	if len(newPassword) < 10 {
		return ErrPasswordTooShort
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	if err := s.customers.UpdatePassword(ctx, tx, customerID, string(hash)); err != nil {
		return fmt.Errorf("update password: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerPasswordSet,
		ResourceType: "customer",
		ResourceID:   customerID,
	}); err != nil {
		return fmt.Errorf("audit set password: %w", err)
	}

	return nil
}

// ChangePassword updates a customer's password after verifying the current one.
// Returns ErrInvalidCredentials if currentPassword does not match the stored hash,
// or if no password is currently set. Returns ErrPasswordTooShort if newPassword
// is fewer than 10 characters.
//
// Records AuditCustomerPasswordChanged with the provided actor.
func (s *AuthService) ChangePassword(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, currentPassword, newPassword string, actor Actor) error {
	customer, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCustomerNotFound
		}
		return fmt.Errorf("get customer for password change: %w", err)
	}

	if customer.PasswordHash == nil {
		return ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(*customer.PasswordHash), []byte(currentPassword)); err != nil {
		return ErrInvalidCredentials
	}

	if len(newPassword) < 10 {
		return ErrPasswordTooShort
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}

	if err := s.customers.UpdatePassword(ctx, tx, customerID, string(hash)); err != nil {
		return fmt.Errorf("update password: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerPasswordChanged,
		ResourceType: "customer",
		ResourceID:   customerID,
	}); err != nil {
		return fmt.Errorf("audit change password: %w", err)
	}

	return nil
}
