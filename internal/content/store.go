// Package content stores and queries the trivia question library.
package content

import (
	"context"
	"fmt"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/db"
	"github.com/hjordan6/trivial/internal/grading"
)

// Difficulty is one of the three tiers every topic contributes each day.
type Difficulty string

const (
	Easy   Difficulty = "easy"
	Medium Difficulty = "medium"
	Hard   Difficulty = "hard"
)

// AllDifficulties lists the tiers in the order they appear on the board.
var AllDifficulties = []Difficulty{Easy, Medium, Hard}

// Topic is a subject area questions belong to.
type Topic struct {
	ID     int64
	Slug   string
	Name   string
	Active bool
	// SelectionWeight is relative to other active topics. A topic weighted 3
	// is three times as likely to be drawn as one weighted 1.
	SelectionWeight int
}

// Question is a single trivia question.
type Question struct {
	ID               int64
	TopicID          int64
	Difficulty       Difficulty
	DifficultyRating int
	Prompt           string
	CanonicalAnswer  string
}

// QuestionInput is the writable shape of a question.
type QuestionInput struct {
	TopicID          int64
	Difficulty       Difficulty
	DifficultyRating int
	Prompt           string
	CanonicalAnswer  string
	Status           string // "draft", "active", or "retired"
	Source           string
	ExternalID       string
}

// UpsertTopic inserts a topic or updates its name, returning its id.
func UpsertTopic(ctx context.Context, q db.DBTX, slug, name string) (int64, error) {
	return UpsertTopicWithWeight(ctx, q, slug, name, 1)
}

// UpsertTopicWithWeight inserts a topic or updates its name and relative
// selection weight, returning its id.
func UpsertTopicWithWeight(ctx context.Context, q db.DBTX, slug, name string, weight int) (int64, error) {
	if weight < 1 || weight > 1000 {
		return 0, fmt.Errorf("topic %q selection weight must be between 1 and 1000, got %d", slug, weight)
	}
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO topics (slug, name, selection_weight) VALUES ($1, $2, $3)
		ON CONFLICT (slug) DO UPDATE SET
			name = EXCLUDED.name,
			selection_weight = EXCLUDED.selection_weight
		RETURNING id`, slug, name, weight).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert topic %q: %w", slug, err)
	}
	return id, nil
}

// UpsertQuestion inserts a question, or updates the existing one with the same
// (source, external_id). This is what makes re-running an importer safe.
func UpsertQuestion(ctx context.Context, q db.DBTX, in QuestionInput) (int64, error) {
	rating := in.DifficultyRating
	if rating == 0 {
		rating = representativeRating(in.Difficulty)
	}
	band, err := BandForRating(rating)
	if err != nil {
		return 0, err
	}
	if in.Difficulty != "" && in.Difficulty != band {
		return 0, fmt.Errorf("difficulty rating %d is %s, not %s", rating, band, in.Difficulty)
	}
	var id int64
	err = q.QueryRow(ctx, `
		INSERT INTO questions
			(topic_id, difficulty, difficulty_rating, prompt, canonical_answer, status, source, external_id)
		VALUES ($1, $2::difficulty, $3, $4, $5, $6::question_status, $7, NULLIF($8, ''))
		ON CONFLICT (source, external_id) WHERE external_id IS NOT NULL
		DO UPDATE SET
			topic_id         = EXCLUDED.topic_id,
			difficulty       = EXCLUDED.difficulty,
			difficulty_rating = EXCLUDED.difficulty_rating,
			prompt           = EXCLUDED.prompt,
			canonical_answer = EXCLUDED.canonical_answer,
			status           = EXCLUDED.status,
			updated_at       = now()
		RETURNING id`,
		in.TopicID, string(band), rating, in.Prompt, in.CanonicalAnswer,
		in.Status, in.Source, in.ExternalID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert question %q: %w", in.ExternalID, err)
	}
	return id, nil
}

// BandForRating converts the internal 1–10 scale to the only difficulty label
// exposed to players.
func BandForRating(rating int) (Difficulty, error) {
	switch {
	case rating >= 1 && rating <= 4:
		return Easy, nil
	case rating >= 5 && rating <= 7:
		return Medium, nil
	case rating >= 8 && rating <= 10:
		return Hard, nil
	default:
		return "", fmt.Errorf("difficulty rating must be between 1 and 10, got %d", rating)
	}
}

func representativeRating(difficulty Difficulty) int {
	switch difficulty {
	case Easy:
		return 2
	case Medium:
		return 6
	case Hard:
		return 9
	default:
		return 0
	}
}

// ReplaceAliases rewrites a question's accepted answers. Each alias is stored
// alongside its normalized form, produced by the same function that normalizes
// player input, so the two can never drift apart.
func ReplaceAliases(ctx context.Context, q db.DBTX, questionID int64, aliases []string) error {
	if _, err := q.Exec(ctx, `DELETE FROM question_aliases WHERE question_id = $1`, questionID); err != nil {
		return fmt.Errorf("clear aliases for question %d: %w", questionID, err)
	}

	seen := make(map[string]bool, len(aliases))
	for _, alias := range aliases {
		normalized := grading.Normalize(alias)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		if _, err := q.Exec(ctx,
			`INSERT INTO question_aliases (question_id, alias, normalized) VALUES ($1, $2, $3)`,
			questionID, alias, normalized); err != nil {
			return fmt.Errorf("insert alias %q for question %d: %w", alias, questionID, err)
		}
	}
	return nil
}

// ReplaceDistractors rewrites a question's wrong multiple-choice options.
func ReplaceDistractors(ctx context.Context, q db.DBTX, questionID int64, options []string) error {
	if _, err := q.Exec(ctx, `DELETE FROM question_distractors WHERE question_id = $1`, questionID); err != nil {
		return fmt.Errorf("clear distractors for question %d: %w", questionID, err)
	}

	seen := make(map[string]bool, len(options))
	for _, option := range options {
		if option == "" || seen[option] {
			continue
		}
		seen[option] = true
		if _, err := q.Exec(ctx,
			`INSERT INTO question_distractors (question_id, option_text) VALUES ($1, $2)`,
			questionID, option); err != nil {
			return fmt.Errorf("insert distractor %q for question %d: %w", option, questionID, err)
		}
	}
	return nil
}

// ActiveTopics returns every selectable topic, ordered by id so deterministic
// weighted selection starts from a stable input order.
func ActiveTopics(ctx context.Context, q db.DBTX) ([]Topic, error) {
	rows, err := q.Query(ctx, `SELECT id, slug, name, active, selection_weight FROM topics WHERE active ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query active topics: %w", err)
	}
	defer rows.Close()

	var topics []Topic
	for rows.Next() {
		var t Topic
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.Active, &t.SelectionWeight); err != nil {
			return nil, fmt.Errorf("scan topic: %w", err)
		}
		topics = append(topics, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read active topics: %w", err)
	}
	return topics, nil
}

// EligibleQuestions returns the questions that may be used for a topic and
// difficulty on the target date.
//
// A question is eligible when it is active, has at least one alias and at
// least three distractors, and has not been used within cooldownDays of the
// target date in either direction. Uses on the target date itself are ignored,
// so regenerating a day is not blocked by its own existing rows.
func EligibleQuestions(
	ctx context.Context,
	q db.DBTX,
	topicID int64,
	difficulty Difficulty,
	target clock.Date,
	cooldownDays int,
) ([]Question, error) {
	rows, err := q.Query(ctx, `
		SELECT q.id, q.topic_id, q.difficulty, q.difficulty_rating, q.prompt, q.canonical_answer
		FROM questions q
		WHERE q.topic_id = $1
		  AND q.difficulty = $2::difficulty
		  AND q.status = 'active'
		  AND EXISTS (SELECT 1 FROM question_aliases a WHERE a.question_id = q.id)
		  AND (SELECT count(*) FROM question_distractors d WHERE d.question_id = q.id) >= 3
		  AND NOT EXISTS (
		        SELECT 1 FROM daily_puzzle_questions dpq
		        WHERE dpq.question_id = q.id
		          AND dpq.puzzle_date <> $3::date
		          AND abs(dpq.puzzle_date - $3::date) < $4
		      )
		ORDER BY q.id`,
		topicID, string(difficulty), target, cooldownDays)
	if err != nil {
		return nil, fmt.Errorf("query eligible questions for topic %d %s: %w", topicID, difficulty, err)
	}
	defer rows.Close()

	var questions []Question
	for rows.Next() {
		var qn Question
		if err := rows.Scan(&qn.ID, &qn.TopicID, &qn.Difficulty, &qn.DifficultyRating, &qn.Prompt, &qn.CanonicalAnswer); err != nil {
			return nil, fmt.Errorf("scan question: %w", err)
		}
		questions = append(questions, qn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read eligible questions for topic %d %s: %w", topicID, difficulty, err)
	}
	return questions, nil
}
