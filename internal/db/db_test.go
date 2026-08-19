package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/db"
	"github.com/hjordan6/trivial/internal/testsupport"
)

func TestMigrationsCreateSchema(t *testing.T) {
	pool := testsupport.MustPool(t)
	ctx := context.Background()

	for _, table := range []string{
		"topics", "questions", "question_aliases", "question_distractors",
		"daily_puzzles", "daily_puzzle_questions",
	} {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                WHERE table_schema = 'public' AND table_name = $1)`,
			table).Scan(&exists)
		if err != nil {
			t.Fatalf("query for table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s does not exist after migration", table)
		}
	}
}

func TestDateRoundTripsThroughPostgres(t *testing.T) {
	pool := testsupport.MustPool(t)
	tx := testsupport.Tx(t, pool)
	ctx := context.Background()

	want, err := clock.ParseDate("2026-03-08")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO daily_puzzles (puzzle_date, time_limit_seconds) VALUES ($1, $2)`,
		want, 135); err != nil {
		t.Fatalf("insert puzzle: %v", err)
	}

	var got clock.Date
	if err := tx.QueryRow(ctx,
		`SELECT puzzle_date FROM daily_puzzles WHERE puzzle_date = $1`, want).Scan(&got); err != nil {
		t.Fatalf("select puzzle: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("round-tripped date = %s, want %s", got, want)
	}
}

// TestMigrateFailsFastWithTooFewConns does not need a live database: pgxpool
// only connects lazily (its default MinConns is 0), so constructing a pool
// with pool_max_conns=1 never dials anything. It exists to pin down that
// Migrate rejects such a pool immediately instead of hanging forever waiting
// for a second connection that a single-connection pool can never hand out.
// The outer context timeout is a safety net: if this check ever regresses,
// the test fails after a few seconds instead of hanging the whole suite.
func TestMigrateFailsFastWithTooFewConns(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://user:pass@127.0.0.1:1/db?pool_max_conns=1")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = db.Migrate(ctx, pool, db.Up)
	if err == nil {
		t.Fatal("Migrate: want error for a pool with pool_max_conns=1, got nil")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("Migrate error = %q, want it to name the required minimum of 2 connections", err)
	}
}
