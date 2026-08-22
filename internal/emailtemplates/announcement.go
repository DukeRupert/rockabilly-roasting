package emailtemplates

import (
	"net/url"
	"strings"
)

// AnnouncementData holds data for a staff-composed announcement — a one-off
// notice to a whole customer audience (a holiday shipping delay, a closure, a
// price change).
//
// Nothing here is HTML. Staff compose in a plain textarea and the body arrives
// as pre-split paragraphs and segments, so the branded shell always survives
// and no character anyone types can become markup. That is the whole reason
// this type does not simply carry a body string.
type AnnouncementData struct {
	Heading string
	// Greeting is the recipient's name — company for a wholesale account, first
	// name for retail. Empty renders no greeting line at all rather than
	// "Hi there," which reads worse than no greeting.
	Greeting   string
	Paragraphs []AnnouncementParagraph
	// UnsubscribeURL is the signed opt-out link. Empty when no signing secret
	// is configured; the footer then falls back to asking them to reply.
	UnsubscribeURL string
	StoreName      string
	StoreURL       string
}

// AnnouncementParagraph is one paragraph in both renderings: Segments for the
// HTML body (so bare URLs become real links) and Text for the plain-text part,
// where a URL is already clickable in every mail client.
type AnnouncementParagraph struct {
	Segments []AnnouncementSegment
	Text     string
}

// AnnouncementSegment is a run of text within a paragraph. A non-empty URL
// means "render this run as a link"; the template escapes Text either way.
type AnnouncementSegment struct {
	Text string
	URL  string
}

// ParseAnnouncementBody turns a staff-typed body into paragraphs.
//
// Blank lines start a new paragraph; single newlines inside one are collapsed,
// so a soft-wrapped paste does not render as a ragged block. Bare http(s) URLs
// become links — staff writing "details at https://... " should not have to
// know markup, and the alternative (a rich editor, or accepting HTML) is how
// staff input ends up in the mail body as markup.
func ParseAnnouncementBody(body string) []AnnouncementParagraph {
	var out []AnnouncementParagraph
	for _, block := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		var lines []string
		for _, line := range strings.Split(block, "\n") {
			if t := strings.TrimSpace(line); t != "" {
				lines = append(lines, t)
			}
		}
		if len(lines) == 0 {
			continue
		}
		text := strings.Join(lines, " ")
		out = append(out, AnnouncementParagraph{Text: text, Segments: linkifySegments(text)})
	}
	return out
}

// linkifySegments splits a paragraph into plain runs and URL runs.
func linkifySegments(text string) []AnnouncementSegment {
	var (
		segments []AnnouncementSegment
		plain    strings.Builder
	)
	flush := func() {
		if plain.Len() > 0 {
			segments = append(segments, AnnouncementSegment{Text: plain.String()})
			plain.Reset()
		}
	}

	for i, word := range strings.Fields(text) {
		if i > 0 {
			plain.WriteByte(' ')
		}
		// Trailing sentence punctuation is part of the sentence, not the URL —
		// linking it would break the target as often as not.
		trimmed := strings.TrimRight(word, ".,;:!?)")
		if !isLinkableURL(trimmed) {
			plain.WriteString(word)
			continue
		}
		flush()
		segments = append(segments, AnnouncementSegment{Text: trimmed, URL: trimmed})
		if tail := word[len(trimmed):]; tail != "" {
			plain.WriteString(tail)
		}
	}
	flush()
	return segments
}

// isLinkableURL reports whether a word is an absolute http(s) URL with a host.
// Scheme-less "example.com" is deliberately not linked: guessing at intent in
// somebody's mail body produces links to things they never meant to link.
func isLinkableURL(word string) bool {
	if !strings.HasPrefix(word, "http://") && !strings.HasPrefix(word, "https://") {
		return false
	}
	u, err := url.Parse(word)
	return err == nil && u.Host != ""
}
