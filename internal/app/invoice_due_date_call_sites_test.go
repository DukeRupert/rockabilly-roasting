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

// termsArithmetic matches anyone counting payment terms by hand: adding a
// multiple of a day, or AddDate-ing a terms count, onto an order timestamp.
var termsArithmetic = regexp.MustCompile(
	`(?:PlacedAt|placedAt)\s*\.\s*(?:Add\s*\([^)]*(?:termsDays|TermsDays|PaymentTermsDays)|AddDate\s*\([^)]*(?:termsDays|TermsDays|PaymentTermsDays))`)

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
// asserts the property that actually matters: nobody counts payment terms
// anywhere else.
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
