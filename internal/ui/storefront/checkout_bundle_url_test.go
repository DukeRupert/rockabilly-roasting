package storefront

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/ui/utils"
)

// The Svelte checkout bundles are rebuilt with every change to the checkout or
// subscribe flow, keep their names, and are served with max-age=3600. Linked by
// a bare path, a returning visitor ran the previous bundle for up to an hour
// after a deploy — against a server whose endpoints had already moved on. After
// the multi-line subscribe release that bundle mounts from data attributes the
// page no longer renders. Each is linked through utils.ScriptURL, which versions
// the URL per deploy (the process start time), as the templui scripts are.

func renderedScriptSrcs(t *testing.T, html string) []string {
	t.Helper()
	var out []string
	for _, m := range regexp.MustCompile(`<script[^>]*\ssrc="([^"]+)"`).FindAllStringSubmatch(html, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestSubscribePage_LinksItsBundleThroughScriptURL(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, SubscribeContent(SubscribePageProps{
		Lines: []SubscribeLineProps{{PlanName: "Weekly", Quantity: 1, ProductTitle: "Coffee"}},
	}).Render(context.Background(), &buf))
	assert.Contains(t, renderedScriptSrcs(t, buf.String()), utils.ScriptURL("/static/checkout/subscribe.js"))
}

func TestCheckoutPage_LinksItsBundleThroughScriptURL(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, CheckoutContent(CheckoutPageProps{}).Render(context.Background(), &buf))
	assert.Contains(t, renderedScriptSrcs(t, buf.String()), utils.ScriptURL("/static/checkout/checkout.js"))
}

// The render tests cover the two pages that exist. This covers the next one: no
// template links a checkout bundle by a bare path.
func TestNoTemplateLinksACheckoutBundleByABarePath(t *testing.T) {
	bare := regexp.MustCompile(`(src|href)="/static/checkout/`)
	root := filepath.Join("..")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".templ" {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		assert.False(t, bare.Match(src), "%s links a checkout bundle without utils.ScriptURL", path)
		return nil
	})
	require.NoError(t, err)
}
