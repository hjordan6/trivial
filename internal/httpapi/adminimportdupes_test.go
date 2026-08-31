package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func countQuestions(t *testing.T, pool *pgxpool.Pool, slug string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM questions q JOIN topics t ON t.id = q.topic_id WHERE t.slug = $1`,
		slug).Scan(&n); err != nil {
		t.Fatalf("count questions: %v", err)
	}
	return n
}

func importPaste(t *testing.T, s *Server, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodPost, "/api/admin/questions/import", body))
	return res
}

// A pasted question whose answer is already taken in the same topic and band is
// refused, and refused before anything is written.
func TestAdminImportRefusesDuplicateAnswer(t *testing.T) {
	s, handler, pool := editServer(t)
	const slug = "httpapi-import-dupes"
	makeTopic(t, pool, slug)

	createQuestion(t, s, handler, `{
		"topic_slug":"`+slug+`","difficulty_rating":6,
		"prompt":"The original wording","answer":"Repeated Answer",
		"distractors":["a","b","c"]}`)
	before := countQuestions(t, pool, slug)

	// Same answer, same band, different question, plus a clean second question
	// that must not be written either.
	res := importPaste(t, s, handler, `[
		{"question":"A completely different wording for the same fact.",
		 "category":"`+slug+`","difficulty":6,"answer":"Repeated Answer",
		 "multipleChoiceOptions":["Repeated Answer","x","y","z"]},
		{"question":"An unrelated question that would have been fine.",
		 "category":"`+slug+`","difficulty":6,"answer":"Innocent Bystander",
		 "multipleChoiceOptions":["Innocent Bystander","x","y","z"]}]`)

	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "Repeated Answer") {
		t.Errorf("message does not name the answer: %s", res.Body.String())
	}
	if got := countQuestions(t, pool, slug); got != before {
		t.Errorf("question count went %d -> %d; a refused paste must write nothing", before, got)
	}
}

// The same answer at a different difficulty is allowed: those are different
// slots, and the operator has said that case is fine.
func TestAdminImportAllowsSameAnswerInAnotherBand(t *testing.T) {
	s, handler, pool := editServer(t)
	const slug = "httpapi-import-otherband"
	makeTopic(t, pool, slug)

	createQuestion(t, s, handler, `{
		"topic_slug":"`+slug+`","difficulty_rating":2,
		"prompt":"The easy wording","answer":"Shared Answer",
		"distractors":["a","b","c"]}`)

	res := importPaste(t, s, handler, `[
		{"question":"A much harder question with the same answer.",
		 "category":"`+slug+`","difficulty":9,"answer":"Shared Answer",
		 "multipleChoiceOptions":["Shared Answer","x","y","z"]}]`)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", res.Code, res.Body.String())
	}
	if got := countQuestions(t, pool, slug); got != 2 {
		t.Errorf("question count = %d, want 2", got)
	}
}

// Two questions inside one paste can collide with each other, with nothing in
// the library involved.
func TestAdminImportRefusesDuplicateWithinOnePaste(t *testing.T) {
	s, handler, pool := editServer(t)
	const slug = "httpapi-import-selfdupe"
	makeTopic(t, pool, slug)

	res := importPaste(t, s, handler, `[
		{"question":"First wording of the fact.","category":"`+slug+`","difficulty":6,
		 "answer":"Twice Over","multipleChoiceOptions":["Twice Over","x","y","z"]},
		{"question":"Second wording of the very same fact.","category":"`+slug+`","difficulty":6,
		 "answer":"Twice Over","multipleChoiceOptions":["Twice Over","x","y","z"]}]`)

	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "twice in this paste") {
		t.Errorf("message does not explain the clash: %s", res.Body.String())
	}
	if got := countQuestions(t, pool, slug); got != 0 {
		t.Errorf("wrote %d questions for a refused paste", got)
	}
}

// Re-pasting the same payload is how a typo gets fixed, so a question matching
// an existing row's external_id is its own update rather than a duplicate.
func TestAdminImportRepasteIsAnUpdateNotADuplicate(t *testing.T) {
	s, handler, pool := editServer(t)
	const slug = "httpapi-import-repaste"
	makeTopic(t, pool, slug)

	paste := `[{"question":"A question worth pasting twice.","category":"` + slug + `",
		"difficulty":6,"answer":"Stable Answer","acceptedAnswers":["Stable"],
		"multipleChoiceOptions":["Stable Answer","x","y","z"]}]`

	for _, pass := range []string{"first", "second"} {
		res := importPaste(t, s, handler, paste)
		if res.Code != http.StatusOK {
			t.Fatalf("%s paste = %d, want 200 (%s)", pass, res.Code, res.Body.String())
		}
	}
	if got := countQuestions(t, pool, slug); got != 1 {
		t.Errorf("question count = %d, want 1 -- the re-paste should have updated in place", got)
	}
}

// Retiring the question already holding an answer is how the operator makes
// room for a replacement, so a retired row must stop blocking the import.
func TestAdminImportIgnoresRetiredQuestions(t *testing.T) {
	s, handler, pool := editServer(t)
	const slug = "httpapi-import-retired"
	makeTopic(t, pool, slug)

	q := createQuestion(t, s, handler, `{
		"topic_slug":"`+slug+`","difficulty_rating":6,
		"prompt":"The wording being replaced","answer":"Handover",
		"distractors":["a","b","c"]}`)
	if _, err := pool.Exec(context.Background(),
		`UPDATE questions SET status = 'retired' WHERE id = $1`, q.ID); err != nil {
		t.Fatalf("retire: %v", err)
	}

	res := importPaste(t, s, handler, `[
		{"question":"The replacement wording for that fact.","category":"`+slug+`",
		 "difficulty":6,"answer":"Handover",
		 "multipleChoiceOptions":["Handover","x","y","z"]}]`)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", res.Code, res.Body.String())
	}
}

// The repair reaches the import endpoint, not just ParseSeed's own tests.
func TestAdminImportAcceptsCurlyQuotePaste(t *testing.T) {
	s, handler, pool := editServer(t)
	const slug = "httpapi-import-curly"
	makeTopic(t, pool, slug)

	res := importPaste(t, s, handler, "```json\n{\n  “questions”: [\n    {\n"+
		"      “question”: “A prompt quoting “a phrase,” then continuing on.”,\n"+
		"      “category”: “"+slug+"”,\n"+
		"      “difficulty”: “medium”,\n"+
		"      “answer”: “Curly”,\n"+
		"      “acceptedAnswers”: [],\n"+
		"      “multipleChoiceOptions”: [“Curly”, “x”, “y”, “z”]\n"+
		"    }\n  ]\n}\n```")
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", res.Code, res.Body.String())
	}
	var result struct{ Questions int }
	_ = json.Unmarshal(res.Body.Bytes(), &result)
	if result.Questions != 1 {
		t.Errorf("imported %d questions, want 1", result.Questions)
	}
	var prompt string
	if err := pool.QueryRow(context.Background(),
		`SELECT q.prompt FROM questions q JOIN topics t ON t.id = q.topic_id WHERE t.slug = $1`,
		slug).Scan(&prompt); err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	if !strings.Contains(prompt, "“a phrase,”") || !strings.HasSuffix(prompt, "then continuing on.") {
		t.Errorf("prompt did not survive the repair: %q", prompt)
	}
}
