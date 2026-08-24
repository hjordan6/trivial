package httpapi

import (
	"net/http"
	"time"
)

// adminBoardEntry is one cell of a day's board: the question itself, plus where
// the board put it. Slot is the difficulty daily_puzzle_questions recorded,
// which is the question's band at the moment it was scheduled -- it is stored
// separately from the question, so the two are shown separately here.
type adminBoardEntry struct {
	adminQuestion
	TopicPosition int    `json:"topic_position"`
	Slot          string `json:"slot"`
}

// adminBoard answers with a date's nine questions in board order.
//
// A date with no board is not an error: the panel asks about arbitrary dates,
// including ones nobody has generated yet, so "there is nothing here" comes
// back as an empty board rather than a 404.
func (s *Server) adminBoard(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	date, ok := adminDateParam(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	// Ordering by the difficulty enum gives easy, medium, hard: Postgres orders
	// an enum by the order its labels were declared, not alphabetically.
	rows, err := s.Pool.Query(ctx, `SELECT `+questionColumns+`,
		       dpq.topic_position, dpq.difficulty::text
		FROM daily_puzzle_questions dpq
		JOIN questions q ON q.id = dpq.question_id
		JOIN topics t ON t.id = q.topic_id
		WHERE dpq.puzzle_date = $1
		ORDER BY dpq.topic_position, dpq.difficulty`, date)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()

	entries := make([]adminBoardEntry, 0, 9)
	for rows.Next() {
		var entry adminBoardEntry
		var lastUsed *time.Time
		args := append(questionScanArgs(&entry.adminQuestion, &lastUsed),
			&entry.TopicPosition, &entry.Slot)
		if err := rows.Scan(args...); err != nil {
			s.internal(w, err)
			return
		}
		datePlayed(&entry.adminQuestion, lastUsed)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		s.internal(w, err)
		return
	}

	var runs int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM runs WHERE puzzle_date = $1`, date).Scan(&runs); err != nil {
		s.internal(w, err)
		return
	}

	s.write(w, http.StatusOK, map[string]any{
		"date":      date,
		"generated": len(entries) > 0,
		"has_runs":  runs > 0,
		"entries":   entries,
	})
}
