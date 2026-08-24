package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/testsupport"
)

// signedRequest builds a request already carrying a valid admin session, since
// every question route is behind one.
func signedRequest(s *Server, method, path, body string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.AddCookie(&http.Cookie{Name: adminCookie, Value: s.signAdminSession(s.now().Add(time.Hour))})
	return req
}

// hideTopic takes a fixture topic out of automatic selection.
//
// These tests write through the pool, so their rows are committed and visible
// to every other package's tests running against the same database at the same
// time. An active fixture topic therefore changes what content.ActiveTopics
// returns inside internal/puzzle's transactions -- and because those run at
// READ COMMITTED, it can change between two statements of one transaction,
// which is enough to make the generator's determinism test see two different
// boards for one date. Fixtures here are never meant to be selectable, so they
// are hidden the moment they exist.
func hideTopic(t *testing.T, pool *pgxpool.Pool, slug string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE topics SET active = false WHERE slug = $1`, slug); err != nil {
		t.Fatalf("hide topic %s: %v", slug, err)
	}
}

// These reject before any database work, so they need no pool.
func TestAdminQuestionsRejectsBadFilters(t *testing.T) {
	s := &Server{AdminPassword: "correct horse", Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}}
	handler := s.Handler()

	cases := []struct {
		name string
		path string
	}{
		{"unknown difficulty", "/api/admin/questions?difficulty=trivial"},
		{"limit over the cap", "/api/admin/questions?limit=999"},
		{"negative offset", "/api/admin/questions?offset=-1"},
		{"non-numeric limit", "/api/admin/questions?limit=lots"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, signedRequest(s, http.MethodGet, tc.path, ""))
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", res.Code)
			}
		})
	}
}

func TestAdminImportRejectsUnusablePayloads(t *testing.T) {
	s := &Server{AdminPassword: "correct horse", Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}}
	handler := s.Handler()

	cases := []struct {
		name string
		body string
		want string
	}{
		{"empty", "", "invalid_request"},
		{"whitespace only", "   \n\t ", "invalid_request"},
		{"not json", "{nope", "invalid_seed"},
		// A question with too few distractors can never become eligible, so it
		// is refused at the paste rather than discovered when a board fails.
		{"too few distractors", `{"topics":[{"slug":"t","name":"T","questions":[
			{"external_id":"x","difficulty":3,"prompt":"P","answer":"A","distractors":["b"]}]}]}`, "invalid_seed"},
		// A distractor that grades as correct would put two right answers on
		// the same board.
		{"distractor is the answer", `{"topics":[{"slug":"t","name":"T","questions":[
			{"external_id":"x","difficulty":3,"prompt":"P","answer":"A","distractors":["A","b","c"]}]}]}`, "invalid_seed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, signedRequest(s, http.MethodPost, "/api/admin/questions/import", tc.body))
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %q)", res.Code, res.Body.String())
			}
			var apiErr apiError
			if err := json.Unmarshal(res.Body.Bytes(), &apiErr); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if apiErr.Code != tc.want {
				t.Fatalf("code = %q, want %q", apiErr.Code, tc.want)
			}
			if apiErr.Message == "" {
				t.Error("no message; the operator gets nothing to act on")
			}
		})
	}
}

// TestAdminImportThenList covers the round trip: a pasted payload lands in the
// library and comes back out of the listing with the pieces the panel renders.
func TestAdminImportThenList(t *testing.T) {
	pool := testsupport.MustPool(t)
	ctx := context.Background()
	s := &Server{Pool: pool, AdminPassword: "correct horse", Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}}
	handler := s.Handler()

	const slug = "httpapi-import-fixture"
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM questions WHERE topic_id IN (SELECT id FROM topics WHERE slug = $1)`, slug)
		_, _ = pool.Exec(ctx, `DELETE FROM topics WHERE slug = $1`, slug)
	})

	payload := `{"topics":[{"slug":"` + slug + `","name":"Import Fixture","weight":2,"questions":[
		{"external_id":"import-fixture-1","difficulty":3,"prompt":"Which planet is closest to the Sun?",
		 "answer":"Mercury","aliases":["Planet Mercury"],"distractors":["Venus","Mars","Earth"]}]}]}`

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodPost, "/api/admin/questions/import", payload))
	if res.Code != http.StatusOK {
		t.Fatalf("import = %d, want 200 (%s)", res.Code, res.Body.String())
	}
	var imported struct {
		Topics    int `json:"topics"`
		Questions int `json:"questions"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &imported); err != nil {
		t.Fatalf("decode import result: %v", err)
	}
	if imported.Topics != 1 || imported.Questions != 1 {
		t.Fatalf("imported %d topics / %d questions, want 1 / 1", imported.Topics, imported.Questions)
	}
	hideTopic(t, pool, slug)

	res = httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodGet, "/api/admin/questions?topic="+slug, ""))
	if res.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200 (%s)", res.Code, res.Body.String())
	}
	var listed struct {
		Questions []adminQuestion `json:"questions"`
		Total     int             `json:"total"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if listed.Total != 1 || len(listed.Questions) != 1 {
		t.Fatalf("listed %d of %d, want 1 of 1", len(listed.Questions), listed.Total)
	}

	got := listed.Questions[0]
	if got.TopicSlug != slug || got.TopicName != "Import Fixture" {
		t.Errorf("topic = %s/%s, want %s/Import Fixture", got.TopicSlug, got.TopicName, slug)
	}
	if got.Answer != "Mercury" {
		t.Errorf("answer = %q, want Mercury", got.Answer)
	}
	// A rating of 3 sits in the easy band; the listing reports both so the
	// panel can show the band and sort by the finer number.
	if got.Difficulty != "easy" || got.DifficultyRating != 3 {
		t.Errorf("difficulty = %s/%d, want easy/3", got.Difficulty, got.DifficultyRating)
	}
	if len(got.Distractors) != 3 {
		t.Errorf("distractors = %v, want 3", got.Distractors)
	}
	// ParseSeed prepends the canonical answer to the aliases, so both are
	// accepted at grading time and both should be visible here.
	if len(got.Aliases) != 2 {
		t.Errorf("aliases = %v, want the answer plus the listed alias", got.Aliases)
	}
	if got.Status != "active" {
		t.Errorf("status = %q, want active so it is immediately eligible", got.Status)
	}
	if got.UsedCount != 0 || got.LastUsed != nil {
		t.Errorf("used = %d/%v, want an unused question", got.UsedCount, got.LastUsed)
	}
}

// Re-importing the same external_id must update the row rather than add a
// second copy, which is what makes fixing a typo a paste-again operation.
func TestAdminImportIsIdempotent(t *testing.T) {
	pool := testsupport.MustPool(t)
	ctx := context.Background()
	s := &Server{Pool: pool, AdminPassword: "correct horse", Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}}
	handler := s.Handler()

	const slug = "httpapi-idempotent-fixture"
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM questions WHERE topic_id IN (SELECT id FROM topics WHERE slug = $1)`, slug)
		_, _ = pool.Exec(ctx, `DELETE FROM topics WHERE slug = $1`, slug)
	})

	body := func(answer string) string {
		return `{"topics":[{"slug":"` + slug + `","name":"Idempotent Fixture","questions":[
			{"external_id":"idempotent-1","difficulty":6,"prompt":"P","answer":"` + answer + `",
			 "distractors":["x","y","z"]}]}]}`
	}
	for _, answer := range []string{"First", "Second"} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, signedRequest(s, http.MethodPost, "/api/admin/questions/import", body(answer)))
		if res.Code != http.StatusOK {
			t.Fatalf("import %s = %d (%s)", answer, res.Code, res.Body.String())
		}
	}
	hideTopic(t, pool, slug)

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodGet, "/api/admin/questions?topic="+slug, ""))
	var listed struct {
		Questions []adminQuestion `json:"questions"`
		Total     int             `json:"total"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if listed.Total != 1 {
		t.Fatalf("total = %d after re-importing one external_id, want 1", listed.Total)
	}
	if listed.Questions[0].Answer != "Second" {
		t.Errorf("answer = %q, want the re-imported value", listed.Questions[0].Answer)
	}
}
