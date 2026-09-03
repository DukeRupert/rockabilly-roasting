package app_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// termsArithmetic matches any arithmetic on an order's placement time.
//
// Deliberately wider than "an expression mentioning payment terms": the first
// version of this only matched when the terms identifier appeared inline before
// the closing paren, so hoisting it into a local variable — days :=
// EffectivePaymentTermsDays(customer); order.PlacedAt.AddDate(0, 0, days) —
// reintroduced the bug invisibly. No production code adds anything to PlacedAt
// for any other purpose, so the broad form costs nothing; a future legitimate
// use should read this failure and decide deliberately.
var termsArithmetic = regexp.MustCompile(`(?:PlacedAt|placedAt)\s*\.\s*Add(?:Date)?\s*\(`)

// knownBad are shapes this check exists to reject. Asserted against the regex
// itself, because everything below looks for the *absence* of matches: neuter
// the pattern and a healthy repo and a broken one become indistinguishable.
// (This is the positive control the renewal-options test gets from its
// GreaterOrEqual on the number of call sites found — that guard cannot work
// here, where zero matches is the healthy state.)
var knownBad = []string{
	`due := order.PlacedAt.Add(time.Duration(termsDays) * 24 * time.Hour)`,
	`due := order.PlacedAt.AddDate(0, 0, app.EffectivePaymentTermsDays(customer))`,
	`due := order.PlacedAt.AddDate(0, 0, days)`,
	`x := placedAt.Add(7 * 24 * time.Hour)`,
	`y := order.PlacedAt .AddDate( 0, 0, n )`,
}

func TestTermsArithmeticPatternStillMatchesTheBugItLooksFor(t *testing.T) {
	for _, sample := range knownBad {
		assert.Regexpf(t, termsArithmetic, sample,
			"the pattern no longer recognises %q, so the sweep below is passing "+
				"over call sites it can no longer see", sample)
	}
	// And does not fire on the shape that replaced them.
	assert.NotRegexp(t, termsArithmetic,
		`due := app.InvoiceDueDate(order.PlacedAt, termsDays, loc)`,
		"the fixed form must not be reported as the bug")
}

// TestEveryInvoiceDueDateGoesThroughInvoiceDueDate reads the source, because
// this bug did not live in any one call site.
//
// Three places computed when a NET-terms invoice falls due: the QuickBooks
// payload, our shadow preview row, and the admin order page's Due row. The
// first two shared an expression that counted 24-hour spans on a UTC instant,
// so an evening order in Los Angeles — already tomorrow in UTC — was billed
// NET 7 on day eight. The third re-zoned for display and so showed the right
// day, which is why the order detail page could print September 8 beside a
// QuickBooks invoice due September 9.
//
// Each of the three was locally reasonable. What was wrong was that there were
// three. A test over InvoiceDueDate's return value cannot see that, so this
// reads the source instead: nothing does arithmetic on an order's placement
// time, anywhere.
//
// It asserts an absence, which is a weaker thing than it looks — a pattern that
// has quietly stopped matching produces the same green as a clean repo. The
// matcher is therefore kept honest separately, by
// TestTermsArithmeticPatternStillMatchesTheBugItLooksFor.
func TestEveryInvoiceDueDateGoesThroughInvoiceDueDate(t *testing.T) {
	root, err := filepath.Abs("../..")
	require.NoError(t, err)

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(root, path)
		for _, m := range termsArithmetic.FindAllString(string(src), -1) {
			assert.Failf(t, "payment terms counted outside app.InvoiceDueDate",
				"%s computes a due date with %q. Counting terms as spans on an "+
					"instant bills evening orders a day late, and a second "+
					"expression can disagree with the one QuickBooks was sent. "+
					"Use app.InvoiceDueDate(placedAt, termsDays, merchantTZ).",
				rel, strings.TrimSpace(m))
		}
		return nil
	})
	require.NoError(t, err)
}

// The merchant zone has to actually reach the invoice job. Nothing else asserts
// it: the QB workers have no tests of their own, and a due date computed in UTC
// is wrong only for evening orders, so it would not announce itself.
func TestCreateQBInvoiceWorkerIsWiredWithTheMerchantZone(t *testing.T) {
	root, err := filepath.Abs("../..")
	require.NoError(t, err)

	src, err := os.ReadFile(filepath.Join(root, "cmd", "server", "main.go"))
	require.NoError(t, err)

	call := regexp.MustCompile(`jobs\.NewCreateQBInvoiceWorker\(([^)]*)\)`)
	m := call.FindStringSubmatch(string(src))
	require.NotNil(t, m, "cmd/server/main.go no longer registers the QB invoice worker")
	assert.Contains(t, m[1], "merchantTZ",
		"the QB invoice worker must be given the merchant zone; without it every "+
			"due date is counted on UTC's calendar and evening orders bill a day late")
}
