package app

import (
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/ratelimit"
	"github.com/dukerupert/hiri/internal/platform/sessions"
	"github.com/dukerupert/hiri/internal/store"
)

// AuthService contains business logic for authentication and session management.
type AuthService struct {
	customers *store.CustomerStore
	sessions  *sessions.Manager
	limiter   *ratelimit.Limiter
	audit     *audit.AuditWriter
	metrics   *metrics.Registry
}

// NewAuthService creates a new AuthService.
func NewAuthService(
	customers *store.CustomerStore,
	sessions *sessions.Manager,
	limiter *ratelimit.Limiter,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *AuthService {
	return &AuthService{
		customers: customers,
		sessions:  sessions,
		limiter:   limiter,
		audit:     audit,
		metrics:   metrics,
	}
}
