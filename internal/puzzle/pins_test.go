package puzzle_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/puzzle"
	"github.com/hjordan6/trivial/internal/testsupport"
)

var sixTopics = []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}

// topicAt returns the slug occupying a board position.
func topicAt(t *testing.T, p *puzzle.Puzzle, position int) string {
	t.Helper()
	for _, e := range p.Entries {
		if e.TopicPosition == position {
			return e.TopicSlug
		}
	}
	t.Fatalf("no entry at position %d", position)
	return ""
}

func TestGenerateForHonoursAFullPin(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-09-01")

	ids := map[string]int64{}
	for _, slug := range sixTopics {
		ids[slug] = seedTopic(t, tx, slug).ID
	}

	// Pin all three slots, deliberately not in id order.
	if err := puzzle.SetPins(ctx, tx, date, map[int]int64{
		0: ids["zeta"], 1: ids["alpha"], 2: ids["delta"],
	}); err != nil {
		t.Fatalf("SetPins: %v", err)
	}

	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}
	got, err := g.GenerateFor(ctx, date)
	if err != nil {
		t.Fatalf("GenerateFor: %v", err)
	}
	if len(got.Entries) != 9 {
		t.Fatalf("got %d entries, want 9", len(got.Entries))
	}
	for position, want := range map[int]string{0: "zeta", 1: "alpha", 2: "delta"} {
		if slug := topicAt(t, got, position); slug != want {
			t.Errorf("position %d = %s, want %s", position, slug, want)
		}
	}
}

func TestGenerateForHonoursAPartialPin(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-09-02")

	ids := map[string]int64{}
	for _, slug := range sixTopics {
		ids[slug] = seedTopic(t, tx, slug).ID
	}

	// Only the middle slot is pinned; the other two stay automatic.
	if err := puzzle.SetPins(ctx, tx, date, map[int]int64{1: ids["epsilon"]}); err != nil {
		t.Fatalf("SetPins: %v", err)
	}

	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}
	got, err := g.GenerateFor(ctx, date)
	if err != nil {
		t.Fatalf("GenerateFor: %v", err)
	}
	if len(got.Entries) != 9 {
		t.Fatalf("got %d entries, want 9", len(got.Entries))
	}
	if slug := topicAt(t, got, 1); slug != "epsilon" {
		t.Fatalf("position 1 = %s, want epsilon", slug)
	}
	// The automatic slots must still be filled, distinct, and never the pin.
	seen := map[string]bool{}
	for _, position := range []int{0, 2} {
		slug := topicAt(t, got, position)
		if slug == "epsilon" {
			t.Errorf("automatic position %d reused the pinned topic", position)
		}
		if seen[slug] {
			t.Errorf("topic %s appears twice", slug)
		}
		seen[slug] = true
	}
}

func TestGenerateForRefusesAStarvedPin(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-09-03")

	ids := map[string]int64{}
	for _, slug := range sixTopics {
		ids[slug] = seedTopic(t, tx, slug).ID
	}
	// Starve the pinned topic at one difficulty only.
	if _, err := tx.Exec(ctx,
		`UPDATE questions SET status = 'retired' WHERE topic_id = $1 AND difficulty = 'hard'`,
		ids["beta"]); err != nil {
		t.Fatalf("retire questions: %v", err)
	}
	if err := puzzle.SetPins(ctx, tx, date, map[int]int64{0: ids["beta"]}); err != nil {
		t.Fatalf("SetPins: %v", err)
	}

	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}
	_, err := g.GenerateFor(ctx, date)

	// An automatically chosen topic in this state would be skipped. A pinned
	// one must fail loudly, naming the topic and the starving difficulty.
	var starved *puzzle.PinnedTopicStarvedError
	if !errors.As(err, &starved) {
		t.Fatalf("err = %v, want *PinnedTopicStarvedError", err)
	}
	if starved.Slug != "beta" || starved.Difficulty != content.Hard {
		t.Errorf("got %s(%s), want beta(hard)", starved.Slug, starved.Difficulty)
	}
	assertNoBoard(t, tx, date)
}

func TestGenerateForWithPinsIsDeterministic(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-09-04")

	ids := map[string]int64{}
	for _, slug := range sixTopics {
		ids[slug] = seedTopic(t, tx, slug).ID
	}
	if err := puzzle.SetPins(ctx, tx, date, map[int]int64{2: ids["gamma"]}); err != nil {
		t.Fatalf("SetPins: %v", err)
	}
	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}

	first, err := g.GenerateFor(ctx, date)
	if err != nil {
		t.Fatalf("first GenerateFor: %v", err)
	}
	firstIDs := questionIDs(first)

	// Discard the board and rebuild it from the same library, date and pin.
	// One transaction throughout: a second concurrent transaction would block
	// on the topic rows this one already holds.
	if _, err := tx.Exec(ctx, `DELETE FROM daily_puzzles WHERE puzzle_date = $1`, date); err != nil {
		t.Fatalf("delete puzzle: %v", err)
	}

	second, err := g.GenerateFor(ctx, date)
	if err != nil {
		t.Fatalf("second GenerateFor: %v", err)
	}
	secondIDs := questionIDs(second)

	if len(firstIDs) != len(secondIDs) {
		t.Fatalf("entry counts differ: %d then %d", len(firstIDs), len(secondIDs))
	}
	for i := range firstIDs {
		if firstIDs[i] != secondIDs[i] {
			t.Errorf("entry %d: question %d then %d, want the same board for the same date and pins",
				i, firstIDs[i], secondIDs[i])
		}
	}
	if slug := topicAt(t, second, 2); slug != "gamma" {
		t.Errorf("position 2 = %s, want gamma", slug)
	}
}

func TestSetPinsReplacesRatherThanMerges(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-09-05")

	ids := map[string]int64{}
	for _, slug := range sixTopics {
		ids[slug] = seedTopic(t, tx, slug).ID
	}

	if err := puzzle.SetPins(ctx, tx, date, map[int]int64{0: ids["alpha"], 1: ids["beta"]}); err != nil {
		t.Fatalf("SetPins: %v", err)
	}
	// A second call carrying only one slot must drop the other.
	if err := puzzle.SetPins(ctx, tx, date, map[int]int64{0: ids["gamma"]}); err != nil {
		t.Fatalf("SetPins(replace): %v", err)
	}
	pins, err := puzzle.Pins(ctx, tx, date)
	if err != nil {
		t.Fatalf("Pins: %v", err)
	}
	if len(pins) != 1 || pins[0].Slug != "gamma" {
		t.Fatalf("pins = %v, want only position 0 = gamma", pins)
	}

	// An empty map reverts the date to fully automatic.
	if err := puzzle.SetPins(ctx, tx, date, nil); err != nil {
		t.Fatalf("SetPins(clear): %v", err)
	}
	pins, err = puzzle.Pins(ctx, tx, date)
	if err != nil {
		t.Fatalf("Pins after clear: %v", err)
	}
	if len(pins) != 0 {
		t.Fatalf("pins = %v, want none", pins)
	}
}

func TestSetPinsRejectsTheSameTopicTwice(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-09-06")
	id := seedTopic(t, tx, "alpha").ID

	err := puzzle.SetPins(ctx, tx, date, map[int]int64{0: id, 1: id})
	if err == nil {
		t.Fatal("pinning one topic to two slots was accepted, want an error")
	}
}

// assertNoBoard checks a failed generation wrote nothing, which is the half of
// the contract that a returned error alone does not prove.
func assertNoBoard(t *testing.T, tx pgx.Tx, date clock.Date) {
	t.Helper()
	var boards, entries int
	if err := tx.QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM daily_puzzles WHERE puzzle_date = $1),
		        (SELECT count(*) FROM daily_puzzle_questions WHERE puzzle_date = $1)`,
		date).Scan(&boards, &entries); err != nil {
		t.Fatalf("count board rows: %v", err)
	}
	if boards != 0 || entries != 0 {
		t.Fatalf("wrote %d puzzle row(s) and %d entrie(s), want none", boards, entries)
	}
}
