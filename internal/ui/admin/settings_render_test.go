package admin

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

func renderSettings(t *testing.T, props SettingsProps) string {
	t.Helper()
	if props.MerchantTZ == nil {
		props.MerchantTZ = time.UTC
	}
	var buf bytes.Buffer
	require.NoError(t, SettingsContent(props).Render(context.Background(), &buf))
	return buf.String()
}

// healthyShipping is a fully configured shop — the baseline the issue rules are
// measured against, so a new rule that fires on a correct setup fails here.
func healthyShipping() ShippingSettings {
	return ShippingSettings{
		FlatRateCents:           600,
		LocalDeliveryEnabled:    true,
		LocalDeliveryWeekdays:   []time.Weekday{time.Monday, time.Thursday},
		LocalDeliveryCutoff:     "09:00",
		LocalPickupEnabled:      true,
		LocalPickupInstructions: "101 W Kennewick Ave, Tue–Sat 8a–4p",
		OriginName:              "Rockabilly Roasting Co.",
		OriginStreet1:           "101 W Kennewick Ave",
		OriginCity:              "Kennewick",
		OriginState:             "WA",
		OriginZip:               "99336",
		OriginCountry:           "US",
		OriginEmail:             "shop@example.com",
		OriginPhone:             "5095852320",
	}
}

// Nothing wrong must produce nothing to read. An "all clear" panel on every
// settings page is a thing staff learn to skip, which is exactly the habit that
// makes the real warnings invisible.
func TestSettingsIssues_SilentWhenHealthy(t *testing.T) {
	qb := QBConnectionStatus{Connected: true, RefreshExpiresAt: ptrTime(time.Now().Add(90 * 24 * time.Hour))}
	assert.Empty(t, SettingsIssuesFor(healthyShipping(), qb, true, 3))

	html := renderSettings(t, SettingsProps{Shipping: healthyShipping()})
	assert.NotContains(t, html, "Needs attention")
}

// The origin email and phone are not decoration: USPS rejects the label buy
// without them, and the failure surfaces in Fulfillment with nothing pointing
// back here.
func TestSettingsIssues_OriginContactBreaksLabels(t *testing.T) {
	s := healthyShipping()
	s.OriginPhone = ""

	issues := SettingsIssuesFor(s, QBConnectionStatus{}, false, 1)
	require.Len(t, issues, 1)
	assert.True(t, issues[0].Broken)
	assert.Equal(t, settingsTabShipping, issues[0].Tab)
	assert.Equal(t, "/admin/settings#origin", issues[0].href())
}

// A pickup offer with no instructions sends a "come and get it" email that
// names no address.
func TestSettingsIssues_PickupWithoutInstructions(t *testing.T) {
	s := healthyShipping()
	s.LocalPickupInstructions = ""

	issues := SettingsIssuesFor(s, QBConnectionStatus{}, false, 1)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0].Title, "Pickup is offered")
}

// Delivery days drive a real date at checkout. On with none is an offer the
// shop cannot keep.
func TestSettingsIssues_DeliveryOnWithNoDays(t *testing.T) {
	s := healthyShipping()
	s.LocalDeliveryWeekdays = nil

	issues := SettingsIssuesFor(s, QBConnectionStatus{}, false, 1)
	require.Len(t, issues, 1)
	assert.True(t, issues[0].Broken)
	assert.Equal(t, "/admin/settings#local-fulfillment", issues[0].href())
}

// QuickBooks tokens expire on a clock nobody watches and the failure is silent.
// Fourteen days out is a warning; a week out it is treated as already broken,
// because reconnecting needs a person with QuickBooks credentials to be free.
func TestSettingsIssues_QuickBooksExpiryEscalates(t *testing.T) {
	s := healthyShipping()

	far := SettingsIssuesFor(s, QBConnectionStatus{Connected: true, RefreshExpiresAt: ptrTime(time.Now().Add(40 * 24 * time.Hour))}, true, 1)
	assert.Empty(t, far)

	soon := SettingsIssuesFor(s, QBConnectionStatus{Connected: true, RefreshExpiresAt: ptrTime(time.Now().Add(12*24*time.Hour + time.Hour))}, true, 1)
	require.Len(t, soon, 1)
	assert.False(t, soon[0].Broken)
	assert.Equal(t, settingsTabIntegrations, soon[0].Tab)

	urgent := SettingsIssuesFor(s, QBConnectionStatus{Connected: true, RefreshExpiresAt: ptrTime(time.Now().Add(5*24*time.Hour + time.Hour))}, true, 1)
	require.Len(t, urgent, 1)
	assert.True(t, urgent[0].Broken)

	// With QuickBooks not configured at all there is nothing to warn about.
	assert.Empty(t, SettingsIssuesFor(s, QBConnectionStatus{}, false, 1))
}

// Past the expiry the raw countdown goes negative. "-3 days" reads as a
// rendering bug at the exact moment the page is describing an outage, so both
// the attention row and the card say the outage instead.
func TestSettingsIssues_QuickBooksAlreadyExpired(t *testing.T) {
	expired := ptrTime(time.Now().Add(-3 * 24 * time.Hour))

	issues := SettingsIssuesFor(healthyShipping(), QBConnectionStatus{Connected: true, RefreshExpiresAt: expired}, true, 1)
	require.Len(t, issues, 1)
	assert.Equal(t, "QuickBooks access has expired", issues[0].Title)
	assert.True(t, issues[0].Broken)

	assert.Equal(t, "Expired", refreshTokenRemainingLabel(expired))
	assert.Equal(t, "Expires today", refreshTokenRemainingLabel(ptrTime(time.Now().Add(6*time.Hour))))
	assert.Equal(t, "1 day", refreshTokenRemainingLabel(ptrTime(time.Now().Add(25*time.Hour))))
}

// Broken settings sort ahead of warnings: the list is read from the top and
// attention runs out before the scrollbar does.
func TestSettingsIssues_BrokenFirst(t *testing.T) {
	s := healthyShipping()
	s.OriginEmail = "" // broken

	issues := SettingsIssuesFor(s, QBConnectionStatus{}, true, 1) // QB unconnected = warning
	require.Len(t, issues, 2)
	assert.True(t, issues[0].Broken)
	assert.False(t, issues[1].Broken)
}

// The count belongs on the tab that owns the fix, and only the staff who can
// reach the team roster get its tab.
func TestSettingsNav_TabCountsAndTeamVisibility(t *testing.T) {
	nav := SettingsNav{
		StaffRole: string(domain.StaffRoleAdmin),
		Issues: []SettingsIssue{
			{Tab: settingsTabShipping, Broken: true},
			{Tab: settingsTabShipping},
			{Tab: settingsTabIntegrations},
		},
	}
	tabs := nav.tabs()
	require.Len(t, tabs, 5)
	assert.Equal(t, 2, tabs[0].Count)
	assert.Equal(t, 0, tabs[1].Count)
	assert.Equal(t, 1, tabs[3].Count)
	assert.Equal(t, settingsTabTeam, tabs[4].Href)

	support := SettingsNav{StaffRole: string(domain.StaffRoleSupport)}
	for _, tab := range support.tabs() {
		assert.NotEqual(t, settingsTabTeam, tab.Href)
	}
}

// A problem on another tab still has to reach the staffer standing here — that
// is the whole reason the strip renders on every page in the section.
func TestSettings_AttentionListSurfacesOtherTabsProblems(t *testing.T) {
	html := renderSettings(t, SettingsProps{
		Shipping: healthyShipping(),
		Nav: SettingsNav{
			StaffRole: string(domain.StaffRoleAdmin),
			Issues: []SettingsIssue{
				{Tab: settingsTabIntegrations, Title: "QuickBooks access expires in 3 days", Detail: "Reconnect before it lapses.", Broken: true},
			},
		},
	})

	assert.Contains(t, html, "Needs attention")
	assert.Contains(t, html, "QuickBooks access expires in 3 days")
	assert.Contains(t, html, "/admin/settings/integrations")
	// Broken issues carry the same rust flag the order lists use for stuck work.
	assert.Contains(t, html, "row-link-stale")
}

// A rejected save must not throw away the twenty fields that were fine. The
// form comes back with what was typed, the bad field marked, and a message that
// does not look like success.
func TestSettings_RejectedSaveKeepsInputAndMarksField(t *testing.T) {
	s := healthyShipping()
	s.FlatRateInput = "six dollars"
	s.ThresholdInput = "45"

	html := renderSettings(t, SettingsProps{
		Shipping:    s,
		FieldErrors: map[string]string{"flat_rate": "Enter a dollar amount, e.g. 6.00."},
		Flash:       Flash{Message: "Nothing was saved — check the fields marked below.", Error: true},
	})

	assert.Contains(t, html, `value="six dollars"`)
	assert.Contains(t, html, `value="45"`)
	assert.Contains(t, html, "Enter a dollar amount, e.g. 6.00.")
	assert.Contains(t, html, "border-rr-red")
	// An error must not arrive in the success panel.
	assert.Contains(t, html, "badge-red")
	assert.NotContains(t, html, "badge-green")
}

// The two rate fields only mean something together. The echo is what catches a
// threshold typed in cents before a customer meets it.
func TestShippingRuleSummary(t *testing.T) {
	s := ShippingSettings{FlatRateCents: 600}
	assert.Equal(t, "Every non-local order pays $6.00 shipping — no free-shipping threshold is set.", shippingRuleSummary(s))

	threshold := 4500
	s.FreeShippingThreshold = &threshold
	s.LocalZipCodes = []string{"99336", "99337"}
	assert.Equal(t,
		"Orders under $45.00 pay $6.00 shipping; $45.00 and over ship free. Orders to the 2 local zips ship free either way.",
		shippingRuleSummary(s))
}

func ptrTime(t time.Time) *time.Time { return &t }
