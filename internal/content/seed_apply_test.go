package content_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/testsupport"
)

func TestApplySeedIsIdempotent(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	data, err := os.ReadFile("../../seed/questions.json")
	if err != nil {
		t.Fatalf("read seed file: %v", err)
	}
	seed, err := content.ParseSeed(data)
	if err != nil {
		t.Fatalf("ParseSeed: %v", err)
	}

	first, err := content.ApplySeed(ctx, tx, seed)
	if err != nil {
		t.Fatalf("first ApplySeed: %v", err)
	}
	firstQuestions, firstAliases, firstDistractors := countSeedRows(t, ctx, tx)

	second, err := content.ApplySeed(ctx, tx, seed)
	if err != nil {
		t.Fatalf("second ApplySeed: %v", err)
	}
	if first != second {
		t.Errorf("ApplySeed stats differ across runs: %+v then %+v", first, second)
	}

	secondQuestions, secondAliases, secondDistractors := countSeedRows(t, ctx, tx)

	// Counts are scoped to source = 'seed' rather than every row in the
	// table, so this test cannot be broken by another test (or `make seed`
	// pointed at the test database) leaving unrelated rows behind.
	if secondQuestions != firstQuestions {
		t.Errorf("question rows (source=seed) = %d after two applies, want %d", secondQuestions, firstQuestions)
	}
	if secondQuestions != first.Questions {
		t.Errorf("question rows (source=seed) = %d after two applies, want %d (ApplySeed stats)", secondQuestions, first.Questions)
	}
	// Aliases and distractors are where an orphan would actually show up:
	// ReplaceAliases/ReplaceDistractors delete-then-reinsert per question, so
	// a second apply that somehow left stale rows behind (e.g. from a
	// question_id mismatch on re-upsert) would grow these counts even though
	// the question count above stayed put.
	if secondAliases != firstAliases {
		t.Errorf("alias rows = %d after second apply, want %d (unchanged from first apply)", secondAliases, firstAliases)
	}
	if secondDistractors != firstDistractors {
		t.Errorf("distractor rows = %d after second apply, want %d (unchanged from first apply)", secondDistractors, firstDistractors)
	}
}

// countSeedRows reports how many questions, aliases, and distractors belong
// to rows written by the seed loader (source = 'seed'), so the idempotency
// check can't be fooled by rows some other test or process left behind.
func countSeedRows(t *testing.T, ctx context.Context, tx pgx.Tx) (questions, aliases, distractors int) {
	t.Helper()

	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM questions WHERE source = 'seed'`,
	).Scan(&questions); err != nil {
		t.Fatalf("count seed questions: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM question_aliases a
		JOIN questions q ON q.id = a.question_id
		WHERE q.source = 'seed'`,
	).Scan(&aliases); err != nil {
		t.Fatalf("count seed aliases: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM question_distractors d
		JOIN questions q ON q.id = d.question_id
		WHERE q.source = 'seed'`,
	).Scan(&distractors); err != nil {
		t.Fatalf("count seed distractors: %v", err)
	}
	return questions, aliases, distractors
}

func TestSeedFileIsRichEnoughToGenerate(t *testing.T) {
	data, err := os.ReadFile("../../seed/questions.json")
	if err != nil {
		t.Fatalf("read seed file: %v", err)
	}
	seed, err := content.ParseSeed(data)
	if err != nil {
		t.Fatalf("ParseSeed: %v", err)
	}
	if len(seed.Topics) < 6 {
		t.Errorf("seed has %d topics, want at least 6 so a day can pick 3 with room to skip", len(seed.Topics))
	}
	for _, topic := range seed.Topics {
		counts := map[content.Difficulty]int{}
		for _, q := range topic.Questions {
			counts[q.Difficulty]++
		}
		for _, d := range content.AllDifficulties {
			if counts[d] < 3 {
				t.Errorf("topic %s has %d %s questions, want at least 3", topic.Slug, counts[d], d)
			}
		}
	}
}
