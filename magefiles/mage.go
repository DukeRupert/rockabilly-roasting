//go:build mage

package main

import (
	"fmt"
	"os"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

var Default = Build

// Build compiles the server binary.
func Build() error {
	return sh.RunV("go", "build", "./cmd/server")
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

// Check runs lint and tests together (CI-style gate).
func Check() {
	mg.Deps(Lint, Test)
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

// Dev namespace groups development tool commands.
type Dev mg.Namespace

// Templ generates templ templates.
func (Dev) Templ() error {
	return sh.RunV("templ", "generate")
}

// CSS compiles Tailwind CSS.
func (Dev) CSS() error {
	return sh.RunV("npx", "@tailwindcss/cli",
		"-i", "internal/ui/assets/css/input.css",
		"-o", "internal/ui/assets/css/output.css",
		"--minify",
	)
}

// Watch runs Tailwind CSS in watch mode.
func (Dev) Watch() error {
	return sh.RunV("npx", "@tailwindcss/cli",
		"-i", "internal/ui/assets/css/input.css",
		"-o", "internal/ui/assets/css/output.css",
		"--watch",
	)
}

// Checkout builds the Svelte checkout bundle.
func (Dev) Checkout() error {
	return sh.RunV("npm", "run", "build", "--prefix", "ui/checkout")
}

// Run generates templates and CSS, builds the server, and runs it.
func (Dev) Run() error {
	d := Dev{}
	if err := d.Templ(); err != nil {
		return err
	}
	if err := d.CSS(); err != nil {
		return err
	}
	if err := Build(); err != nil {
		return err
	}
	return sh.RunV("./server")
}

// Seed creates an admin staff user. Set SEED_EMAIL, SEED_PASSWORD, and optionally SEED_NAME.
func (Dev) Seed() error {
	return sh.RunV("go", "run", "./cmd/seed")
}
