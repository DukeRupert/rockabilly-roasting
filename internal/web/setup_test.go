package web

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// A database for the web package's tests.
//
// internal/app and internal/store have had one since the beginning; this half
// of the repo did not, so a handler calling a well-covered service had nothing
// watching the wiring between them. That seam is where this package's defects
// actually live. Ported from hiri-core along with the checkout tests that need
// it.
//
// # Handler tests commit
//
// internal/app tests take a pgx.Tx from NewTestTx and get a rollback for free.
// A handler cannot: it owns its transaction, through store.Tx(ctx, d.Pool, …),
// and the whole point of testing one is to let it do that. So anything driving
// a handler writes for real and the rows stay.
//
// Which is fine, and is why the fixtures below key on fresh UUIDs: a test reads
// back the cart or the order it just made, never "the cart" or "every order".
// Do not add a test here that assumes an empty database, and do not add
// t.Parallel() to one that writes — the container is shared by the package.
//
// A function taking a tx rather than a pool should still use
// testutil.NewTestTx and roll back like an app test.
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, cleanup := testutil.SetupTestDB()
	defer cleanup()
	testPool = pool
	os.Exit(m.Run())
}

// newTestDeps wires the services the tests in this package drive, and nothing
// else. Deps has forty-odd fields; a test that needs a forty-first should add
// it here rather than build its own, so there is one answer to "how is this
// assembled" and it is the same shape cmd/server uses.
//
// Unset fields are nil on purpose. A handler reaching for one panics, which is
// a better failure than a zero-valued service quietly answering wrong.
func newTestDeps() *Deps {
	catalogStore := store.NewCatalogStore()

	pricing := app.NewPricingService(store.NewPricingStore(), store.NewCustomerStore())
	catalog := app.NewCatalogService(catalogStore, store.NewCustomerStore(), audit.NewAuditWriter(), metrics.NewRegistry())
	cart := app.NewCartService(store.NewCartStore(), catalogStore, pricing, catalog)

	return &Deps{
		Pool:           testPool,
		CatalogService: catalog,
		CartService:    cart,
		PricingService: pricing,
		OrderService:   app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry()),
	}
}
