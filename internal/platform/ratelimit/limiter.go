package ratelimit

import "time"

// LimitConfig defines the parameters for a token-bucket rate limiter.
type LimitConfig struct {
	Capacity float64
	Rate     float64
	Window   time.Duration
}

// Predefined limit configurations.
var (
	LimitCustomerLoginIP    = LimitConfig{Capacity: 5, Rate: 0.1, Window: 15 * time.Minute}
	LimitCustomerLoginEmail = LimitConfig{Capacity: 5, Rate: 0.1, Window: 15 * time.Minute}
	LimitStaffLoginIP       = LimitConfig{Capacity: 3, Rate: 0.05, Window: 15 * time.Minute}
	LimitStaffLoginEmail    = LimitConfig{Capacity: 3, Rate: 0.05, Window: 15 * time.Minute}
	LimitCustomerRegisterIP = LimitConfig{Capacity: 10, Rate: 0.003, Window: time.Hour}
	LimitCustomerResetEmail = LimitConfig{Capacity: 3, Rate: 0.001, Window: time.Hour}
	LimitCustomerResetIP    = LimitConfig{Capacity: 10, Rate: 0.003, Window: time.Hour}
)

// Limiter provides rate limiting functionality.
type Limiter struct {
	store Store
}

// NewLimiter creates a new Limiter backed by the given store.
func NewLimiter(store Store) *Limiter {
	return &Limiter{store: store}
}

// Allow checks whether the given key is allowed under the specified config.
func (l *Limiter) Allow(key string, cfg LimitConfig) (bool, float64, time.Duration) {
	return l.store.Allow(key, cfg)
}
