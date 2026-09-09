package puzzle_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/db"
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

	g := puzzle.Generator{DB: tx, CooldownDays: 180, AnswerCooldownDays: 14, TimeLimitSeconds: 135}
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
	g := puzzle.Generator{DB: tx, CooldownDays: 180, AnswerCooldownDays: 14, TimeLimitSeconds: 135}

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
	g := puzzle.Generator{DB: tx, CooldownDays: 180, AnswerCooldownDays: 14, TimeLimitSeconds: 135}

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
	g := puzzle.Generator{DB: tx, CooldownDays: 180, AnswerCooldownDays: 14, TimeLimitSeconds: 135}

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

	g := puzzle.Generator{DB: tx, CooldownDays: 180, AnswerCooldownDays: 14, TimeLimitSeconds: 135}
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

	g := puzzle.Generator{DB: tx, CooldownDays: 180, AnswerCooldownDays: 14, TimeLimitSeconds: 135}
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
	g := puzzle.Generator{DB: tx, CooldownDays: 180, AnswerCooldownDays: 14, TimeLimitSeconds: 135}

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

// TestGenerateForRejectsOneAnswerTwiceOnABoard pins the answer cooldown's
// core claim at its tightest range — zero days apart. alpha's easy and hard
// questions have different prompts and different difficulties, so nothing in
// the per-question cooldown or the per-topic structure stops them sharing a
// board; only the answer cooldown does. With exactly three topics seeded, a
// skipped alpha is a board that cannot be filled, which is how the test
// observes the rejection.
func TestGenerateForRejectsOneAnswerTwiceOnABoard(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	seedTopicWithAnswers(t, tx, "alpha", map[content.Difficulty]string{
		content.Easy:   "Marie Curie",
		content.Medium: "Unique alpha medium",
		content.Hard:   "Marie Curie",
	})
	seedTopicWithQuestionsPerDifficulty(t, tx, "beta", 1)
	seedTopicWithQuestionsPerDifficulty(t, tx, "gamma", 1)

	g := puzzle.Generator{DB: tx, CooldownDays: 180, AnswerCooldownDays: 14, TimeLimitSeconds: 135}
	_, err := g.GenerateFor(ctx, mustDate(t, "2026-08-18"))

	var insufficient *puzzle.InsufficientContentError
	if !errors.As(err, &insufficient) {
		t.Fatalf("GenerateFor() error = %T (%v), want *puzzle.InsufficientContentError", err, err)
	}
	if insufficient.Accepted != 2 {
		t.Fatalf("Accepted = %d, want 2 (alpha skipped, beta and gamma filled)", insufficient.Accepted)
	}
	want := puzzle.StarvedTopic{Slug: "alpha", Difficulty: content.Hard, Reason: puzzle.StarvedAnswerRepeat}
	if len(insufficient.Starved) != 1 || insufficient.Starved[0] != want {
		t.Errorf("Starved = %+v, want exactly [%+v]", insufficient.Starved, want)
	}
}

// TestGenerateForRejectsARepeatedAnswerAcrossDays runs the question cooldown
// short and the answer cooldown long, so re-serving the very same questions
// the next day breaks nothing except the answer cooldown. Every answer in the
// library is spent on day one, so day two must fail rather than repeat one.
func TestGenerateForRejectsARepeatedAnswerAcrossDays(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	for _, slug := range []string{"alpha", "beta", "gamma"} {
		seedTopicWithQuestionsPerDifficulty(t, tx, slug, 1)
	}
	g := puzzle.Generator{DB: tx, CooldownDays: 1, AnswerCooldownDays: 14, TimeLimitSeconds: 135}

	if _, err := g.GenerateFor(ctx, mustDate(t, "2026-08-18")); err != nil {
		t.Fatalf("day one GenerateFor() error = %v", err)
	}

	// Sanity check on the setup: at one day apart the question cooldown is
	// satisfied, so without the answer cooldown this day would generate.
	noAnswerRule := puzzle.Generator{DB: tx, CooldownDays: 1, TimeLimitSeconds: 135}
	if _, err := noAnswerRule.GenerateFor(ctx, mustDate(t, "2026-08-19")); err != nil {
		t.Fatalf("day two without the answer cooldown: %v; the question cooldown was meant to allow it", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM daily_puzzles WHERE puzzle_date = $1`, mustDate(t, "2026-08-19")); err != nil {
		t.Fatalf("discard the sanity-check board: %v", err)
	}

	_, err := g.GenerateFor(ctx, mustDate(t, "2026-08-19"))
	var insufficient *puzzle.InsufficientContentError
	if !errors.As(err, &insufficient) {
		t.Fatalf("day two GenerateFor() error = %T (%v), want *puzzle.InsufficientContentError", err, err)
	}
	if insufficient.Accepted != 0 {
		t.Errorf("day two Accepted = %d, want 0", insufficient.Accepted)
	}
	for _, starved := range insufficient.Starved {
		if starved.Reason != puzzle.StarvedAnswerRepeat {
			t.Errorf("topic %s starved with reason %q, want %q",
				starved.Slug, starved.Reason, puzzle.StarvedAnswerRepeat)
		}
	}
}

// TestGenerateForStopsBlockingOnceTheAnswerCooldownPasses is the other half
// of the guarantee: the rule is a window, not a permanent ban. The library
// here is the same fully-spent one as the across-days test, so the only
// reason the later date can generate is that day one has aged out.
func TestGenerateForStopsBlockingOnceTheAnswerCooldownPasses(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	for _, slug := range []string{"alpha", "beta", "gamma"} {
		seedTopicWithQuestionsPerDifficulty(t, tx, slug, 1)
	}
	g := puzzle.Generator{DB: tx, CooldownDays: 1, AnswerCooldownDays: 14, TimeLimitSeconds: 135}

	dayOne := mustDate(t, "2026-08-18")
	if _, err := g.GenerateFor(ctx, dayOne); err != nil {
		t.Fatalf("day one GenerateFor() error = %v", err)
	}

	// 13 days apart is still inside a 14-day window; 14 days apart is out.
	if _, err := g.GenerateFor(ctx, dayOne.AddDays(13)); err == nil {
		t.Error("GenerateFor 13 days later succeeded, want it blocked inside the 14-day window")
	}
	got, err := g.GenerateFor(ctx, dayOne.AddDays(14))
	if err != nil {
		t.Fatalf("GenerateFor 14 days later: %v, want the answer cooldown to have lapsed", err)
	}
	if len(got.Entries) != 9 {
		t.Errorf("entries = %d, want 9", len(got.Entries))
	}
}

// TestGenerateForSpendsNoAnswersForASkippedTopic covers the subtle half of
// the rule: a topic is filled difficulty by difficulty and may starve
// partway, and the answers it picked before starving must be released. Here
// alpha starves at hard after picking the one easy answer beta also needs. If
// alpha's discarded pick stayed spent, beta would starve too and only one
// topic would fill.
func TestGenerateForSpendsNoAnswersForASkippedTopic(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	alpha := seedTopicWithAnswers(t, tx, "alpha", map[content.Difficulty]string{
		content.Easy:   "Ada Lovelace",
		content.Medium: "Unique alpha medium",
		content.Hard:   "Unique alpha hard",
	})
	if _, err := tx.Exec(ctx,
		`UPDATE questions SET status = 'retired' WHERE topic_id = $1 AND difficulty = 'hard'`,
		alpha.ID); err != nil {
		t.Fatalf("retire alpha's hard questions: %v", err)
	}
	// alpha has to be the first topic tried, or it never makes the partial
	// pick this test is about: were beta visited first, beta would take
	// "Ada Lovelace" and alpha would starve at easy having picked nothing.
	// The weight makes that overwhelmingly likely and the assertion on
	// Starved below confirms it actually happened, so a future change to the
	// shuffle turns this test red rather than quietly vacuous.
	if _, err := tx.Exec(ctx,
		`UPDATE topics SET selection_weight = 1000 WHERE id = $1`, alpha.ID); err != nil {
		t.Fatalf("weight alpha to the front: %v", err)
	}
	seedTopicWithAnswers(t, tx, "beta", map[content.Difficulty]string{
		content.Easy:   "Ada Lovelace",
		content.Medium: "Unique beta medium",
		content.Hard:   "Unique beta hard",
	})
	seedTopicWithQuestionsPerDifficulty(t, tx, "gamma", 1)

	g := puzzle.Generator{DB: tx, CooldownDays: 180, AnswerCooldownDays: 14, TimeLimitSeconds: 135}
	_, err := g.GenerateFor(ctx, mustDate(t, "2026-08-18"))

	// Three topics for a three-topic board, one of which cannot fill: the
	// board fails either way. Accepted is what distinguishes a released pick
	// (beta and gamma fill, 2) from a leaked one (beta starves too, 1). This
	// holds whichever order the weighted shuffle visits alpha and beta in.
	var insufficient *puzzle.InsufficientContentError
	if !errors.As(err, &insufficient) {
		t.Fatalf("GenerateFor() error = %T (%v), want *puzzle.InsufficientContentError", err, err)
	}
	// alpha starving at hard (rather than at easy, on a repeated answer) is
	// what proves it was tried first and did pick an easy question before
	// giving up.
	wantStarved := puzzle.StarvedTopic{
		Slug: "alpha", Difficulty: content.Hard, Reason: puzzle.StarvedNoEligibleQuestion,
	}
	if len(insufficient.Starved) != 1 || insufficient.Starved[0] != wantStarved {
		t.Fatalf("Starved = %+v, want exactly [%+v]", insufficient.Starved, wantStarved)
	}
	if insufficient.Accepted != 2 {
		t.Errorf("Accepted = %d, want 2: a skipped topic must not spend the answers it picked before starving",
			insufficient.Accepted)
	}
}

// TestGenerateForBoardAnswersAreDistinct guards the invariant on an ordinary
// board, where the library has room to avoid collisions rather than being
// forced into them. Every topic offers the same three shared answers plus
// unique ones, so a generator that only deduplicated within a topic would
// still put a repeat on the board.
func TestGenerateForBoardAnswersAreDistinct(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	for _, slug := range []string{"alpha", "beta", "gamma", "delta"} {
		id, err := content.UpsertTopic(ctx, tx, slug, slug)
		if err != nil {
			t.Fatalf("UpsertTopic(%s): %v", slug, err)
		}
		for _, d := range content.AllDifficulties {
			makeAnsweredQuestion(t, tx, id, d, slug+"-shared-"+string(d), "Shared "+string(d))
			makeAnsweredQuestion(t, tx, id, d, slug+"-own-"+string(d), "Own "+slug+" "+string(d))
		}
	}

	g := puzzle.Generator{DB: tx, CooldownDays: 180, AnswerCooldownDays: 14, TimeLimitSeconds: 135}
	got, err := g.GenerateFor(ctx, mustDate(t, "2026-08-18"))
	if err != nil {
		t.Fatalf("GenerateFor() error = %v", err)
	}

	keys := map[string]bool{}
	for _, answer := range boardAnswers(t, tx, got) {
		key := content.AnswerKey(answer)
		if keys[key] {
			t.Errorf("answer %q appears more than once on the board", answer)
		}
		keys[key] = true
	}
	if len(keys) != 9 {
		t.Errorf("board has %d distinct answers, want 9", len(keys))
	}
}

// seedTopicWithAnswers creates a topic with exactly one question per
// difficulty, using the caller's answers so a test can force two questions
// that differ in prompt and difficulty to share an answer.
func seedTopicWithAnswers(t *testing.T, tx db.DBTX, slug string, answers map[content.Difficulty]string) content.Topic {
	t.Helper()
	ctx := context.Background()

	id, err := content.UpsertTopic(ctx, tx, slug, slug)
	if err != nil {
		t.Fatalf("UpsertTopic(%s): %v", slug, err)
	}
	for _, d := range content.AllDifficulties {
		answer, ok := answers[d]
		if !ok {
			t.Fatalf("seedTopicWithAnswers(%s): no answer given for %s", slug, d)
		}
		makeAnsweredQuestion(t, tx, id, d, fmt.Sprintf("%s-%s", slug, d), answer)
	}
	return content.Topic{ID: id, Slug: slug, Name: slug, Active: true}
}

// makeAnsweredQuestion inserts one eligible question with a caller-chosen
// answer and a prompt derived from its external id, so questions that share
// an answer never share a prompt.
func makeAnsweredQuestion(
	t *testing.T,
	tx db.DBTX,
	topicID int64,
	d content.Difficulty,
	externalID string,
	answer string,
) int64 {
	t.Helper()
	ctx := context.Background()

	id, err := content.UpsertQuestion(ctx, tx, content.QuestionInput{
		TopicID: topicID, Difficulty: d,
		Prompt: "Prompt " + externalID, CanonicalAnswer: answer,
		Status: "active", Source: "test", ExternalID: externalID,
	})
	if err != nil {
		t.Fatalf("UpsertQuestion(%s): %v", externalID, err)
	}
	if err := content.ReplaceAliases(ctx, tx, id, []string{answer}); err != nil {
		t.Fatalf("ReplaceAliases: %v", err)
	}
	if err := content.ReplaceDistractors(ctx, tx, id, []string{"w1", "w2", "w3", "w4", "w5"}); err != nil {
		t.Fatalf("ReplaceDistractors: %v", err)
	}
	return id
}

// boardAnswers reads the canonical answers behind a board's entries, which
// Entry deliberately does not carry.
func boardAnswers(t *testing.T, tx pgx.Tx, p *puzzle.Puzzle) []string {
	t.Helper()

	answers := make([]string, 0, len(p.Entries))
	for _, e := range p.Entries {
		var answer string
		if err := tx.QueryRow(context.Background(),
			`SELECT canonical_answer FROM questions WHERE id = $1`, e.QuestionID).Scan(&answer); err != nil {
			t.Fatalf("read answer for question %d: %v", e.QuestionID, err)
		}
		answers = append(answers, answer)
	}
	return answers
}

// seedTopicWithQuestionsPerDifficulty creates a topic with exactly n eligible
// questions per difficulty, so tests can control the library's exact
// capacity rather than the generous headroom seedTopic provides.
func seedTopicWithQuestionsPerDifficulty(t *testing.T, tx db.DBTX, slug string, n int) content.Topic {
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
				TopicID: id, Difficulty: d,
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
