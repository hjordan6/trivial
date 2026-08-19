package content_test

import (
	"context"
	"os"
	"testing"

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
	second, err := content.ApplySeed(ctx, tx, seed)
	if err != nil {
		t.Fatalf("second ApplySeed: %v", err)
	}
	if first != second {
		t.Errorf("ApplySeed stats differ across runs: %+v then %+v", first, second)
	}

	var questions int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM questions`).Scan(&questions); err != nil {
		t.Fatalf("count questions: %v", err)
	}
	if questions != first.Questions {
		t.Errorf("question rows = %d after two applies, want %d", questions, first.Questions)
	}
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
