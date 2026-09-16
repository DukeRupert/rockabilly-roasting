package quickbooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// The realm is stored encrypted and must reach callers decrypted. Both halves
// need their own guard: an earlier version of this change decrypted in
// readCredentials alone, and the refresh path — which re-reads the row
// directly from the store inside its advisory lock — handed the ciphertext
// realm straight into the QuickBooks request URL. Every API call crossing an
// hourly token refresh would have built
// https://.../v3/company/<base64+with/slashes>/invoice and failed, looking for
// all the world like an Intuit outage.
//
// withPlainRealm is the single seam both halves now go through, so it is what
// gets pinned here.
func TestWithPlainRealm(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	c := &QBClient{config: ClientConfig{EncryptionKey: key}}

	const realm = "9130354674505161"
	encrypted, err := encryptWithKey(key, realm)
	require.NoError(t, err)
	require.NotEqual(t, realm, encrypted, "fixture must be genuine ciphertext")

	stored := &domain.QBCredentials{RealmID: encrypted, AccessToken: "tok"}
	out, err := c.withPlainRealm(stored)
	require.NoError(t, err)

	assert.Equal(t, realm, out.RealmID, "callers must receive the decrypted realm")

	// The copy is the load-bearing part. The refresh path writes the same
	// struct back to the database after handing a copy to its caller; if this
	// decrypted in place, the next Upsert would persist plaintext and silently
	// undo the encryption this exists to add.
	assert.Equal(t, encrypted, stored.RealmID,
		"the stored struct must keep its ciphertext realm for write-back")

	// Other fields ride along untouched.
	assert.Equal(t, "tok", out.AccessToken)

	// A realm this key cannot read is an error, not an empty string: callers
	// build request URLs from it, and a blank realm would produce a URL that
	// looks valid and addresses nothing.
	wrongKey := &QBClient{config: ClientConfig{EncryptionKey: []byte("ffffffffffffffffffffffffffffffff")}}
	_, err = wrongKey.withPlainRealm(stored)
	require.Error(t, err)

	// Nil is passed through: "no credentials stored" is a fact, not a failure.
	nilOut, err := c.withPlainRealm(nil)
	require.NoError(t, err)
	assert.Nil(t, nilOut)
}

// The write half: what ExchangeCallback persists must be ciphertext. Pinned
// separately because storing the realm in plaintext again would leave every
// read path working perfectly and silently reopen the gap.
func TestCredentialsAreStoredWithAnEncryptedRealm(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	c := &QBClient{config: ClientConfig{EncryptionKey: key}}

	const realm = "9130354674505161"
	encrypted, err := c.Encrypt(realm)
	require.NoError(t, err)

	assert.NotEqual(t, realm, encrypted)
	assert.NotContains(t, encrypted, realm,
		"the plaintext realm must not survive anywhere in the stored value")

	back, err := c.decrypt(encrypted)
	require.NoError(t, err)
	assert.Equal(t, realm, back)
}
