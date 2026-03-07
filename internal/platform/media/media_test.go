package media

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProductImageURL(t *testing.T) {
	cfg := &Config{
		CFImagesBaseURL: "https://imagedelivery.net/abc123hash",
	}

	tests := []struct {
		name     string
		imageID  string
		variant  ImageVariant
		expected string
	}{
		{
			name:     "thumbnail variant",
			imageID:  "img-uuid-001",
			variant:  VariantThumbnail,
			expected: "https://imagedelivery.net/abc123hash/img-uuid-001/thumbnail",
		},
		{
			name:     "card variant",
			imageID:  "img-uuid-002",
			variant:  VariantCard,
			expected: "https://imagedelivery.net/abc123hash/img-uuid-002/card",
		},
		{
			name:     "hero variant",
			imageID:  "img-uuid-003",
			variant:  VariantHero,
			expected: "https://imagedelivery.net/abc123hash/img-uuid-003/hero",
		},
		{
			name:     "public (original) variant",
			imageID:  "img-uuid-004",
			variant:  VariantPublic,
			expected: "https://imagedelivery.net/abc123hash/img-uuid-004/public",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.ProductImageURL(tt.imageID, tt.variant)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestPlaceholderURL(t *testing.T) {
	t.Run("default placeholder", func(t *testing.T) {
		cfg := &Config{}
		assert.Equal(t, "/static/placeholder-product.png", cfg.PlaceholderURL())
	})

	t.Run("custom placeholder", func(t *testing.T) {
		cfg := &Config{PlaceholderPath: "/static/images/no-image.webp"}
		assert.Equal(t, "/static/images/no-image.webp", cfg.PlaceholderURL())
	})
}
