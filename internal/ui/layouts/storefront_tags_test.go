package layouts

import (
	"bytes"
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The privacy policy says Google Ads gets the order number and total and
// never what was bought. That holds only while the general-purpose tracker
// routes to GA4 alone: a gtag event with no send_to goes to every configured
// tag, AW included, so the purchase event's line items would reach Ads.
func TestRRTrackSendsToGA4Only(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, Storefront(StorefrontProps{Title: "t"}).Render(context.Background(), &buf))
	page := buf.String()

	require.Contains(t, page, "gtag('config', 'AW-18592387319')", "the Ads tag is configured; this guard is about routing around it")

	tracker := regexp.MustCompile(`(?s)window\.rrTrack = function\(name, params\) \{(.*?)\n\s*\};`).FindStringSubmatch(page)
	require.Len(t, tracker, 2, "cannot find the rrTrack definition in the layout")
	assert.Contains(t, tracker[1], "send_to: 'G-5L4668EW6X'",
		"rrTrack must pin send_to to the GA4 tag, or every rrTrack event also reaches Google Ads")
}
