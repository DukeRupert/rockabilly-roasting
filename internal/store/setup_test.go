package store_test

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/testutil"
)

// The store package has historically been exercised through the app-layer
// tests, and for most of it that is still the right place — a store method with
// one caller is best tested through the rule that calls it.
//
// The equipment service tables arrive before their service does, and their
// list queries build their WHERE clauses and placeholder numbering at runtime,
// which is the kind of code that only a real Postgres can vouch for. Hence a
// harness here.

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, cleanup := testutil.SetupTestDB()
	defer cleanup()
	testPool = pool
	os.Exit(m.Run())
}
