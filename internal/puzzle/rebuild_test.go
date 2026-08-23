package puzzle_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/puzzle"
	"github.com/hjordan6/trivial/internal/testsupport"
)

func TestRebuildAppliesNewPins(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	today := mustDate(t, "2026-09-10")
	date := mustDate(t, "2026-09-17")

	ids := map[string]int64{}
	for _, slug := range sixTopics {
		ids[slug] = seedTopic(t, tx, slug).ID
	}
	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}

	// A fully automatic board first.
	before, err := g.GenerateFor(ctx, date)
	if err != nil {
		t.Fatalf("GenerateFor: %v", err)
	}
	if len(before.Entries) != 9 {
		t.Fatalf("got %d entries, want 9", len(before.Entries))
	}

	// GenerateFor alone must not disturb it - that is the idempotency contract
	// Rebuild exists to work around.
	again, err := g.GenerateFor(ctx, date)
	if err != nil {
		t.Fatalf("second GenerateFor: %v", err)
	}
	if topicAt(t, again, 0) != topicAt(t, before, 0) {
		t.Fatal("GenerateFor changed an existing board")
	}

	if err := puzzle.SetPins(ctx, tx, date, map[int]int64{0: ids["zeta"]}); err != nil {
		t.Fatalf("SetPins: %v", err)
	}
	after, err := g.Rebuild(ctx, date, today)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if len(after.Entries) != 9 {
		t.Fatalf("got %d entries after rebuild, want 9", len(after.Entries))
	}
	if slug := topicAt(t, after, 0); slug != "zeta" {
		t.Fatalf("position 0 = %s after rebuild, want zeta", slug)
	}

	// Exactly one board must survive, or Get will reject the date for players.
	var boards, entries int
	if err := tx.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM daily_puzzles WHERE puzzle_date = $1),
		        (SELECT count(*) FROM daily_puzzle_questions WHERE puzzle_date = $1)`,
		date).Scan(&boards, &entries); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if boards != 1 || entries != 9 {
		t.Fatalf("got %d board row(s) and %d entrie(s), want 1 and 9", boards, entries)
	}
}

func TestRebuildRefusesTodayAndPast(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	today := mustDate(t, "2026-09-10")

	for _, slug := range sixTopics {
		seedTopic(t, tx, slug)
	}
	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}

	for _, date := range []clock.Date{today, mustDate(t, "2026-09-09"), mustDate(t, "2026-01-01")} {
		if _, err := g.Rebuild(ctx, date, today); !errors.Is(err, puzzle.ErrNotFuture) {
			t.Errorf("Rebuild(%s) err = %v, want ErrNotFuture", date, err)
		}
	}
}

func TestRebuildRefusesADateWithRuns(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	today := mustDate(t, "2026-09-10")
	date := mustDate(t, "2026-09-17")

	for _, slug := range sixTopics {
		seedTopic(t, tx, slug)
	}
	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}
	before, err := g.GenerateFor(ctx, date)
	if err != nil {
		t.Fatalf("GenerateFor: %v", err)
	}

	// Someone plays it. The board is now history.
	var playerID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO players (id) VALUES (gen_random_uuid()) RETURNING id`).Scan(&playerID); err != nil {
		t.Fatalf("insert player: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO runs (player_id, puzzle_date, started_at, expires_at, option_seed)
		VALUES ($1, $2, now(), now() + interval '135 seconds', 1)`,
		playerID, date); err != nil {
		t.Fatalf("insert run: %v", err)
	}

	if _, err := g.Rebuild(ctx, date, today); !errors.Is(err, puzzle.ErrDateHasRuns) {
		t.Fatalf("err = %v, want ErrDateHasRuns", err)
	}
	assertBoardUnchanged(t, tx, date, before)
}

func assertBoardUnchanged(t *testing.T, tx pgx.Tx, date clock.Date, want *puzzle.Puzzle) {
	t.Helper()
	got, err := puzzle.Get(context.Background(), tx, date)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("board disappeared")
	}
	gotIDs, wantIDs := questionIDs(got), questionIDs(want)
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("got %d entries, want %d", len(gotIDs), len(wantIDs))
	}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("entry %d changed: %d -> %d", i, wantIDs[i], gotIDs[i])
		}
	}
}
