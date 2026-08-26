package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

func TestEquipmentDescription(t *testing.T) {
	assert.Equal(t, "La Marzocco Linea PB",
		domain.Equipment{Make: "La Marzocco", Model: "Linea PB"}.Description())

	// Model is optional — plenty of machines get logged off the badge alone,
	// and "La Marzocco " with a trailing space is not a machine name.
	assert.Equal(t, "La Marzocco", domain.Equipment{Make: "La Marzocco"}.Description())
}

func TestEquipmentUnderWarranty(t *testing.T) {
	today := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	future := today.AddDate(0, 6, 0)
	past := today.AddDate(0, -1, 0)

	assert.True(t, domain.Equipment{WarrantyExpiresOn: &future}.UnderWarranty(today))
	assert.False(t, domain.Equipment{WarrantyExpiresOn: &past}.UnderWarranty(today))

	// The expiry day itself is still covered — a warranty that runs "until the
	// 25th" covers the 25th.
	assert.True(t, domain.Equipment{WarrantyExpiresOn: &today}.UnderWarranty(today))

	// An unknown warranty is not a warranty. Quoting a repair as covered when
	// it is not costs the shop the money; the reverse only costs a phone call.
	assert.False(t, domain.Equipment{}.UnderWarranty(today))
}

func TestEquipmentEnumsValidateAgainstTheirDatabaseCheck(t *testing.T) {
	// Must match the CHECK constraints in migration 074.
	for _, c := range domain.EquipmentCategories() {
		assert.True(t, c.Valid(), "%s", c)
		assert.NotEmpty(t, c.Label())
	}
	for _, o := range domain.EquipmentOwnerships() {
		assert.True(t, o.Valid(), "%s", o)
		assert.NotEmpty(t, o.Label())
	}

	assert.False(t, domain.EquipmentCategory("roaster").Valid())
	assert.False(t, domain.EquipmentOwnership("rented").Valid())
	assert.False(t, domain.EquipmentStatus("broken").Valid(),
		"where the machine is, not what is wrong with it — that is the ticket's job")

	assert.True(t, domain.EquipmentStatusActive.Valid())
	assert.True(t, domain.EquipmentStatusInShop.Valid())
	assert.True(t, domain.EquipmentStatusRetired.Valid())
}

// A `date` column comes back as midnight UTC. Comparing that to an instant
// expired the cover at midnight *on* the last day instead of at the end of it,
// so a machine went out of warranty a day early.
func TestEquipmentUnderWarrantyCoversTheWholeExpiryDay(t *testing.T) {
	expiry := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC) // as pgx returns a date
	e := domain.Equipment{WarrantyExpiresOn: &expiry}

	assert.True(t, e.UnderWarranty(time.Date(2026, 8, 25, 0, 1, 0, 0, time.UTC)),
		"just after midnight on the expiry day is still covered")
	assert.True(t, e.UnderWarranty(time.Date(2026, 8, 25, 23, 59, 0, 0, time.UTC)),
		"the expiry day is covered to the end")
	assert.False(t, e.UnderWarranty(time.Date(2026, 8, 26, 0, 1, 0, 0, time.UTC)),
		"the day after is not")
}
