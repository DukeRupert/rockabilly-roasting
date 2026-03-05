package storefront

import "fmt"

// formatCents formats an integer cents amount as a dollar string.
func formatCents(cents int) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}
