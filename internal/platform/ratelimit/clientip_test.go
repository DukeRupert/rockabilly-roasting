package ratelimit

import (
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
	r.Header.Set("X-Forwarded-For", "203.0.113.50, 10.0.0.1")

	assert.Equal(t, "203.0.113.50", ClientIP(r), "should use first XFF IP from trusted proxy")
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
