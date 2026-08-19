package puzzle_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/puzzle"
	"github.com/hjordan6/trivial/internal/testsupport"
)

func TestGenerateForProducesNineQuestionsAcrossThreeTopics(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-08-18")

	for _, slug := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"} {
		seedTopic(t, tx, slug)
	}

	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}
	got, err := g.GenerateFor(ctx, date)
	if err != nil {
		t.Fatalf("GenerateFor() error = %v", err)
	}
	if len(got.Entries) != 9 {
		t.Fatalf("got %d entries, want 9", len(got.Entries))
	}

	topics := map[string]map[content.Difficulty]bool{}
	for _, e := range got.Entries {
		if topics[e.TopicSlug] == nil {
			topics[e.TopicSlug] = map[content.Difficulty]bool{}
		}
		topics[e.TopicSlug][e.Difficulty] = true
	}
	if len(topics) != 3 {
		t.Errorf("got %d distinct topics, want 3", len(topics))
	}
	for slug, difficulties := range topics {
		if len(difficulties) != 3 {
			t.Errorf("topic %s has %d difficulties, want 3", slug, len(difficulties))
		}
	}
}

func TestGenerateForIsDeterministic(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-08-18")

	for _, slug := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"} {
		seedTopic(t, tx, slug)
	}
	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}

	first, err := g.GenerateFor(ctx, date)
	if err != nil {
		t.Fatalf("first GenerateFor: %v", err)
	}
	firstIDs := questionIDs(first)

	// Discard the puzzle and regenerate from the same library and date.
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
			t.Errorf("entry %d: question %d then %d, want the same puzzle for the same date",
				i, firstIDs[i], secondIDs[i])
		}
	}
}

func TestGenerateForDifferentDatesDiffer(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	// Seed exactly three topics — a board uses exactly three (topicsPerDay) —
	// so both dates are forced to draw from the same pool. With more topics
	// than a board needs, the date-seeded shuffle can hand the two days
	// disjoint topic sets, and the zero-overlap assertion below would pass
	// on topic selection alone without the cooldown ever being exercised.
	for _, slug := range []string{"alpha", "beta", "gamma"} {
		seedTopic(t, tx, slug)
	}
	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}

	first, err := g.GenerateFor(ctx, mustDate(t, "2026-08-18"))
	if err != nil {
		t.Fatalf("GenerateFor day one: %v", err)
	}
	second, err := g.GenerateFor(ctx, mustDate(t, "2026-08-19"))
	if err != nil {
		t.Fatalf("GenerateFor day two: %v", err)
	}

	overlap := 0
	firstSet := map[int64]bool{}
	for _, id := range questionIDs(first) {
		firstSet[id] = true
	}
	for _, id := range questionIDs(second) {
		if firstSet[id] {
			overlap++
		}
	}
	if overlap != 0 {
		t.Errorf("%d questions repeated on consecutive days, want 0 inside the cooldown", overlap)
	}
}

func TestGenerateForIsIdempotent(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-08-18")

	for _, slug := range []string{"alpha", "beta", "gamma", "delta"} {
		seedTopic(t, tx, slug)
	}
	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}

	if _, err := g.GenerateFor(ctx, date); err != nil {
		t.Fatalf("first GenerateFor: %v", err)
	}
	if _, err := g.GenerateFor(ctx, date); err != nil {
		t.Fatalf("second GenerateFor: %v", err)
	}

	var rows int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM daily_puzzle_questions WHERE puzzle_date = $1`, date).Scan(&rows); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	if rows != 9 {
		t.Errorf("entry rows = %d after two generations, want 9", rows)
	}
}

func TestGenerateForSkipsStarvedTopics(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-08-18")

	for _, slug := range []string{"alpha", "beta", "gamma"} {
		seedTopic(t, tx, slug)
	}
	starved := seedTopic(t, tx, "starved")
	// Retire every hard question in this topic so it can never fill a board.
	if _, err := tx.Exec(ctx,
		`UPDATE questions SET status = 'retired' WHERE topic_id = $1 AND difficulty = 'hard'`,
		starved.ID); err != nil {
		t.Fatalf("retire hard questions: %v", err)
	}

	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}
	got, err := g.GenerateFor(ctx, date)
	if err != nil {
		t.Fatalf("GenerateFor() error = %v", err)
	}
	for _, e := range got.Entries {
		if e.TopicSlug == "starved" {
			t.Errorf("puzzle used topic %q, which cannot fill all three difficulties", e.TopicSlug)
		}
	}
}

func TestGenerateForFailsWhenLibraryIsTooThin(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-08-18")

	seedTopic(t, tx, "alpha")
	seedTopic(t, tx, "beta")

	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}
	_, err := g.GenerateFor(ctx, date)
	if err == nil {
		t.Fatal("GenerateFor() error = nil, want InsufficientContentError")
	}

	var insufficient *puzzle.InsufficientContentError
	if !errors.As(err, &insufficient) {
		t.Fatalf("GenerateFor() error = %T (%v), want *puzzle.InsufficientContentError", err, err)
	}
	if insufficient.Accepted != 2 {
		t.Errorf("Accepted = %d, want 2", insufficient.Accepted)
	}

	var puzzles int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM daily_puzzles WHERE puzzle_date = $1`, date).Scan(&puzzles); err != nil {
		t.Fatalf("count puzzles: %v", err)
	}
	if puzzles != 0 {
		t.Errorf("wrote %d puzzle rows on failure, want 0", puzzles)
	}
}

func questionIDs(p *puzzle.Puzzle) []int64 {
	ids := make([]int64, 0, len(p.Entries))
	for _, e := range p.Entries {
		ids = append(ids, e.QuestionID)
	}
	return ids
}
