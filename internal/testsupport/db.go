// Package testsupport provides database helpers for integration tests.
package testsupport

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hjordan6/trivial/internal/db"
)

var (
	once    sync.Once
	pool    *pgxpool.Pool
	initErr error
)

// MustPool returns a migrated connection pool against TEST_DATABASE_URL.
//
// It fails the test when TEST_DATABASE_URL is unset rather than skipping. A
// skipped integration suite still reports green, which is exactly how a broken
// database layer reaches production.
func MustPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL is not set; run tests with `make test`")
	}

	once.Do(func() {
		ctx := context.Background()
		pool, initErr = db.Open(ctx, url)
		if initErr != nil {
			return
		}
		initErr = db.Migrate(ctx, pool, db.Up)
	})
	if initErr != nil {
		t.Fatalf("prepare test database: %v", initErr)
	}
	return pool
}

// Tx begins a transaction that is rolled back when the test finishes, so tests
// share one database without sharing state.
func Tx(t *testing.T, p *pgxpool.Pool) pgx.Tx {
	t.Helper()

	tx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(context.Background())
	})
	return tx
}
