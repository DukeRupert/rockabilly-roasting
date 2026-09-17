package ratelimit

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientIP_NoTrustedProxies(t *testing.T) {
	// With no trusted proxies configured, forwarded headers are ignored.
	trustedMu.Lock()
	trustedProxies = nil
	trustedMu.Unlock()

	r := &http.Request{
		RemoteAddr: "203.0.113.1:12345",
		Header:     http.Header{},
	}
	r.Header.Set("X-Forwarded-For", "10.0.0.1")
	r.Header.Set("X-Real-IP", "10.0.0.2")

	assert.Equal(t, "203.0.113.1", ClientIP(r), "should use RemoteAddr when no proxies configured")
}

func TestClientIP_TrustedProxy_XFF(t *testing.T) {
	require.NoError(t, SetTrustedProxies([]string{"127.0.0.1/32"}))
	defer SetTrustedProxies(nil) //nolint:errcheck

	r := &http.Request{
		RemoteAddr: "127.0.0.1:54321",
		Header:     http.Header{},
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.50")

	assert.Equal(t, "203.0.113.50", ClientIP(r), "should use the XFF entry our proxy wrote")
}

// The left-most X-Forwarded-For entry is whatever the caller claimed. Reading
// it positionally is the classic spoofing footgun, and this value keys the rate
// limiter — so a visitor could pin abuse on an arbitrary address, or evade a
// limiter by rotating the header.
func TestClientIP_TrustedProxy_IgnoresForgedXFFPrefix(t *testing.T) {
	require.NoError(t, SetTrustedProxies([]string{"127.0.0.1/32"}))
	defer SetTrustedProxies(nil) //nolint:errcheck

	r := &http.Request{
		RemoteAddr: "127.0.0.1:54321",
		Header:     http.Header{},
	}
	r.Header.Set("X-Forwarded-For", "9.9.9.9, 8.8.8.8, 203.0.113.50")

	assert.Equal(t, "203.0.113.50", ClientIP(r), "should take the right-most entry, not the claimed original client")
}

func TestClientIP_StripsPort(t *testing.T) {
	require.NoError(t, SetTrustedProxies([]string{"127.0.0.1/32"}))
	defer SetTrustedProxies(nil) //nolint:errcheck

	r := &http.Request{
		RemoteAddr: "127.0.0.1:54321",
		Header:     http.Header{},
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.50:51252")

	assert.Equal(t, "203.0.113.50", ClientIP(r), "a port would give the same visitor a new identity per connection")
}

func TestClientIP_IPv6RemoteAddr(t *testing.T) {
	trustedMu.Lock()
	trustedProxies = nil
	trustedMu.Unlock()

	r := &http.Request{
		RemoteAddr: "[2001:db8::1]:51252",
		Header:     http.Header{},
	}

	assert.Equal(t, "2001:db8::1", ClientIP(r), "should strip brackets and port from an IPv6 peer")
}

func TestClientIP_TrustedProxy_XRealIP(t *testing.T) {
	require.NoError(t, SetTrustedProxies([]string{"10.0.0.0/8"}))
	defer SetTrustedProxies(nil) //nolint:errcheck

	r := &http.Request{
		RemoteAddr: "10.0.0.1:54321",
		Header:     http.Header{},
	}
	r.Header.Set("X-Real-IP", "203.0.113.99")

	assert.Equal(t, "203.0.113.99", ClientIP(r), "should use X-Real-IP from trusted proxy")
}

func TestClientIP_UntrustedProxy_IgnoresHeaders(t *testing.T) {
	require.NoError(t, SetTrustedProxies([]string{"10.0.0.0/8"}))
	defer SetTrustedProxies(nil) //nolint:errcheck

	r := &http.Request{
		RemoteAddr: "203.0.113.1:12345",
		Header:     http.Header{},
	}
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("X-Real-IP", "5.6.7.8")

	assert.Equal(t, "203.0.113.1", ClientIP(r), "should ignore forwarded headers from untrusted source")
}

func TestClientIP_NoHeaders_FallsBackToRemoteAddr(t *testing.T) {
	require.NoError(t, SetTrustedProxies([]string{"127.0.0.1/32"}))
	defer SetTrustedProxies(nil) //nolint:errcheck

	r := &http.Request{
		RemoteAddr: "127.0.0.1:54321",
		Header:     http.Header{},
	}

	assert.Equal(t, "127.0.0.1", ClientIP(r), "should fall back to RemoteAddr when no forwarded headers")
}

func TestSetTrustedProxies_InvalidCIDR(t *testing.T) {
	err := SetTrustedProxies([]string{"not-a-cidr"})
	assert.Error(t, err)
}

// A peer that forwards but is not trusted means a reverse proxy is in front of
// us and we are discarding what it tells us — every request then resolves to
// that same peer and every per-IP limit collapses into one global bucket. This
// happened in production and went unnoticed for months because the limiter
// keeps working, just globally. The report is what makes it loud.
func TestClientIP_ReportsUntrustedForwarder(t *testing.T) {
	trustedMu.Lock()
	trustedProxies = nil
	trustedMu.Unlock()
	directlyExposed.Store(false)
	reportedUntrusted.Store(false)
	t.Cleanup(func() { reportedUntrusted.Store(false); directlyExposed.Store(false) })

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	r := &http.Request{RemoteAddr: "172.19.0.1:41234", Header: http.Header{}}
	r.Header.Set("X-Forwarded-For", "203.0.113.44")

	assert.Equal(t, "172.19.0.1", ClientIP(r), "an untrusted peer's header is still ignored")

	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	assert.Equal(t, "ERROR", out["level"], "this needs a human — per-IP limiting is inert until it is fixed")
	assert.Equal(t, "untrusted proxy forwarding", out["msg"])
	assert.Equal(t, "172.19.0.1", out["peer"], "the report must name the address TRUSTED_PROXIES needs to contain")

	// Once per process: this is a static misconfiguration, and repeating it on
	// every request would bury the line that names the fix.
	buf.Reset()
	ClientIP(r)
	assert.Empty(t, buf.String(), "the report must not repeat")
}

// A service genuinely exposed to the internet sees client sockets directly, so
// the fallback is correct there and the report would be noise.
func TestClientIP_DirectlyExposedSilencesReport(t *testing.T) {
	trustedMu.Lock()
	trustedProxies = nil
	trustedMu.Unlock()
	reportedUntrusted.Store(false)
	SetDirectlyExposed()
	t.Cleanup(func() { reportedUntrusted.Store(false); directlyExposed.Store(false) })

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	r := &http.Request{RemoteAddr: "203.0.113.1:41234", Header: http.Header{}}
	r.Header.Set("X-Forwarded-For", "9.9.9.9")

	assert.Equal(t, "203.0.113.1", ClientIP(r))
	assert.Empty(t, buf.String(), "no report when the socket address is the right answer")
}

// The common case must stay quiet: a correctly configured proxy forwards, is
// trusted, and nothing is reported.
func TestClientIP_TrustedForwarderIsNotReported(t *testing.T) {
	require.NoError(t, SetTrustedProxies([]string{"172.19.0.0/16"}))
	reportedUntrusted.Store(false)
	t.Cleanup(func() { SetTrustedProxies(nil); reportedUntrusted.Store(false) }) //nolint:errcheck

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	r := &http.Request{RemoteAddr: "172.19.0.1:41234", Header: http.Header{}}
	r.Header.Set("X-Forwarded-For", "203.0.113.44")

	assert.Equal(t, "203.0.113.44", ClientIP(r))
	assert.Empty(t, buf.String(), "a working configuration must not warn")
}
