package media

import "fmt"

// ImageVariant identifies a named transform preset. Each variant maps to
// Cloudflare Image Transformation parameters appended via /cdn-cgi/image/.
type ImageVariant string

const (
	VariantThumbnail ImageVariant = "thumbnail" // 200x200, crop
	VariantCard      ImageVariant = "card"      // 400x400, crop
	VariantHero      ImageVariant = "hero"      // 800x800, fit
	VariantPublic    ImageVariant = "public"     // original size
)

// variantParams maps each variant to its Cloudflare Image Transformation params.
var variantParams = map[ImageVariant]string{
	VariantThumbnail: "width=200,height=200,fit=crop",
	VariantCard:      "width=400,height=400,fit=crop",
	VariantHero:      "width=800,height=800,fit=contain",
	VariantPublic:    "width=1200,fit=scale-down",
}

// Config holds media delivery configuration.
type Config struct {
	// MediaBaseURL is the public domain serving R2 content with Cloudflare
	// Image Transformations enabled, e.g. "https://media.hiri.com".
	MediaBaseURL string

	// PlaceholderPath is the local static path for missing images.
	PlaceholderPath string
}

// ProductImageURL constructs a Cloudflare Image Transformations URL for an
// R2 object key and a named variant.
//
//	https://<domain>/cdn-cgi/image/<params>/<r2_key>
func (c *Config) ProductImageURL(r2Key string, variant ImageVariant) string {
	params, ok := variantParams[variant]
	if !ok {
		params = variantParams[VariantPublic]
	}
	return fmt.Sprintf("%s/cdn-cgi/image/%s/%s", c.MediaBaseURL, params, r2Key)
}

// PlaceholderURL returns the local fallback path when no image exists.
func (c *Config) PlaceholderURL() string {
	if c.PlaceholderPath != "" {
		return c.PlaceholderPath
	}
	return "/static/placeholder-product.png"
}
