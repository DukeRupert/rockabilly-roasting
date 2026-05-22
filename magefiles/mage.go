//go:build mage

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

var Default = Dev

// Dev generates templates + CSS, builds the server, and runs it.
func Dev() error {
	if err := Templ(); err != nil {
		return err
	}
	if err := CSS(); err != nil {
		return err
	}
	if err := Build(); err != nil {
		return err
	}
	return sh.RunV("./server")
}

// Build compiles the server binary.
func Build() error {
	return sh.RunV("go", "build", "./cmd/server")
}

// Templ generates templ templates.
func Templ() error {
	return sh.RunV("templ", "generate")
}

// CSS compiles Tailwind CSS (minified).
func CSS() error {
	return sh.RunV("npx", "@tailwindcss/cli",
		"-i", "internal/ui/assets/css/input.css",
		"-o", "internal/ui/assets/css/output.css",
		"--minify",
	)
}

// Watch runs Tailwind CSS in watch mode.
func Watch() error {
	return sh.RunV("npx", "@tailwindcss/cli",
		"-i", "internal/ui/assets/css/input.css",
		"-o", "internal/ui/assets/css/output.css",
		"--watch",
	)
}

// Checkout builds the Svelte checkout bundle.
func Checkout() error {
	return sh.RunV("npm", "run", "build", "--prefix", "ui/checkout")
}

// Seed creates an admin staff user. Set SEED_EMAIL, SEED_PASSWORD, and optionally SEED_NAME.
func Seed() error {
	return sh.RunV("go", "run", "./cmd/seed")
}

// Test runs all tests.
func Test() error {
	return sh.RunV("go", "test", "./...", "-count=1")
}

// TestVerbose runs all tests with verbose output.
func TestVerbose() error {
	return sh.RunV("go", "test", "./...", "-v", "-count=1")
}

// Vet runs go vet on all packages.
func Vet() error {
	return sh.RunV("go", "vet", "./...")
}

// Lint runs static analysis (currently go vet, extensible for golangci-lint).
func Lint() {
	mg.Deps(Vet)
}

// Generate runs all code generators.
func Generate() error {
	return sh.RunV("go", "generate", "./...")
}

// Clean removes build artifacts.
func Clean() error {
	return os.Remove("server")
}

// Check runs lint, scoping check, admin UI lint, and tests together (CI-style gate).
func Check() {
	mg.Deps(Lint, CheckScoping, CheckAdminUI, Test)
}

// CheckAdminUI fails if any admin .templ file reaches for storefront/marketing
// classes (paper-and-ink colors, stamp shadows, brand fonts, heavy borders, etc.).
// The admin uses the rr-* token layer with a warm-professional override; direct
// paper-and-ink utilities bypass that and lock you into the wrong palette.
//
// staff_login.templ is excluded — it's a standalone branded splash page using
// its own layout and font import.
//
// See docs/admin-ui.md for the full allowed/banned class lists and rationale.
func CheckAdminUI() error {
	// Banned classes — listed exactly as they appear in templates (no prefix
	// stripping). The regex below treats each as a whole class name and won't
	// match prefixes of longer class names (e.g. `bg-ink` does NOT flag
	// `bg-ink-soft`, which is listed separately).
	bannedClasses := []string{
		// Direct paper-and-ink color utilities — admin must go through rr-* tokens.
		"bg-paper", "bg-paper-warm", "bg-paper-deep", "bg-cream-hi",
		"bg-ink", "bg-ink-soft",
		"bg-rust", "bg-rust-deep",
		"bg-candle", "bg-candle-deep", "bg-candle-soft",
		"bg-espresso", "bg-espresso-deep",
		"bg-chrome", "bg-chrome-deep",
		"bg-sage",
		"text-paper", "text-paper-warm", "text-paper-deep", "text-cream-hi",
		"text-ink", "text-ink-soft",
		"text-rust", "text-rust-deep",
		"text-candle", "text-candle-deep", "text-candle-soft",
		"text-espresso", "text-espresso-deep",
		"text-chrome", "text-chrome-deep",
		"text-sage",
		"border-paper", "border-paper-warm", "border-paper-deep",
		"border-ink", "border-ink-soft",
		"border-rust", "border-rust-deep",
		"border-candle", "border-espresso", "border-chrome", "border-sage",
		// Storefront display/script/typewriter font families.
		"font-slab", "font-heritage", "font-script", "font-special", "font-oswald",
		// Storefront stamp / texture / motion behaviors.
		"btn-stamp", "btn-stamp-paper",
		"shadow-stamp", "shadow-stamp-sm", "shadow-stamp-lg", "shadow-stamp-paper",
		"texture-halftone-paper", "texture-dots", "texture-grid",
		"flame-stripe", "candle-flicker", "marquee-inner",
		"window-glow", "string-lights",
		"product-card", "nav-link", "roast-dot", "cart-checkmark",
		// Heavy border weights — admin uses 1px hairlines.
		"border-2", "border-t-2", "border-r-2", "border-b-2", "border-l-2",
		"border-4", "border-t-4", "border-r-4", "border-b-4", "border-l-4",
	}

	excluded := map[string]bool{
		filepath.FromSlash("internal/ui/admin/staff_login.templ"): true,
	}

	// Match a banned class only when it appears as a complete token — bounded
	// on each side by something other than a word char or hyphen. This rejects
	// false-positives like `bg-ink-soft` for the pattern `bg-ink`.
	re := regexp.MustCompile(`(^|[^\w-])(` + strings.Join(bannedClasses, `|`) + `)([^\w-]|$)`)

	var violations []string

	err := filepath.Walk("internal/ui/admin", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".templ") {
			return nil
		}
		if excluded[path] {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for lineNum, line := range strings.Split(string(data), "\n") {
			matches := re.FindAllStringSubmatch(line, -1)
			for _, m := range matches {
				violations = append(violations, fmt.Sprintf(
					"%s:%d: banned class %q — %s",
					path, lineNum+1, m[2], strings.TrimSpace(line),
				))
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk admin templ files: %w", err)
	}

	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, v)
		}
		return fmt.Errorf("%d admin UI lint violation(s) — see docs/admin-ui.md", len(violations))
	}
	return nil
}

// CheckScoping verifies customer-facing handlers don't call staff-only
// lookup methods (suffixed `AsStaff`) without an immediately preceding
// `// scoping:` waiver comment.
//
// The check is a defense-in-depth against accidental IDOR — staff-only
// methods bypass customer scoping by design; any customer-facing call site
// must document why the lookup is safe.
func CheckScoping() error {
	customerFacingFiles := []string{
		"internal/web/account.go",
		"internal/web/cart.go",
		"internal/web/checkout.go",
		"internal/web/customer_auth.go",
		"internal/web/storefront.go",
		"internal/web/subscribe.go",
		"internal/web/wholesale.go",
	}

	var violations []string

	for _, path := range customerFacingFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			// Missing file is a drift signal; fail loudly so the list is maintained.
			return fmt.Errorf("scoping check: %s: %w", path, err)
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if !strings.Contains(line, "AsStaff(") {
				continue
			}
			// Accept a `// scoping:` waiver within the 3 lines above the call.
			hasWaiver := false
			for j := i - 1; j >= 0 && j >= i-3; j-- {
				if strings.Contains(lines[j], "// scoping:") {
					hasWaiver = true
					break
				}
			}
			if !hasWaiver {
				violations = append(violations, fmt.Sprintf(
					"%s:%d: AsStaff call without `// scoping:` waiver — %s",
					path, i+1, strings.TrimSpace(line),
				))
			}
		}
	}

	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, v)
		}
		return fmt.Errorf("%d scoping violation(s) in customer-facing handlers", len(violations))
	}
	return nil
}

// DB namespace groups database migration commands.
type DB mg.Namespace

func gooseCmd(args ...string) error {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL environment variable is required")
	}
	base := []string{"-dir", "db/migrations", "postgres", dbURL}
	return sh.RunV("goose", append(base, args...)...)
}

// Migrate runs all pending database migrations.
func (DB) Migrate() error {
	return gooseCmd("up")
}

// Rollback rolls back the most recent database migration.
func (DB) Rollback() error {
	return gooseCmd("down")
}

// Status prints the current migration status.
func (DB) Status() error {
	return gooseCmd("status")
}

// Create creates a new SQL migration file with the given name.
func (DB) Create(name string) error {
	return gooseCmd("create", name, "sql")
}

// FixPendingPaid sweeps orders stuck in status=pending, payment_status=captured.
// Pass --dry-run to preview without writing.
func FixPendingPaid(args ...string) error {
	cmdArgs := []string{"run", "./cmd/fix-pending-paid"}
	cmdArgs = append(cmdArgs, args...)
	return sh.RunV("go", cmdArgs...)
}

// WCMigrate imports WooCommerce subscriptions into Hiri.
// Set WC_CONSUMER_KEY, WC_CONSUMER_SECRET, and DATABASE_URL.
// Use --dry-run to validate without importing.
// Use --mapping=path/to/mapping.json for variant ID mapping.
func WCMigrate(args ...string) error {
	cmdArgs := []string{"run", "./cmd/migrate"}
	cmdArgs = append(cmdArgs, args...)
	return sh.RunV("go", cmdArgs...)
}
