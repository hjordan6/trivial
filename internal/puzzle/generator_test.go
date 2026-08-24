package puzzle_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

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

// TestGenerateForExhaustsCooldownWithNoSlack pins the branch's headline
// guarantee: the question cooldown is a hard constraint that is never
// relaxed, even when relaxing it is the only way to fill a board. The
// library here has exactly one question per topic per difficulty, so day one
// consumes the entire pool and day two has nothing left within the cooldown.
// No prior test constructs this: the existing "too thin" test seeds too few
// topics (not exhaustion), the "starved" test retires questions outright,
// and the "different dates differ" test has three questions per difficulty
// so it never runs out.
func TestGenerateForExhaustsCooldownWithNoSlack(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	for _, slug := range []string{"alpha", "beta", "gamma"} {
		seedTopicWithQuestionsPerDifficulty(t, tx, slug, 1)
	}
	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}

	dayOne := mustDate(t, "2026-08-18")
	first, err := g.GenerateFor(ctx, dayOne)
	if err != nil {
		t.Fatalf("day one GenerateFor() error = %v", err)
	}
	if len(first.Entries) != 9 {
		t.Fatalf("day one entries = %d, want 9", len(first.Entries))
	}

	dayTwo := mustDate(t, "2026-08-19")
	_, err = g.GenerateFor(ctx, dayTwo)
	if err == nil {
		t.Fatal("day two GenerateFor() error = nil, want *puzzle.InsufficientContentError")
	}
	var insufficient *puzzle.InsufficientContentError
	if !errors.As(err, &insufficient) {
		t.Fatalf("day two GenerateFor() error = %T (%v), want *puzzle.InsufficientContentError", err, err)
	}
	if insufficient.Accepted != 0 {
		t.Errorf("day two Accepted = %d, want 0", insufficient.Accepted)
	}

	var rows int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM daily_puzzles WHERE puzzle_date = $1`, dayTwo).Scan(&rows); err != nil {
		t.Fatalf("count puzzles: %v", err)
	}
	if rows != 0 {
		t.Errorf("wrote %d puzzle rows for day two on failure, want 0", rows)
	}
}

// seedTopicWithQuestionsPerDifficulty creates a topic with exactly n eligible
// questions per difficulty, so tests can control the library's exact
// capacity rather than the generous headroom seedTopic provides.
func seedTopicWithQuestionsPerDifficulty(t *testing.T, tx pgx.Tx, slug string, n int) content.Topic {
	t.Helper()
	ctx := context.Background()

	id, err := content.UpsertTopic(ctx, tx, slug, slug)
	if err != nil {
		t.Fatalf("UpsertTopic(%s): %v", slug, err)
	}
	for _, d := range content.AllDifficulties {
		for i := 1; i <= n; i++ {
			externalID := fmt.Sprintf("%s-%s-%d", slug, d, i)
			qid, err := content.UpsertQuestion(ctx, tx, content.QuestionInput{
				TopicID: id, Difficulty: d, DifficultyRating: hardRatingFor(d, i),
				Prompt: "Prompt " + externalID, CanonicalAnswer: "Answer " + externalID,
				Status: "active", Source: "test", ExternalID: externalID,
			})
			if err != nil {
				t.Fatalf("UpsertQuestion(%s): %v", externalID, err)
			}
			if err := content.ReplaceAliases(ctx, tx, qid, []string{"Answer " + externalID}); err != nil {
				t.Fatalf("ReplaceAliases: %v", err)
			}
			if err := content.ReplaceDistractors(ctx, tx, qid, []string{"w1", "w2", "w3", "w4", "w5"}); err != nil {
				t.Fatalf("ReplaceDistractors: %v", err)
			}
		}
	}
	return content.Topic{ID: id, Slug: slug, Name: slug, Active: true}
}

func questionIDs(p *puzzle.Puzzle) []int64 {
	ids := make([]int64, 0, len(p.Entries))
	for _, e := range p.Entries {
		ids = append(ids, e.QuestionID)
	}
	return ids
}

// seedTopicWithHardRatings builds a topic whose hard band holds exactly the
// given ratings, so a test can describe a library that does or does not contain
// a floor-rated hard question.
func seedTopicWithHardRatings(t *testing.T, tx pgx.Tx, slug string, hard []int) content.Topic {
	t.Helper()
	ctx := context.Background()

	id, err := content.UpsertTopic(ctx, tx, slug, slug)
	if err != nil {
		t.Fatalf("UpsertTopic(%s): %v", slug, err)
	}
	write := func(d content.Difficulty, rating, n int) {
		externalID := fmt.Sprintf("%s-%s-%d-%d", slug, d, rating, n)
		qid, err := content.UpsertQuestion(ctx, tx, content.QuestionInput{
			TopicID: id, Difficulty: d, DifficultyRating: rating,
			Prompt: "Prompt " + externalID, CanonicalAnswer: "Answer " + externalID,
			Status: "active", Source: "test", ExternalID: externalID,
		})
		if err != nil {
			t.Fatalf("UpsertQuestion(%s): %v", externalID, err)
		}
		if err := content.ReplaceAliases(ctx, tx, qid, []string{"Answer " + externalID}); err != nil {
			t.Fatalf("ReplaceAliases: %v", err)
		}
		if err := content.ReplaceDistractors(ctx, tx, qid, []string{"w1", "w2", "w3", "w4", "w5"}); err != nil {
			t.Fatalf("ReplaceDistractors: %v", err)
		}
	}
	for n := 1; n <= 3; n++ {
		write(content.Easy, 2, n)
		write(content.Medium, 6, n)
	}
	for n, rating := range hard {
		write(content.Hard, rating, n+1)
	}
	return content.Topic{ID: id, Slug: slug, Name: slug, Active: true}
}

// hardRatingsOf reads back the rating of every hard question on a board.
func hardRatingsOf(t *testing.T, tx pgx.Tx, p *puzzle.Puzzle) []int {
	t.Helper()
	ctx := context.Background()

	ratings := make([]int, 0, 3)
	for _, e := range p.Entries {
		if e.Difficulty != content.Hard {
			continue
		}
		var rating int
		if err := tx.QueryRow(ctx,
			`SELECT difficulty_rating FROM questions WHERE id = $1`, e.QuestionID).Scan(&rating); err != nil {
			t.Fatalf("read rating for question %d: %v", e.QuestionID, err)
		}
		ratings = append(ratings, rating)
	}
	return ratings
}

func TestGenerateForAlwaysLandsOneHardQuestionOnTheFloor(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	// A library that is mostly 9s, so the natural picks frequently miss the
	// floor and the rule has to do real work.
	for _, slug := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		seedTopicWithHardRatings(t, tx, slug, []int{9, 9, 9, 8})
	}

	g := puzzle.Generator{DB: tx, CooldownDays: 0, TimeLimitSeconds: 135}
	for _, day := range []string{"2031-01-01", "2031-01-02", "2031-01-03", "2031-01-04", "2031-01-05"} {
		got, err := g.GenerateFor(ctx, mustDate(t, day))
		if err != nil {
			t.Fatalf("GenerateFor(%s) error = %v", day, err)
		}
		ratings := hardRatingsOf(t, tx, got)
		if len(ratings) != 3 {
			t.Fatalf("%s: got %d hard questions, want 3", day, len(ratings))
		}
		floors := 0
		for _, r := range ratings {
			if r == 8 {
				floors++
			}
			if r < 8 || r > 10 {
				t.Errorf("%s: hard question rated %d, outside the band", day, r)
			}
		}
		if floors < 1 {
			t.Errorf("%s: hard ratings %v, want at least one 8", day, ratings)
		}
	}
}

// The rule constrains one slot, not all three: a board is free to carry 9s and
// 10s alongside its floor-rated question.
func TestGenerateForLeavesTheOtherHardSlotsAlone(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	// Only one topic can supply the floor, so the other two boards slots must
	// keep whatever they drew from a pool with no 8s in it at all.
	seedTopicWithHardRatings(t, tx, "gentle", []int{8})
	seedTopicWithHardRatings(t, tx, "steep-one", []int{9, 10})
	seedTopicWithHardRatings(t, tx, "steep-two", []int{9, 10})

	g := puzzle.Generator{DB: tx, CooldownDays: 0, TimeLimitSeconds: 135}
	got, err := g.GenerateFor(ctx, mustDate(t, "2031-02-01"))
	if err != nil {
		t.Fatalf("GenerateFor() error = %v", err)
	}

	ratings := hardRatingsOf(t, tx, got)
	floors, above := 0, 0
	for _, r := range ratings {
		switch {
		case r == 8:
			floors++
		case r > 8:
			above++
		}
	}
	if floors != 1 {
		t.Errorf("hard ratings %v, want exactly one 8 from the only topic that has one", ratings)
	}
	if above != 2 {
		t.Errorf("hard ratings %v, want the other two slots left above the floor", ratings)
	}
}

// The floor is a real requirement, not a preference: a library that cannot meet
// it leaves the day ungenerated rather than publishing a hard row with no way
// in. This mirrors how the cooldown is treated.
func TestGenerateForRefusesWhenNothingSitsOnTheFloor(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2031-03-01")

	for _, slug := range []string{"steep-one", "steep-two", "steep-three"} {
		seedTopicWithHardRatings(t, tx, slug, []int{9, 10})
	}

	g := puzzle.Generator{DB: tx, CooldownDays: 0, TimeLimitSeconds: 135}
	_, err := g.GenerateFor(ctx, date)

	var unavailable *puzzle.GentleHardQuestionUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error = %v, want GentleHardQuestionUnavailableError", err)
	}
	if unavailable.Rating != 8 {
		t.Errorf("Rating = %d, want 8", unavailable.Rating)
	}
	if len(unavailable.Topics) != 3 {
		t.Errorf("Topics = %v, want the three that were on the board", unavailable.Topics)
	}

	var rows int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM daily_puzzles WHERE puzzle_date = $1`, date).Scan(&rows); err != nil {
		t.Fatalf("count puzzles: %v", err)
	}
	if rows != 0 {
		t.Errorf("wrote %d puzzle rows on refusal, want 0", rows)
	}
}
