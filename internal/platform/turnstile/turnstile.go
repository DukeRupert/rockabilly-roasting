// Package turnstile verifies Cloudflare Turnstile CAPTCHA tokens against
// Cloudflare's siteverify endpoint. It guards public form submissions
// (currently /wholesale/apply) against bot signups.
package turnstile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const siteVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// ErrInvalidToken is returned when Cloudflare reports the token as invalid,
// expired, or already used. Treat as a user-facing failure (resubmit needed).
var ErrInvalidToken = errors.New("turnstile: invalid token")

// Verifier checks Turnstile tokens. A Verifier with empty SecretKey is a
// no-op — Verify always returns nil. This lets local dev and tests run
// without a real Turnstile configuration; production must set the key.
type Verifier struct {
	SecretKey string
	HTTP      *http.Client
}

// New returns a Verifier with a 5-second HTTP timeout. Pass an empty secret
// to disable verification (no-op mode).
func New(secret string) *Verifier {
	return &Verifier{
		SecretKey: strings.TrimSpace(secret),
		HTTP:      &http.Client{Timeout: 5 * time.Second},
	}
}

// Enabled reports whether the verifier will actually call siteverify.
func (v *Verifier) Enabled() bool {
	return v != nil && v.SecretKey != ""
}

type siteVerifyResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
	Hostname   string   `json:"hostname"`
	Action     string   `json:"action"`
}

// Verify exchanges a client-supplied token for a yes/no decision from
// Cloudflare. remoteIP is optional but recommended — Cloudflare uses it as
// a signal. Returns ErrInvalidToken on a "success: false" response, or a
// wrapped error on transport failures.
func (v *Verifier) Verify(ctx context.Context, token, remoteIP string) error {
	if !v.Enabled() {
		return nil
	}
	if strings.TrimSpace(token) == "" {
		return ErrInvalidToken
	}

	form := url.Values{}
	form.Set("secret", v.SecretKey)
	form.Set("response", token)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, siteVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("turnstile: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("turnstile: siteverify call: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
	if err != nil {
		return fmt.Errorf("turnstile: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("turnstile: siteverify status %d: %s", resp.StatusCode, body)
	}

	var parsed siteVerifyResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("turnstile: decode response: %w", err)
	}
	if !parsed.Success {
		return fmt.Errorf("%w: %s", ErrInvalidToken, strings.Join(parsed.ErrorCodes, ","))
	}
	return nil
}
