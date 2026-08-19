// Package puzzle builds and stores the daily nine-question board.
package puzzle

import (
	"context"
	"fmt"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/db"
)

// Entry is one cell of the board: a question at a difficulty within a topic.
type Entry struct {
	TopicID       int64
	TopicSlug     string
	TopicName     string
	TopicPosition int
	Difficulty    content.Difficulty
	QuestionID    int64
	Prompt        string
}

// Puzzle is one day's board of nine questions.
type Puzzle struct {
	Date             clock.Date
	TimeLimitSeconds int
	Entries          []Entry
}

// Get returns the puzzle for a date, or nil when none has been generated.
func Get(ctx context.Context, q db.DBTX, date clock.Date) (*Puzzle, error) {
	rows, err := q.Query(ctx, `
		SELECT p.time_limit_seconds,
		       dpq.topic_id, t.slug, t.name, dpq.topic_position,
		       dpq.difficulty, dpq.question_id, qn.prompt
		FROM daily_puzzles p
		JOIN daily_puzzle_questions dpq ON dpq.puzzle_date = p.puzzle_date
		JOIN topics t ON t.id = dpq.topic_id
		JOIN questions qn ON qn.id = dpq.question_id
		WHERE p.puzzle_date = $1
		ORDER BY dpq.topic_position,
		         array_position(ARRAY['easy','medium','hard']::difficulty[], dpq.difficulty)`,
		date)
	if err != nil {
		return nil, fmt.Errorf("query puzzle for %s: %w", date, err)
	}
	defer rows.Close()

	p := &Puzzle{Date: date}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(
			&p.TimeLimitSeconds,
			&e.TopicID, &e.TopicSlug, &e.TopicName, &e.TopicPosition,
			&e.Difficulty, &e.QuestionID, &e.Prompt,
		); err != nil {
			return nil, fmt.Errorf("scan puzzle entry: %w", err)
		}
		p.Entries = append(p.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read puzzle rows: %w", err)
	}
	if len(p.Entries) == 0 {
		return nil, nil
	}
	return p, nil
}

// Insert writes a puzzle and its entries. Callers that need atomicity — the
// generator does — should pass a transaction.
func Insert(ctx context.Context, q db.DBTX, p *Puzzle) error {
	if _, err := q.Exec(ctx,
		`INSERT INTO daily_puzzles (puzzle_date, time_limit_seconds) VALUES ($1, $2)`,
		p.Date, p.TimeLimitSeconds); err != nil {
		return fmt.Errorf("insert puzzle %s: %w", p.Date, err)
	}
	for _, e := range p.Entries {
		if _, err := q.Exec(ctx, `
			INSERT INTO daily_puzzle_questions
				(puzzle_date, topic_id, topic_position, difficulty, question_id)
			VALUES ($1, $2, $3, $4::difficulty, $5)`,
			p.Date, e.TopicID, e.TopicPosition, string(e.Difficulty), e.QuestionID); err != nil {
			return fmt.Errorf("insert puzzle entry %s/%s: %w", e.TopicSlug, e.Difficulty, err)
		}
	}
	return nil
}
