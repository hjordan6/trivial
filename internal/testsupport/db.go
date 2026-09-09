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
//
// The transaction is REPEATABLE READ, which fixes a read-committed hazard
// that is easy to hit here and very hard to read off a failure. `go test ./...`
// runs packages in parallel against this one database, and some suites commit
// their fixtures rather than rolling them back — the HTTP tests insert real
// topics through the pool. Under read committed, every statement in a test
// takes a fresh snapshot, so those commits become visible mid-test: a test
// that reads the topic list twice can legitimately see two different
// libraries. That is enough to break the generator's determinism tests, which
// build the same board twice and expect the same questions, because any extra
// active topic shifts the weighted topic draw and therefore the whole RNG
// stream. The failure surfaces as a puzzle that differs from itself, with
// nothing in the failing package's own code to explain it.
//
// One snapshot for the whole transaction makes "the same library" true by
// construction. Nothing here depends on observing another session's commits,
// and tests only ever write rows they created themselves, so there are no
// write conflicts for the stricter level to reject.
func Tx(t *testing.T, p *pgxpool.Pool) pgx.Tx {
	t.Helper()

	tx, err := p.BeginTx(context.Background(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(context.Background())
	})
	return tx
}
