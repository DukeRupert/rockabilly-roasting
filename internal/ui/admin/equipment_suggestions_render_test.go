package admin

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

func renderEquipmentForm(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, EquipmentFormContent(EquipmentFormProps{
		StaffName: "UI Check",
		StaffRole: "admin",
	}).Render(context.Background(), &buf))
	return buf.String()
}

// The type-ahead is invisible to the accessibility tree and its dropdown is an
// OS-level widget that cannot be screenshotted, so the markup is the only thing
// that can be checked — and a datalist that silently stops being emitted looks
// exactly like one that works.
func TestEquipmentFormEmitsTypeAheadSuggestions(t *testing.T) {
	html := renderEquipmentForm(t)

	// Both lists exist and the inputs point at them. An id/list mismatch is the
	// classic way this breaks: everything renders, nothing suggests.
	assert.Contains(t, html, `<datalist id="equipment-makes">`)
	assert.Contains(t, html, `<datalist id="equipment-models">`)
	assert.Contains(t, html, `list="equipment-makes"`)
	assert.Contains(t, html, `list="equipment-models"`)

	// Every catalogue entry actually reaches the page.
	for _, make := range domain.EquipmentMakes() {
		assert.Contains(t, html, `<option value="`+make+`">`, "make %q missing from the suggestions", make)
	}
	assert.Contains(t, html, `<option value="La Marzocco">`)
	assert.Contains(t, html, `<option value="Linea PB">`)
	assert.Contains(t, html, `<option value="EK43">`)

	// Counts match the catalogue, so a duplicate or dropped entry shows up here
	// rather than as a slightly odd dropdown nobody reports. Scoped to inside
	// each datalist — the category and ownership selects on the same page emit
	// <option> too, and counting those would make this assertion meaningless.
	assert.Equal(t, len(domain.EquipmentMakes()), countOptions(t, html, "equipment-makes"))
	assert.Equal(t, len(domain.EquipmentModels()), countOptions(t, html, "equipment-models"))
}

// countOptions counts the <option> elements inside one named datalist.
func countOptions(t *testing.T, html, id string) int {
	t.Helper()
	open := `<datalist id="` + id + `">`
	start := strings.Index(html, open)
	require.GreaterOrEqual(t, start, 0, "datalist %q not found", id)
	rest := html[start+len(open):]
	end := strings.Index(rest, "</datalist>")
	require.GreaterOrEqual(t, end, 0, "datalist %q never closed", id)
	return strings.Count(rest[:end], "<option value=\"")
}

// The fields stay free text. A suggestion list that became a whitelist would
// reject the first machine nobody anticipated, which is the one failure this
// feature must never cause.
func TestEquipmentFormMakeAndModelStayFreeText(t *testing.T) {
	html := renderEquipmentForm(t)

	assert.Contains(t, html, `id="make"`)
	assert.Contains(t, html, `id="model"`)
	// Not <select>, not readonly, not pattern-constrained.
	assert.NotContains(t, html, `<select id="make"`)
	assert.NotContains(t, html, `<select id="model"`)
	assert.NotContains(t, html, `readonly`)
	assert.NotContains(t, html, `pattern=`)

	// The browser's own form history is suppressed so it does not compete with
	// the catalogue for the same dropdown.
	assert.Contains(t, html, `autocomplete="off"`)
}

// --- Site picker ---

func renderSitePicker(t *testing.T, addresses []EquipmentOption, selected string, chosen bool) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, EquipmentSitePicker(addresses, selected, chosen).Render(context.Background(), &buf))
	return buf.String()
}

// The Site field was unusable on the standalone add form: addresses are loaded
// per-customer, the customer starts unset there, and nothing reloaded the field
// when one was chosen — so it could only ever say "Not recorded". These cover
// the three states it can now be in, which look alike in a screenshot and need
// completely different things from the person reading them.
func TestEquipmentSitePickerStates(t *testing.T) {
	sites := []EquipmentOption{
		{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Label: "4208 114th Ave E, Edgewood, WA 98372"},
		{ID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Label: "4839 tin top road, Weatherford, TX 76087"},
	}

	t.Run("no customer chosen yet", func(t *testing.T) {
		html := renderSitePicker(t, nil, "", false)
		assert.Contains(t, html, "Choose a customer first.")
		assert.NotContains(t, html, "Nothing on file")
		// Nothing to add an address to yet.
		assert.NotContains(t, html, "Somewhere else")
	})

	t.Run("customer chosen, addresses on file", func(t *testing.T) {
		html := renderSitePicker(t, sites, "", true)
		assert.Contains(t, html, "4208 114th Ave E, Edgewood, WA 98372")
		assert.Contains(t, html, "4839 tin top road, Weatherford, TX 76087")
		assert.NotContains(t, html, "Choose a customer first.")
		assert.NotContains(t, html, "Nothing on file")
		assert.Contains(t, html, "Somewhere else")
	})

	t.Run("customer chosen, nothing on file", func(t *testing.T) {
		html := renderSitePicker(t, nil, "", true)
		// Distinct from the no-customer case — an empty dropdown looks identical
		// in both, and only one of them is fixed by picking a customer.
		assert.Contains(t, html, "Nothing on file for this customer.")
		assert.NotContains(t, html, "Choose a customer first.")
		assert.Contains(t, html, "Somewhere else")
	})

	t.Run("a stored site comes back selected", func(t *testing.T) {
		html := renderSitePicker(t, sites, "22222222-2222-2222-2222-222222222222", true)
		assert.Contains(t, html, `value="22222222-2222-2222-2222-222222222222" selected`)
	})
}

// The swap target and the new-site fields the create handler reads by name.
// A rename on either side breaks the feature silently.
func TestEquipmentSitePickerContract(t *testing.T) {
	html := renderSitePicker(t, nil, "", true)

	assert.Contains(t, html, `id="site-picker"`, "htmx swaps this by id")
	for _, field := range []string{
		"new_site_line1", "new_site_line2", "new_site_city",
		"new_site_state", "new_site_postal_code",
	} {
		assert.Contains(t, html, `name="`+field+`"`, "create handler reads %q", field)
	}
}
