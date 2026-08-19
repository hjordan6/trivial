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

// TestSeedAliasesAreAcceptedVerbatim is the mirror image of the sweep above:
// every alias a question lists (plus its canonical answer, which ParseSeed
// folds into the alias list) must itself be graded correct when typed
// exactly. This exists because round 2 of review found aliases that were
// *supposed* to cover a real player spelling (a taxonomic name, a common
// US spelling of a title, a digit's word form) but were only ever added to
// the JSON, never exercised against Grade. A typo in an alias, or an alias
// that collides with the Roman-numeral tolerance rule in a way that makes
// it reject itself, would slip past every other test in this package.
func TestSeedAliasesAreAcceptedVerbatim(t *testing.T) {
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
			for _, a := range q.Aliases {
				result := grading.Grade(a, normalizedAliases)
				if !result.Correct {
					t.Errorf("%s: alias %q is NOT graded correct against its own question's aliases %v",
						q.ExternalID, a, q.Aliases)
				}
			}
		}
	}
}
