package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// StaffInviteDuration is how long a staff invite / password-reset link is valid.
// Matches the wholesale setup-token window. Consumed by AuthService, which owns
// the staff-token lifecycle (mint + redeem).
const StaffInviteDuration = 72 * time.Hour

// StaffService manages internal staff accounts: inviting new members, changing
// roles, and activating/deactivating accounts. New staff are onboarded via an
// email invite link (they set their own password) rather than an admin-chosen
// password, so no plaintext password is ever passed around. Invite-token minting
// and password-set redemption live in AuthService (the credential-lifecycle
// boundary); StaffService delegates to it, mirroring WhiteLabelService.
type StaffService struct {
	staff   *store.StaffStore
	audit   *audit.AuditWriter
	metrics *metrics.Registry
	email   EmailEnv     // populated via WithEmail; required for SendInviteEmail
	auth    *AuthService // populated via WithEmail; mints staff invite tokens
}

// NewStaffService creates a new StaffService.
func NewStaffService(staff *store.StaffStore, audit *audit.AuditWriter, metrics *metrics.Registry) *StaffService {
	return &StaffService{
		staff:   staff,
		audit:   audit,
		metrics: metrics,
	}
}

// WithEmail attaches the email environment and AuthService needed by
// SendInviteEmail (which mints its invite token through AuthService).
func (s *StaffService) WithEmail(env EmailEnv, auth *AuthService) *StaffService {
	s.email = env
	s.auth = auth
	return s
}

// InviteStaffParams describes a new staff member to invite.
type InviteStaffParams struct {
	Email string
	Name  string
	Role  domain.StaffRole
}

// List returns staff members ordered by name.
func (s *StaffService) List(ctx context.Context, tx pgx.Tx, limit, offset int32) ([]domain.Staff, error) {
	return s.staff.List(ctx, tx, limit, offset)
}

// Get returns a single staff member, mapping a missing row to ErrStaffNotFound.
func (s *StaffService) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Staff, error) {
	st, err := s.staff.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrStaffNotFound
		}
		return nil, fmt.Errorf("get staff: %w", err)
	}
	return st, nil
}

// Invite creates a new staff member with an unusable random password and records
// the creation. The caller is responsible for enqueuing the invite email job
// (SendInviteEmail) after the transaction commits. Returns ErrStaffEmailExists
// if a staff member with the same email already exists.
func (s *StaffService) Invite(ctx context.Context, tx pgx.Tx, params InviteStaffParams, actor Actor) (*domain.Staff, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return nil, ErrStaffNameRequired
	}
	emailAddr := domain.NormalizeEmail(params.Email)
	if emailAddr == "" {
		return nil, ErrStaffEmailRequired
	}
	if !params.Role.Valid() {
		return nil, ErrInvalidStaffRole
	}

	// Reject duplicate emails up front (the DB also enforces this via a unique
	// index, but this gives a clean sentinel error).
	if _, err := s.staff.GetByEmail(ctx, tx, emailAddr); err == nil {
		return nil, ErrStaffEmailExists
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check existing staff email: %w", err)
	}

	// The account is created with an unusable password; the invited member sets a
	// real one via the invite link. StaffLogin uses bcrypt.CompareHashAndPassword,
	// which can never match this random hash.
	placeholder, err := unusablePasswordHash()
	if err != nil {
		return nil, err
	}

	staff, err := s.staff.Create(ctx, tx, sqlcgen.CreateStaffParams{
		ID:           uuid.New(),
		Email:        emailAddr,
		Name:         name,
		PasswordHash: placeholder,
		Role:         string(params.Role),
	})
	if err != nil {
		return nil, fmt.Errorf("create staff: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditStaffCreated,
		ResourceType: "staff",
		ResourceID:   staff.ID,
		// Deliberately not recording the full Staff object — it carries the
		// password hash. Metadata captures the meaningful fields only.
		Metadata: map[string]any{
			"email": staff.Email,
			"name":  staff.Name,
			"role":  string(staff.Role),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit staff created: %w", err)
	}

	return staff, nil
}

// ChangeRole updates a staff member's role. Guards against an admin changing
// their own role (lockout) and against demoting the last active admin.
func (s *StaffService) ChangeRole(ctx context.Context, tx pgx.Tx, id uuid.UUID, newRole domain.StaffRole, actor Actor) error {
	if !newRole.Valid() {
		return ErrInvalidStaffRole
	}
	if actor.ID != nil && *actor.ID == id {
		return ErrCannotModifySelf
	}

	target, err := s.staff.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStaffNotFound
		}
		return fmt.Errorf("get staff: %w", err)
	}

	if target.Role == newRole {
		return nil // no-op
	}

	// Demoting the last active admin would lock everyone out of staff management.
	if target.Role == domain.StaffRoleAdmin && target.IsActive {
		if err := s.ensureNotLastActiveAdmin(ctx, tx); err != nil {
			return err
		}
	}

	if err := s.staff.UpdateRole(ctx, tx, id, string(newRole)); err != nil {
		return err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditStaffRoleChanged,
		ResourceType: "staff",
		ResourceID:   id,
		Metadata: map[string]any{
			"email": target.Email,
			"from":  string(target.Role),
			"to":    string(newRole),
		},
	}); err != nil {
		return fmt.Errorf("audit staff role changed: %w", err)
	}

	return nil
}

// SetActive activates or deactivates a staff member. Guards against a staff
// member deactivating themselves and against deactivating the last active admin.
func (s *StaffService) SetActive(ctx context.Context, tx pgx.Tx, id uuid.UUID, active bool, actor Actor) error {
	if !active && actor.ID != nil && *actor.ID == id {
		return ErrCannotModifySelf
	}

	target, err := s.staff.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStaffNotFound
		}
		return fmt.Errorf("get staff: %w", err)
	}

	if target.IsActive == active {
		return nil // no-op
	}

	if !active && target.Role == domain.StaffRoleAdmin {
		if err := s.ensureNotLastActiveAdmin(ctx, tx); err != nil {
			return err
		}
	}

	if err := s.staff.UpdateActive(ctx, tx, id, active); err != nil {
		return err
	}

	action := audit.AuditStaffActivated
	if !active {
		action = audit.AuditStaffDeactivated
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       action,
		ResourceType: "staff",
		ResourceID:   id,
		Metadata:     map[string]any{"email": target.Email},
	}); err != nil {
		return fmt.Errorf("audit staff active change: %w", err)
	}

	return nil
}

// ensureNotLastActiveAdmin returns ErrLastActiveAdmin if there is only one
// active admin left. Call before demoting or deactivating an active admin.
func (s *StaffService) ensureNotLastActiveAdmin(ctx context.Context, tx pgx.Tx) error {
	n, err := s.staff.CountActiveByRole(ctx, tx, string(domain.StaffRoleAdmin))
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastActiveAdmin
	}
	return nil
}

// SendInviteEmail mints an invite token for a staff member and emails them the
// setup link. Token creation is tx 1, the send is outside any tx, the audit is
// tx 2 — the same shape as WhiteLabelService.SendInviteEmail. The token is
// minted through AuthService (the credential-lifecycle boundary). Idempotent-
// safe: re-running (a resend) simply issues a fresh token.
func (s *StaffService) SendInviteEmail(ctx context.Context, pool *pgxpool.Pool, staffID uuid.UUID) error {
	var (
		staff    *domain.Staff
		rawToken string
	)
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		st, err := s.staff.GetByID(ctx, tx, staffID)
		if err != nil {
			return fmt.Errorf("get staff %s: %w", staffID, err)
		}
		staff = st
		token, err := s.auth.CreateStaffInviteToken(ctx, tx, staffID)
		if err != nil {
			return err
		}
		rawToken = token
		return nil
	}); err != nil {
		return err
	}

	inviteURL := fmt.Sprintf("%s/staff/setup?token=%s", s.email.BaseURL, rawToken)
	html, text, err := s.email.Renderer.Render("staff_invite", emailtemplates.StaffInviteData{
		StaffName: staff.Name,
		InviteURL: inviteURL,
		StoreName: s.email.StoreName,
		StoreURL:  s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("staff_invite", "failed").Inc()
		return fmt.Errorf("render staff invite template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      staff.Email,
		Subject: "Set up your Rockabilly staff account",
		HTML:    html,
		Text:    text,
		Tag:     "staff-invite",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("staff_invite", "failed").Inc()
		return fmt.Errorf("send staff invite email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "staff_invite_worker",
			Action:       audit.AuditEmailStaffInvite,
			ResourceType: "staff",
			ResourceID:   staff.ID,
			Metadata:     map[string]any{"email": staff.Email},
		})
	}); err != nil {
		return fmt.Errorf("audit staff invite email: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("staff_invite", "sent").Inc()
	return nil
}

// unusablePasswordHash returns a bcrypt hash of a random secret. No password
// entered by a user will ever match it, so a freshly-invited account cannot be
// logged into until the member sets a real password via the invite link.
func unusablePasswordHash() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate placeholder password: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(secret)), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash placeholder password: %w", err)
	}
	return string(hash), nil
}
