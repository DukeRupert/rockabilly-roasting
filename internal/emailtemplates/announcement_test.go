package emailtemplates_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/emailtemplates"
)

func TestParseAnnouncementBody_SplitsOnBlankLinesAndCollapsesSoftWraps(t *testing.T) {
	paragraphs := emailtemplates.ParseAnnouncementBody(
		"We're closed Monday\nfor Labor Day.\n\nThe run moves to Tuesday.")

	require.Len(t, paragraphs, 2)
	// The soft wrap inside the first block must not survive as a line break —
	// pasted-in text is nearly always wrapped, and honouring those newlines
	// renders a ragged block.
	assert.Equal(t, "We're closed Monday for Labor Day.", paragraphs[0].Text)
	assert.Equal(t, "The run moves to Tuesday.", paragraphs[1].Text)
}

func TestParseAnnouncementBody_IgnoresEmptyBlocks(t *testing.T) {
	paragraphs := emailtemplates.ParseAnnouncementBody("\n\n  \n\nOnly one paragraph.\n\n\n")
	require.Len(t, paragraphs, 1)
	assert.Equal(t, "Only one paragraph.", paragraphs[0].Text)
}

func TestParseAnnouncementBody_LinkifiesBareURLs(t *testing.T) {
	paragraphs := emailtemplates.ParseAnnouncementBody("Schedule is at https://example.com/hours now.")
	require.Len(t, paragraphs, 1)

	var linked []string
	for _, seg := range paragraphs[0].Segments {
		if seg.URL != "" {
			linked = append(linked, seg.URL)
		}
	}
	require.Equal(t, []string{"https://example.com/hours"}, linked)

	// The whole paragraph must still be reconstructible from the segments, or
	// linkifying silently eats text.
	var rebuilt string
	for _, seg := range paragraphs[0].Segments {
		rebuilt += seg.Text
	}
	assert.Equal(t, "Schedule is at https://example.com/hours now.", rebuilt)
}

func TestParseAnnouncementBody_LeavesTrailingPunctuationOutOfTheLink(t *testing.T) {
	paragraphs := emailtemplates.ParseAnnouncementBody("See https://example.com/hours.")
	require.Len(t, paragraphs, 1)

	var link string
	for _, seg := range paragraphs[0].Segments {
		if seg.URL != "" {
			link = seg.URL
		}
	}
	// The full stop belongs to the sentence. Including it would break the link
	// as often as not.
	assert.Equal(t, "https://example.com/hours", link)
	assert.Equal(t, "See https://example.com/hours.", paragraphs[0].Text)
}

func TestParseAnnouncementBody_DoesNotLinkifySchemelessOrNonHTTP(t *testing.T) {
	for _, body := range []string{
		"Visit example.com for details.",
		"Mail javascript:alert(1) is not a link.",
		"Try ftp://example.com/file for the file.",
	} {
		paragraphs := emailtemplates.ParseAnnouncementBody(body)
		require.Len(t, paragraphs, 1, body)
		for _, seg := range paragraphs[0].Segments {
			assert.Empty(t, seg.URL, "should not linkify in %q", body)
		}
	}
}

func TestRenderAnnouncement_EscapesStaffInputAndOmitsEmptyGreeting(t *testing.T) {
	renderer, err := emailtemplates.New()
	require.NoError(t, err)

	html, text, err := renderer.Render("announcement", emailtemplates.AnnouncementData{
		Heading:    "Closed <b>Monday</b>",
		Paragraphs: emailtemplates.ParseAnnouncementBody("Back <script>alert(1)</script> Tuesday."),
		StoreName:  "Rockabilly Roasting Co.",
		StoreURL:   "https://example.com",
	})
	require.NoError(t, err)

	// Staff type into a plain textarea; nothing they type may reach the body as
	// markup. This is the guarantee that lets the composer stay a textarea.
	assert.NotContains(t, html, "<script>")
	assert.NotContains(t, html, "<b>Monday</b>")
	assert.Contains(t, html, "&lt;b&gt;Monday&lt;/b&gt;")

	// No greeting configured means no greeting line, not "Hi ,".
	assert.NotContains(t, html, "Hi ,")
	assert.NotContains(t, text, "Hi ,")

	// With no signing secret there is no link, so the footer must still tell
	// the reader how to get off the list.
	assert.Contains(t, text, "Reply and we'll take you off the list")
}
