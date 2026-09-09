package content_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/testsupport"
)

func target(t *testing.T) clock.Date {
	t.Helper()
	d, err := clock.ParseDate("2026-08-18")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	return d
}

// makeQuestion inserts an active question with one alias and five distractors,
// which is the minimum for eligibility.
func makeQuestion(t *testing.T, tx pgx.Tx, topicID int64, d content.Difficulty, externalID string) int64 {
	t.Helper()
	ctx := context.Background()

	id, err := content.UpsertQuestion(ctx, tx, content.QuestionInput{
		TopicID:         topicID,
		Difficulty:      d,
		Prompt:          "Prompt " + externalID,
		CanonicalAnswer: "Answer " + externalID,
		Status:          "active",
		Source:          "test",
		ExternalID:      externalID,
	})
	if err != nil {
		t.Fatalf("UpsertQuestion(%s): %v", externalID, err)
	}
	if err := content.ReplaceAliases(ctx, tx, id, []string{"Answer " + externalID}); err != nil {
		t.Fatalf("ReplaceAliases: %v", err)
	}
	if err := content.ReplaceDistractors(ctx, tx, id, []string{"w1", "w2", "w3", "w4", "w5"}); err != nil {
		t.Fatalf("ReplaceDistractors: %v", err)
	}
	return id
}

// useQuestion records the question as having appeared on the given date.
func useQuestion(t *testing.T, tx pgx.Tx, questionID, topicID int64, d content.Difficulty, on clock.Date) {
	t.Helper()
	ctx := context.Background()

	if _, err := tx.Exec(ctx,
		`INSERT INTO daily_puzzles (puzzle_date) VALUES ($1) ON CONFLICT DO NOTHING`, on); err != nil {
		t.Fatalf("insert puzzle for %s: %v", on, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO daily_puzzle_questions
		   (puzzle_date, topic_id, topic_position, difficulty, question_id)
		 VALUES ($1, $2, 0, $3::difficulty, $4)`, on, topicID, string(d), questionID); err != nil {
		t.Fatalf("insert puzzle question: %v", err)
	}
}

func TestUpsertTopicIsIdempotent(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	first, err := content.UpsertTopic(ctx, tx, "geography", "Geography")
	if err != nil {
		t.Fatalf("first UpsertTopic: %v", err)
	}
	second, err := content.UpsertTopic(ctx, tx, "geography", "World Geography")
	if err != nil {
		t.Fatalf("second UpsertTopic: %v", err)
	}
	if first != second {
		t.Errorf("UpsertTopic returned %d then %d, want the same id", first, second)
	}

	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM topics WHERE id = $1`, first).Scan(&name); err != nil {
		t.Fatalf("select topic: %v", err)
	}
	if name != "World Geography" {
		t.Errorf("topic name = %q, want the updated %q", name, "World Geography")
	}
}

func TestUpsertTopicWithWeightPersistsAndUpdatesWeight(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	id, err := content.UpsertTopicWithWeight(ctx, tx, "science", "Science", 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := content.UpsertTopicWithWeight(ctx, tx, "science", "Science & Nature", 7); err != nil {
		t.Fatal(err)
	}
	var weight int
	if err := tx.QueryRow(ctx, `SELECT selection_weight FROM topics WHERE id=$1`, id).Scan(&weight); err != nil {
		t.Fatal(err)
	}
	if weight != 7 {
		t.Fatalf("selection weight = %d, want 7", weight)
	}
	if _, err := content.UpsertTopicWithWeight(ctx, tx, "bad", "Bad", 0); err == nil {
		t.Fatal("zero weight accepted")
	}
}

func TestUpsertQuestionUpdatesByExternalID(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	topicID, err := content.UpsertTopic(ctx, tx, "geography", "Geography")
	if err != nil {
		t.Fatalf("UpsertTopic: %v", err)
	}

	in := content.QuestionInput{
		TopicID: topicID, Difficulty: content.Easy,
		Prompt: "Capital of France?", CanonicalAnswer: "Paris",
		Status: "active", Source: "seed", ExternalID: "geo-easy-1",
	}
	firstID, err := content.UpsertQuestion(ctx, tx, in)
	if err != nil {
		t.Fatalf("first UpsertQuestion: %v", err)
	}

	in.Prompt = "What is the capital of France?"
	secondID, err := content.UpsertQuestion(ctx, tx, in)
	if err != nil {
		t.Fatalf("second UpsertQuestion: %v", err)
	}
	if firstID != secondID {
		t.Errorf("UpsertQuestion returned %d then %d, want the same id", firstID, secondID)
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM questions`).Scan(&count); err != nil {
		t.Fatalf("count questions: %v", err)
	}
	if count != 1 {
		t.Errorf("question count = %d, want 1", count)
	}
}

// TestUpsertQuestionTreatsEmptyExternalIDAsDistinct pins that an empty
// ExternalID is stored as SQL NULL, not empty string, so it never
// participates in the (source, external_id) uniqueness the partial index
// enforces. Content with no natural key must insert fresh every time; only
// content that supplies a real external id gets upsert-by-key behavior.
func TestUpsertQuestionTreatsEmptyExternalIDAsDistinct(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	topicID, err := content.UpsertTopic(ctx, tx, "geography", "Geography")
	if err != nil {
		t.Fatalf("UpsertTopic: %v", err)
	}

	firstID, err := content.UpsertQuestion(ctx, tx, content.QuestionInput{
		TopicID: topicID, Difficulty: content.Easy,
		Prompt: "Capital of France?", CanonicalAnswer: "Paris",
		Status: "active", Source: "manual", ExternalID: "",
	})
	if err != nil {
		t.Fatalf("first UpsertQuestion: %v", err)
	}
	secondID, err := content.UpsertQuestion(ctx, tx, content.QuestionInput{
		TopicID: topicID, Difficulty: content.Easy,
		Prompt: "Capital of Japan?", CanonicalAnswer: "Tokyo",
		Status: "active", Source: "manual", ExternalID: "",
	})
	if err != nil {
		t.Fatalf("second UpsertQuestion: %v", err)
	}
	if firstID == secondID {
		t.Errorf("UpsertQuestion with empty ExternalID returned the same id %d twice, want distinct rows", firstID)
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM questions`).Scan(&count); err != nil {
		t.Fatalf("count questions: %v", err)
	}
	if count != 2 {
		t.Errorf("question count = %d, want 2", count)
	}

	// The existing non-empty ExternalID behavior — upsert by (source,
	// external_id) — must still hold alongside the above.
	in := content.QuestionInput{
		TopicID: topicID, Difficulty: content.Easy,
		Prompt: "Capital of Italy?", CanonicalAnswer: "Rome",
		Status: "active", Source: "manual", ExternalID: "geo-easy-2",
	}
	thirdID, err := content.UpsertQuestion(ctx, tx, in)
	if err != nil {
		t.Fatalf("third UpsertQuestion: %v", err)
	}
	in.Prompt = "What is the capital of Italy?"
	fourthID, err := content.UpsertQuestion(ctx, tx, in)
	if err != nil {
		t.Fatalf("fourth UpsertQuestion: %v", err)
	}
	if thirdID != fourthID {
		t.Errorf("UpsertQuestion with non-empty ExternalID returned %d then %d, want the same id", thirdID, fourthID)
	}

	if err := tx.QueryRow(ctx, `SELECT count(*) FROM questions`).Scan(&count); err != nil {
		t.Fatalf("count questions: %v", err)
	}
	if count != 3 {
		t.Errorf("question count = %d, want 3 (two distinct empty-external-id rows plus one upserted row)", count)
	}
}

func TestReplaceAliasesNormalizesAndDeduplicates(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	topicID, err := content.UpsertTopic(ctx, tx, "geography", "Geography")
	if err != nil {
		t.Fatalf("UpsertTopic: %v", err)
	}
	questionID := makeQuestion(t, tx, topicID, content.Easy, "geo-easy-1")

	// "The Paris" and "paris" normalize to the same string and must collapse.
	if err := content.ReplaceAliases(ctx, tx, questionID, []string{"The Paris", "paris", "Ville Lumière", "   "}); err != nil {
		t.Fatalf("ReplaceAliases: %v", err)
	}

	rows, err := tx.Query(ctx, `SELECT normalized FROM question_aliases WHERE question_id = $1 ORDER BY normalized`, questionID)
	if err != nil {
		t.Fatalf("select aliases: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan alias: %v", err)
		}
		got = append(got, n)
	}
	want := []string{"paris", "ville lumiere"}
	if len(got) != len(want) {
		t.Fatalf("aliases = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("alias[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestEligibleQuestionsFiltersIneligibleContent(t *testing.T) {
	pool := testsupport.MustPool(t)
	ctx := context.Background()
	when := target(t)

	t.Run("excludes draft questions", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id, err := content.UpsertQuestion(ctx, tx, content.QuestionInput{
			TopicID: topicID, Difficulty: content.Easy, Prompt: "p", CanonicalAnswer: "a",
			Status: "draft", Source: "test", ExternalID: "draft-1",
		})
		if err != nil {
			t.Fatalf("UpsertQuestion: %v", err)
		}
		_ = content.ReplaceAliases(ctx, tx, id, []string{"a"})
		_ = content.ReplaceDistractors(ctx, tx, id, []string{"1", "2", "3", "4", "5"})

		got, err := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if err != nil {
			t.Fatalf("EligibleQuestions: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d eligible questions, want 0", len(got))
		}
	})

	t.Run("excludes questions with fewer than three distractors", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id := makeQuestion(t, tx, topicID, content.Easy, "thin-1")
		if err := content.ReplaceDistractors(ctx, tx, id, []string{"1", "2"}); err != nil {
			t.Fatalf("ReplaceDistractors: %v", err)
		}

		got, _ := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if len(got) != 0 {
			t.Errorf("got %d eligible questions, want 0", len(got))
		}
	})

	t.Run("excludes questions with no aliases", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id := makeQuestion(t, tx, topicID, content.Easy, "noalias-1")
		if _, err := tx.Exec(ctx, `DELETE FROM question_aliases WHERE question_id = $1`, id); err != nil {
			t.Fatalf("delete aliases: %v", err)
		}

		got, _ := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if len(got) != 0 {
			t.Errorf("got %d eligible questions, want 0", len(got))
		}
	})

	t.Run("excludes a question used inside the cooldown", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id := makeQuestion(t, tx, topicID, content.Easy, "recent-1")
		useQuestion(t, tx, id, topicID, content.Easy, when.AddDays(-179))

		got, _ := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if len(got) != 0 {
			t.Errorf("got %d eligible questions, want 0", len(got))
		}
	})

	t.Run("includes a question used exactly at the cooldown boundary", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id := makeQuestion(t, tx, topicID, content.Easy, "boundary-1")
		useQuestion(t, tx, id, topicID, content.Easy, when.AddDays(-180))

		got, _ := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if len(got) != 1 {
			t.Fatalf("got %d eligible questions, want 1", len(got))
		}
		if got[0].ID != id {
			t.Errorf("eligible question id = %d, want %d", got[0].ID, id)
		}
	})

	t.Run("excludes a question already used on a future date", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id := makeQuestion(t, tx, topicID, content.Easy, "future-1")
		useQuestion(t, tx, id, topicID, content.Easy, when.AddDays(30))

		got, _ := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if len(got) != 0 {
			t.Errorf("got %d eligible questions, want 0", len(got))
		}
	})

	t.Run("includes a question used exactly at the future cooldown boundary", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id := makeQuestion(t, tx, topicID, content.Easy, "future-boundary-1")
		useQuestion(t, tx, id, topicID, content.Easy, when.AddDays(180))

		got, _ := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if len(got) != 1 {
			t.Fatalf("got %d eligible questions, want 1", len(got))
		}
		if got[0].ID != id {
			t.Errorf("eligible question id = %d, want %d", got[0].ID, id)
		}
	})

	t.Run("includes a question already used on the target date itself", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id := makeQuestion(t, tx, topicID, content.Easy, "same-day-1")
		useQuestion(t, tx, id, topicID, content.Easy, when)

		got, _ := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if len(got) != 1 {
			t.Errorf("got %d eligible questions, want 1 (regeneration must not block itself)", len(got))
		}
	})
}

// TestEligibleQuestionsOrdersByIDRegardlessOfHeapOrder pins the ORDER BY q.id
// in EligibleQuestions, which is what makes puzzle generation deterministic.
// `questions.id` is a bigserial — a plain column with a sequence-derived
// default — so it accepts an explicit value on insert. This test uses that to
// insert rows whose ids are deliberately out of physical (heap) insertion
// order: without ORDER BY, a sequential scan returns rows in the order they
// were physically written, which here disagrees with id order. That gap
// between "how it happens to come back today" and "what the id column says"
// is exactly what a heap reorder (e.g. a re-run of `seed apply`, which
// rewrites every existing question row) could expose in production.
func TestEligibleQuestionsOrdersByIDRegardlessOfHeapOrder(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	when := target(t)

	topicID, err := content.UpsertTopic(ctx, tx, "geography", "Geography")
	if err != nil {
		t.Fatalf("UpsertTopic: %v", err)
	}

	// Physical insertion order (1000, 500, 1500) deliberately disagrees with
	// id order (500, 1000, 1500).
	insertOrder := []int64{1000, 500, 1500}
	for _, id := range insertOrder {
		if _, err := tx.Exec(ctx, `
			INSERT INTO questions (id, topic_id, difficulty, difficulty_rating, prompt, canonical_answer, status, source, external_id)
			VALUES ($1, $2, 'easy'::difficulty, 2, $3, $4, 'active'::question_status, 'test', $5)`,
			id, topicID, fmt.Sprintf("Prompt %d", id), fmt.Sprintf("Answer %d", id), fmt.Sprintf("order-%d", id)); err != nil {
			t.Fatalf("insert question %d: %v", id, err)
		}
		if err := content.ReplaceAliases(ctx, tx, id, []string{fmt.Sprintf("Answer %d", id)}); err != nil {
			t.Fatalf("ReplaceAliases: %v", err)
		}
		if err := content.ReplaceDistractors(ctx, tx, id, []string{"w1", "w2", "w3", "w4", "w5"}); err != nil {
			t.Fatalf("ReplaceDistractors: %v", err)
		}
	}

	got, err := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
	if err != nil {
		t.Fatalf("EligibleQuestions: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d eligible questions, want 3", len(got))
	}
	want := []int64{500, 1000, 1500}
	for i, q := range got {
		if q.ID != want[i] {
			t.Errorf("entry %d has id %d, want %d (id-ascending order)", i, q.ID, want[i])
		}
	}
}

func TestAnswersUsedNearMatchesOnTheAnswerAlone(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	topicID, err := content.UpsertTopic(ctx, tx, "sports", "Sports")
	if err != nil {
		t.Fatalf("UpsertTopic: %v", err)
	}
	used := target(t)
	// The answer that lands on the board carries a diacritic; the question
	// the cooldown has to bar spells it plainly. AnswerKey folds both to the
	// same form, which is the whole point: the rule is about what a player
	// would type, not about the stored text.
	id := makeQuestion(t, tx, topicID, content.Hard, "jokic-hard")
	if _, err := tx.Exec(ctx,
		`UPDATE questions SET canonical_answer = $1 WHERE id = $2`, "Nikola Jokić", id); err != nil {
		t.Fatalf("set canonical answer: %v", err)
	}
	useQuestion(t, tx, id, topicID, content.Hard, used)

	inWindow, err := content.AnswersUsedNear(ctx, tx, used.AddDays(5), 14)
	if err != nil {
		t.Fatalf("AnswersUsedNear inside the window: %v", err)
	}
	if !inWindow[content.AnswerKey("Nikola Jokic")] {
		t.Errorf("AnswersUsedNear = %v, want it to contain the key for %q", inWindow, "Nikola Jokic")
	}

	outOfWindow, err := content.AnswersUsedNear(ctx, tx, used.AddDays(14), 14)
	if err != nil {
		t.Fatalf("AnswersUsedNear outside the window: %v", err)
	}
	if outOfWindow[content.AnswerKey("Nikola Jokic")] {
		t.Error("an answer used 14 days before a 14-day window is still reported as spent")
	}
}

func TestAnswersUsedNearIgnoresTheTargetDateItself(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	topicID, err := content.UpsertTopic(ctx, tx, "sports", "Sports")
	if err != nil {
		t.Fatalf("UpsertTopic: %v", err)
	}
	date := target(t)
	id := makeQuestion(t, tx, topicID, content.Easy, "easy-1")
	useQuestion(t, tx, id, topicID, content.Easy, date)

	// Regenerating a day must not be blocked by the board it is replacing,
	// matching EligibleQuestions.
	used, err := content.AnswersUsedNear(ctx, tx, date, 14)
	if err != nil {
		t.Fatalf("AnswersUsedNear: %v", err)
	}
	if len(used) != 0 {
		t.Errorf("AnswersUsedNear = %v, want empty for the target date's own board", used)
	}
}

func TestAnswersUsedNearDisabledByANonPositiveWindow(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	topicID, err := content.UpsertTopic(ctx, tx, "sports", "Sports")
	if err != nil {
		t.Fatalf("UpsertTopic: %v", err)
	}
	date := target(t)
	id := makeQuestion(t, tx, topicID, content.Easy, "easy-1")
	useQuestion(t, tx, id, topicID, content.Easy, date.AddDays(-1))

	// A nil set is how "answer cooldown off" is represented, so callers can
	// switch on it. Reading it must still be safe.
	used, err := content.AnswersUsedNear(ctx, tx, date, 0)
	if err != nil {
		t.Fatalf("AnswersUsedNear: %v", err)
	}
	if used != nil {
		t.Errorf("AnswersUsedNear = %v, want nil for a non-positive window", used)
	}
	if used[content.AnswerKey("Answer easy-1")] {
		t.Error("nil set reported an answer as spent")
	}
}
