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

// A status read that failed is a different fact from a disconnection, and only
// one of them is worth sending someone to Intuit over.
func TestSettingsIssues_UnreadableQuickBooksStatusIsNotADisconnection(t *testing.T) {
	unavailable := QBConnectionStatus{Unavailable: true}
	assert.Empty(t, SettingsIssuesFor(healthyShipping(), unavailable, true, 1))

	// A genuine disconnection still warns.
	issues := SettingsIssuesFor(healthyShipping(), QBConnectionStatus{}, true, 1)
	require.Len(t, issues, 1)
	assert.Equal(t, "QuickBooks is not connected", issues[0].Title)

	html := renderIntegrations(t, SettingsIntegrationsProps{QB: unavailable, QBEnabled: true})
	assert.Contains(t, html, "Status unknown")
	assert.NotContains(t, html, "Not connected")
	// Not knowing the status is exactly when someone may need to reconnect, so
	// the way out has to stay on the page — but not the destructive one.
	require.NotEmpty(t, anchorsTo(html, "/admin/settings/integrations/quickbooks/connect"))
	// Asserted on the route rather than the label: the word appears in prose on
	// this card, and it is the destructive *action* that must be absent.
	assert.NotContains(t, html, "quickbooks/disconnect")
}

// The OAuth handoff 302s to intuit.com. A boosted link fetches that by XHR,
// where a cross-origin redirect cannot become a navigation, so both entry
// points have to opt out of hx-boost.
func TestSettingsIntegrations_OAuthLinksAreNotBoosted(t *testing.T) {
	// Asserted per-anchor rather than as one attribute-ordered string: the point
	// is that every link to the connect route opts out, whatever order its
	// attributes end up in.
	// Every state that offers the connect route, not just the two obvious ones:
	// the unavailable card offers it too, and a guard that misses an entry point
	// is a guard that will not notice the one that regresses.
	for name, html := range map[string]string{
		"reconnect": renderIntegrations(t, SettingsIntegrationsProps{
			QB:        QBConnectionStatus{Connected: true},
			QBEnabled: true,
		}),
		"connect": renderIntegrations(t, SettingsIntegrationsProps{QBEnabled: true}),
		"unavailable": renderIntegrations(t, SettingsIntegrationsProps{
			QB:        QBConnectionStatus{Unavailable: true},
			QBEnabled: true,
		}),
	} {
		anchors := anchorsTo(html, "/admin/settings/integrations/quickbooks/connect")
		require.NotEmpty(t, anchors, "%s state should offer a link to the connect route", name)
		for _, anchor := range anchors {
			assert.Contains(t, anchor, `hx-boost="false"`, "%s link must not be boosted: %s", name, anchor)
		}
	}
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
	s.Draft = true
	s.FlatRateInput = "six dollars"
	s.ThresholdInput = "45"
	s.TareInput = "2.50"

	html := renderSettings(t, SettingsProps{
		Shipping:    s,
		FieldErrors: map[string]string{"flat_rate": "Enter a dollar amount, e.g. 6.00."},
		Flash:       Flash{Message: "Nothing was saved — check the fields marked below.", Error: true},
	})

	assert.Contains(t, html, `value="six dollars"`)
	assert.Contains(t, html, `value="45"`)
	assert.Contains(t, html, "Enter a dollar amount, e.g. 6.00.")
	assert.Contains(t, html, "border-rr-red")
	// An error must not arrive in the success panel, and it interrupts a screen
	// reader where a success does not.
	assert.Contains(t, html, "badge-red")
	assert.Contains(t, html, `role="alert"`)
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

// A rejected save re-renders in place instead of redirecting, and hx-boost
// pushes the form's action into history — so that action is the URL the staffer
// is left on and the one a refresh will GET. It has to be the Shipping tab's own
// URL, which has a GET route; a POST-only verb path would answer that refresh
// with a 405 on the page they were just told to go fix.
func TestSettings_FormPostsToATabURLThatAnswersGET(t *testing.T) {
	html := renderSettings(t, SettingsProps{Shipping: healthyShipping()})

	assert.Contains(t, html, `action="`+settingsTabShipping+`"`)
	assert.NotContains(t, html, `action="`+settingsTabShipping+`/shipping"`)
}

// "Customers currently see" has to describe what a customer actually meets.
// On a rejected save the form fields hold the draft, but nothing has reached
// disk — so the echo reads from the saved config. Rendering the draft there
// meant a mistyped rate announced "$0.00 shipping" directly under a banner
// saying nothing had been saved: the page contradicted itself at the one
// moment the staffer was reading it closely.
func TestSettings_EchoShowsSavedRuleNotRejectedDraft(t *testing.T) {
	saved := healthyShipping() // $6.00 flat rate, in force

	// The staffer fluffs the rate; parsing leaves it at zero in the draft.
	draft := healthyShipping()
	draft.Draft = true
	draft.FlatRateCents = 0
	draft.FlatRateInput = "6..00"

	html := renderSettings(t, SettingsProps{
		Shipping:    draft,
		Saved:       saved,
		FieldErrors: map[string]string{"flat_rate": "Enter a dollar amount, e.g. 6.00."},
		Flash:       Flash{Message: "Nothing was saved — check the fields marked below.", Error: true},
	})

	// The echo states the rule still in force...
	assert.Contains(t, html, "Every non-local order pays $6.00 shipping")
	assert.NotContains(t, html, "pays $0.00 shipping")
	// ...while the field still hands back exactly what was typed.
	assert.Contains(t, html, `value="6..00"`)
}

// Clearing a number is an edit, not an absence. The threshold is how free
// shipping gets switched off and the tare is optional, so emptying either is a
// thing a staffer means to do — and if validation then trips on some other
// field, the cleared box has to come back cleared. It used to come back holding
// the old number, because the helpers inferred "no draft" from an empty input
// string and could not tell a cleared field from an unsubmitted one. The form
// would then argue with an edit the staffer had just made, and saving again
// would quietly restore the value they had removed.
func TestSettings_RejectedSaveKeepsClearedFieldsCleared(t *testing.T) {
	stored := 4500
	s := healthyShipping()
	s.FreeShippingThreshold = &stored // $45.00 currently in force
	s.TareWeightOz = 2.5

	// The staffer clears both, then trips validation on the flat rate.
	s.Draft = true
	s.FlatRateInput = "six dollars"
	s.ThresholdInput = ""
	s.TareInput = ""

	html := renderSettings(t, SettingsProps{
		Shipping:    s,
		Saved:       healthyShipping(),
		FieldErrors: map[string]string{"flat_rate": "Enter a dollar amount, e.g. 6.00."},
		Flash:       Flash{Message: "Nothing was saved — check the fields marked below.", Error: true},
	})

	// Both cleared boxes come back empty rather than repopulated.
	assert.NotContains(t, html, `value="45.00"`)
	assert.NotContains(t, html, `value="2.50"`)
	assert.Contains(t, html, `name="free_threshold"`)
	// The field that actually failed still shows what was typed.
	assert.Contains(t, html, `value="six dollars"`)
}

// The flag is what separates a draft from a fresh load: with it unset the form
// renders the stored config, so an ordinary GET is never at the mercy of a
// stale input string.
func TestShippingSettings_NonDraftRendersStoredValues(t *testing.T) {
	stored := 4500
	s := healthyShipping()
	s.FreeShippingThreshold = &stored
	s.TareWeightOz = 2.5
	s.FlatRateInput = "ignored" // set, but Draft is false

	assert.Equal(t, "6.00", s.flatRateValue())
	assert.Equal(t, "45.00", s.thresholdValue())
	assert.Equal(t, "2.50", s.tareValue())
}

func renderIntegrations(t *testing.T, props SettingsIntegrationsProps) string {
	t.Helper()
	if props.MerchantTZ == nil {
		props.MerchantTZ = time.UTC
	}
	var buf bytes.Buffer
	require.NoError(t, SettingsIntegrationsContent(props).Render(context.Background(), &buf))
	return buf.String()
}

// anchorsTo returns every <a> tag in html whose href is exactly the given path.
func anchorsTo(html, href string) []string {
	var out []string
	for _, chunk := range strings.Split(html, "<a ")[1:] {
		tag := "<a " + chunk[:strings.Index(chunk, ">")+1]
		if strings.Contains(tag, `href="`+href+`"`) {
			out = append(out, tag)
		}
	}
	return out
}

func ptrTime(t time.Time) *time.Time { return &t }

// --- Moved delivery runs ---

// renderPostponements renders the settings page the way production does: the
// panel reads the saved schedule, so the helper sets both halves rather than
// letting a test accidentally prove the draft path.
func renderPostponements(t *testing.T, saved ShippingSettings, rows []PostponementRow) string {
	t.Helper()
	return renderSettings(t, SettingsProps{Shipping: saved, Saved: saved, Postponements: rows})
}

// The panel only exists to amend a schedule, so it must not appear when there
// is no schedule to amend. A shop that has not set up local delivery would
// otherwise be offered a form whose every submission the service refuses.
func TestPostponementsPanel_HiddenWithoutSchedule(t *testing.T) {
	noDelivery := healthyShipping()
	noDelivery.LocalDeliveryEnabled = false
	assert.NotContains(t, renderPostponements(t, noDelivery, nil), "Moved delivery runs")

	noDays := healthyShipping()
	noDays.LocalDeliveryWeekdays = nil
	assert.NotContains(t, renderPostponements(t, noDays, nil), "Moved delivery runs")

	// With a schedule, it is there.
	assert.Contains(t, renderPostponements(t, healthyShipping(), nil), "Moved delivery runs")
}

// The panel's visibility follows the *saved* schedule, not the draft. A rejected
// save re-renders with what was typed, so gating on the draft meant unticking
// local delivery alongside a mistyped rate made the whole panel disappear —
// while the postponements sat untouched on disk, invisible and unmanageable
// until the staffer got the rest of the form right.
func TestPostponementsPanel_FollowsSavedNotDraft(t *testing.T) {
	draft := healthyShipping()
	draft.LocalDeliveryEnabled = false // what the staffer just typed, and got rejected

	html := renderSettings(t, SettingsProps{
		Shipping:      draft,
		Saved:         healthyShipping(),
		Postponements: []PostponementRow{{OriginalValue: "2026-09-07", OriginalLabel: "Monday, September 7", MovedToLabel: "Tuesday, September 8"}},
	})
	assert.Contains(t, html, "Moved delivery runs", "a rejected draft must not hide runs that are still on disk")
	assert.Contains(t, html, "Monday, September 7")
}

// An empty panel has to say so. Rendering just the add form would read as
// "nothing loaded" rather than "nothing moved", and the difference matters to a
// staffer checking whether somebody already handled a holiday.
func TestPostponementsPanel_EmptyState(t *testing.T) {
	assert.Contains(t, renderPostponements(t, healthyShipping(), nil), "No runs have been moved.")
}

// A recorded postponement renders both dates, its note, and a Restore control
// carrying the date the handler needs back.
func TestPostponementsPanel_RendersRows(t *testing.T) {
	html := renderPostponements(t, healthyShipping(), []PostponementRow{{
		OriginalValue: "2026-09-07",
		OriginalLabel: "Monday, September 7",
		MovedToLabel:  "Tuesday, September 8",
		Note:          "Labor Day",
	}})

	assert.Contains(t, html, "Monday, September 7")
	assert.Contains(t, html, "Tuesday, September 8")
	assert.Contains(t, html, "Labor Day")
	assert.NotContains(t, html, "No runs have been moved.")

	// The Restore form has to post the date back, or the handler cannot tell
	// which run to put back.
	assert.Contains(t, html, `name="original_date"`)
	assert.Contains(t, html, `value="2026-09-07"`)
	assert.Contains(t, html, "/admin/settings/delivery-postponements/delete")
}

// A run that has already been and gone is kept on the page and labelled, rather
// than hidden — a staffer looking back at a holiday should be able to see it
// was handled.
func TestPostponementsPanel_MarksPastRuns(t *testing.T) {
	html := renderPostponements(t, healthyShipping(), []PostponementRow{{
		OriginalValue: "2026-01-01",
		OriginalLabel: "Thursday, January 1",
		MovedToLabel:  "Friday, January 2",
		Past:          true,
	}})
	assert.Contains(t, html, "Already run")
}

// The add form posts to its own endpoint, not to the shipping form's. Sharing
// the shipping POST would mean a mistyped flat rate discards a holiday somebody
// just marked, which is the whole reason this is a separate form.
func TestPostponementsPanel_HasItsOwnForm(t *testing.T) {
	html := renderPostponements(t, healthyShipping(), nil)
	assert.Contains(t, html, `action="/admin/settings/delivery-postponements"`)
	assert.Contains(t, html, `name="moved_to_date"`)
	assert.Contains(t, html, `name="note"`)

	// Both date inputs are required — a blank one would post an unparseable
	// date and bounce back with an error the staffer could have been spared.
	postponeForm := html[strings.Index(html, "Moved delivery runs"):]
	assert.GreaterOrEqual(t, strings.Count(postponeForm, `type="date"`), 2)
}
