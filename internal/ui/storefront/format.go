package storefront

import "fmt"

// formatCents formats an integer cents amount as a dollar string.
func formatCents(cents int) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
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
