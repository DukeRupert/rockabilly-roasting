package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/platform/quickbooks"
)

// The audit log is readable by every staffer who can open the audit page, and
// by anyone who reads a database backup. Neither QuickBooks secret may ever
// reach it — so the payload is pinned by exact key set, not by "does not
// contain the secret": a test that only checked the latter would pass on the
// day someone adds a third field carrying something else sensitive.
func TestQBAppConfigAuditPayloadCarriesNoSecrets(t *testing.T) {
	payload := qbAppConfigAuditPayload(quickbooks.AppConfigInput{
		ClientID:        "  ABclientId  ",
		ClientSecret:    "sk_the_client_secret",
		WebhookVerifier: "the_webhook_verifier",
		Environment:     " production ",
	})

	require.ElementsMatch(t, []string{"client_id", "environment"}, keysOf(payload))
	assert.Equal(t, "ABclientId", payload["client_id"], "trimmed, as it is stored")
	assert.Equal(t, "production", payload["environment"])

	// Belt as well as braces: no value anywhere in the payload is either secret.
	for key, value := range payload {
		str, ok := value.(string)
		require.True(t, ok, "%s should be a string", key)
		assert.NotContains(t, str, "sk_the_client_secret")
		assert.NotContains(t, str, "the_webhook_verifier")
	}
}

// The entry has to answer "whose books is this shop about to write into".
// Dropping either field would leave it unable to.
func TestQBAppConfigAuditPayloadIdentifiesTheCompany(t *testing.T) {
	payload := qbAppConfigAuditPayload(quickbooks.AppConfigInput{
		ClientID:    "ABclientId",
		Environment: "production",
	})

	assert.NotEmpty(t, payload["client_id"])
	assert.NotEmpty(t, payload["environment"])
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
