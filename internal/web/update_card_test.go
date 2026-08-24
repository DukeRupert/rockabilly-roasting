package web_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/platform/auth"
)

// All three emailed one-click links are signed by the same secret, so the
// purpose is the only thing stopping a token minted for one from acting on
// another. That matters most here: update-card is the one purpose that opens a
// session on Stripe, so a skip-undo or pickup token that could be replayed
// against it would turn the two most innocuous links in our mail into a payment
// surface.
func TestUpdateCardTokenPurposeIsolation(t *testing.T) {
	signer := auth.NewOrderActionSigner("test-secret")
	id := uuid.New()
	now := time.Now()

	cardToken := signer.Sign(auth.OrderActionUpdateCard, id, now)
	undoToken := signer.Sign(auth.OrderActionUndoSkip, id, now)
	pickupToken := signer.Sign(auth.OrderActionSwitchToPickup, id, now)
	require.NotEqual(t, cardToken, undoToken)
	require.NotEqual(t, cardToken, pickupToken)

	got, err := signer.Verify(cardToken, auth.OrderActionUpdateCard, now)
	require.NoError(t, err)
	assert.Equal(t, id, got)

	// Nothing else verifies as an update-card token...
	for name, tok := range map[string]string{"undo_skip": undoToken, "pickup": pickupToken} {
		_, err = signer.Verify(tok, auth.OrderActionUpdateCard, now)
		assert.ErrorIsf(t, err, auth.ErrInvalidOrderActionToken, "%s token must not open the card form", name)
	}
	// ...and an update-card token is good for nothing else.
	for name, purpose := range map[string]auth.OrderActionPurpose{
		"undo_skip": auth.OrderActionUndoSkip,
		"pickup":    auth.OrderActionSwitchToPickup,
	} {
		_, err = signer.Verify(cardToken, purpose, now)
		assert.ErrorIsf(t, err, auth.ErrInvalidOrderActionToken, "card token must not act as %s", name)
	}
}

// The link's lifetime has to outlast the dunning ladder it is mailed from. The
// first notice goes out on day zero and the subscription is not given up on
// until day fourteen, so a TTL shorter than that would kill the customer's way
// back in while the subscription was still recoverable.
func TestUpdateCardTokenOutlivesDunningWindow(t *testing.T) {
	signer := auth.NewOrderActionSigner("test-secret")
	issued := time.Now()
	token := signer.Sign(auth.OrderActionUpdateCard, uuid.New(), issued)

	// Day 14: the last day the ladder can still be rescued.
	_, err := signer.Verify(token, auth.OrderActionUpdateCard, issued.Add(14*24*time.Hour-time.Minute))
	require.NoError(t, err, "the link must still work on the final day of the dunning window")

	// Well past the window it should be gone — an inbox is not a permanent key.
	_, err = signer.Verify(token, auth.OrderActionUpdateCard, issued.Add(30*24*time.Hour))
	assert.ErrorIs(t, err, auth.ErrOrderActionTokenExpired)
}

// A disabled signer (no ORDER_ACTION_SECRET configured) must mint nothing and
// verify nothing. The email templates check for the empty URL and fall back to
// the sign-in link, so the failure mode is a slightly worse email rather than a
// dead link — but only if Sign really does return "".
func TestUpdateCardTokenDisabledSigner(t *testing.T) {
	signer := auth.NewOrderActionSigner("")
	assert.False(t, signer.Enabled())
	assert.Empty(t, signer.Sign(auth.OrderActionUpdateCard, uuid.New(), time.Now()))

	_, err := signer.Verify("anything", auth.OrderActionUpdateCard, time.Now())
	assert.ErrorIs(t, err, auth.ErrInvalidOrderActionToken)
}
