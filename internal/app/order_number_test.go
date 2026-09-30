package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// orders.number is UNIQUE, and the renewal paths charge the card in a separate
// transaction before they write the order. So a number that will not insert is
// not a failed order — it is a customer charged with nothing to show for it, and
// no code path that undoes the charge.
//
// The renewal paths used to mint `SUB-<unix millis>`. River runs renewals
// concurrently against a due set the scheduler enqueues in one go, so two
// renewals sharing a millisecond was a question of how many subscriptions the
// shop has. Retail never had this: it has always used random bytes, and reached
// for the timestamp only if crypto/rand failed.
func TestNewOrderNumber(t *testing.T) {
	t.Run("carries its prefix", func(t *testing.T) {
		assert.True(t, strings.HasPrefix(newOrderNumber("SUB"), "SUB-"))
		assert.True(t, strings.HasPrefix(generateOrderNumber(), "ORD-"))
	})

	t.Run("a tight loop produces no duplicates", func(t *testing.T) {
		// The shape of the old defect: many numbers minted inside one clock
		// tick. A timestamp key fails this; 40 bits of randomness does not.
		seen := make(map[string]bool, 10000)
		for range 10000 {
			n := newOrderNumber("SUB")
			require.Falsef(t, seen[n], "minted %s twice", n)
			seen[n] = true
		}
	})

	t.Run("retail and renewal numbers cannot collide with each other", func(t *testing.T) {
		// Same generator, different prefix — a subscription order and a retail
		// order are never mistaken for one another in support or in the admin
		// search, which is the reason the prefixes exist.
		assert.NotEqual(t,
			strings.SplitN(newOrderNumber("SUB"), "-", 2)[0],
			strings.SplitN(generateOrderNumber(), "-", 2)[0])
	})
}
