package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/testsupport"
)

// The response is decoded into the wire shape rather than into
// adminBoardEntry, so these tests pin the JSON the panel actually receives.
// (clock.Date marshals but does not unmarshal, so the Go struct could not be
// decoded here anyway -- nothing in the application parses a date out of JSON.)
type boardEntryJSON struct {
	Slot             string   `json:"slot"`
	TopicPosition    int      `json:"topic_position"`
	TopicSlug        string   `json:"topic_slug"`
	Difficulty       string   `json:"difficulty"`
	DifficultyRating int      `json:"difficulty_rating"`
	Prompt           string   `json:"prompt"`
	Answer           string   `json:"answer"`
	Aliases          []string `json:"aliases"`
	Distractors      []string `json:"distractors"`
	UsedCount        int      `json:"used_count"`
	LastUsed         *string  `json:"last_used"`
}

type boardResponse struct {
	Date      string           `json:"date"`
	Generated bool             `json:"generated"`
	HasRuns   bool             `json:"has_runs"`
	Entries   []boardEntryJSON `json:"entries"`
}

func getBoard(t *testing.T, s *Server, handler http.Handler, date string) boardResponse {
	t.Helper()
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodGet, "/api/admin/puzzles/"+date+"/questions", ""))
	if res.Code != http.StatusOK {
		t.Fatalf("board %s = %d, want 200 (%s)", date, res.Code, res.Body.String())
	}
	var out boardResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode board: %v", err)
	}
	return out
}

// A date nobody has generated is an answer, not a failure: the panel lets an
// operator type any date.
func TestAdminBoardReportsAnEmptyDayWithoutFailing(t *testing.T) {
	pool := testsupport.MustPool(t)
	s := &Server{Pool: pool, AdminPassword: "correct horse",
		Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}, Timezone: time.UTC}

	got := getBoard(t, s, s.Handler(), "2031-12-25")
	if got.Generated {
		t.Error("Generated = true for a date with no board")
	}
	if len(got.Entries) != 0 {
		t.Errorf("got %d entries, want none", len(got.Entries))
	}
}

func TestAdminBoardRejectsAnUnparseableDate(t *testing.T) {
	s := &Server{AdminPassword: "correct horse", Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, signedRequest(s, http.MethodGet, "/api/admin/puzzles/last-tuesday/questions", ""))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.Code)
	}
}

func TestAdminBoardReturnsTheDayInBoardOrder(t *testing.T) {
	pool := testsupport.MustPool(t)
	ctx := context.Background()
	s := &Server{Pool: pool, AdminPassword: "correct horse",
		Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}, Timezone: time.UTC}
	handler := s.Handler()

	const slug = "httpapi-board-fixture"
	const date = "2031-11-11"
	var topicID int64
	// Inactive so this fixture never enters another package's topic selection;
	// see hideTopic.
	if err := pool.QueryRow(ctx,
		`INSERT INTO topics (slug, name, selection_weight, active) VALUES ($1, 'Board Fixture', 1, false) RETURNING id`,
		slug).Scan(&topicID); err != nil {
		t.Fatalf("create topic: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM daily_puzzles WHERE puzzle_date = $1`, date)
		_, _ = pool.Exec(ctx, `DELETE FROM questions WHERE topic_id = $1`, topicID)
		_, _ = pool.Exec(ctx, `DELETE FROM topics WHERE id = $1`, topicID)
	})

	payload := `{"topics":[{"slug":"` + slug + `","name":"Board Fixture","questions":[
		{"external_id":"board-easy","difficulty":2,"prompt":"Easy prompt","answer":"EasyAnswer","distractors":["a","b","c"]},
		{"external_id":"board-medium","difficulty":6,"prompt":"Medium prompt","answer":"MediumAnswer","distractors":["d","e","f"]},
		{"external_id":"board-hard","difficulty":8,"prompt":"Hard prompt","answer":"HardAnswer","distractors":["g","h","i"]}]}]}`
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodPost, "/api/admin/questions/import", payload))
	if res.Code != http.StatusOK {
		t.Fatalf("seed the fixture: %d (%s)", res.Code, res.Body.String())
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO daily_puzzles (puzzle_date, time_limit_seconds) VALUES ($1, 135)`, date); err != nil {
		t.Fatalf("create puzzle: %v", err)
	}
	// Inserted hard-first so the response's ordering is the query's doing
	// rather than the insert order's.
	for _, band := range []string{"hard", "medium", "easy"} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO daily_puzzle_questions (puzzle_date, topic_id, topic_position, difficulty, question_id)
			SELECT $1, $2, 0, $3::difficulty, q.id
			FROM questions q WHERE q.topic_id = $2 AND q.difficulty = $3::difficulty`,
			date, topicID, band); err != nil {
			t.Fatalf("schedule %s: %v", band, err)
		}
	}

	got := getBoard(t, s, handler, date)
	if !got.Generated {
		t.Fatal("Generated = false for a date with a board")
	}
	if len(got.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(got.Entries))
	}

	wantOrder := []string{"easy", "medium", "hard"}
	for i, want := range wantOrder {
		if got.Entries[i].Slot != want {
			t.Errorf("entry %d slot = %q, want %q", i, got.Entries[i].Slot, want)
		}
	}

	// The board carries the whole question, answers and options included, which
	// is the point of the view.
	hard := got.Entries[2]
	if hard.Prompt != "Hard prompt" || hard.Answer != "HardAnswer" {
		t.Errorf("hard entry = %q / %q", hard.Prompt, hard.Answer)
	}
	if len(hard.Distractors) != 3 {
		t.Errorf("hard distractors = %v, want 3", hard.Distractors)
	}
	if hard.DifficultyRating != 8 {
		t.Errorf("hard rating = %d, want 8", hard.DifficultyRating)
	}
	if hard.TopicSlug != slug {
		t.Errorf("topic = %q, want %q", hard.TopicSlug, slug)
	}
	// Scheduling is what use counts, so a board's own questions report it.
	if hard.UsedCount != 1 {
		t.Errorf("used_count = %d, want the scheduled board counted", hard.UsedCount)
	}
}
