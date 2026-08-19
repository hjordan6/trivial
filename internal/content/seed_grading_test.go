package content_test

import (
	"os"
	"testing"

	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/grading"
)

// TestSeedDistractorsAreNeverGradedCorrect is a sweep over every question in
// the seed file asserting that none of its distractors would be accepted as
// a correct free-text answer. This is the invariant behind the item 1 bug
// fix in toleranceFor: a distractor is worthless — worse than worthless,
// actively harmful — if a player who types it is told they were right. It
// mirrors runtime behavior: distractors are graded as raw player input
// (grading.Grade normalizes internally) against the question's aliases,
// pre-normalized exactly as ReplaceAliases stores them.
func TestSeedDistractorsAreNeverGradedCorrect(t *testing.T) {
	data, err := os.ReadFile("../../seed/questions.json")
	if err != nil {
		t.Fatalf("read seed file: %v", err)
	}
	seed, err := content.ParseSeed(data)
	if err != nil {
		t.Fatalf("ParseSeed: %v", err)
	}

	for _, topic := range seed.Topics {
		for _, q := range topic.Questions {
			normalizedAliases := make([]string, len(q.Aliases))
			for i, a := range q.Aliases {
				normalizedAliases[i] = grading.Normalize(a)
			}
			for _, d := range q.Distractors {
				result := grading.Grade(d, normalizedAliases)
				if result.Correct {
					t.Errorf("%s: distractor %q is graded correct against aliases %v (matched %q at distance %d)",
						q.ExternalID, d, q.Aliases, result.Matched, result.Distance)
				}
			}
		}
	}
}
