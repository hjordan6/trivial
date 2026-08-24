package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
)

// adminSource marks questions written in the panel rather than loaded from a
// seed file, so re-running the seed loader cannot overwrite hand-written work.
const adminSource = "admin"

// questionBody is one hand-written question. Create and update take the same
// shape: an edit replaces a question's content rather than patching fields,
// because the rules that matter -- a distractor must not also be an accepted
// answer -- can only be checked against the whole question at once.
type questionBody struct {
	TopicSlug        string   `json:"topic_slug"`
	Prompt           string   `json:"prompt"`
	Answer           string   `json:"answer"`
	DifficultyRating int      `json:"difficulty_rating"`
	Aliases          []string `json:"aliases"`
	Distractors      []string `json:"distractors"`
	Status           string   `json:"status"`
}

// clean drops blank entries. The panel collects aliases and distractors as
// one-per-line text, where a stray newline is a formatting artefact rather
// than an empty alias the operator meant to write.
func clean(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func validStatus(status string) bool {
	switch status {
	case "draft", "active", "retired":
		return true
	}
	return false
}

// parseQuestionBody decodes, validates and normalizes a submitted question.
// Validation runs through content.ValidateQuestion, the same code a pasted
// seed file goes through, so the two entry points cannot drift apart.
func (s *Server) parseQuestionBody(w http.ResponseWriter, r *http.Request, fallbackStatus string) (questionBody, content.SeedQuestion, bool) {
	var body questionBody
	if err := decodeOptional(r, &body); err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request", err.Error())
		return body, content.SeedQuestion{}, false
	}
	body.Prompt = strings.TrimSpace(body.Prompt)
	body.Answer = strings.TrimSpace(body.Answer)
	body.TopicSlug = strings.TrimSpace(body.TopicSlug)
	body.Aliases = clean(body.Aliases)
	body.Distractors = clean(body.Distractors)
	if body.Status == "" {
		body.Status = fallbackStatus
	}
	if !validStatus(body.Status) {
		s.fail(w, http.StatusBadRequest, "invalid_request", "Status must be draft, active or retired.")
		return body, content.SeedQuestion{}, false
	}
	if body.TopicSlug == "" {
		s.fail(w, http.StatusBadRequest, "invalid_request", "Pick a topic.")
		return body, content.SeedQuestion{}, false
	}

	question := content.SeedQuestion{
		Difficulty:  content.DifficultyRating(body.DifficultyRating),
		Prompt:      body.Prompt,
		Answer:      body.Answer,
		Aliases:     body.Aliases,
		Distractors: body.Distractors,
	}
	// ValidateQuestion also prepends the canonical answer to the aliases, so
	// what is written afterwards carries every spelling the grader accepts.
	if err := content.ValidateQuestion(&question); err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_question", capitalize(err.Error())+".")
		return body, content.SeedQuestion{}, false
	}
	return body, question, true
}

func capitalize(msg string) string {
	if msg == "" {
		return msg
	}
	return strings.ToUpper(msg[:1]) + msg[1:]
}

func (s *Server) topicIDFor(ctx context.Context, q pgx.Tx, slug string) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `SELECT id FROM topics WHERE slug = $1`, slug).Scan(&id)
	return id, err
}

// adminCreateQuestion writes one hand-written question.
func (s *Server) adminCreateQuestion(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	body, question, ok := s.parseQuestionBody(w, r, "active")
	if !ok {
		return
	}

	ctx := r.Context()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	topicID, err := s.topicIDFor(ctx, tx, body.TopicSlug)
	if errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, http.StatusBadRequest, "unknown_topic", "No topic called "+body.TopicSlug+".")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}

	band, err := content.BandForRating(body.DifficultyRating)
	if err != nil {
		s.internal(w, err)
		return
	}
	// A hand-written question still gets an external_id so it round-trips
	// through the CSV export and the seed importer like any other row.
	token, err := randomToken()
	if err != nil {
		s.internal(w, err)
		return
	}
	id, err := content.UpsertQuestion(ctx, tx, content.QuestionInput{
		TopicID:          topicID,
		Difficulty:       band,
		DifficultyRating: body.DifficultyRating,
		Prompt:           question.Prompt,
		CanonicalAnswer:  question.Answer,
		Status:           body.Status,
		Source:           adminSource,
		ExternalID:       adminSource + "-" + token,
	})
	if err != nil {
		s.internal(w, err)
		return
	}
	if err := content.ReplaceAliases(ctx, tx, id, question.Aliases); err != nil {
		s.internal(w, err)
		return
	}
	if err := content.ReplaceDistractors(ctx, tx, id, question.Distractors); err != nil {
		s.internal(w, err)
		return
	}

	created, err := s.loadAdminQuestion(ctx, tx, id)
	if err != nil {
		s.internal(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internal(w, err)
		return
	}
	s.write(w, http.StatusCreated, created)
}

// adminUpdateQuestion replaces one question's content.
//
// Wording, answer, aliases, distractors and status can always change. Topic
// and difficulty band cannot while the question is on a generated board:
// daily_puzzle_questions stores both alongside the question id, so moving a
// scheduled question would leave a board claiming a topic and difficulty its
// own question no longer has. Changing the rating within its band is fine,
// since the band is what the board recorded.
func (s *Server) adminUpdateQuestion(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.fail(w, http.StatusBadRequest, "invalid_request", "Invalid question id.")
		return
	}
	ctx := r.Context()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The current row is read before the body is parsed so that a submission
	// which omits status keeps the one the question already has, rather than
	// being rejected for leaving it blank.
	var currentTopic int64
	var currentBand, currentStatus string
	err = tx.QueryRow(ctx,
		`SELECT topic_id, difficulty::text, status::text FROM questions WHERE id = $1`, id).
		Scan(&currentTopic, &currentBand, &currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, http.StatusNotFound, "unknown_question", "No question with that id.")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}

	body, question, ok := s.parseQuestionBody(w, r, currentStatus)
	if !ok {
		return
	}

	topicID, err := s.topicIDFor(ctx, tx, body.TopicSlug)
	if errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, http.StatusBadRequest, "unknown_topic", "No topic called "+body.TopicSlug+".")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	band, err := content.BandForRating(body.DifficultyRating)
	if err != nil {
		s.internal(w, err)
		return
	}

	if topicID != currentTopic || string(band) != currentBand {
		var scheduled []clock.Date
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(array_agg(puzzle_date ORDER BY puzzle_date), '{}')
			   FROM daily_puzzle_questions WHERE question_id = $1`, id).Scan(&scheduled); err != nil {
			s.internal(w, err)
			return
		}
		if len(scheduled) > 0 {
			s.fail(w, http.StatusConflict, "question_is_scheduled",
				"This question is on the board for "+datesSentence(scheduled)+
					". Its topic and difficulty are fixed while it is scheduled -- edit the wording, "+
					"or rebuild those dates first.")
			return
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE questions
		   SET topic_id = $2, difficulty = $3::difficulty, difficulty_rating = $4,
		       prompt = $5, canonical_answer = $6, status = $7::question_status, updated_at = now()
		 WHERE id = $1`,
		id, topicID, string(band), body.DifficultyRating,
		question.Prompt, question.Answer, body.Status); err != nil {
		s.internal(w, err)
		return
	}
	if err := content.ReplaceAliases(ctx, tx, id, question.Aliases); err != nil {
		s.internal(w, err)
		return
	}
	if err := content.ReplaceDistractors(ctx, tx, id, question.Distractors); err != nil {
		s.internal(w, err)
		return
	}

	updated, err := s.loadAdminQuestion(ctx, tx, id)
	if err != nil {
		s.internal(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internal(w, err)
		return
	}
	s.write(w, http.StatusOK, updated)
}

// datesSentence lists dates for a message, trimming a long tail rather than
// printing months of them.
func datesSentence(dates []clock.Date) string {
	const most = 3
	parts := make([]string, 0, most)
	for i, d := range dates {
		if i == most {
			parts = append(parts, "and "+strconv.Itoa(len(dates)-most)+" more")
			break
		}
		parts = append(parts, d.String())
	}
	return strings.Join(parts, ", ")
}
