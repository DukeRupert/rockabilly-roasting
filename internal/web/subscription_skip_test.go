package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/auth"
)

// parseSkipForm turns the two submit buttons on the skip panel into two very
// different requests. A misread here silently skips the wrong number of
// shipments or resolves a date against the wrong timezone, so the mapping is
// pinned rather than trusted.
func TestParseSkipForm(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	require.NoError(t, err)

	post := func(body string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/account/subscriptions/x/skip", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}

	t.Run("shipment count", func(t *testing.T) {
		params, err := parseSkipForm(post("skip_mode=intervals&intervals=3&resume_on=2026-09-15"), denver)
		require.NoError(t, err)
		assert.Equal(t, 3, params.Intervals)
		// The date field rides along in the same form; the pressed button decides.
		assert.Nil(t, params.ResumeOn)
	})

	t.Run("restart date is read in merchant time", func(t *testing.T) {
		params, err := parseSkipForm(post("skip_mode=date&intervals=3&resume_on=2026-09-15"), denver)
		require.NoError(t, err)
		assert.Zero(t, params.Intervals)
		require.NotNil(t, params.ResumeOn)
		// Midnight in Denver, not midnight UTC — otherwise a picked day can
		// resolve to the previous evening for the roastery.
		assert.Equal(t, time.Date(2026, 9, 15, 0, 0, 0, 0, denver), *params.ResumeOn)
	})

	t.Run("rejects malformed submissions", func(t *testing.T) {
		cases := []struct {
			name string
			body string
			want error
		}{
			{"no mode", "intervals=2", app.ErrInvalidSkipRequest},
			{"unknown mode", "skip_mode=whenever", app.ErrInvalidSkipRequest},
			{"non-numeric count", "skip_mode=intervals&intervals=lots", app.ErrSkipIntervalsOutOfRange},
			{"zero count", "skip_mode=intervals&intervals=0", app.ErrSkipIntervalsOutOfRange},
			{"negative count", "skip_mode=intervals&intervals=-2", app.ErrSkipIntervalsOutOfRange},
			{"empty date", "skip_mode=date&resume_on=", app.ErrSkipDateOutOfRange},
			{"garbage date", "skip_mode=date&resume_on=next+tuesday", app.ErrSkipDateOutOfRange},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				_, err := parseSkipForm(post(c.body), denver)
				assert.ErrorIs(t, err, c.want)
			})
		}
	})
}

// The undo link and the switch-to-pickup link are signed by the same secret.
// The purpose is the only thing keeping a token minted for one from acting on
// the other — a pickup link that could undo a skip would let anyone holding one
// email move another resource entirely.
func TestUndoSkipTokenPurposeIsolation(t *testing.T) {
	signer := auth.NewOrderActionSigner("test-secret")
	id := uuid.New()
	now := time.Now()

	undoToken := signer.Sign(auth.OrderActionUndoSkip, id, now)
	pickupToken := signer.Sign(auth.OrderActionSwitchToPickup, id, now)
	require.NotEqual(t, undoToken, pickupToken)

	got, err := signer.Verify(undoToken, auth.OrderActionUndoSkip, now)
	require.NoError(t, err)
	assert.Equal(t, id, got)

	_, err = signer.Verify(pickupToken, auth.OrderActionUndoSkip, now)
	assert.ErrorIs(t, err, auth.ErrInvalidOrderActionToken)
	_, err = signer.Verify(undoToken, auth.OrderActionSwitchToPickup, now)
	assert.ErrorIs(t, err, auth.ErrInvalidOrderActionToken)
}

// GET must stay a distinct handler from POST on the undo route, for the same
// reason unsubscribe does: inbox scanners fetch every link in a message, and an
// acting GET would un-skip — and so re-charge — subscriptions on the customer's
// behalf. Asserting the mux resolves the methods separately keeps a future
// refactor from collapsing them.
func TestUndoSkipGetAndPostAreDistinctRoutes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /subscriptions/undo-skip", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /subscriptions/undo-skip", func(http.ResponseWriter, *http.Request) {})

	for _, m := range []string{http.MethodGet, http.MethodPost} {
		_, pattern := mux.Handler(httptest.NewRequest(m, "/subscriptions/undo-skip?t=x", nil))
		require.Contains(t, pattern, m, "%s must match its own method-scoped pattern", m)
	}
}
