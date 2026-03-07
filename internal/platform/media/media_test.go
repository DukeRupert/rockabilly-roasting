package media

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProductImageURL(t *testing.T) {
	cfg := &Config{
		MediaBaseURL: "https://media.hiri.com",
	}

	tests := []struct {
		name     string
		r2Key    string
		variant  ImageVariant
		expected string
	}{
		{
			name:     "thumbnail variant",
			r2Key:    "products/img-001.jpg",
			variant:  VariantThumbnail,
			expected: "https://media.hiri.com/cdn-cgi/image/width=200,height=200,fit=crop/products/img-001.jpg",
		},
		{
			name:     "card variant",
			r2Key:    "products/img-002.jpg",
			variant:  VariantCard,
			expected: "https://media.hiri.com/cdn-cgi/image/width=400,height=400,fit=crop/products/img-002.jpg",
		},
		{
			name:     "hero variant",
			r2Key:    "products/img-003.jpg",
			variant:  VariantHero,
			expected: "https://media.hiri.com/cdn-cgi/image/width=800,height=800,fit=contain/products/img-003.jpg",
		},
		{
			name:     "public (original) variant",
			r2Key:    "products/img-004.jpg",
			variant:  VariantPublic,
			expected: "https://media.hiri.com/cdn-cgi/image/width=1200,fit=scale-down/products/img-004.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.ProductImageURL(tt.r2Key, tt.variant)
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
