package puzzle

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
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

// GenerateFor builds and stores the puzzle for a date.
//
// It is idempotent: if a puzzle already exists for the date it is returned
// unchanged. It is deterministic: the same date and the same library always
// produce the same board, because the shuffle and every pick come from an RNG
// seeded off the date.
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

	topics, err := content.ActiveTopics(ctx, g.DB)
	if err != nil {
		return nil, err
	}

	rng := rand.New(rand.NewPCG(seedFor(date), 0x9E3779B97F4A7C15))
	rng.Shuffle(len(topics), func(i, j int) { topics[i], topics[j] = topics[j], topics[i] })

	var (
		entries  []Entry
		starved  []StarvedTopic
		accepted int
	)
	for _, topic := range topics {
		if accepted == topicsPerDay {
			break
		}
		picked, missing, err := g.fillTopic(ctx, topic, date, accepted, rng)
		if err != nil {
			return nil, err
		}
		if missing != nil {
			starved = append(starved, StarvedTopic{Slug: topic.Slug, Difficulty: *missing})
			continue
		}
		entries = append(entries, picked...)
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
