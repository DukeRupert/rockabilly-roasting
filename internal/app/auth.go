package app

import (
	"context"
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

// AuthService contains business logic for authentication and session management.
type AuthService struct {
	staff     *store.StaffStore
	customers *store.CustomerStore
	sessions  *sessions.Manager
	audit     *audit.AuditWriter
	metrics   *metrics.Registry
}

// NewAuthService creates a new AuthService.
func NewAuthService(
	staff *store.StaffStore,
	customers *store.CustomerStore,
	sessions *sessions.Manager,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *AuthService {
	return &AuthService{
		staff:     staff,
		customers: customers,
		sessions:  sessions,
		audit:     audit,
		metrics:   metrics,
	}
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
