package jobs_test

import (
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/testutil"
)

// Most of this package's tests are pure — schedules, error handling, quiet
// hours — and run in milliseconds. The few that drive a worker against a
// real database start the container on first use, so the rest do not pay for
// it.
var (
	dbOnce    sync.Once
	dbPool    *pgxpool.Pool
	dbCleanup func()
)

// testPool returns the package's database, starting it if this is the first
// test to ask.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbOnce.Do(func() { dbPool, dbCleanup = testutil.SetupTestDB() })
	return dbPool
}

func TestMain(m *testing.M) {
	code := m.Run()
	if dbCleanup != nil {
		dbCleanup()
	}
	os.Exit(code)
}
