package help

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// Article holds a single rendered help article.
type Article struct {
	Slug  string
	Title string
	HTML  template.HTML
}

// Registry holds all pre-rendered help articles, keyed by audience.
type Registry struct {
	articles map[string]map[string]Article // audience -> slug -> Article
	tocs     map[string][]Article          // audience -> ordered list
}

// article ordering per audience
var tocOrder = map[string][]string{
	"admin": {
		"dashboard",
		"catalog",
		"orders",
		"fulfillment",
		"subscriptions",
		"customers",
		"wholesale",
		"invoices",
		"discounts",
		"settings",
		"audit",
	},
	"storefront": {
		"shopping",
		"checkout",
		"subscriptions",
		"account",
	},
	"wholesale": {
		"applying",
		"getting-started",
		"ordering",
	},
}

// New reads all Markdown files from the given embed.FS, converts them to HTML,
// and returns a Registry. The FS should contain files at guide/<audience>/<slug>.md.
func New(fs embed.FS) (*Registry, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)

	r := &Registry{
		articles: make(map[string]map[string]Article),
		tocs:     make(map[string][]Article),
	}

	for audience, slugs := range tocOrder {
		r.articles[audience] = make(map[string]Article)
		for _, slug := range slugs {
			path := fmt.Sprintf("guide/%s/%s.md", audience, slug)
			data, err := fs.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", path, err)
			}

			var buf bytes.Buffer
			if err := md.Convert(data, &buf); err != nil {
				return nil, fmt.Errorf("convert %s: %w", path, err)
			}

			title := extractTitle(data, slug)
			art := Article{
				Slug:  slug,
				Title: title,
				HTML:  template.HTML(buf.String()),
			}
			r.articles[audience][slug] = art
			r.tocs[audience] = append(r.tocs[audience], art)
		}
	}

	return r, nil
}

// Article returns a single article by audience and slug.
func (r *Registry) Article(audience, slug string) (Article, error) {
	bySlug, ok := r.articles[audience]
	if !ok {
		return Article{}, fmt.Errorf("unknown audience %q", audience)
	}
	art, ok := bySlug[slug]
	if !ok {
		return Article{}, fmt.Errorf("article %q not found in %q", slug, audience)
	}
	return art, nil
}

// TOC returns the ordered list of articles for an audience.
func (r *Registry) TOC(audience string) []Article {
	return r.tocs[audience]
}

// extractTitle pulls the first # heading from markdown, or falls back to slug.
func extractTitle(data []byte, fallback string) string {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return humanize(fallback)
}

func humanize(slug string) string {
	s := strings.ReplaceAll(slug, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	return strings.Title(s) //nolint:staticcheck
}
