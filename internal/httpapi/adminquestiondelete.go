package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/hjordan6/trivial/internal/clock"
)

// adminDeleteQuestion removes one question and everything that hangs off it.
// Aliases and distractors cascade; nothing else is touched.
//
// A question that has ever been on a board is refused rather than deleted.
// daily_puzzle_questions and run_answers both reference it ON DELETE RESTRICT,
// because a played board is history: dropping the question would leave a run
// nobody could ever explain again. The message names the dates so the operator
// can rebuild them, or retire the question instead -- retiring keeps it off
// every future board without erasing the ones behind it.
func (s *Server) adminDeleteQuestion(w http.ResponseWriter, r *http.Request) {
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
				", so it cannot be deleted. Retire it to keep it off future boards, "+
				"or rebuild those dates first.")
		return
	}

	tag, err := tx.Exec(ctx, `DELETE FROM questions WHERE id = $1`, id)
	if err != nil {
		// A board written between the check above and this delete lands here.
		// It is the same refusal, just without the dates to name.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			s.fail(w, http.StatusConflict, "question_is_scheduled",
				"This question was just scheduled on a board, so it cannot be deleted.")
			return
		}
		s.internal(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		s.fail(w, http.StatusNotFound, "unknown_question", "No question with that id.")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

const foreignKeyViolation = "23503"
