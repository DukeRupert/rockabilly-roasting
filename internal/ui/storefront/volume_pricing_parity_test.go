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
// This runs the script the page actually ships — extracted from the rendered
// markup, not a copy — against the Go ladder over a grid of quantities, and
// fails on the first disagreement. Skipped when node is unavailable, so it
// never blocks a machine without it.

type parityCase struct {
	Rungs    [][2]int `json:"rungs"`
	Qty      int      `json:"qty"`
	Multiple int      `json:"multiple"`
}

type parityResult struct {
	UnitPrice int `json:"unitPrice"`
	Upgrade   *struct {
		Add         int `json:"add"`
		Target      int `json:"target"`
		Unit        int `json:"unit"`
		UnitSaving  int `json:"unitSaving"`
		TotalSaving int `json:"totalSaving"`
	} `json:"upgrade"`
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
	multiples := []int{1, 6, 20}

	var cases []parityCase
	type key struct {
		name     string
		qty      int
		multiple int
	}
	var keys []key
	for name, l := range ladders {
		rungs := make([][2]int, 0, len(l.Rungs()))
		for _, r := range l.Rungs() {
			rungs = append(rungs, [2]int{r.MinQuantity, r.Amount})
		}
		for _, m := range multiples {
			for qty := 1; qty <= 40; qty++ {
				cases = append(cases, parityCase{Rungs: rungs, Qty: qty, Multiple: m})
				keys = append(keys, key{name, qty, m})
			}
		}
	}

	got := runSheetScript(t, node, cases)
	require.Len(t, got, len(cases))

	for i, c := range cases {
		k := keys[i]
		l := ladders[k.name]
		where := fmt.Sprintf("%s, qty %d, multiple %d", k.name, k.qty, k.multiple)

		assert.Equal(t, l.UnitPriceAt(c.Qty), got[i].UnitPrice, "unit price disagrees — %s", where)

		wantUp, wantOK := l.Upgrade(c.Qty, c.Multiple)
		if !wantOK {
			assert.Nil(t, got[i].Upgrade, "script offered an upgrade Go withholds — %s", where)
			continue
		}
		if !assert.NotNil(t, got[i].Upgrade, "script withheld an upgrade Go offers — %s", where) {
			continue
		}
		assert.Equal(t, wantUp.AddQty, got[i].Upgrade.Add, "add quantity — %s", where)
		assert.Equal(t, wantUp.TargetQty, got[i].Upgrade.Target, "target quantity — %s", where)
		assert.Equal(t, wantUp.TargetUnitPrice, got[i].Upgrade.Unit, "target unit price — %s", where)
		assert.Equal(t, wantUp.UnitSavingCents, got[i].Upgrade.UnitSaving, "unit saving — %s", where)
		assert.Equal(t, wantUp.TotalSavingCents, got[i].Upgrade.TotalSaving, "total saving — %s", where)
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
// The script reads its thresholds off the form, exactly as it does in the page.
sheet.$root = { dataset: { nudgePct: %q, nudgeFloor: %q }, querySelectorAll: () => [] };
const cases = JSON.parse(process.argv[2]);
const out = cases.map(c => {
  const el = { dataset: { ladder: JSON.stringify(c.rungs) } };
  const rungs = sheet.rungs(el);
  const u = sheet.upgrade(rungs, c.qty, c.multiple);
  return { unitPrice: sheet.unitPriceAt(rungs, c.qty), upgrade: u };
});
process.stdout.write(JSON.stringify(out));
`, portalScript(t), nudgePctAttr(), nudgeFloorAttr())

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
