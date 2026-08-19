package db_test

import (
	"context"
	"testing"

	"github.com/hjordan6/trivial/internal/clock"
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
