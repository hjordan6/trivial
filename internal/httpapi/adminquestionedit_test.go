package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/testsupport"
)

func editServer(t *testing.T) (*Server, http.Handler, *pgxpool.Pool) {
	t.Helper()
	pool := testsupport.MustPool(t)
	s := &Server{Pool: pool, AdminPassword: "correct horse",
		Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}, Timezone: time.UTC}
	return s, s.Handler(), pool
}

// makeTopic creates a topic that the test removes on the way out, along with
// anything written into it.
func makeTopic(t *testing.T, pool *pgxpool.Pool, slug string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO topics (slug, name, selection_weight) VALUES ($1, $2, 1) RETURNING id`,
		slug, slug).Scan(&id); err != nil {
		t.Fatalf("create topic %s: %v", slug, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM daily_puzzle_questions WHERE topic_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM questions WHERE topic_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM topics WHERE id = $1`, id)
	})
	return id
}

func createQuestion(t *testing.T, s *Server, handler http.Handler, body string) adminQuestion {
	t.Helper()
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodPost, "/api/admin/questions", body))
	if res.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201 (%s)", res.Code, res.Body.String())
	}
	var q adminQuestion
	if err := json.Unmarshal(res.Body.Bytes(), &q); err != nil {
		t.Fatalf("decode created question: %v", err)
	}
	return q
}

func TestAdminCreateQuestionWritesEverySide(t *testing.T) {
	s, handler, pool := editServer(t)
	const slug = "httpapi-handwritten"
	makeTopic(t, pool, slug)

	q := createQuestion(t, s, handler, `{
		"topic_slug":"`+slug+`","difficulty_rating":6,
		"prompt":"  Which ocean is the largest?  ","answer":" Pacific ",
		"aliases":["Pacific Ocean","  "],"distractors":["Atlantic","Indian","Arctic",""]}`)

	if q.ID == 0 {
		t.Fatal("no id came back")
	}
	// Whitespace is trimmed and blank lines dropped, because the panel
	// collects these as one-per-line text.
	if q.Prompt != "Which ocean is the largest?" || q.Answer != "Pacific" {
		t.Errorf("prompt/answer = %q / %q, want them trimmed", q.Prompt, q.Answer)
	}
	if len(q.Distractors) != 3 {
		t.Errorf("distractors = %v, want the blank dropped", q.Distractors)
	}
	// The canonical answer is prepended as an alias by validation, so both
	// spellings grade as correct.
	if len(q.Aliases) != 2 || q.Aliases[0] != "Pacific" {
		t.Errorf("aliases = %v, want the answer first then the alias", q.Aliases)
	}
	if q.Difficulty != "medium" || q.DifficultyRating != 6 {
		t.Errorf("difficulty = %s/%d, want medium/6", q.Difficulty, q.DifficultyRating)
	}
	if q.Status != "active" {
		t.Errorf("status = %q, want active by default", q.Status)
	}
	if q.TopicSlug != slug {
		t.Errorf("topic = %q, want %q", q.TopicSlug, slug)
	}
}

func TestAdminCreateQuestionRejectsUnusableContent(t *testing.T) {
	s, handler, pool := editServer(t)
	const slug = "httpapi-handwritten-bad"
	makeTopic(t, pool, slug)

	cases := []struct {
		name string
		body string
		want string
	}{
		{"no topic", `{"difficulty_rating":5,"prompt":"P","answer":"A","distractors":["x","y","z"]}`, "invalid_request"},
		{"unknown topic", `{"topic_slug":"nope","difficulty_rating":5,"prompt":"P","answer":"A","distractors":["x","y","z"]}`, "unknown_topic"},
		{"rating out of range", `{"topic_slug":"` + slug + `","difficulty_rating":11,"prompt":"P","answer":"A","distractors":["x","y","z"]}`, "invalid_question"},
		{"no prompt", `{"topic_slug":"` + slug + `","difficulty_rating":5,"answer":"A","distractors":["x","y","z"]}`, "invalid_question"},
		{"two distractors", `{"topic_slug":"` + slug + `","difficulty_rating":5,"prompt":"P","answer":"A","distractors":["x","y"]}`, "invalid_question"},
		// Duplicates collapse on write, so three entries that are really two
		// would leave the question short of options.
		{"duplicate distractors", `{"topic_slug":"` + slug + `","difficulty_rating":5,"prompt":"P","answer":"A","distractors":["x","x","y"]}`, "invalid_question"},
		{"distractor is the answer", `{"topic_slug":"` + slug + `","difficulty_rating":5,"prompt":"P","answer":"A","distractors":["A","y","z"]}`, "invalid_question"},
		{"distractor is an alias", `{"topic_slug":"` + slug + `","difficulty_rating":5,"prompt":"P","answer":"A","aliases":["Ay"],"distractors":["Ay","y","z"]}`, "invalid_question"},
		{"bad status", `{"topic_slug":"` + slug + `","difficulty_rating":5,"prompt":"P","answer":"A","distractors":["x","y","z"],"status":"lively"}`, "invalid_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, signedRequest(s, http.MethodPost, "/api/admin/questions", tc.body))
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", res.Code, res.Body.String())
			}
			var apiErr apiError
			_ = json.Unmarshal(res.Body.Bytes(), &apiErr)
			if apiErr.Code != tc.want {
				t.Fatalf("code = %q, want %q (%s)", apiErr.Code, tc.want, apiErr.Message)
			}
		})
	}
}

func TestAdminUpdateQuestionReplacesContent(t *testing.T) {
	s, handler, pool := editServer(t)
	const slug = "httpapi-editable"
	makeTopic(t, pool, slug)

	q := createQuestion(t, s, handler, `{
		"topic_slug":"`+slug+`","difficulty_rating":2,
		"prompt":"Original prompt","answer":"Original","distractors":["a","b","c"]}`)

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodPut, "/api/admin/questions/"+strconv.FormatInt(q.ID, 10), `{
		"topic_slug":"`+slug+`","difficulty_rating":3,
		"prompt":"Corrected prompt","answer":"Corrected","aliases":["Fixed"],
		"distractors":["d","e","f","g"],"status":"retired"}`))
	if res.Code != http.StatusOK {
		t.Fatalf("update = %d, want 200 (%s)", res.Code, res.Body.String())
	}
	var updated adminQuestion
	if err := json.Unmarshal(res.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated: %v", err)
	}

	if updated.ID != q.ID {
		t.Errorf("id changed from %d to %d; an edit must not replace the row", q.ID, updated.ID)
	}
	if updated.Prompt != "Corrected prompt" || updated.Answer != "Corrected" {
		t.Errorf("prompt/answer = %q / %q", updated.Prompt, updated.Answer)
	}
	if updated.Status != "retired" {
		t.Errorf("status = %q, want retired", updated.Status)
	}
	if updated.DifficultyRating != 3 || updated.Difficulty != "easy" {
		t.Errorf("difficulty = %s/%d, want easy/3", updated.Difficulty, updated.DifficultyRating)
	}
	// Aliases and distractors are replaced wholesale, not merged: the old
	// values must be gone.
	if len(updated.Distractors) != 4 {
		t.Errorf("distractors = %v, want the four new ones", updated.Distractors)
	}
	for _, d := range updated.Distractors {
		if d == "a" {
			t.Error("an old distractor survived the edit")
		}
	}
	if len(updated.Aliases) != 2 {
		t.Errorf("aliases = %v, want the answer plus the new alias", updated.Aliases)
	}
}

func TestAdminUpdateQuestionRejectsUnknownID(t *testing.T) {
	s, handler, pool := editServer(t)
	const slug = "httpapi-unknown-id"
	makeTopic(t, pool, slug)

	body := `{"topic_slug":"` + slug + `","difficulty_rating":5,"prompt":"P","answer":"A","distractors":["x","y","z"]}`
	for _, path := range []string{"/api/admin/questions/999999999", "/api/admin/questions/0"} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, signedRequest(s, http.MethodPut, path, body))
		if res.Code != http.StatusNotFound && res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 404 or 400", path, res.Code)
		}
	}
}

// The board denormalizes topic and difficulty next to the question id, so a
// scheduled question cannot move between them without leaving the board
// describing content it no longer holds.
func TestAdminUpdateQuestionGuardsScheduledBoards(t *testing.T) {
	s, handler, pool := editServer(t)
	ctx := context.Background()
	const slug = "httpapi-scheduled"
	const otherSlug = "httpapi-scheduled-other"
	topicID := makeTopic(t, pool, slug)
	makeTopic(t, pool, otherSlug)

	q := createQuestion(t, s, handler, `{
		"topic_slug":"`+slug+`","difficulty_rating":2,
		"prompt":"Scheduled prompt","answer":"Scheduled","distractors":["a","b","c"]}`)

	// Put the question on a board, the way generation would.
	if _, err := pool.Exec(ctx,
		`INSERT INTO daily_puzzles (puzzle_date, time_limit_seconds) VALUES ($1, 135)
		 ON CONFLICT (puzzle_date) DO NOTHING`, "2031-03-03"); err != nil {
		t.Fatalf("create puzzle: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM daily_puzzles WHERE puzzle_date = $1`, "2031-03-03") })
	if _, err := pool.Exec(ctx,
		`INSERT INTO daily_puzzle_questions (puzzle_date, topic_id, topic_position, difficulty, question_id)
		 VALUES ($1, $2, 0, 'easy', $3)`, "2031-03-03", topicID, q.ID); err != nil {
		t.Fatalf("schedule the question: %v", err)
	}

	base := func(topic string, rating int) string {
		return `{"topic_slug":"` + topic + `","difficulty_rating":` + strconv.Itoa(rating) + `,
			"prompt":"Scheduled prompt","answer":"Scheduled","distractors":["a","b","c"]}`
	}
	path := "/api/admin/questions/" + strconv.FormatInt(q.ID, 10)

	// Moving band or topic is refused, and the message names the date.
	for _, tc := range []struct {
		name string
		body string
	}{
		{"band change", base(slug, 9)},
		{"topic change", base(otherSlug, 2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, signedRequest(s, http.MethodPut, path, tc.body))
			if res.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (%s)", res.Code, res.Body.String())
			}
			if !strings.Contains(res.Body.String(), "2031-03-03") {
				t.Errorf("message does not name the date: %s", res.Body.String())
			}
		})
	}

	// Rewording is always allowed, and so is a rating that stays in the band.
	t.Run("reword and re-rate within the band", func(t *testing.T) {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, signedRequest(s, http.MethodPut, path, `{
			"topic_slug":"`+slug+`","difficulty_rating":4,
			"prompt":"Reworded prompt","answer":"Scheduled","distractors":["a","b","c"]}`))
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (%s)", res.Code, res.Body.String())
		}
		var updated adminQuestion
		_ = json.Unmarshal(res.Body.Bytes(), &updated)
		if updated.Prompt != "Reworded prompt" || updated.DifficultyRating != 4 {
			t.Errorf("got %q at rating %d", updated.Prompt, updated.DifficultyRating)
		}
		if updated.UsedCount != 1 {
			t.Errorf("used_count = %d, want the scheduled board counted", updated.UsedCount)
		}
	})
}
