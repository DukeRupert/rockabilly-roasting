package app

import "testing"

// TestIsShippingGrandfathered guards the metadata read that decides whether a
// renewal waives shipping. jsonb 'true' round-trips through map[string]any as a
// Go bool; anything else (absent, wrong type, false) must read as not
// grandfathered so renewals price shipping normally.
func TestIsShippingGrandfathered(t *testing.T) {
	cases := []struct {
		name string
		meta map[string]any
		want bool
	}{
		{"flag true", map[string]any{"shipping_grandfathered": true}, true},
		{"flag false", map[string]any{"shipping_grandfathered": false}, false},
		{"flag absent", map[string]any{"dunning_attempt": float64(2)}, false},
		{"nil metadata", nil, false},
		{"wrong type (string)", map[string]any{"shipping_grandfathered": "true"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isShippingGrandfathered(c.meta); got != c.want {
				t.Fatalf("isShippingGrandfathered(%v) = %v, want %v", c.meta, got, c.want)
			}
		})
	}
}
