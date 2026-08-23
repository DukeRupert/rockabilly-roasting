package admin

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

func renderJobList(t *testing.T, props JobListProps) string {
	t.Helper()
	if props.MerchantTZ == nil {
		props.MerchantTZ = time.UTC
	}
	var buf bytes.Buffer
	require.NoError(t, JobListContent(props).Render(context.Background(), &buf))
	return buf.String()
}

func deadJob(id int64, kind string) domain.DeadJob {
	return domain.DeadJob{
		ID:          id,
		Kind:        kind,
		Queue:       "default",
		Attempt:     5,
		MaxAttempts: 5,
		LastError:   "postmark: 401 unauthorized",
		Args:        `{"order_id":"c73ead7a-cf9d-4ef0-a838-cc2c20cf6fb2"}`,
		FinalizedAt: time.Now().Add(-3 * time.Hour),
	}
}

// The row exists to be retried, and the retry has to post to the job's own id.
// Args are shown because they are the only thing identifying which record was
// affected.
func TestJobList_RowCarriesRetryAndArgs(t *testing.T) {
	html := renderJobList(t, JobListProps{
		Jobs:  []domain.DeadJob{deadJob(126125, "email_order_confirm")},
		Total: 1,
		Page:  1,
	})

	assert.Contains(t, html, "Email order confirm")
	assert.Contains(t, html, "/admin/jobs/126125/retry")
	assert.Contains(t, html, "c73ead7a-cf9d-4ef0-a838-cc2c20cf6fb2")
	assert.Contains(t, html, "postmark: 401 unauthorized")
	assert.Contains(t, html, "5/5")

	// Retry re-runs real work, including sending mail, so it goes through the
	// admin confirm dialog rather than posting on a single click.
	assert.Contains(t, html, "data-confirm")
	assert.Contains(t, html, "admin-confirm")
	assert.Contains(t, html, "If the job sends email, the email goes out now")
}

// The kind rollup is the diagnosis — one broken dependency discards a whole
// kind at once — so each chip has to carry its count and filter the list.
func TestJobList_KindChipsFilter(t *testing.T) {
	html := renderJobList(t, JobListProps{
		Jobs: []domain.DeadJob{deadJob(1, "email_order_confirm")},
		Kinds: []domain.DeadJobKindCount{
			{Kind: "email_order_confirm", Count: 4},
			{Kind: "qb_create_invoice", Count: 1},
		},
		Total: 5,
		Page:  1,
	})

	assert.Contains(t, html, "/admin/jobs?page=1&amp;kind=email_order_confirm")
	assert.Contains(t, html, "/admin/jobs?page=1&amp;kind=qb_create_invoice")
	assert.Contains(t, html, ">4<")
	assert.Contains(t, html, ">5<") // the "All" chip carries the unfiltered total
}

// The "All" chip is the escape hatch back to the full list, so its count must
// stay the overall total while the list below it is narrowed. Feeding it a
// filtered count made the page claim there was one dead job when there were
// three — on a branch whose whole point is counts that do not lie.
func TestJobList_AllChipKeepsTheUnfilteredTotalWhenNarrowed(t *testing.T) {
	kinds := []domain.DeadJobKindCount{
		{Kind: "email_order_confirm", Count: 2},
		{Kind: "qb_create_invoice", Count: 1},
	}
	html := renderJobList(t, JobListProps{
		Jobs:       []domain.DeadJob{deadJob(1, "qb_create_invoice")},
		Kinds:      kinds,
		KindFilter: "qb_create_invoice",
		Total:      3,
		Page:       1,
	})

	// All still reads 3 while the list shows the one job of the active kind.
	// It renders as a link here — narrowed means All is no longer the active
	// chip — so the assertion pins the muted label form.
	assert.Contains(t, html, `<span class="text-sm font-bold tabular-nums">3</span> <span class="label-font text-rr-muted" style="font-size:0.6rem;">All</span>`)

	// The active kind chip carries its own count, not the total.
	assert.Contains(t, html, `<span class="text-sm font-bold tabular-nums">1</span> <span class="label-font" style="font-size:0.6rem;">Qb create invoice</span>`)

	// And "All" is a live link back out to the unfiltered list.
	assert.Contains(t, html, `href="/admin/jobs?page=1"`)
}

// An empty list is the healthy state and should read as reassurance, not as a
// broken page.
func TestJobList_EmptyStateIsPositive(t *testing.T) {
	html := renderJobList(t, JobListProps{Total: 0, Page: 1})

	assert.Contains(t, html, "Every job is landing.")
	assert.NotContains(t, html, "<table")
}

// Filtering to a kind that has since been cleared must not claim the whole
// system is healthy — the count is zero for this filter, not overall.
func TestJobList_EmptyFilteredKindKeepsTable(t *testing.T) {
	html := renderJobList(t, JobListProps{
		KindFilter: "qb_create_invoice",
		Kinds:      []domain.DeadJobKindCount{{Kind: "email_order_confirm", Count: 2}},
		Total:      0,
		Page:       1,
	})

	assert.NotContains(t, html, "Every job is landing.")
	assert.Contains(t, html, "No failed jobs of this kind.")
}

func TestJobErrorDisplay(t *testing.T) {
	// Only the first line — River records stack traces, and a table row is not
	// where anyone reads one.
	assert.Equal(t, "boom", jobErrorDisplay("boom\n\tat worker.go:12\n\tat river.go:99"))

	assert.Equal(t, "No error recorded", jobErrorDisplay(""))
	assert.Equal(t, "No error recorded", jobErrorDisplay("   "))

	long := strings.Repeat("x", 200)
	got := jobErrorDisplay(long)
	assert.Len(t, []rune(got), 141) // 140 runes plus the ellipsis
	assert.True(t, strings.HasSuffix(got, "…"))

	// Truncation counts runes, so a message of multi-byte characters is cut at
	// a character boundary rather than mid-rune.
	wide := jobErrorDisplay(strings.Repeat("é", 200))
	assert.Len(t, []rune(wide), 141)
	assert.NotContains(t, wide, "\uFFFD")
}

func TestJobKindDisplay(t *testing.T) {
	assert.Equal(t, "Email order confirm", jobKindDisplay("email_order_confirm"))
	assert.Equal(t, "Buy label", jobKindDisplay("buy_label"))
	assert.Equal(t, "—", jobKindDisplay(""))
}

// "How many" is answered by the chips; the next question is whether this is an
// outage happening now or rot that piled up unnoticed. The span line is what
// distinguishes them.
func TestJobList_FailureSpan(t *testing.T) {
	now := time.Now()
	spread := JobListProps{
		MerchantTZ: time.UTC,
		Kinds: []domain.DeadJobKindCount{
			{Kind: "a", Count: 2, Oldest: now.Add(-72 * time.Hour), Newest: now.Add(-2 * time.Hour)},
			{Kind: "b", Count: 1, Oldest: now.Add(-30 * time.Minute), Newest: now.Add(-30 * time.Minute)},
		},
	}
	// Oldest and newest are taken across every kind, not within one.
	assert.Equal(t, "Oldest failed 3d ago, most recent 30m ago.", spread.failureSpan())

	single := JobListProps{
		MerchantTZ: time.UTC,
		Kinds:      []domain.DeadJobKindCount{{Kind: "a", Count: 1, Oldest: now.Add(-time.Hour), Newest: now.Add(-time.Hour)}},
	}
	assert.Equal(t, "Failed 1h ago.", single.failureSpan())

	// Nothing to say with no rollup, and no panic on a nil timezone.
	assert.Equal(t, "", JobListProps{}.failureSpan())

	html := renderJobList(t, JobListProps{
		Jobs:  []domain.DeadJob{deadJob(1, "a")},
		Kinds: spread.Kinds,
		Total: 3,
		Page:  1,
	})
	assert.Contains(t, html, "Oldest failed 3d ago, most recent 30m ago.")
}
