package emailtemplates

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// singleLineConfirm is a one-item signup as the email was written before a
// signup could carry several items.
func singleLineConfirm() SubscriptionConfirmData {
	return SubscriptionConfirmData{
		CustomerName: "Jane",
		PlanName:     "Every 30 Days",
		ProductName:  "Bonneville Blend 12oz",
		Quantity:     2,
		IntervalDays: 30,
		NextChargeOn: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC),
		StoreName:    "Test Store",
		StoreURL:     "https://example.com",
		AccountURL:   "https://example.com/account/subscriptions",
	}
}

// assertGolden compares got with testdata/name. UPDATE_GOLDEN=1 rewrites it.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), got)
}

// The single-item email is pinned byte for byte to what it read before the
// template learned to list several items, so the common case cannot drift.
func TestRender_SubscriptionConfirm_OneItemReadsAsItAlwaysHas(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	html, text, err := r.Render("subscription_confirm", singleLineConfirm())
	require.NoError(t, err)
	assertGolden(t, "subscription_confirm_one.html", html)
	assertGolden(t, "subscription_confirm_one.txt", text)
}

func TestRender_SubscriptionConfirm_OneLineInLinesIsTheSameEmail(t *testing.T) {
	// The sender fills Lines and the top-level fields both. One line in Lines
	// must not change a byte of the one-item email.
	r, err := New(time.UTC)
	require.NoError(t, err)

	data := singleLineConfirm()
	data.Lines = []SubscriptionConfirmLine{{
		ProductName: data.ProductName, PlanName: data.PlanName, Quantity: data.Quantity,
		IntervalDays: data.IntervalDays, NextChargeOn: data.NextChargeOn,
	}}
	html, text, err := r.Render("subscription_confirm", data)
	require.NoError(t, err)
	assertGolden(t, "subscription_confirm_one.html", html)
	assertGolden(t, "subscription_confirm_one.txt", text)
}

func TestRender_SubscriptionConfirm_ListsEveryItem(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	may15 := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	data := singleLineConfirm()
	data.Lines = []SubscriptionConfirmLine{
		{ProductName: "Bonneville Blend 12oz", PlanName: "Every 30 Days", Quantity: 2, IntervalDays: 30, NextChargeOn: may15},
		{ProductName: "Crazy Mountain Decaf", PlanName: "Every 30 Days", Quantity: 3, IntervalDays: 30, NextChargeOn: may15},
	}

	html, text, err := r.Render("subscription_confirm", data)
	require.NoError(t, err)
	for _, body := range []string{html, text} {
		assert.Contains(t, body, "Bonneville Blend 12oz")
		assert.Contains(t, body, "Crazy Mountain Decaf")
		assert.Contains(t, body, "Every 30 Days")
		assert.Contains(t, body, "May 15, 2026")
		assert.NotContains(t, body, "on their own schedules",
			"items renewing together say nothing about separate days")
	}
	assert.Contains(t, text, "2 × Bonneville Blend 12oz")
	assert.Contains(t, text, "3 × Crazy Mountain Decaf")
}

func TestRender_SubscriptionConfirm_SaysWhenItemsRenewOnDifferentDays(t *testing.T) {
	r, err := New(time.UTC)
	require.NoError(t, err)

	data := singleLineConfirm()
	data.NextChargeOn = time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	data.Lines = []SubscriptionConfirmLine{
		{ProductName: "Bonneville Blend 12oz", PlanName: "Weekly", Quantity: 1, IntervalDays: 7,
			NextChargeOn: time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)},
		{ProductName: "Crazy Mountain Decaf", PlanName: "Monthly", Quantity: 1, IntervalDays: 30,
			NextChargeOn: time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC)},
	}

	html, text, err := r.Render("subscription_confirm", data)
	require.NoError(t, err)
	for _, body := range []string{html, text} {
		assert.Contains(t, body, "on their own schedules")
		assert.Contains(t, body, "Bonneville Blend 12oz (Weekly) on May 7, 2026")
		assert.Contains(t, body, "Crazy Mountain Decaf (Monthly) on May 30, 2026")
	}
}
