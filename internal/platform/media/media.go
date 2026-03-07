package media

import "fmt"

// ImageVariant identifies a named transform preset configured in the
// Cloudflare Images dashboard. Appended to the delivery URL as the
// final path segment.
type ImageVariant string

const (
	VariantThumbnail ImageVariant = "thumbnail" // 200x200, crop
	VariantCard      ImageVariant = "card"      // 400x400, crop
	VariantHero      ImageVariant = "hero"      // 800x800, fit
	VariantPublic    ImageVariant = "public"     // original size
)

// Config holds Cloudflare Images and R2 configuration.
type Config struct {
	// CFImagesBaseURL is the delivery base URL, e.g.
	// "https://imagedelivery.net/<account_hash>".
	CFImagesBaseURL string

	// PlaceholderPath is the local static path for missing images.
	PlaceholderPath string
}

// ProductImageURL constructs a Cloudflare Images delivery URL from a
// CF image ID and a named variant.
//
//	https://imagedelivery.net/<account_hash>/<image_id>/<variant>
func (c *Config) ProductImageURL(cfImageID string, variant ImageVariant) string {
	return fmt.Sprintf("%s/%s/%s", c.CFImagesBaseURL, cfImageID, variant)
}

// PlaceholderURL returns the local fallback path when no image exists.
func (c *Config) PlaceholderURL() string {
	if c.PlaceholderPath != "" {
		return c.PlaceholderPath
	}
	return "/static/placeholder-product.png"
}
