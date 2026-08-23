package puzzle

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/db"
)

// topicsPerDay is how many topics appear on a board.
const topicsPerDay = 3

// Generator builds the daily board.
type Generator struct {
	DB               pgx.Tx
	CooldownDays     int
	TimeLimitSeconds int
}

// StarvedTopic records a topic that was skipped and the difficulty that
// starved it. It is the signal that the library needs content in a specific
// place.
type StarvedTopic struct {
	Slug       string
	Difficulty content.Difficulty
}

// InsufficientContentError means the library cannot fill a board without
// violating the question cooldown. The cooldown is never relaxed to avoid it.
type InsufficientContentError struct {
	Date         clock.Date
	Accepted     int
	CooldownDays int
	Starved      []StarvedTopic
}

func (e *InsufficientContentError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "cannot generate puzzle for %s: only %d of %d topics could fill a board without breaking the %d-day cooldown",
		e.Date, e.Accepted, topicsPerDay, e.CooldownDays)
	if len(e.Starved) > 0 {
		b.WriteString("; starved topics:")
		for _, s := range e.Starved {
			fmt.Fprintf(&b, " %s(%s)", s.Slug, s.Difficulty)
		}
	}
	return b.String()
}

// PinnedTopicStarvedError means a topic the operator pinned to a date cannot
// fill all three difficulties. An automatically chosen topic in this position
// is quietly skipped in favour of the next candidate; a pinned one is not,
// because the operator asked for it by name and silently substituting
// something else would be a lie.
type PinnedTopicStarvedError struct {
	Date         clock.Date
	Slug         string
	Position     int
	Difficulty   content.Difficulty
	CooldownDays int
}

func (e *PinnedTopicStarvedError) Error() string {
	return fmt.Sprintf("pinned topic %s has no eligible %s question for %s under the %d-day cooldown",
		e.Slug, e.Difficulty, e.Date, e.CooldownDays)
}

// GenerateFor builds and stores the puzzle for a date.
//
// It is idempotent: if a puzzle already exists for the date it is returned
// unchanged. It is deterministic: the same date, the same pins and the same
// library always produce the same board, because the shuffle and every pick
// come from an RNG seeded off the date.
//
// Topics an operator pinned to the date (see puzzle_topic_pins) claim their
// positions first; the rest are filled by weighted selection from whatever is
// left. Because one RNG feeds both the topic order and every question pick,
// adding or removing a pin shifts the stream and therefore changes which
// questions the unpinned slots get. That is surprising but harmless: a date is
// only ever rebuilt before anyone has played it.
//
// GenerateFor does not open a transaction of its own: it issues every read
// and write through g.DB exactly as given. g.DB is typed as pgx.Tx, so the
// caller always supplies a transaction; committing on success and rolling
// back on any error is what makes GenerateFor's idempotency guarantee hold.
func (g Generator) GenerateFor(ctx context.Context, date clock.Date) (*Puzzle, error) {
	existing, err := Get(ctx, g.DB, date)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	pins, err := pinsFor(ctx, g.DB, date)
	if err != nil {
		return nil, err
	}
	topics, err := content.ActiveTopics(ctx, g.DB)
	if err != nil {
		return nil, err
	}

	rng := rand.New(rand.NewPCG(seedFor(date), 0x9E3779B97F4A7C15))

	var (
		entries  []Entry
		starved  []StarvedTopic
		accepted int
	)

	// Pinned positions first, in position order, so the RNG is consumed in an
	// order that depends only on the pin set and not on map iteration.
	for _, position := range sortedPositions(pins) {
		topic := pins[position]
		picked, missing, err := g.fillTopic(ctx, topic, date, position, rng)
		if err != nil {
			return nil, err
		}
		if missing != nil {
			return nil, &PinnedTopicStarvedError{
				Date:         date,
				Slug:         topic.Slug,
				Position:     position,
				Difficulty:   *missing,
				CooldownDays: g.CooldownDays,
			}
		}
		entries = append(entries, picked...)
		accepted++
	}

	// Then the free positions, from whatever the operator did not pin.
	free := freePositions(pins)
	candidates := make([]content.Topic, 0, len(topics))
	for _, topic := range topics {
		if !pinnedTopic(pins, topic.ID) {
			candidates = append(candidates, topic)
		}
	}
	for _, topic := range weightedTopicOrder(candidates, rng) {
		if len(free) == 0 {
			break
		}
		picked, missing, err := g.fillTopic(ctx, topic, date, free[0], rng)
		if err != nil {
			return nil, err
		}
		if missing != nil {
			starved = append(starved, StarvedTopic{Slug: topic.Slug, Difficulty: *missing})
			continue
		}
		entries = append(entries, picked...)
		free = free[1:]
		accepted++
	}

	if accepted < topicsPerDay {
		return nil, &InsufficientContentError{
			Date:         date,
			Accepted:     accepted,
			CooldownDays: g.CooldownDays,
			Starved:      starved,
		}
	}

	p := &Puzzle{Date: date, TimeLimitSeconds: g.TimeLimitSeconds, Entries: entries}
	if err := Insert(ctx, g.DB, p); err != nil {
		return nil, err
	}
	return p, nil
}

// pinsFor loads the operator's chosen topics for a date, keyed by position.
//
// It deliberately does not filter on topics.active: the active flag governs
// automatic selection, and a pin is an explicit instruction for one specific
// date. Deactivating a topic stops it being drawn at random; it does not
// silently cancel a pin someone already made.
func pinsFor(ctx context.Context, q db.DBTX, date clock.Date) (map[int]content.Topic, error) {
	rows, err := q.Query(ctx, `
		SELECT p.topic_position, t.id, t.slug, t.name, t.active, t.selection_weight
		FROM puzzle_topic_pins p
		JOIN topics t ON t.id = p.topic_id
		WHERE p.puzzle_date = $1
		ORDER BY p.topic_position`, date)
	if err != nil {
		return nil, fmt.Errorf("query topic pins for %s: %w", date, err)
	}
	defer rows.Close()

	pins := make(map[int]content.Topic)
	for rows.Next() {
		var (
			position int
			t        content.Topic
		)
		if err := rows.Scan(&position, &t.ID, &t.Slug, &t.Name, &t.Active, &t.SelectionWeight); err != nil {
			return nil, fmt.Errorf("scan topic pin: %w", err)
		}
		pins[position] = t
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read topic pins for %s: %w", date, err)
	}
	return pins, nil
}

func sortedPositions(pins map[int]content.Topic) []int {
	positions := make([]int, 0, len(pins))
	for position := range pins {
		positions = append(positions, position)
	}
	sort.Ints(positions)
	return positions
}

func freePositions(pins map[int]content.Topic) []int {
	free := make([]int, 0, topicsPerDay)
	for position := 0; position < topicsPerDay; position++ {
		if _, taken := pins[position]; !taken {
			free = append(free, position)
		}
	}
	return free
}

func pinnedTopic(pins map[int]content.Topic, topicID int64) bool {
	for _, t := range pins {
		if t.ID == topicID {
			return true
		}
	}
	return false
}

// weightedTopicOrder performs deterministic weighted sampling without
// replacement. Each draw is proportional to the remaining topics' weights;
// removing the winner ensures a topic can appear only once on a board.
func weightedTopicOrder(topics []content.Topic, rng *rand.Rand) []content.Topic {
	remaining := append([]content.Topic(nil), topics...)
	ordered := make([]content.Topic, 0, len(topics))
	for len(remaining) > 0 {
		total := 0
		for _, topic := range remaining {
			total += topic.SelectionWeight
		}
		draw := rng.IntN(total)
		chosen := 0
		for i, topic := range remaining {
			draw -= topic.SelectionWeight
			if draw < 0 {
				chosen = i
				break
			}
		}
		ordered = append(ordered, remaining[chosen])
		remaining = append(remaining[:chosen], remaining[chosen+1:]...)
	}
	return ordered
}

// fillTopic picks one question at each difficulty for a topic. It returns a
// non-nil difficulty when the topic has nothing eligible at that tier, which
// tells the caller to skip the topic entirely.
func (g Generator) fillTopic(
	ctx context.Context,
	topic content.Topic,
	date clock.Date,
	position int,
	rng *rand.Rand,
) ([]Entry, *content.Difficulty, error) {
	entries := make([]Entry, 0, len(content.AllDifficulties))
	for _, difficulty := range content.AllDifficulties {
		candidates, err := content.EligibleQuestions(ctx, g.DB, topic.ID, difficulty, date, g.CooldownDays)
		if err != nil {
			return nil, nil, err
		}
		if len(candidates) == 0 {
			missing := difficulty
			return nil, &missing, nil
		}
		chosen := candidates[rng.IntN(len(candidates))]
		entries = append(entries, Entry{
			TopicID:       topic.ID,
			TopicSlug:     topic.Slug,
			TopicName:     topic.Name,
			TopicPosition: position,
			Difficulty:    difficulty,
			QuestionID:    chosen.ID,
			Prompt:        chosen.Prompt,
		})
	}
	return entries, nil, nil
}

// seedFor derives a stable RNG seed from a date, so regenerating a day
// reproduces it exactly.
func seedFor(date clock.Date) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(date.String()))
	return h.Sum64()
}
