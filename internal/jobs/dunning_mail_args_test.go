package jobs

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A past-due notice or ended mail queued before the args named several
// subscriptions has only the single id. It must still send, about that one.
func TestDunningMailArgs_AJobQueuedBeforeTheListStillNamesItsSubscription(t *testing.T) {
	id := uuid.New()
	var old SubscriptionPastDueArgs
	require.NoError(t, json.Unmarshal([]byte(`{"subscription_id":"`+id.String()+`","customer_id":"`+uuid.NewString()+`","stage":1}`), &old))
	assert.Equal(t, []uuid.UUID{id}, argIDs(old.SubscriptionID, old.SubscriptionIDs))

	a, b := uuid.New(), uuid.New()
	assert.Equal(t, []uuid.UUID{a, b}, argIDs(a, []uuid.UUID{a, b}), "the list wins when set")
}
