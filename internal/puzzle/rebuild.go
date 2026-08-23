package puzzle

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/db"
)

// ErrNotFuture means the date is today or earlier. Boards are only ever
// rebuilt before anyone can have seen them.
var ErrNotFuture = errors.New("puzzle date is not in the future")

// ErrDateHasRuns means someone has already played the date, so its board is
// history and must not change.
var ErrDateHasRuns = errors.New("puzzle date already has runs")

// Rebuild replaces a future date's board, honouring whatever topics are
// pinned to it. It is how an operator's pin edit reaches the board, since
// GenerateFor by contract leaves an existing puzzle alone.
//
// Every statement runs on g.DB, which is a pgx.Tx, so the delete and the
// insert of the new board commit together. That matters more here than
// anywhere else in the package: Get treats any entry count other than 0 or 9
// as a hard error for every reader of that date, so a half-replaced board
// would break the day for players, not just for the operator.
func (g Generator) Rebuild(ctx context.Context, date, today clock.Date) (*Puzzle, error) {
	if !today.Before(date) {
		return nil, fmt.Errorf("rebuild %s: %w", date, ErrNotFuture)
	}

	// The runs foreign key is ON DELETE RESTRICT, so the delete below would
	// fail anyway. Checking first turns a constraint violation into an error
	// the API can explain.
	var runs int
	if err := g.DB.QueryRow(ctx, `SELECT count(*) FROM runs WHERE puzzle_date = $1`, date).Scan(&runs); err != nil {
		return nil, fmt.Errorf("count runs for %s: %w", date, err)
	}
	if runs > 0 {
		return nil, fmt.Errorf("rebuild %s: %d run(s) exist: %w", date, runs, ErrDateHasRuns)
	}

	// Entries cascade from daily_puzzles, so this clears the whole board.
	if _, err := g.DB.Exec(ctx, `DELETE FROM daily_puzzles WHERE puzzle_date = $1`, date); err != nil {
		return nil, fmt.Errorf("delete puzzle %s: %w", date, err)
	}

	return g.GenerateFor(ctx, date)
}

// SetPins replaces the pinned topics for a date, keyed by board position.
// Positions absent from slots are left automatic, and an empty map reverts the
// date to fully automatic selection.
//
// It replaces rather than merges so that clearing one slot is expressible: the
// caller sends the state it wants, not a diff.
func SetPins(ctx context.Context, q db.DBTX, date clock.Date, slots map[int]int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM puzzle_topic_pins WHERE puzzle_date = $1`, date); err != nil {
		return fmt.Errorf("clear topic pins for %s: %w", date, err)
	}
	for _, position := range sortedSlots(slots) {
		if _, err := q.Exec(ctx, `
			INSERT INTO puzzle_topic_pins (puzzle_date, topic_position, topic_id)
			VALUES ($1, $2, $3)`, date, position, slots[position]); err != nil {
			return fmt.Errorf("pin topic %d at position %d on %s: %w", slots[position], position, date, err)
		}
	}
	return nil
}

// Pins returns the topics pinned to a date, keyed by position.
func Pins(ctx context.Context, q db.DBTX, date clock.Date) (map[int]content.Topic, error) {
	return pinsFor(ctx, q, date)
}

func sortedSlots(slots map[int]int64) []int {
	positions := make([]int, 0, len(slots))
	for position := range slots {
		positions = append(positions, position)
	}
	sort.Ints(positions)
	return positions
}
