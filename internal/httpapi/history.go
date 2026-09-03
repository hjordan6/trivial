package httpapi

import (
	"net/http"
	"sort"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/play"
)

// HistoryDay is one finished day, scored.
type HistoryDay struct {
	Date    clock.Date `json:"date"`
	Points  int        `json:"points"`
	Correct int        `json:"correct"`
	Typed   int        `json:"typed"`
}

// HistoryTopic is one category's record across every day the viewer has played
// it. Asked counts resolved questions, which for a finished run is every
// question on that day's board: Finish sweeps the untouched ones in as expired.
type HistoryTopic struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Asked   int    `json:"asked"`
	Correct int    `json:"correct"`
	Points  int    `json:"points"`
}

// History is the whole record, aggregated but not ranked: averages, bests and
// the strongest and weakest categories are the client's to derive, so the page
// can change its mind about how to present them without a new endpoint.
//
// Days is in date order, oldest first.
type History struct {
	Days   []HistoryDay   `json:"days"`
	Topics []HistoryTopic `json:"topics"`
}

// historyQuery returns one row per answer of every day that counts, plus a row
// with null answer columns for a day whose run resolved nothing at all. Scoring
// happens in Go rather than in a CASE expression here, so play.Points stays the
// only place the game's scoring rule is written down.
const historyQuery = viewerRunsCTE + `
SELECT c.puzzle_date, t.slug, t.name, q.difficulty, ra.outcome
  FROM chosen c
  LEFT JOIN run_answers ra ON ra.run_id = c.id
  LEFT JOIN questions q ON q.id = ra.question_id
  LEFT JOIN topics t ON t.id = q.topic_id
 ORDER BY c.puzzle_date`

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	out := History{Days: []HistoryDay{}, Topics: []HistoryTopic{}}
	id := s.optionalPlayer(r)
	if id == "" {
		s.write(w, 200, out)
		return
	}
	userID, err := s.viewerUserID(r, id)
	if err != nil {
		s.internal(w, err)
		return
	}
	rows, err := s.Pool.Query(r.Context(), historyQuery, id, userID)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()

	byTopic := map[string]*HistoryTopic{}
	for rows.Next() {
		var date clock.Date
		var slug, name *string
		var difficulty *content.Difficulty
		var outcome *play.Outcome
		if err := rows.Scan(&date, &slug, &name, &difficulty, &outcome); err != nil {
			s.internal(w, err)
			return
		}
		// Rows arrive grouped by date, so a new date is always a new day.
		if len(out.Days) == 0 || !out.Days[len(out.Days)-1].Date.Equal(date) {
			out.Days = append(out.Days, HistoryDay{Date: date})
		}
		if outcome == nil || difficulty == nil || slug == nil {
			continue
		}
		day := &out.Days[len(out.Days)-1]
		points := play.Points(*difficulty, *outcome)
		correct := *outcome == play.Star || *outcome == play.Circle
		day.Points += points
		if correct {
			day.Correct++
		}
		if *outcome == play.Star {
			day.Typed++
		}

		topic := byTopic[*slug]
		if topic == nil {
			topic = &HistoryTopic{Slug: *slug, Name: *name}
			byTopic[*slug] = topic
		}
		topic.Asked++
		topic.Points += points
		if correct {
			topic.Correct++
		}
	}
	if err := rows.Err(); err != nil {
		s.internal(w, err)
		return
	}
	for _, topic := range byTopic {
		out.Topics = append(out.Topics, *topic)
	}
	// Slug order, so the payload is stable between identical requests. The
	// client ranks.
	sort.Slice(out.Topics, func(i, j int) bool { return out.Topics[i].Slug < out.Topics[j].Slug })
	s.write(w, 200, out)
}
