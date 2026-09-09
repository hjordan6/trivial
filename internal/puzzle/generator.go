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
	DB pgx.Tx
	// CooldownDays is the window in which one question may not repeat.
	CooldownDays int
	// AnswerCooldownDays is the window in which one *answer* may not repeat,
	// no matter how many distinct questions ask for it. Two questions with
	// the same answer are barred from landing within this many days of each
	// other even when their prompts, topics, and difficulties all differ.
	// Non-positive disables the rule.
	AnswerCooldownDays int
	TimeLimitSeconds   int
}

// StarvedReason says which constraint left a topic unable to fill a board.
// The two are indistinguishable from the outside -- a topic is skipped either
// way -- but they call for opposite fixes: more questions versus more variety
// in the answers the existing questions have.
type StarvedReason string

const (
	// StarvedNoEligibleQuestion means the topic had no active, well-formed
	// question at that difficulty outside the question cooldown.
	StarvedNoEligibleQuestion StarvedReason = "no eligible question"
	// StarvedAnswerRepeat means the topic had eligible questions at that
	// difficulty, but every one of them repeats an answer already spent
	// inside the answer cooldown.
	StarvedAnswerRepeat StarvedReason = "answer already used in the answer cooldown"
)

// StarvedTopic records a topic that was skipped and the difficulty that
// starved it. It is the signal that the library needs content in a specific
// place.
type StarvedTopic struct {
	Slug       string
	Difficulty content.Difficulty
	Reason     StarvedReason
}

// InsufficientContentError means the library cannot fill a board without
// violating the question cooldown. The cooldown is never relaxed to avoid it.
type InsufficientContentError struct {
	Date               clock.Date
	Accepted           int
	CooldownDays       int
	AnswerCooldownDays int
	Starved            []StarvedTopic
}

func (e *InsufficientContentError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "cannot generate puzzle for %s: only %d of %d topics could fill a board without breaking the %d-day question cooldown or the %d-day answer cooldown",
		e.Date, e.Accepted, topicsPerDay, e.CooldownDays, e.AnswerCooldownDays)
	if len(e.Starved) > 0 {
		b.WriteString("; starved topics:")
		for _, s := range e.Starved {
			fmt.Fprintf(&b, " %s(%s: %s)", s.Slug, s.Difficulty, s.Reason)
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
	// Reason distinguishes a pin the library cannot serve at all from one
	// whose questions are fine but whose answers are spoken for nearby. The
	// second is fixable by moving the pin a few days rather than by writing
	// new questions, so the operator needs to be told which it is.
	Reason StarvedReason
}

func (e *PinnedTopicStarvedError) Error() string {
	return fmt.Sprintf("pinned topic %s cannot fill %s for %s: %s",
		e.Slug, e.Difficulty, e.Date, e.Reason)
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

	// spentAnswers starts as the answers already used inside the answer
	// cooldown and grows as the board fills, so the rule holds across days
	// and within the board being built. Only accepted topics contribute: a
	// topic that starves halfway through must not leave its partial picks
	// behind to block a later topic.
	//
	// A nil set is the one representation of "answer cooldown off", so the
	// switch lives in exactly one place: AnswersUsedNear returns nil for a
	// non-positive window, and every site below is a nil check rather than a
	// second reading of AnswerCooldownDays.
	//
	// Pinned positions are filled first, so a pin always gets first claim on
	// the answers it needs and can only ever be starved by another pin or by
	// a nearby day -- never by a topic the generator chose itself.
	spentAnswers, err := content.AnswersUsedNear(ctx, g.DB, date, g.AnswerCooldownDays)
	if err != nil {
		return nil, err
	}

	var (
		entries  []Entry
		starved  []StarvedTopic
		accepted int
	)

	// Pinned positions first, in position order, so the RNG is consumed in an
	// order that depends only on the pin set and not on map iteration.
	for _, position := range sortedPositions(pins) {
		topic := pins[position]
		picked, missing, err := g.fillTopic(ctx, topic, date, position, rng, spentAnswers)
		if err != nil {
			return nil, err
		}
		if missing != nil {
			return nil, &PinnedTopicStarvedError{
				Date:         date,
				Slug:         topic.Slug,
				Position:     position,
				Difficulty:   missing.Difficulty,
				CooldownDays: g.CooldownDays,
				Reason:       missing.Reason,
			}
		}
		entries = append(entries, picked.entries...)
		spend(spentAnswers, picked)
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
		picked, missing, err := g.fillTopic(ctx, topic, date, free[0], rng, spentAnswers)
		if err != nil {
			return nil, err
		}
		if missing != nil {
			starved = append(starved, *missing)
			continue
		}
		entries = append(entries, picked.entries...)
		spend(spentAnswers, picked)
		free = free[1:]
		accepted++
	}

	if accepted < topicsPerDay {
		return nil, &InsufficientContentError{
			Date:               date,
			Accepted:           accepted,
			CooldownDays:       g.CooldownDays,
			AnswerCooldownDays: g.AnswerCooldownDays,
			Starved:            starved,
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

// topicPick is one topic's three questions together with the answers they
// consume. The answers are kept separate from the entries because Entry is
// serialized to players, and an answer key on it would leak the answer.
type topicPick struct {
	entries []Entry
	answers map[string]bool
}

// spend folds an accepted topic's answers into the board-wide set. It is a
// no-op when the answer cooldown is off, which is the nil set.
func spend(spentAnswers map[string]bool, picked *topicPick) {
	if spentAnswers == nil {
		return
	}
	for answer := range picked.answers {
		spentAnswers[answer] = true
	}
}

// fillTopic picks one question at each difficulty for a topic. It returns a
// non-nil StarvedTopic when the topic has nothing usable at that tier, which
// tells the caller to skip the topic entirely -- or, for a pinned position,
// to fail.
//
// spentAnswers is the set of answer keys already spent, and is read-only
// here: fillTopic records its own picks in the returned topicPick so a
// half-filled topic that ends up skipped costs nothing.
func (g Generator) fillTopic(
	ctx context.Context,
	topic content.Topic,
	date clock.Date,
	position int,
	rng *rand.Rand,
	spentAnswers map[string]bool,
) (*topicPick, *StarvedTopic, error) {
	pick := &topicPick{
		entries: make([]Entry, 0, len(content.AllDifficulties)),
		answers: make(map[string]bool, len(content.AllDifficulties)),
	}
	for _, difficulty := range content.AllDifficulties {
		candidates, err := content.EligibleQuestions(ctx, g.DB, topic.ID, difficulty, date, g.CooldownDays)
		if err != nil {
			return nil, nil, err
		}
		if len(candidates) == 0 {
			return nil, &StarvedTopic{
				Slug: topic.Slug, Difficulty: difficulty, Reason: StarvedNoEligibleQuestion,
			}, nil
		}
		if spentAnswers != nil {
			candidates = withFreshAnswers(candidates, spentAnswers, pick.answers)
			if len(candidates) == 0 {
				return nil, &StarvedTopic{
					Slug: topic.Slug, Difficulty: difficulty, Reason: StarvedAnswerRepeat,
				}, nil
			}
		}
		chosen := candidates[rng.IntN(len(candidates))]
		pick.answers[content.AnswerKey(chosen.CanonicalAnswer)] = true
		pick.entries = append(pick.entries, Entry{
			TopicID:       topic.ID,
			TopicSlug:     topic.Slug,
			TopicName:     topic.Name,
			TopicPosition: position,
			Difficulty:    difficulty,
			QuestionID:    chosen.ID,
			Prompt:        chosen.Prompt,
		})
	}
	return pick, nil, nil
}

// withFreshAnswers drops the candidates whose answer is already spent, either
// on a nearby day or earlier in the board being built. Order is preserved so
// the caller's pick from the filtered list stays deterministic.
func withFreshAnswers(candidates []content.Question, spent, takenSoFar map[string]bool) []content.Question {
	fresh := make([]content.Question, 0, len(candidates))
	for _, c := range candidates {
		key := content.AnswerKey(c.CanonicalAnswer)
		if spent[key] || takenSoFar[key] {
			continue
		}
		fresh = append(fresh, c)
	}
	return fresh
}

// seedFor derives a stable RNG seed from a date, so regenerating a day
// reproduces it exactly.
func seedFor(date clock.Date) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(date.String()))
	return h.Sum64()
}
