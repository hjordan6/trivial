package content

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

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
	Weight    int            `json:"weight,omitempty"`
	Questions []SeedQuestion `json:"questions"`
}

// SeedQuestion is one question with everything needed to make it eligible.
type SeedQuestion struct {
	ExternalID  string           `json:"external_id"`
	Difficulty  DifficultyRating `json:"difficulty"`
	Prompt      string           `json:"prompt"`
	Answer      string           `json:"answer"`
	Aliases     []string         `json:"aliases"`
	Distractors []string         `json:"distractors"`
}

// DifficultyRating is the internal 1–10 question scale. It accepts the old
// easy/medium/hard strings while existing seed files are migrated.
type DifficultyRating int

func (d *DifficultyRating) UnmarshalJSON(data []byte) error {
	var rating int
	if err := json.Unmarshal(data, &rating); err == nil {
		*d = DifficultyRating(rating)
		return nil
	}
	var legacy string
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("difficulty must be a number from 1 to 10")
	}
	switch Difficulty(legacy) {
	case Easy:
		*d = 2
	case Medium:
		*d = 6
	case Hard:
		*d = 9
	default:
		return fmt.Errorf("unknown difficulty %q", legacy)
	}
	return nil
}

type flatQuestion struct {
	Question              string           `json:"question"`
	Category              string           `json:"category"`
	Difficulty            DifficultyRating `json:"difficulty"`
	Answer                string           `json:"answer"`
	AcceptedAnswers       []string         `json:"acceptedAnswers"`
	MultipleChoiceOptions []string         `json:"multipleChoiceOptions"`
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
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var flat []flatQuestion
		if err := json.Unmarshal(data, &flat); err != nil {
			return SeedFile{}, fmt.Errorf("parse seed: %w", err)
		}
		seed = convertFlatQuestions(flat)
	} else {
		var wrapped struct {
			Topics    []SeedTopic    `json:"topics"`
			Questions []flatQuestion `json:"questions"`
		}
		if err := json.Unmarshal(data, &wrapped); err != nil {
			return SeedFile{}, fmt.Errorf("parse seed: %w", err)
		}
		if wrapped.Questions != nil {
			seed = convertFlatQuestions(wrapped.Questions)
		} else {
			seed.Topics = wrapped.Topics
		}
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
		if topic.Weight == 0 {
			seed.Topics[i].Weight = 1
		} else if topic.Weight < 1 || topic.Weight > 1000 {
			return SeedFile{}, fmt.Errorf("topic %q: weight must be between 1 and 1000", topic.Slug)
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

			if err := ValidateQuestion(q); err != nil {
				return SeedFile{}, fmt.Errorf("%s: %w", where, err)
			}
		}
	}
	return seed, nil
}

// ValidateQuestion checks that one question could actually reach a board, and
// normalizes what it can. Errors are unprefixed so each caller can say where
// the question came from: ParseSeed names the topic and index within a file,
// while the admin panel is editing a single question and has nowhere to point.
//
// It also appends the canonical answer to Aliases when it is missing, so a
// validated question always carries every spelling a grader accepts.
func ValidateQuestion(q *SeedQuestion) error {
	if _, err := BandForRating(int(q.Difficulty)); err != nil {
		return err
	}
	if q.Prompt == "" {
		return fmt.Errorf("prompt is required")
	}
	if q.Answer == "" {
		return fmt.Errorf("answer is required")
	}

	// accepted collects the normalized form of every answer a grader would
	// mark correct: the canonical answer plus every alias. A distractor is
	// only a real wrong option if it normalizes to something outside this set
	// — matching an alias would put two correct options on the board just as
	// surely as matching the canonical answer would.
	answerKey := grading.Normalize(q.Answer)
	if answerKey == "" {
		return fmt.Errorf("answer %q normalizes to empty", q.Answer)
	}
	accepted := map[string]bool{answerKey: true}
	hasAnswerAlias := false
	for _, a := range q.Aliases {
		ak := grading.Normalize(a)
		if ak == "" {
			return fmt.Errorf("alias %q normalizes to empty", a)
		}
		accepted[ak] = true
		if ak == answerKey {
			hasAnswerAlias = true
		}
	}

	// Distractors are counted by normalized form, not raw count: a duplicate
	// distractor (same text, or merely the same after normalization) collapses
	// to one row in ReplaceDistractors, so counting raw entries would let a
	// question with fewer than three real wrong options pass validation and
	// then silently fall short of EligibleQuestions' requirement with no error
	// anywhere.
	distractorKeys := map[string]bool{}
	for _, d := range q.Distractors {
		key := grading.Normalize(d)
		if accepted[key] {
			return fmt.Errorf("distractor %q is an accepted answer", d)
		}
		distractorKeys[key] = true
	}
	if len(distractorKeys) < 3 {
		return fmt.Errorf("has %d distinct distractors, want at least 3 distractors", len(distractorKeys))
	}

	// The canonical answer is always accepted, whether or not it was listed
	// among the aliases.
	if !hasAnswerAlias {
		q.Aliases = append([]string{q.Answer}, q.Aliases...)
	}
	return nil
}

func convertFlatQuestions(flat []flatQuestion) SeedFile {
	var seed SeedFile
	byCategory := map[string]int{}
	for _, item := range flat {
		slug := slugify(item.Category)
		index, ok := byCategory[slug]
		if !ok {
			index = len(seed.Topics)
			byCategory[slug] = index
			seed.Topics = append(seed.Topics, SeedTopic{Slug: slug, Name: item.Category, Weight: 1})
		}
		accepted := append([]string(nil), item.AcceptedAnswers...)
		acceptedKeys := map[string]bool{grading.Normalize(item.Answer): true}
		for _, answer := range accepted {
			acceptedKeys[grading.Normalize(answer)] = true
		}
		var distractors []string
		for _, option := range item.MultipleChoiceOptions {
			if !acceptedKeys[grading.Normalize(option)] {
				distractors = append(distractors, option)
			}
		}
		hash := sha256.Sum256([]byte(item.Question))
		seed.Topics[index].Questions = append(seed.Topics[index].Questions, SeedQuestion{
			ExternalID: fmt.Sprintf("%s-%x", slug, hash[:8]), Difficulty: item.Difficulty,
			Prompt: item.Question, Answer: item.Answer, Aliases: accepted, Distractors: distractors,
		})
	}
	return seed
}

func slugify(value string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
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
		topicID, err := UpsertTopicWithWeight(ctx, q, topic.Slug, topic.Name, topic.Weight)
		if err != nil {
			return SeedStats{}, err
		}
		stats.Topics++

		for _, question := range topic.Questions {
			band, err := BandForRating(int(question.Difficulty))
			if err != nil {
				return SeedStats{}, err
			}
			questionID, err := UpsertQuestion(ctx, q, QuestionInput{
				TopicID:          topicID,
				Difficulty:       band,
				DifficultyRating: int(question.Difficulty),
				Prompt:           question.Prompt,
				CanonicalAnswer:  question.Answer,
				Status:           "active",
				Source:           seedSource,
				ExternalID:       question.ExternalID,
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
