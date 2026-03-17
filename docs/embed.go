// Package docs embeds the user guide Markdown files for in-app help pages.
package docs

import "embed"

//go:embed guide/admin/*.md guide/storefront/*.md guide/wholesale/*.md
var GuideFS embed.FS
