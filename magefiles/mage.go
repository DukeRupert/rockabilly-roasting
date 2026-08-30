//go:build mage

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	mg.Deps(Lint, CheckScoping, CheckAdminUI, CheckTemplSync, Test)
}

// CheckTemplSync fails if any *_templ.go on disk differs from what templ
// generates for its source.
//
// This has shipped twice. Both times the cause was the same: `templ generate`
// failed or was never re-run, and nothing downstream noticed — `go build`,
// `go vet`, the tests and the admin-UI lint all read the stale artifact quite
// happily, so a green check said nothing about it. The second time, an
// accessibility fix lived in the source and not in the file the repo ships.
//
// Compared by content across a regeneration rather than against git, so it says
// "this artifact does not match its source" and not "you have uncommitted
// work" — the latter would fire through every ordinary edit. Regenerating
// leaves the corrected files in place, which is the state you wanted anyway.
func CheckTemplSync() error {
	before, err := templArtifactHashes()
	if err != nil {
		return err
	}
	if err := Templ(); err != nil {
		return err
	}
	after, err := templArtifactHashes()
	if err != nil {
		return err
	}

	var stale []string
	for path, sum := range after {
		if before[path] != sum {
			stale = append(stale, path)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		fmt.Fprintln(os.Stderr, "generated templ files did not match their source:")
		for _, path := range stale {
			fmt.Fprintln(os.Stderr, "  "+path)
		}
		return fmt.Errorf("regenerated %d file(s) — commit the result", len(stale))
	}
	return nil
}

// templArtifactHashes fingerprints every generated templ file.
func templArtifactHashes() (map[string]string, error) {
	sums := map[string]string{}
	err := filepath.Walk("internal/ui", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, "_templ.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sums[path] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("hash templ artifacts: %w", err)
	}
	return sums, nil
}

// CheckAdminUI fails if any admin .templ file reaches for storefront/marketing
// classes (paper-and-ink colors, stamp shadows, brand fonts, heavy borders, etc.).
// The admin uses the rr-* token layer with a warm-professional override; direct
// paper-and-ink utilities bypass that and lock you into the wrong palette.
//
// staff_login.templ and staff_setup.templ are excluded — they're standalone
// branded splash pages (login, and the public invite password-setup page) using
// their own layout and font import rather than the admin shell.
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
		filepath.FromSlash("internal/ui/admin/staff_setup.templ"): true,
		// The storefront layout is *meant* to use paper-and-ink directly; it
		// only shares a directory with the admin shell.
		filepath.FromSlash("internal/ui/layouts/storefront.templ"): true,
	}

	// Match a banned class only when it appears as a complete token — bounded
	// on each side by something other than a word char or hyphen. This rejects
	// false-positives like `bg-ink-soft` for the pattern `bg-ink`.
	re := regexp.MustCompile(`(^|[^\w-])(` + strings.Join(bannedClasses, `|`) + `)([^\w-]|$)`)

	var violations []string

	// Both roots, like the dead-token half. layouts/ holds the admin shell —
	// sixty-odd rr-* usages framing every page — alongside storefront.templ,
	// which uses the banned classes legitimately and is excluded below.
	blockWalk := func(path string, info os.FileInfo, err error) error {
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
		// <style> blocks are blanked, not scanned: the admin shell names these
		// tokens in order to override them, which is a definition rather than a
		// class somebody reached for.
		for lineNum, line := range strings.Split(blankStyleBlocks(string(data)), "\n") {
			matches := re.FindAllStringSubmatch(line, -1)
			for _, m := range matches {
				violations = append(violations, fmt.Sprintf(
					"%s:%d: banned class %q — %s",
					path, lineNum+1, m[2], strings.TrimSpace(line),
				))
			}
		}
		return nil
	}

	for _, root := range adminUIRoots() {
		if err := filepath.Walk(root, blockWalk); err != nil {
			return fmt.Errorf("walk %s: %w", root, err)
		}
	}

	dead, err := deadTokenClasses(excluded)
	if err != nil {
		return err
	}
	violations = append(violations, dead...)

	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, v)
		}
		return fmt.Errorf("%d admin UI lint violation(s) — see docs/admin-ui.md", len(violations))
	}
	return nil
}

// blankStyleBlocks replaces the contents of <style> blocks with empty lines.
//
// Inside one, a token name is a definition rather than a class somebody reached
// for — the admin shell names the tokens in order to override them. Blanking
// rather than skipping keeps the line numbering honest, and unlike a running
// in-a-style-block flag it cannot swallow the rest of a file that mentions
// "<style" without ever closing it.
func blankStyleBlocks(src string) string {
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.Contains(lines[i], "<style") {
			continue
		}
		// Only a block that actually closes is blanked. An unmatched "<style"
		// — the literal in a comment, say — is left alone, because swallowing
		// the rest of the file from there is the failure this replaced.
		end := -1
		for j := i; j < len(lines); j++ {
			if strings.Contains(lines[j], "</style>") {
				end = j
				break
			}
		}
		if end == -1 {
			return strings.Join(lines, "\n")
		}
		for j := i; j <= end; j++ {
			lines[j] = ""
		}
		i = end
	}
	return strings.Join(lines, "\n")
}

// emitsCSS reports whether Tailwind produced a rule for a utility.
//
// Substring rather than ".class", because a variant is emitted escaped —
// `hover:text-rr-red-lt` becomes `.hover\:text-rr-red-lt:hover` — and matching
// on the leading dot would call every variant dead. The trailing guard stops
// `bg-rr-red` matching inside `bg-rr-red-lt`.
func emitsCSS(compiled, class string) bool {
	return regexp.MustCompile(regexp.QuoteMeta(class) + `([^a-z0-9-]|$)`).MatchString(compiled)
}

// adminUIRoots are the trees both halves of the lint read. The admin shell is
// admin UI too: scanning only internal/ui/admin would leave the frame around
// every page unchecked.
func adminUIRoots() []string {
	return []string{"internal/ui/admin", "internal/ui/layouts"}
}

// deadTokenClasses finds rr-* utilities the admin uses that Tailwind never
// emitted a rule for.
//
// The blocklist above catches a class that is wrong. This catches one that does
// not exist: Tailwind v4 drops an unknown utility silently, so `bg-rr-paper-warm`
// — the token is `--color-paper-warm`, un-prefixed and storefront-only —
// compiled, passed this lint, passed the render tests, and produced no CSS at
// all. Today simply was not marked on the maintenance calendar, and nothing but
// a browser could have said so.
//
// Read against the committed output.css rather than a fresh build. Having
// `mage check` rebuild first made a lint depend on Node and a network, and
// rewrote a tracked file as a side effect of checking it.
//
// That leaves one sharp edge: a token added without running `mage css` reads as
// dead. It is not a false failure — the stylesheet genuinely does not carry the
// class yet — so the message says so and names the fix rather than the check
// standing down and letting a real dead token through.
func deadTokenClasses(excluded map[string]bool) ([]string, error) {
	roots := adminUIRoots()

	const cssPath = "internal/ui/assets/css/output.css"

	css, err := os.ReadFile(cssPath)
	if os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "checkAdminUI: output.css not built — skipping the dead-token half; run `mage css`")
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read output.css: %w", err)
	}
	compiled := string(css)

	// Utilities only, so a token name inside a CSS variable or a comment is not
	// mistaken for one. Opacity suffixes (bg-rr-raised/60) compile from the
	// base utility, so they are checked as the base.
	use := regexp.MustCompile(`\b((?:bg|text|border|border-[trblxy]|ring|ring-offset|divide|from|via|to|fill|stroke|decoration|outline|accent|shadow|placeholder|caret)-rr-[a-z0-9-]+)`)

	var dead []string
	seen := map[string]bool{}

	walk := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".templ") {
			return nil
		}
		if excluded[path] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Same rule as the blocklist half.
		for lineNum, line := range strings.Split(blankStyleBlocks(string(data)), "\n") {
			for _, m := range use.FindAllStringSubmatch(line, -1) {
				class := strings.TrimRight(m[1], "-")
				key := path + class
				if seen[key] || emitsCSS(compiled, class) {
					continue
				}
				seen[key] = true
				dead = append(dead, fmt.Sprintf(
					"%s:%d: %q emits no CSS — check the spelling, or run `mage css` if you just added the token — %s",
					path, lineNum+1, class, strings.TrimSpace(line),
				))
			}
		}
		return nil
	}

	for _, root := range roots {
		if err := filepath.Walk(root, walk); err != nil {
			return nil, fmt.Errorf("walk %s: %w", root, err)
		}
	}
	return dead, nil
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

// GeocodeWarm geocodes the addresses on local-delivery orders so the first
// route plan reads from cache instead of firing a burst of billable lookups,
// and reports any address the provider could not pin precisely.
// Set DATABASE_URL and GOOGLE_GEOCODING_API_KEY.
//
// For a no-spend preview of the working set, run the command directly:
//
//	go run ./cmd/geocode-warm --dry-run
func GeocodeWarm() error {
	return sh.RunV("go", "run", "./cmd/geocode-warm")
}

// OSRM namespace groups routing-dataset commands. See ops/osrm/README.md.
type OSRM mg.Namespace

// Build builds the OSRM routing dataset: download the Washington extract, run
// the MLD pipeline, package a tarball.
//
// Run this on angmar.dev, NOT on prod — osrm-extract peaks around 5GB and prod
// has 3.7GB with no swap. The script refuses to run on a host too small for it.
func (OSRM) Build() error {
	return sh.RunV("bash", "ops/osrm/osrm-build.sh")
}

// Push builds the OSRM routing dataset and scp's it to the production host,
// where osrm-install.sh unpacks it. Same host requirement as Build.
func (OSRM) Push() error {
	return sh.RunV("bash", "ops/osrm/osrm-build.sh", "--push")
}
