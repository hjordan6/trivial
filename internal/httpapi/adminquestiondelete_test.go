package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestAdminDeleteQuestionRemovesEverySide(t *testing.T) {
	s, handler, pool := editServer(t)
	ctx := context.Background()
	const slug = "httpapi-delete"
	makeTopic(t, pool, slug)

	q := createQuestion(t, s, handler, `{
		"topic_slug":"`+slug+`","difficulty_rating":6,
		"prompt":"Doomed prompt","answer":"Doomed","aliases":["Doomd"],
		"distractors":["a","b","c"]}`)
	path := "/api/admin/questions/" + strconv.FormatInt(q.ID, 10)

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodDelete, path, ""))
	if res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204 (%s)", res.Code, res.Body.String())
	}

	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM questions WHERE id = $1`, q.ID).Scan(&rows); err != nil {
		t.Fatalf("count questions: %v", err)
	}
	if rows != 0 {
		t.Errorf("question row survived the delete")
	}
	// Aliases and distractors cascade; leaving them behind would orphan rows
	// that nothing can ever reach again.
	for _, table := range []string{"question_aliases", "question_distractors"} {
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM `+table+` WHERE question_id = $1`, q.ID).Scan(&rows); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if rows != 0 {
			t.Errorf("%s kept %d row(s) for a deleted question", table, rows)
		}
	}

	// Deleting the same id twice is a 404, not a silent success: the second
	// caller is looking at a list that has moved on.
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodDelete, path, ""))
	if res.Code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", res.Code)
	}
}

// A question on a generated board is history the moment the board exists, so
// the delete is refused and the message names the date to rebuild.
func TestAdminDeleteQuestionRefusesScheduled(t *testing.T) {
	s, handler, pool := editServer(t)
	ctx := context.Background()
	const slug = "httpapi-delete-scheduled"
	topicID := makeTopic(t, pool, slug)

	q := createQuestion(t, s, handler, `{
		"topic_slug":"`+slug+`","difficulty_rating":2,
		"prompt":"Booked prompt","answer":"Booked","distractors":["a","b","c"]}`)

	if _, err := pool.Exec(ctx,
		`INSERT INTO daily_puzzles (puzzle_date, time_limit_seconds) VALUES ($1, 135)
		 ON CONFLICT (puzzle_date) DO NOTHING`, "2031-04-04"); err != nil {
		t.Fatalf("create puzzle: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM daily_puzzles WHERE puzzle_date = $1`, "2031-04-04") })
	if _, err := pool.Exec(ctx,
		`INSERT INTO daily_puzzle_questions (puzzle_date, topic_id, topic_position, difficulty, question_id)
		 VALUES ($1, $2, 0, 'easy', $3)`, "2031-04-04", topicID, q.ID); err != nil {
		t.Fatalf("schedule the question: %v", err)
	}

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodDelete,
		"/api/admin/questions/"+strconv.FormatInt(q.ID, 10), ""))
	if res.Code != http.StatusConflict {
		t.Fatalf("delete = %d, want 409 (%s)", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "2031-04-04") {
		t.Errorf("message does not name the date: %s", res.Body.String())
	}

	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM questions WHERE id = $1`, q.ID).Scan(&rows); err != nil {
		t.Fatalf("count questions: %v", err)
	}
	if rows != 1 {
		t.Errorf("a refused delete removed the question anyway")
	}
}

func TestAdminDeleteQuestionNeedsAdmin(t *testing.T) {
	s := &Server{AdminPassword: "correct horse"}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "/api/admin/questions/1", nil))
	if res.Code != http.StatusNotFound {
		t.Errorf("unauthenticated delete = %d, want 404", res.Code)
	}
}

func TestAdminDeleteQuestionRejectsBadID(t *testing.T) {
	s := &Server{AdminPassword: "correct horse"}
	handler := s.Handler()
	for _, path := range []string{"/api/admin/questions/0", "/api/admin/questions/abc"} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, signedRequest(s, http.MethodDelete, path, ""))
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", path, res.Code)
		}
	}
}
