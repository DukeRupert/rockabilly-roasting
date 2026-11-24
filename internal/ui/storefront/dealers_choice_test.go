package storefront

import "testing"

func TestBagSizesSentence(t *testing.T) {
	cases := map[string][]int{
		"Three, four, or six 12oz bags.": {3, 4, 6},
		"Three or six 12oz bags.":        {3, 6},
		"Four 12oz bags.":                {4},
		"One 12oz bag.":                  {1},
		"Six, twelve, or 24 12oz bags.":  {6, 12, 24},
		"Choose how many 12oz bags.":     nil,
	}
	for want, sizes := range cases {
		if got := bagSizesSentence(sizes); got != want {
			t.Errorf("bagSizesSentence(%v) = %q, want %q", sizes, got, want)
		}
	}
}
