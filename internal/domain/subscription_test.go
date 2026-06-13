package domain

import "testing"

// TestSubscriptionShippingGrandfathered guards the metadata read that decides
// whether a renewal waives shipping. jsonb true round-trips through
// map[string]any as a Go bool; anything else (absent, wrong type, false, nil)
// must read as not grandfathered so renewals price shipping normally.
func TestSubscriptionShippingGrandfathered(t *testing.T) {
	cases := []struct {
		name string
		meta map[string]any
		want bool
	}{
		{"flag true", map[string]any{SubscriptionMetaShippingGrandfathered: true}, true},
		{"flag false", map[string]any{SubscriptionMetaShippingGrandfathered: false}, false},
		{"flag absent", map[string]any{"dunning_attempt": float64(2)}, false},
		{"nil metadata", nil, false},
		{"wrong type (string)", map[string]any{SubscriptionMetaShippingGrandfathered: "true"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Subscription{Metadata: c.meta}
			if got := s.ShippingGrandfathered(); got != c.want {
				t.Fatalf("ShippingGrandfathered(%v) = %v, want %v", c.meta, got, c.want)
			}
		})
	}
}
