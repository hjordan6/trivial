package puzzle_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/puzzle"
	"github.com/hjordan6/trivial/internal/testsupport"
)

func mustDate(t *testing.T, s string) clock.Date {
	t.Helper()
	d, err := clock.ParseDate(s)
	if err != nil {
		t.Fatalf("ParseDate(%s): %v", s, err)
	}
	return d
}

// hardRatingFor spreads the hard band across fixtures instead of parking every
// hard question at the representative 9. Real libraries hold a mix, and the
// generator now requires one hard question at the floor rating, so a fixture
// that only ever produced 9s would describe a library no board could be built
// from. The first hard question of a topic is always the floor rating, so even
// a one-question-per-difficulty fixture can fill a board.
func hardRatingFor(d content.Difficulty, n int) int {
	switch d {
	case content.Easy:
		return 2
	case content.Medium:
		return 6
	default:
		if n%2 == 1 {
			return 8
		}
		return 9
	}
}

// seedTopic creates a topic with three eligible questions per difficulty.
func seedTopic(t *testing.T, tx pgx.Tx, slug string) content.Topic {
	t.Helper()
	ctx := context.Background()

	id, err := content.UpsertTopic(ctx, tx, slug, slug)
	if err != nil {
		t.Fatalf("UpsertTopic(%s): %v", slug, err)
	}
	for _, d := range content.AllDifficulties {
		for n := 1; n <= 3; n++ {
			externalID := fmt.Sprintf("%s-%s-%d", slug, d, n)
			qid, err := content.UpsertQuestion(ctx, tx, content.QuestionInput{
				TopicID: id, Difficulty: d, DifficultyRating: hardRatingFor(d, n),
				Prompt: "Prompt " + externalID, CanonicalAnswer: "Answer " + externalID,
				Status: "active", Source: "test", ExternalID: externalID,
			})
			if err != nil {
				t.Fatalf("UpsertQuestion(%s): %v", externalID, err)
			}
			if err := content.ReplaceAliases(ctx, tx, qid, []string{"Answer " + externalID}); err != nil {
				t.Fatalf("ReplaceAliases: %v", err)
			}
			if err := content.ReplaceDistractors(ctx, tx, qid, []string{"w1", "w2", "w3", "w4", "w5"}); err != nil {
				t.Fatalf("ReplaceDistractors: %v", err)
			}
		}
	}
	return content.Topic{ID: id, Slug: slug, Name: slug, Active: true}
}

func TestGetReturnsNilForMissingPuzzle(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))

	got, err := puzzle.Get(context.Background(), tx, mustDate(t, "2030-01-01"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != nil {
		t.Errorf("Get() = %+v, want nil for a date with no puzzle", got)
	}
}

func TestInsertAndGetPreserveBoardOrder(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-08-18")

	topics := []content.Topic{
		seedTopic(t, tx, "alpha"),
		seedTopic(t, tx, "beta"),
		seedTopic(t, tx, "gamma"),
	}

	var entries []puzzle.Entry
	for position, topic := range topics {
		// Insert difficulties out of order to prove the read path sorts them.
		for _, d := range []content.Difficulty{content.Hard, content.Easy, content.Medium} {
			candidates, err := content.EligibleQuestions(ctx, tx, topic.ID, d, date, 180)
			if err != nil {
				t.Fatalf("EligibleQuestions: %v", err)
			}
			if len(candidates) == 0 {
				t.Fatalf("no eligible %s questions for topic %s", d, topic.Slug)
			}
			entries = append(entries, puzzle.Entry{
				TopicID: topic.ID, TopicSlug: topic.Slug, TopicName: topic.Name,
				TopicPosition: position, Difficulty: d,
				QuestionID: candidates[0].ID, Prompt: candidates[0].Prompt,
			})
		}
	}

	want := &puzzle.Puzzle{Date: date, TimeLimitSeconds: 135, Entries: entries}
	if err := puzzle.Insert(ctx, tx, want); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}

	got, err := puzzle.Get(ctx, tx, date)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got == nil {
		t.Fatal("Get() = nil, want the inserted puzzle")
	}
	if len(got.Entries) != 9 {
		t.Fatalf("got %d entries, want 9", len(got.Entries))
	}
	if got.TimeLimitSeconds != 135 {
		t.Errorf("TimeLimitSeconds = %d, want 135", got.TimeLimitSeconds)
	}

	wantOrder := []struct {
		position   int
		difficulty content.Difficulty
	}{
		{0, content.Easy}, {0, content.Medium}, {0, content.Hard},
		{1, content.Easy}, {1, content.Medium}, {1, content.Hard},
		{2, content.Easy}, {2, content.Medium}, {2, content.Hard},
	}
	for i, w := range wantOrder {
		e := got.Entries[i]
		if e.TopicPosition != w.position || e.Difficulty != w.difficulty {
			t.Errorf("entry %d = (position %d, %s), want (position %d, %s)",
				i, e.TopicPosition, e.Difficulty, w.position, w.difficulty)
		}
		if e.TopicSlug == "" || e.Prompt == "" {
			t.Errorf("entry %d has empty topic slug or prompt: %+v", i, e)
		}
	}
}

// TestGetErrorsOnPartiallyWrittenBoard pins that a board with more than zero
// but fewer than nine entries is reported as an error rather than read back
// as a complete puzzle. Without this, a partial write that slipped past the
// generator's transaction (or any future writer) would look identical to a
// finished nine-question board to every caller, including a future HTTP
// handler.
func TestGetErrorsOnPartiallyWrittenBoard(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-08-18")

	topic := seedTopic(t, tx, "alpha")

	var entries []puzzle.Entry
	for _, d := range []content.Difficulty{content.Easy, content.Medium} {
		candidates, err := content.EligibleQuestions(ctx, tx, topic.ID, d, date, 180)
		if err != nil {
			t.Fatalf("EligibleQuestions: %v", err)
		}
		if len(candidates) == 0 {
			t.Fatalf("no eligible %s questions for topic %s", d, topic.Slug)
		}
		entries = append(entries, puzzle.Entry{
			TopicID: topic.ID, TopicSlug: topic.Slug, TopicName: topic.Name,
			TopicPosition: 0, Difficulty: d,
			QuestionID: candidates[0].ID, Prompt: candidates[0].Prompt,
		})
	}

	// Deliberately write a two-entry board — never zero, never nine.
	short := &puzzle.Puzzle{Date: date, TimeLimitSeconds: 135, Entries: entries}
	if err := puzzle.Insert(ctx, tx, short); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}

	_, err := puzzle.Get(ctx, tx, date)
	if err == nil {
		t.Fatal("Get() error = nil, want an error for a partially written board")
	}
}
