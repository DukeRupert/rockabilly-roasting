package web

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

// siteBelongsTo is the ownership control on equipment.address_id.
//
// The column is a plain foreign key with nothing in the schema tying it to the
// machine's customer, and the value arrives from a form field anybody can edit.
// The scoped picker in the UI is a convenience; this is the control. It shipped
// once with no test at all, which is how a control quietly becomes a comment.
//
// The list handed to it always comes from ListAddresses scoped to the machine's
// customer, so these cases are the whole of the question.
func TestSiteBelongsTo(t *testing.T) {
	mine := domain.Address{ID: uuid.New()}
	alsoMine := domain.Address{ID: uuid.New()}
	theirs := domain.Address{ID: uuid.New()}
	owned := []domain.Address{mine, alsoMine}

	t.Run("an address on the account is accepted", func(t *testing.T) {
		assert.True(t, siteBelongsTo(owned, mine.ID))
		assert.True(t, siteBelongsTo(owned, alsoMine.ID))
	})

	t.Run("another customer's address is refused", func(t *testing.T) {
		assert.False(t, siteBelongsTo(owned, theirs.ID),
			"the whole point: a hand-altered form field must not file one cafe's machine at another's address")
	})

	t.Run("an address that does not exist is refused", func(t *testing.T) {
		assert.False(t, siteBelongsTo(owned, uuid.New()))
	})

	t.Run("no addresses on file refuses everything", func(t *testing.T) {
		// The safe direction. A customer with nothing on file cannot have a
		// machine filed at an address, so an empty list must not read as
		// "nothing to check against, let it through".
		assert.False(t, siteBelongsTo(nil, mine.ID))
		assert.False(t, siteBelongsTo([]domain.Address{}, mine.ID))
	})

	t.Run("the nil UUID is refused", func(t *testing.T) {
		// Not reachable today — resolveEquipmentSite returns early on a nil
		// pointer — but a zero id must never match by accident if that changes.
		assert.False(t, siteBelongsTo(owned, uuid.Nil))
	})
}
