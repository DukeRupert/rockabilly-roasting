package jobs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRenewalInsertOptsPinned pins the options themselves. They are not
// arbitrary: ByArgs makes the subscription ID the unit of deduplication, and
// ByPeriod bounds it to a day so a completed job inside River's retention
// window cannot swallow a legitimate later attempt.
//
// Changing either value is a real decision about how often a subscription can
// be charged, so it should require changing this test too.
func TestRenewalInsertOptsPinned(t *testing.T) {
	opts := RenewalInsertOpts()
	require.NotNil(t, opts)
	assert.True(t, opts.UniqueOpts.ByArgs, "the subscription ID must be the unique key")
	assert.Equal(t, 24*time.Hour, opts.UniqueOpts.ByPeriod,
		"a charge attempt already made today is one this subscription has had")

	// A fresh struct per call. River's InsertOpts is mutable, and a shared
	// pointer would let one caller's edit reach every other insert site.
	assert.NotSame(t, opts, RenewalInsertOpts())
}

// renewalInsertCall matches an InsertTx of a subscription renewal together with
// whatever was passed as its opts, across the line breaks gofmt puts in.
var renewalInsertCall = regexp.MustCompile(
	`InsertTx\([^)]*?SubscriptionRenewalArgs\{[^}]*\}\s*,\s*([^)\n]+)`)

// TestEveryRenewalInsertUsesSharedOpts is the regression test for a double
// charge that reached production review.
//
// River derives a job's unique_key by hashing a string built from the unique
// options that were *set*: the "&period=" segment appears only when ByPeriod is
// non-zero. So two inserts whose UniqueOpts differ hash to different keys and
// deduplicate against each other not at all, however identical their args.
//
// The scheduler's dead-card rung used ByArgs+ByPeriod while both manual Retry
// buttons used ByArgs alone. Each got its own key, so a staff Retry and the
// scheduler's rung for the same subscription both ran — and RenewSubscription
// has no period guard, so that is two PaymentIntents and two renewal orders for
// one billing period.
//
// A test on the helper's return value would not have caught it: every call site
// was individually reasonable, and the bug lived in the disagreement between
// them. So this asserts the thing that actually matters — that there is only
// one set of options in the codebase — by reading the source.
func TestEveryRenewalInsertUsesSharedOpts(t *testing.T) {
	root, err := filepath.Abs("../..")
	require.NoError(t, err)

	found := 0
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Skip vendored and generated trees; nothing there enqueues renewals.
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
		for _, m := range renewalInsertCall.FindAllStringSubmatch(string(src), -1) {
			found++
			opts := strings.TrimSpace(m[1])
			assert.Containsf(t, opts, "RenewalInsertOpts(",
				"%s enqueues a subscription renewal with %q instead of the shared "+
					"RenewalInsertOpts(). Options that differ from the other insert "+
					"sites hash to a different unique_key and deduplicate against "+
					"none of them, which double-charges the subscription.", rel, opts)
		}
		return nil
	})
	require.NoError(t, err)

	// The scheduler's dead-card rung, the staff Retry button, and the customer's
	// own retry. If this drops, the regex has gone stale and the check above is
	// silently passing over call sites it can no longer see.
	assert.GreaterOrEqual(t, found, 3,
		"expected to find every subscription-renewal insert site; the matcher may be stale")
}
