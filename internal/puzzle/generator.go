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

// hardFloorRating is the gentlest rating inside the hard band (8-10). Every
// board is guaranteed one hard question at exactly this rating, so the hardest
// row always has a way in. The other two hard slots are unconstrained: they may
// be 8 as well, or 9, or 10.
const hardFloorRating = 8

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

// GentleHardQuestionUnavailableError means the board's three topics were all
// filled, but none of them could supply a hard question at hardFloorRating.
// Like the cooldown, the rule is not relaxed to get a board out: a day that
// cannot satisfy it is left ungenerated rather than published with a hard row
// that has no way in.
type GentleHardQuestionUnavailableError struct {
	Date         clock.Date
	CooldownDays int
	Topics       []string
	Rating       int
}

func (e *GentleHardQuestionUnavailableError) Error() string {
	return fmt.Sprintf(
		"cannot generate puzzle for %s: no topic on the board (%s) has an eligible hard question rated %d under the %d-day cooldown",
		e.Date, strings.Join(e.Topics, ", "), e.Rating, e.CooldownDays)
}

// topicFill is one topic's contribution to a board, carrying what the hard-slot
// rule needs to repair the board later without re-querying: the rating that was
// actually picked for the hard slot, and the eligible hard questions that sit
// at the floor rating.
type topicFill struct {
	entries    []Entry
	hardRating int
	hardFloors []content.Question
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
		fills   []topicFill
		onBoard []string
		starved []StarvedTopic
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
		fills = append(fills, picked)
		onBoard = append(onBoard, topic.Slug)
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
		fills = append(fills, picked)
		onBoard = append(onBoard, topic.Slug)
		free = free[1:]
	}

	if len(fills) < topicsPerDay {
		return nil, &InsufficientContentError{
			Date:         date,
			Accepted:     len(fills),
			CooldownDays: g.CooldownDays,
			Starved:      starved,
		}
	}

	// The hard-floor rule is applied once the board's three topics are known,
	// because it is a property of the board rather than of any one topic.
	if !ensureGentleHardQuestion(fills, rng) {
		return nil, &GentleHardQuestionUnavailableError{
			Date:         date,
			CooldownDays: g.CooldownDays,
			Topics:       onBoard,
			Rating:       hardFloorRating,
		}
	}

	entries := make([]Entry, 0, topicsPerDay*len(content.AllDifficulties))
	for _, fill := range fills {
		entries = append(entries, fill.entries...)
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
//
// It also records what the hard-floor rule needs: the rating the hard slot
// actually drew, and every eligible hard question at the floor rating. Both are
// collected here so the repair never issues a second round of queries.
func (g Generator) fillTopic(
	ctx context.Context,
	topic content.Topic,
	date clock.Date,
	position int,
	rng *rand.Rand,
) (topicFill, *content.Difficulty, error) {
	fill := topicFill{entries: make([]Entry, 0, len(content.AllDifficulties))}
	for _, difficulty := range content.AllDifficulties {
		candidates, err := content.EligibleQuestions(ctx, g.DB, topic.ID, difficulty, date, g.CooldownDays)
		if err != nil {
			return topicFill{}, nil, err
		}
		if len(candidates) == 0 {
			missing := difficulty
			return topicFill{}, &missing, nil
		}
		chosen := candidates[rng.IntN(len(candidates))]
		if difficulty == content.Hard {
			fill.hardRating = chosen.DifficultyRating
			for _, candidate := range candidates {
				if candidate.DifficultyRating == hardFloorRating {
					fill.hardFloors = append(fill.hardFloors, candidate)
				}
			}
		}
		fill.entries = append(fill.entries, Entry{
			TopicID:       topic.ID,
			TopicSlug:     topic.Slug,
			TopicName:     topic.Name,
			TopicPosition: position,
			Difficulty:    difficulty,
			QuestionID:    chosen.ID,
			Prompt:        chosen.Prompt,
		})
	}
	return fill, nil, nil
}

// ensureGentleHardQuestion guarantees that at least one of the board's three
// hard questions is rated at the floor of the hard band, reporting whether the
// board can satisfy it at all.
//
// A board that already drew a floor-rated hard question is left exactly as it
// was, so the rule costs nothing in the common case and does not perturb the
// RNG stream. Otherwise one topic is chosen from those that can supply one and
// its hard slot is swapped. The choice is drawn from the same seeded RNG, so a
// date still regenerates to the same board.
func ensureGentleHardQuestion(fills []topicFill, rng *rand.Rand) bool {
	for _, fill := range fills {
		if fill.hardRating == hardFloorRating {
			return true
		}
	}

	capable := make([]int, 0, len(fills))
	for i, fill := range fills {
		if len(fill.hardFloors) > 0 {
			capable = append(capable, i)
		}
	}
	if len(capable) == 0 {
		return false
	}

	target := capable[rng.IntN(len(capable))]
	replacement := fills[target].hardFloors[rng.IntN(len(fills[target].hardFloors))]
	for i := range fills[target].entries {
		if fills[target].entries[i].Difficulty == content.Hard {
			fills[target].entries[i].QuestionID = replacement.ID
			fills[target].entries[i].Prompt = replacement.Prompt
			break
		}
	}
	fills[target].hardRating = replacement.DifficultyRating
	return true
}

// seedFor derives a stable RNG seed from a date, so regenerating a day
// reproduces it exactly.
func seedFor(date clock.Date) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(date.String()))
	return h.Sum64()
}
