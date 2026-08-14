package storefront

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// The order sheet reprices rows as the buyer types, so it carries a JavaScript
// mirror of domain.TierLadder. Two implementations of one pricing rule is the
// standing risk in this feature: if they drift, the sheet quotes a price the
// cart will not honor.
//
// The mirror is now two functions — which price is in force, and which rung
// earned it. Upgrade arithmetic used to live here too; moving the nudge to the
// cart took its thresholds, rounding and case-multiple rules out of the browser,
// and out of this test with them.
//
// This runs the script the page actually ships — extracted from the rendered
// markup, not a copy — against the Go ladder over a grid of quantities, and
// fails on the first disagreement. Skipped when node is unavailable, so it
// never blocks a machine without it.

type parityCase struct {
	Rungs [][2]int `json:"rungs"`
	Qty   int      `json:"qty"`
}

type parityResult struct {
	UnitPrice  int `json:"unitPrice"`
	ActiveRung int `json:"activeRung"`
}

func TestOrderSheetScriptMatchesGoLadder(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; skipping client/server pricing parity check")
	}

	ladders := map[string]domain.TierLadder{
		"three rungs": testLadder(),
		"flat price":  domain.NewTierLadder([]domain.PriceTier{{MinQuantity: 1, Amount: 1100}}),
		"deep break": domain.NewTierLadder([]domain.PriceTier{
			{MinQuantity: 1, Amount: 1100}, {MinQuantity: 100, Amount: 800},
		}),
		"awkward break": domain.NewTierLadder([]domain.PriceTier{
			{MinQuantity: 1, Amount: 1100}, {MinQuantity: 25, Amount: 950},
		}),
		"inverted rung": domain.NewTierLadder([]domain.PriceTier{
			{MinQuantity: 1, Amount: 900}, {MinQuantity: 12, Amount: 1100},
		}),
	}
	var cases []parityCase
	type key struct {
		name string
		qty  int
	}
	var keys []key
	for name, l := range ladders {
		rungs := make([][2]int, 0, len(l.Rungs()))
		for _, r := range l.Rungs() {
			rungs = append(rungs, [2]int{r.MinQuantity, r.Amount})
		}
		for qty := 1; qty <= 40; qty++ {
			cases = append(cases, parityCase{Rungs: rungs, Qty: qty})
			keys = append(keys, key{name, qty})
		}
	}

	got := runSheetScript(t, node, cases)
	require.Len(t, got, len(cases))

	for i, c := range cases {
		k := keys[i]
		l := ladders[k.name]
		where := fmt.Sprintf("%s, qty %d", k.name, k.qty)

		assert.Equal(t, l.UnitPriceAt(c.Qty), got[i].UnitPrice, "unit price disagrees — %s", where)
		assert.Equal(t, activeBreak(l, c.Qty), got[i].ActiveRung, "active rung disagrees — %s", where)
	}

	// The sheet must not have grown pricing logic back. Anything beyond picking
	// a rung belongs on the server, where there is one implementation of it.
	script := portalScript(t)
	for _, gone := range []string{"upgrade(", "nudgeText", "ceilTo", "nudgePct", "nudgeFloor"} {
		assert.NotContains(t, script, gone, "nudge arithmetic is the cart's job, in Go")
	}
}

// runSheetScript evaluates the sheet's x-data object in node and reports what it
// computes for each case.
func runSheetScript(t *testing.T, node string, cases []parityCase) []parityResult {
	t.Helper()

	input, err := json.Marshal(cases)
	require.NoError(t, err)

	harness := fmt.Sprintf(`
const sheet = (%s);
sheet.$root = { querySelectorAll: () => [] };
const cases = JSON.parse(process.argv[2]);
const out = cases.map(c => {
  const el = { dataset: { ladder: JSON.stringify(c.rungs) } };
  const rungs = sheet.rungs(el);
  return { unitPrice: sheet.unitPriceAt(rungs, c.qty), activeRung: sheet.activeRung(rungs, c.qty) };
});
process.stdout.write(JSON.stringify(out));
`, portalScript(t))

	dir := t.TempDir()
	path := filepath.Join(dir, "parity.mjs")
	require.NoError(t, os.WriteFile(path, []byte(harness), 0o600))

	cmd := exec.Command(node, path, string(input))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	require.NoError(t, err, "node failed: %s", stderr.String())

	var results []parityResult
	require.NoError(t, json.Unmarshal(out, &results))
	return results
}
