package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretsDisabledWithoutAppSecret(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n\t"} {
		s := NewSecrets(raw)
		assert.False(t, s.Enabled(), "secret %q should be treated as unset", raw)

		key, err := s.Key(PurposeUnsubscribe, 32)
		require.NoError(t, err)
		assert.Nil(t, key)

		str, err := s.Secret(PurposeUnsubscribe)
		require.NoError(t, err)
		assert.Empty(t, str)
	}
}

func TestSecretsDerivationIsDeterministic(t *testing.T) {
	// Two processes booted from the same APP_SECRET must sign identically —
	// otherwise a restart would invalidate links already in customers' inboxes.
	a, err := NewSecrets("correct horse battery staple").Key(PurposeOrderAction, 32)
	require.NoError(t, err)
	b, err := NewSecrets("correct horse battery staple").Key(PurposeOrderAction, 32)
	require.NoError(t, err)

	assert.Equal(t, a, b)
	assert.Len(t, a, 32)
}

func TestSecretsPurposesAreIndependent(t *testing.T) {
	s := NewSecrets("correct horse battery staple")

	unsub, err := s.Key(PurposeUnsubscribe, 32)
	require.NoError(t, err)
	order, err := s.Key(PurposeOrderAction, 32)
	require.NoError(t, err)
	qb, err := s.Key(PurposeQBTokenEncryption, 32)
	require.NoError(t, err)

	assert.NotEqual(t, unsub, order)
	assert.NotEqual(t, unsub, qb)
	assert.NotEqual(t, order, qb)
}

func TestSecretsDifferentMastersDeriveDifferentKeys(t *testing.T) {
	a, err := NewSecrets("one").Key(PurposeQBTokenEncryption, 32)
	require.NoError(t, err)
	b, err := NewSecrets("two").Key(PurposeQBTokenEncryption, 32)
	require.NoError(t, err)

	assert.NotEqual(t, a, b)
}

func TestSecretsDerivedKeyIsUsableByTheSigners(t *testing.T) {
	// The QB client wants raw 32 bytes; the signers want a string. Both shapes
	// come off the same master.
	s := NewSecrets("correct horse battery staple")

	raw, err := s.Key(PurposeQBTokenEncryption, 32)
	require.NoError(t, err)
	assert.Len(t, raw, 32)

	str, err := s.Secret(PurposeUnsubscribe)
	require.NoError(t, err)
	assert.True(t, NewUnsubscribeSigner(str).Enabled())
	assert.True(t, NewOrderActionSigner(str).Enabled())
}
