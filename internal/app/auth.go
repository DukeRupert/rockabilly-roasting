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

// MagicLinkSessionDuration is the session lifetime for magic link logins.
const MagicLinkSessionDuration = 30 * 24 * time.Hour

// AuthService contains business logic for authentication and session management.
type AuthService struct {
	staff      *store.StaffStore
	customers  *store.CustomerStore
	magicLinks *store.MagicLinkStore
	sessions   *sessions.Manager
	audit      *audit.AuditWriter
	metrics    *metrics.Registry
	email      EmailEnv // populated via WithEmail; required for SendMagicLink
}

// NewAuthService creates a new AuthService.
func NewAuthService(
	staff *store.StaffStore,
	customers *store.CustomerStore,
	magicLinks *store.MagicLinkStore,
	sessions *sessions.Manager,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *AuthService {
	return &AuthService{
		staff:      staff,
		customers:  customers,
		magicLinks: magicLinks,
		sessions:   sessions,
		audit:      audit,
		metrics:    metrics,
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

	duration := sessions.CustomerSessionDuration
	if rememberMe {
		duration = sessions.CustomerRememberMeDuration
	}

	expiresAt := time.Now().Add(duration)
	session, rawToken, err := s.sessions.GetStore().Create(ctx, tx, domain.SessionActorTypeCustomer, customer.ID, expiresAt, ipAddress, userAgent)
	if err != nil {
		return nil, "", fmt.Errorf("create customer session: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    domain.AuditActorTypeCustomer,
		ActorID:      &customer.ID,
		ActorName:    customer.Email,
		Action:       audit.AuditCustomerLogin,
		ResourceType: "session",
		ResourceID:   session.ID,
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
	_, err = s.magicLinks.Create(ctx, tx, customerID, tokenHash, expiresAt)
	if err != nil {
		return "", fmt.Errorf("store magic link token: %w", err)
	}

	return rawToken, nil
}

// RedeemMagicLink validates a magic link token and creates a session.
// The token is single-use and expires after MagicLinkDuration.
func (s *AuthService) RedeemMagicLink(ctx context.Context, tx pgx.Tx, rawToken string, ipAddress, userAgent *string) (*domain.Session, string, error) {
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	token, err := s.magicLinks.Redeem(ctx, tx, tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrMagicLinkExpired
		}
		return nil, "", fmt.Errorf("redeem magic link: %w", err)
	}

	// Create a long-lived session (30 days).
	expiresAt := time.Now().Add(MagicLinkSessionDuration)
	session, sessionToken, err := s.sessions.GetStore().Create(ctx, tx, domain.SessionActorTypeCustomer, token.CustomerID, expiresAt, ipAddress, userAgent)
	if err != nil {
		return nil, "", fmt.Errorf("create magic link session: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    domain.AuditActorTypeCustomer,
		ActorID:      &token.CustomerID,
		ActorName:    "",
		Action:       audit.AuditCustomerLogin,
		ResourceType: "session",
		ResourceID:   session.ID,
		Metadata:     map[string]any{"method": "magic_link"},
	}); err != nil {
		return nil, "", fmt.Errorf("audit magic link login: %w", err)
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
	_, err = s.magicLinks.Create(ctx, tx, customerID, tokenHash, expiresAt)
	if err != nil {
		return "", fmt.Errorf("store setup token: %w", err)
	}

	return rawToken, nil
}

// SetPasswordWithToken validates a setup token and sets the customer's password.
// The token is single-use.
func (s *AuthService) SetPasswordWithToken(ctx context.Context, tx pgx.Tx, rawToken, password string) (*domain.Customer, error) {
	if len(password) < 10 {
		return nil, ErrPasswordTooShort
	}

	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	token, err := s.magicLinks.Redeem(ctx, tx, tokenHash)
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

	return customer, nil
}
