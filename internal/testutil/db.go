package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// NewTestDB spins up a Postgres 16 container, runs all migrations, and returns
// a connection pool. The container is cleaned up via t.Cleanup.
func NewTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("hiri_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	t.Cleanup(func() {
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	// Run migrations using goose with database/sql + pgx stdlib driver.
	migrationsDir := migrationsPath()
	sqlDB, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Fatalf("open sql.DB for migrations: %v", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(nil)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.Up(sqlDB, migrationsDir); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	// Create pgxpool for test usage.
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("create pgxpool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	return pool
}

// NewTestTx begins a transaction and registers a cleanup to roll it back.
// This gives each test perfect isolation without needing to re-run migrations.
func NewTestTx(t *testing.T, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin test tx: %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(ctx)
	})
	return tx
}

// SetupTestDB creates a Postgres container and pool for use in TestMain.
// Returns the pool and a cleanup function. Usage:
//
//	func TestMain(m *testing.M) {
//	    pool, cleanup := testutil.SetupTestDB()
//	    defer cleanup()
//	    testPool = pool
//	    os.Exit(m.Run())
//	}
func SetupTestDB() (*pgxpool.Pool, func()) {
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("hiri_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		panic(fmt.Sprintf("start postgres container: %v", err))
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic(fmt.Sprintf("get connection string: %v", err))
	}

	sqlDB, err := sql.Open("pgx", connStr)
	if err != nil {
		panic(fmt.Sprintf("open sql.DB for migrations: %v", err))
	}

	goose.SetBaseFS(nil)
	if err := goose.SetDialect("postgres"); err != nil {
		panic(fmt.Sprintf("set goose dialect: %v", err))
	}
	if err := goose.Up(sqlDB, migrationsPath()); err != nil {
		panic(fmt.Sprintf("run migrations: %v", err))
	}
	sqlDB.Close()

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		panic(fmt.Sprintf("create pgxpool: %v", err))
	}

	cleanup := func() {
		pool.Close()
		if err := pgContainer.Terminate(ctx); err != nil {
			fmt.Printf("terminate postgres container: %v\n", err)
		}
	}

	return pool, cleanup
}

// migrationsPath resolves the absolute path to db/migrations/ relative to the
// project root (two levels up from internal/testutil/).
func migrationsPath() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "db", "migrations")
}
