package content

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hjordan6/trivial/internal/db"
	"github.com/hjordan6/trivial/internal/grading"
)

// SeedFile is the on-disk format for hand-authored and imported content.
type SeedFile struct {
	Topics []SeedTopic `json:"topics"`
}

// SeedTopic is one subject area and its questions.
type SeedTopic struct {
	Slug      string         `json:"slug"`
	Name      string         `json:"name"`
	Questions []SeedQuestion `json:"questions"`
}

// SeedQuestion is one question with everything needed to make it eligible.
type SeedQuestion struct {
	ExternalID  string     `json:"external_id"`
	Difficulty  Difficulty `json:"difficulty"`
	Prompt      string     `json:"prompt"`
	Answer      string     `json:"answer"`
	Aliases     []string   `json:"aliases"`
	Distractors []string   `json:"distractors"`
}

// SeedStats reports what a seed application touched.
type SeedStats struct {
	Topics    int
	Questions int
}

// seedSource marks rows written by the seed loader so re-running it updates
// them rather than inserting duplicates.
const seedSource = "seed"

// ParseSeed decodes and validates a seed file. Validation is strict: content
// that could never become eligible is a bug in the file, not something to
// discover later when a puzzle fails to generate.
func ParseSeed(data []byte) (SeedFile, error) {
	var seed SeedFile
	if err := json.Unmarshal(data, &seed); err != nil {
		return SeedFile{}, fmt.Errorf("parse seed: %w", err)
	}

	seenExternalIDs := map[string]bool{}
	seenSlugs := map[string]bool{}

	for i, topic := range seed.Topics {
		if topic.Slug == "" {
			return SeedFile{}, fmt.Errorf("topic %d: slug is required", i)
		}
		if seenSlugs[topic.Slug] {
			return SeedFile{}, fmt.Errorf("topic %q: duplicate slug", topic.Slug)
		}
		seenSlugs[topic.Slug] = true
		if topic.Name == "" {
			return SeedFile{}, fmt.Errorf("topic %q: name is required", topic.Slug)
		}

		for j := range topic.Questions {
			q := &seed.Topics[i].Questions[j]
			where := fmt.Sprintf("topic %q question %d", topic.Slug, j)

			if q.ExternalID == "" {
				return SeedFile{}, fmt.Errorf("%s: external_id is required", where)
			}
			if seenExternalIDs[q.ExternalID] {
				return SeedFile{}, fmt.Errorf("%s: duplicate external_id %q", where, q.ExternalID)
			}
			seenExternalIDs[q.ExternalID] = true

			switch q.Difficulty {
			case Easy, Medium, Hard:
			default:
				return SeedFile{}, fmt.Errorf("%s: unknown difficulty %q", where, q.Difficulty)
			}
			if q.Prompt == "" {
				return SeedFile{}, fmt.Errorf("%s: prompt is required", where)
			}
			if q.Answer == "" {
				return SeedFile{}, fmt.Errorf("%s: answer is required", where)
			}

			// accepted collects the normalized form of every answer a grader
			// would mark correct: the canonical answer plus every alias. A
			// distractor is only a real wrong option if it normalizes to
			// something outside this set — matching an alias would put two
			// correct options on the board just as surely as matching the
			// canonical answer would.
			answerKey := grading.Normalize(q.Answer)
			accepted := map[string]bool{answerKey: true}
			hasAnswerAlias := false
			for _, a := range q.Aliases {
				ak := grading.Normalize(a)
				if ak == "" {
					return SeedFile{}, fmt.Errorf("%s: alias %q normalizes to empty", where, a)
				}
				accepted[ak] = true
				if ak == answerKey {
					hasAnswerAlias = true
				}
			}

			// Distractors are counted by normalized form, not raw count: a
			// duplicate distractor (same text, or merely the same after
			// normalization) collapses to one row in ReplaceDistractors, so
			// counting raw entries would let a file with fewer than five
			// real options pass validation and then silently fall short of
			// EligibleQuestions' >= 5 requirement with no error anywhere.
			distractorKeys := map[string]bool{}
			for _, d := range q.Distractors {
				dk := grading.Normalize(d)
				if accepted[dk] {
					return SeedFile{}, fmt.Errorf("%s: distractor %q matches an accepted answer", where, d)
				}
				distractorKeys[dk] = true
			}
			if len(distractorKeys) < 5 {
				return SeedFile{}, fmt.Errorf("%s: has %d distinct distractors, want at least 5 distractors", where, len(distractorKeys))
			}

			// The canonical answer is always accepted, whether or not it was
			// listed among the aliases.
			if !hasAnswerAlias {
				q.Aliases = append([]string{q.Answer}, q.Aliases...)
			}
		}
	}
	return seed, nil
}

// ApplySeed writes a parsed seed file to the database, upserting by
// (source, external_id) so it can be run repeatedly.
//
// ApplySeed is not internally atomic: it issues one statement per topic and
// per question/alias/distractor write, with no transaction of its own. A
// failure partway through leaves earlier writes in place. Callers that need
// all-or-nothing behavior must pass a q that is itself a transaction.
func ApplySeed(ctx context.Context, q db.DBTX, seed SeedFile) (SeedStats, error) {
	var stats SeedStats

	for _, topic := range seed.Topics {
		topicID, err := UpsertTopic(ctx, q, topic.Slug, topic.Name)
		if err != nil {
			return SeedStats{}, err
		}
		stats.Topics++

		for _, question := range topic.Questions {
			questionID, err := UpsertQuestion(ctx, q, QuestionInput{
				TopicID:         topicID,
				Difficulty:      question.Difficulty,
				Prompt:          question.Prompt,
				CanonicalAnswer: question.Answer,
				Status:          "active",
				Source:          seedSource,
				ExternalID:      question.ExternalID,
			})
			if err != nil {
				return SeedStats{}, err
			}
			if err := ReplaceAliases(ctx, q, questionID, question.Aliases); err != nil {
				return SeedStats{}, err
			}
			if err := ReplaceDistractors(ctx, q, questionID, question.Distractors); err != nil {
				return SeedStats{}, err
			}
			stats.Questions++
		}
	}
	return stats, nil
}
