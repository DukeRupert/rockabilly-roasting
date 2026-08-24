package jobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

// TestBatchRenewalInsertOptsPinned pins the batch options. Same reasoning as
// the solo path: the group is the unit and the day is the bound.
func TestBatchRenewalInsertOptsPinned(t *testing.T) {
	opts := BatchRenewalInsertOpts()
	require.NotNil(t, opts)
	assert.True(t, opts.UniqueOpts.ByArgs, "the group must be the unique key")
	assert.Equal(t, 24*time.Hour, opts.UniqueOpts.ByPeriod)
	assert.NotSame(t, opts, BatchRenewalInsertOpts())
}

// TestBatchRenewalArgsUniqueTags pins which fields form the batch's unique key.
//
// River builds the key from the fields tagged `river:"unique"` when any are
// present, and from every field when none are. So the tags are the difference
// between "one batch per customer and address" and "one batch per exact list of
// subscription IDs" — and the second is not a key at all: the source query
// orders by next_order_at with no tiebreak, and a group grows as more
// subscriptions come due, so [A,B] and [A,B,C] would both run and charge A and
// B twice.
//
// Losing a tag here is silent. Nothing fails to compile, and the duplicate only
// shows up as a second charge on a real customer.
func TestBatchRenewalArgsUniqueTags(t *testing.T) {
	typ := reflect.TypeOf(BatchRenewalArgs{})

	tagged := map[string]bool{}
	jsonNames := map[string]string{}
	for i := range typ.NumField() {
		f := typ.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		jsonNames[f.Name] = name
		for _, v := range strings.Split(f.Tag.Get("river"), ",") {
			if strings.TrimSpace(v) == "unique" {
				tagged[f.Name] = true
			}
		}
	}

	assert.True(t, tagged["CustomerID"], `CustomerID must carry river:"unique"`)
	assert.True(t, tagged["ShippingAddressID"], `ShippingAddressID must carry river:"unique"`)
	assert.False(t, tagged["SubscriptionIDs"],
		`SubscriptionIDs must NOT carry river:"unique" — membership changes between ticks`)

	// River resolves the tagged fields to their JSON keys, so a rename that
	// misses the json tag would silently change the key.
	assert.Equal(t, "customer_id", jsonNames["CustomerID"])
	assert.Equal(t, "shipping_address_id", jsonNames["ShippingAddressID"])

	// And the args must actually marshal those keys, since River reads them back
	// out of the encoded JSON rather than off the struct.
	encoded, err := json.Marshal(BatchRenewalArgs{})
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"customer_id"`)
	assert.Contains(t, string(encoded), `"shipping_address_id"`)
}

// renewalInsertCall matches an InsertTx of a subscription renewal together with
// whatever was passed as its opts, across the line breaks gofmt puts in.
var renewalInsertCall = regexp.MustCompile(
	`InsertTx\([^)]*?SubscriptionRenewalArgs\{[^}]*\}\s*,\s*([^)\n]+)`)

// batchInsertCall does the same for batch renewals. The args literal spans
// several lines now, so the opts are taken from after the closing brace.
var batchInsertCall = regexp.MustCompile(
	`BatchRenewalArgs\{[^}]*\}\s*,\s*([^)\n]+)`)

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

	// The scheduler's solo path, the staff Retry button, and the customer's own
	// retry. If this drops, the regex has gone stale and the check above is
	// silently passing over call sites it can no longer see.
	assert.GreaterOrEqual(t, found, 3,
		"expected to find every subscription-renewal insert site; the matcher may be stale")
}

// TestEveryBatchInsertUsesSharedOpts is the same guard for the batch path, which
// had no uniqueness at all: it passed nil, and the scheduler runs every minute
// against subscriptions that stay due until their batch job actually runs. Every
// tick in between minted another job for the same group, and every duplicate
// that ran charged the customer and placed an order.
func TestEveryBatchInsertUsesSharedOpts(t *testing.T) {
	root, err := filepath.Abs("../..")
	require.NoError(t, err)

	found := 0
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
		for _, m := range batchInsertCall.FindAllStringSubmatch(string(src), -1) {
			found++
			opts := strings.TrimSpace(m[1])
			assert.Containsf(t, opts, "BatchRenewalInsertOpts(",
				"%s enqueues a batch renewal with %q instead of the shared "+
					"BatchRenewalInsertOpts(). nil means no uniqueness at all, and "+
					"the scheduler re-enqueues every minute until the batch runs.", rel, opts)
		}
		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, 1, found,
		"expected exactly the scheduler's batch-renewal insert site; the matcher may be stale")
}
