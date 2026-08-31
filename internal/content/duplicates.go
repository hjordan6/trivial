package content

import (
	"context"
	"fmt"
	"strings"

	"github.com/hjordan6/trivial/internal/db"
	"github.com/hjordan6/trivial/internal/grading"
)

// DuplicateAnswer is one incoming question whose answer is already taken in
// the topic and difficulty band it is headed for.
//
// Two questions with the same answer in the same topic and band can never
// share a board -- a board draws one question per topic per band -- but they
// are still the same question to a player, who meets the answer twice across
// two days and reads the second as a repeat.
type DuplicateAnswer struct {
	TopicSlug  string
	Difficulty Difficulty
	Answer     string
	Prompt     string
	// ExistingID is the question already holding that answer, or 0 when the
	// clash is with another question in the same payload.
	ExistingID int64
}

// answerKey is what makes two answers the same answer: the grader's own
// normalized form, so "The OED", "the oed" and "OED" collapse together exactly
// as they do when a player's typing is marked.
type answerKey struct {
	topic      string
	difficulty Difficulty
	answer     string
}

func keyFor(topic string, band Difficulty, answer string) answerKey {
	return answerKey{topic: topic, difficulty: band, answer: grading.Normalize(answer)}
}

// FindDuplicateAnswers reports incoming questions that would land on an answer
// already used in the same topic and band, either by a question in the library
// or by another question in the same payload.
//
// Retired questions are ignored. Retiring one is how an operator resolves this
// exact clash, so counting it afterwards would make the resolution look like a
// new problem and block the replacement they retired it for.
//
// A question that matches an existing row's external_id is its own update, not
// a duplicate: re-pasting a payload to fix a typo has to stay a paste-again
// operation.
func FindDuplicateAnswers(ctx context.Context, q db.DBTX, seed SeedFile) ([]DuplicateAnswer, error) {
	slugs := make([]string, 0, len(seed.Topics))
	for _, topic := range seed.Topics {
		slugs = append(slugs, topic.Slug)
	}
	if len(slugs) == 0 {
		return nil, nil
	}

	rows, err := q.Query(ctx, `
		SELECT q.id, q.external_id, t.slug, q.difficulty::text, q.canonical_answer
		  FROM questions q
		  JOIN topics t ON t.id = q.topic_id
		 WHERE t.slug = ANY($1) AND q.status <> 'retired'`, slugs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type existing struct {
		id         int64
		externalID string
	}
	taken := map[answerKey]existing{}
	byExternalID := map[string]bool{}
	for rows.Next() {
		var id int64
		var externalID, slug, band, answer string
		if err := rows.Scan(&id, &externalID, &slug, &band, &answer); err != nil {
			return nil, err
		}
		taken[keyFor(slug, Difficulty(band), answer)] = existing{id: id, externalID: externalID}
		byExternalID[externalID] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var found []DuplicateAnswer
	seen := map[answerKey]bool{}
	for _, topic := range seed.Topics {
		for _, question := range topic.Questions {
			band, err := BandForRating(int(question.Difficulty))
			if err != nil {
				// ParseSeed has already rejected an out-of-range rating; a
				// caller that skipped it gets no duplicate report, not a crash.
				continue
			}
			key := keyFor(topic.Slug, band, question.Answer)

			if seen[key] {
				found = append(found, DuplicateAnswer{TopicSlug: topic.Slug, Difficulty: band,
					Answer: question.Answer, Prompt: question.Prompt})
				continue
			}
			seen[key] = true

			if row, clash := taken[key]; clash && row.externalID != question.ExternalID {
				found = append(found, DuplicateAnswer{TopicSlug: topic.Slug, Difficulty: band,
					Answer: question.Answer, Prompt: question.Prompt, ExistingID: row.id})
			}
		}
	}
	return found, nil
}

// DuplicateAnswerMessage explains a refusal in the terms an operator can act
// on: which answer, where it already lives, and what to do about it.
func DuplicateAnswerMessage(found []DuplicateAnswer) string {
	const most = 3
	parts := make([]string, 0, most+1)
	for i, d := range found {
		if i == most {
			parts = append(parts, fmt.Sprintf("and %d more", len(found)-most))
			break
		}
		switch d.ExistingID {
		case 0:
			parts = append(parts, fmt.Sprintf("%q appears twice in this paste under %s / %s",
				d.Answer, d.TopicSlug, d.Difficulty))
		default:
			parts = append(parts, fmt.Sprintf("%q is already question %d in %s / %s",
				d.Answer, d.ExistingID, d.TopicSlug, d.Difficulty))
		}
	}
	return "Nothing was imported: " + strings.Join(parts, "; ") +
		". A topic and difficulty can only hold one question per answer -- change the answer, " +
		"drop that question from the paste, or retire the one already there."
}
