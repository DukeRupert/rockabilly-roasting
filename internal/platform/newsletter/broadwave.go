// Package newsletter subscribes storefront visitors to the Broadwave mailing
// list. It exists so the Broadwave API key stays server-side: the footer form
// posts to Hiri, Hiri posts to Broadwave. Before this package the form posted
// straight from the browser with the key in a hidden input, which put the
// credential in every page's source and left Hiri's rate limiter and honeypot
// out of the path entirely.
package newsletter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://broadwave.fireflysoftware.dev"

// ErrRejected is returned when Broadwave accepts the request but refuses the
// address (malformed, blocklisted, already unsubscribed-and-suppressed).
// Treat as a user-facing failure, not an outage.
var ErrRejected = errors.New("newsletter: address rejected")

// Client posts subscribe requests to Broadwave. A Client with an empty APIKey
// is a no-op — Subscribe returns nil without calling out. This mirrors the
// turnstile verifier so local dev and tests run without live credentials;
// production must set the key.
type Client struct {
	BaseURL string
	APIKey  string
	List    string
	HTTP    *http.Client
}

// New returns a Client with a 10-second HTTP timeout. baseURL may be empty to
// use the default Broadwave host. Pass an empty apiKey to disable (no-op mode).
func New(baseURL, apiKey, list string) *Client {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = defaultBaseURL
	}
	return &Client{
		BaseURL: base,
		APIKey:  strings.TrimSpace(apiKey),
		List:    strings.TrimSpace(list),
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Enabled reports whether the client will actually call Broadwave.
func (c *Client) Enabled() bool {
	return c != nil && c.APIKey != "" && c.List != ""
}

// Subscribe adds email to the configured list. Returns ErrRejected when
// Broadwave refuses the address, or a wrapped error on transport failures and
// unexpected status codes.
func (c *Client) Subscribe(ctx context.Context, email string) error {
	if !c.Enabled() {
		return nil
	}

	form := url.Values{}
	form.Set("api_key", c.APIKey)
	form.Set("list", c.List)
	form.Set("email", email)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/subscribe", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("newsletter: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("newsletter: subscribe call: %w", err)
	}
	defer resp.Body.Close()

	// Read a bounded prefix so a misbehaving upstream can't stream us to death.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<13))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 400:
		// 3xx included: the form previously relied on Broadwave's redirect-back
		// behavior, so a redirect here still means the subscribe landed.
		return nil
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity || resp.StatusCode == http.StatusConflict:
		return fmt.Errorf("%w: status %d: %s", ErrRejected, resp.StatusCode, strings.TrimSpace(string(body)))
	default:
		return fmt.Errorf("newsletter: subscribe status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}
