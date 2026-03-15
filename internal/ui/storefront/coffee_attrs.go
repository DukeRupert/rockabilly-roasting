package storefront

import "strings"

// CoffeeAttrs holds coffee-specific product attributes for template rendering.
// This is a presentation type populated by the handler from whatever attribute
// storage the domain layer provides. Nil means "no coffee attributes."
type CoffeeAttrs struct {
	RoastLevel     string   // "light", "medium-light", "medium", "medium-dark", "dark"
	Process        string   // "washed", "natural", "honey"
	OriginType     string   // "single-origin", "blend"
	Regions        []string // e.g. ["Ethiopia", "Guatemala"]
	TastingNotes   []string // max 4-5, e.g. ["berry", "dark chocolate"]
	Body           string   // "light", "medium", "full"
	Acidity        string   // "low", "medium", "high"
	Sweetness      string   // "low", "medium", "high"
	Finish         string   // e.g. "clean and crisp"
	BrewMethods    []string // e.g. ["espresso", "pour-over"]
	IsDecaf        bool
	IsSeasonal     bool
	Certifications []string // e.g. ["organic", "fair-trade"]
}

// roastLevelIndex returns 1-5 for the roast level (light=1, dark=5).
// Returns 0 for unrecognized values — callers should use validRoastLevel() to guard.
func roastLevelIndex(level string) int {
	switch strings.ToLower(level) {
	case "light":
		return 1
	case "medium-light":
		return 2
	case "medium":
		return 3
	case "medium-dark":
		return 4
	case "dark":
		return 5
	default:
		return 0
	}
}

// validRoastLevel returns true if the roast level is a recognized enum value.
func validRoastLevel(level string) bool {
	return roastLevelIndex(level) > 0
}

// roastLevelLabel returns a display label for the roast level.
func roastLevelLabel(level string) string {
	switch strings.ToLower(level) {
	case "light":
		return "Light"
	case "medium-light":
		return "Medium-Light"
	case "medium":
		return "Medium"
	case "medium-dark":
		return "Medium-Dark"
	case "dark":
		return "Dark"
	default:
		return ""
	}
}

// originLine returns a formatted origin string like "Single Origin — Ethiopia"
// or "Blend — Ethiopia, Guatemala".
func originLine(attrs *CoffeeAttrs) string {
	if attrs == nil {
		return ""
	}
	var prefix string
	switch strings.ToLower(attrs.OriginType) {
	case "single-origin":
		prefix = "Single Origin"
	case "blend":
		prefix = "Blend"
	default:
		prefix = ""
	}
	regions := strings.Join(attrs.Regions, ", ")
	if prefix != "" && regions != "" {
		return prefix + " — " + regions
	}
	if prefix != "" {
		return prefix
	}
	return regions
}

// scaleLevel returns 1-3 for low/medium/high (or full) enum values.
func scaleLevel(val string) int {
	switch strings.ToLower(val) {
	case "low", "light":
		return 1
	case "medium":
		return 2
	case "high", "full":
		return 3
	default:
		return 0
	}
}

// formatBrewMethod returns a display label for a brew method slug.
func formatBrewMethod(method string) string {
	switch strings.ToLower(method) {
	case "espresso":
		return "Espresso"
	case "drip":
		return "Drip"
	case "pour-over":
		return "Pour-Over"
	case "french-press":
		return "French Press"
	case "cold-brew":
		return "Cold Brew"
	default:
		return strings.Title(method) //nolint:staticcheck
	}
}

// formatCertification returns a display label for a certification slug.
func formatCertification(cert string) string {
	switch strings.ToLower(cert) {
	case "organic":
		return "Organic"
	case "fair-trade":
		return "Fair Trade"
	case "rainforest-alliance":
		return "Rainforest Alliance"
	default:
		return strings.Title(strings.ReplaceAll(cert, "-", " ")) //nolint:staticcheck
	}
}

// hasTastingProfile returns true if the attributes have enough data to render
// the tasting profile section.
func hasTastingProfile(attrs *CoffeeAttrs) bool {
	if attrs == nil {
		return false
	}
	return hasFlavorScales(attrs) || hasProfileDetails(attrs)
}

// hasFlavorScales returns true if at least one flavor scale (body/acidity/sweetness) is set.
func hasFlavorScales(attrs *CoffeeAttrs) bool {
	return attrs.Body != "" || attrs.Acidity != "" || attrs.Sweetness != ""
}

// hasProfileDetails returns true if process, finish, brew methods, certs, or seasonal are set.
func hasProfileDetails(attrs *CoffeeAttrs) bool {
	return attrs.Process != "" || attrs.Finish != "" || len(attrs.BrewMethods) > 0 ||
		len(attrs.Certifications) > 0 || attrs.IsSeasonal
}

// cardTastingNotes returns at most n tasting notes joined by ", " with title case.
func cardTastingNotes(notes []string, n int) string {
	if len(notes) == 0 {
		return ""
	}
	if n > len(notes) {
		n = len(notes)
	}
	titled := make([]string, n)
	for i := 0; i < n; i++ {
		titled[i] = strings.Title(notes[i]) //nolint:staticcheck
	}
	return strings.Join(titled, ", ")
}
