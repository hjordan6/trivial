package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/db"
)

// The library is meant to grow into the thousands -- a 180-day cooldown needs
// roughly 1,600 questions to sustain daily generation -- so the panel pages
// through it rather than loading the whole table and hoping it stays small.
const (
	defaultQuestionPage = 50
	maxQuestionPage     = 200
)

// maxImportBytes caps a pasted payload. It is generous enough for the whole
// starter dump (90KB) many times over, and small enough that a mispaste cannot
// exhaust memory before it is rejected.
const maxImportBytes = 5 << 20

type adminQuestion struct {
	ID               int64       `json:"id"`
	TopicSlug        string      `json:"topic_slug"`
	TopicName        string      `json:"topic_name"`
	Difficulty       string      `json:"difficulty"`
	DifficultyRating int         `json:"difficulty_rating"`
	Prompt           string      `json:"prompt"`
	Answer           string      `json:"answer"`
	Aliases          []string    `json:"aliases"`
	Distractors      []string    `json:"distractors"`
	Status           string      `json:"status"`
	UsedCount        int         `json:"used_count"`
	LastUsed         *clock.Date `json:"last_used"`
}

// questionColumns is the one description of a question row the panel reads.
// The listing and the single-question reload after an edit share it so an
// edited row can never come back in a different shape from its neighbours.
const questionColumns = `
	q.id, t.slug, t.name, q.difficulty::text, q.difficulty_rating,
	q.prompt, q.canonical_answer, q.status::text,
	COALESCE((SELECT array_agg(a.alias ORDER BY a.id)
	            FROM question_aliases a WHERE a.question_id = q.id), '{}'),
	COALESCE((SELECT array_agg(d.option_text ORDER BY d.id)
	            FROM question_distractors d WHERE d.question_id = q.id), '{}'),
	(SELECT count(*) FROM daily_puzzle_questions dpq WHERE dpq.question_id = q.id),
	(SELECT max(dpq.puzzle_date) FROM daily_puzzle_questions dpq WHERE dpq.question_id = q.id)`

func questionScanArgs(q *adminQuestion, lastUsed **time.Time) []any {
	return []any{&q.ID, &q.TopicSlug, &q.TopicName, &q.Difficulty, &q.DifficultyRating,
		&q.Prompt, &q.Answer, &q.Status, &q.Aliases, &q.Distractors, &q.UsedCount, lastUsed}
}

// datePlayed converts the nullable last-used timestamp into the calendar date
// the rest of the application speaks in.
func datePlayed(q *adminQuestion, lastUsed *time.Time) {
	if lastUsed != nil {
		d := clock.PuzzleDateAt(*lastUsed, time.UTC)
		q.LastUsed = &d
	}
}

// loadAdminQuestion re-reads one question, which is how a create or an edit
// answers with the same row shape the listing produces.
func (s *Server) loadAdminQuestion(ctx context.Context, q db.DBTX, id int64) (adminQuestion, error) {
	var out adminQuestion
	var lastUsed *time.Time
	err := q.QueryRow(ctx, `SELECT `+questionColumns+`
		FROM questions q JOIN topics t ON t.id = q.topic_id
		WHERE q.id = $1`, id).Scan(questionScanArgs(&out, &lastUsed)...)
	if err != nil {
		return adminQuestion{}, err
	}
	datePlayed(&out, lastUsed)
	return out, nil
}

// questionFilter is shared by the count and the page query so the two can
// never disagree about what "matching" means. A nil parameter drops its
// clause, which is how "no filter" is expressed.
const questionFilter = `
	FROM questions q
	JOIN topics t ON t.id = q.topic_id
	WHERE ($1::text IS NULL OR t.slug = $1::text)
	  AND ($2::text IS NULL OR q.difficulty::text = $2::text)
	  AND ($3::text IS NULL OR q.prompt ILIKE '%' || $3::text || '%'
	                        OR q.canonical_answer ILIKE '%' || $3::text || '%')`

// adminQuestions lists the content library with its answers, aliases and
// distractors, plus how often each question has been used. Usage is included
// because "why can nothing generate" is answered by the used counts, not by
// the question text.
func (s *Server) adminQuestions(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	query := r.URL.Query()

	var topic, difficulty, search any
	if v := query.Get("topic"); v != "" {
		topic = v
	}
	if v := query.Get("difficulty"); v != "" {
		switch content.Difficulty(v) {
		case content.Easy, content.Medium, content.Hard:
			difficulty = v
		default:
			s.fail(w, http.StatusBadRequest, "invalid_request",
				"difficulty must be easy, medium or hard.")
			return
		}
	}
	if v := query.Get("search"); v != "" {
		search = v
	}

	limit, ok := pageParam(w, query.Get("limit"), defaultQuestionPage, maxQuestionPage)
	if !ok {
		return
	}
	offset, ok := pageParam(w, query.Get("offset"), 0, 1<<30)
	if !ok {
		return
	}

	ctx := r.Context()
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*)`+questionFilter,
		topic, difficulty, search).Scan(&total); err != nil {
		s.internal(w, err)
		return
	}

	rows, err := s.Pool.Query(ctx, `SELECT `+questionColumns+questionFilter+`
		ORDER BY t.slug, q.difficulty_rating, q.id
		LIMIT $4 OFFSET $5`,
		topic, difficulty, search, limit, offset)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()

	out := make([]adminQuestion, 0, limit)
	for rows.Next() {
		var q adminQuestion
		var lastUsed *time.Time
		if err := rows.Scan(questionScanArgs(&q, &lastUsed)...); err != nil {
			s.internal(w, err)
			return
		}
		datePlayed(&q, lastUsed)
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		s.internal(w, err)
		return
	}

	s.write(w, http.StatusOK, map[string]any{
		"questions": out,
		"total":     total,
		"limit":     limit,
		"offset":    offset,
	})
}

func pageParam(w http.ResponseWriter, raw string, fallback, max int) (int, bool) {
	if raw == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > max {
		sFail(w, http.StatusBadRequest, "invalid_request",
			"limit and offset must be whole numbers, and limit at most "+strconv.Itoa(maxQuestionPage)+".")
		return 0, false
	}
	return n, true
}

// adminImportQuestions takes a pasted seed payload in any shape ParseSeed
// already accepts and upserts it. The body is the JSON itself rather than a
// field inside an envelope, so what an operator pastes is byte-for-byte what a
// seed file would contain.
func (s *Server) adminImportQuestions(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBytes))
	if err != nil {
		s.fail(w, http.StatusRequestEntityTooLarge, "payload_too_large",
			"That paste is too large; import it in smaller batches.")
		return
	}
	if len(bytes.TrimSpace(body)) == 0 {
		s.fail(w, http.StatusBadRequest, "invalid_request", "Paste some JSON to import.")
		return
	}

	// ParseSeed's errors name the topic, the question index and what is wrong
	// with it. That is exactly what is needed to fix a paste, so it is passed
	// through verbatim rather than replaced with a generic message.
	seed, err := content.ParseSeed(body)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_seed", err.Error())
		return
	}

	ctx := r.Context()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// ApplySeed is explicitly not atomic on its own, so it runs inside this
	// transaction: a paste lands completely or not at all, and a failure
	// halfway through does not leave a partial topic behind.
	stats, err := content.ApplySeed(ctx, tx, seed)
	if err != nil {
		s.internal(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internal(w, err)
		return
	}

	s.write(w, http.StatusOK, map[string]any{
		"topics":    stats.Topics,
		"questions": stats.Questions,
	})
}
