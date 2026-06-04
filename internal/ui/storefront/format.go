package storefront

import (
	"fmt"
	"strings"
)

// formatCents formats an integer cents amount as a dollar string.
func formatCents(cents int) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}

// initial returns the first two letters of a title, uppercased, for use as a
// monogram placeholder when a product has no image. Safe for short titles.
func initial(title string) string {
	r := []rune(strings.TrimSpace(title))
	if len(r) == 0 {
		return "RR"
	}
	if len(r) == 1 {
		return strings.ToUpper(string(r))
	}
	return strings.ToUpper(string(r[0:2]))
}

// discountedPrice applies a percentage discount to a price in cents.
func discountedPrice(cents int, discountPct int) int {
	return cents - (cents * discountPct / 100)
}

// pageRange returns up to 5 page numbers centered around the current page.
func pageRange(current, total int) []int {
	start := current - 2
	if start < 1 {
		start = 1
	}
	end := start + 4
	if end > total {
		end = total
		start = end - 4
		if start < 1 {
			start = 1
		}
	}
	pages := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		pages = append(pages, i)
	}
	return pages
}
