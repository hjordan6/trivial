package httpapi

import (
	"net/http"
	"sort"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/friends"
	"github.com/hjordan6/trivial/internal/play"
)

// MinimumDays is how many finished days an account needs before it appears in a
// ranked list.
//
// Without a floor, an average is won by whoever had one good day: at the time
// this was written seven of eighteen accounts had played exactly once, and a
// single 40-point Tuesday would have outranked a player with twenty solid days.
// Three is low enough that a new player joins the board within a week and high
// enough that one lucky day cannot carry them.
const MinimumDays = 3

// MinimumTopicQuestions is the same guard for a single category, counted in
// questions rather than days because categories come up at different rates.
// Nine is three days of a category; below that the sample is one sitting.
const MinimumTopicQuestions = 9

// AllTimeEntry is one person on a ranked list.
//
// You marks the viewer's own row so the client can highlight it without
// matching ids, and so the viewer appears in the friends list rather than
// beside it -- the question is where you place among them, which a list you are
// absent from cannot answer.
type AllTimeEntry struct {
	UserID        int64   `json:"user_id"`
	Nickname      string  `json:"nickname"`
	DaysPlayed    int     `json:"days_played"`
	AveragePoints float64 `json:"average_points"`
	Rank          int     `json:"rank"`
	You           bool    `json:"you"`
}

// TopicEntry is one person's record in one category. The average is per
// question, not per day: a category that came up on nine days and one that came
// up on four are not comparable per day.
type TopicEntry struct {
	UserID        int64   `json:"user_id"`
	Nickname      string  `json:"nickname"`
	Asked         int     `json:"asked"`
	AveragePoints float64 `json:"average_points"`
	Rank          int     `json:"rank"`
	You           bool    `json:"you"`
}

// GlobalStanding is the everyone scope: a position and a field size, never a
// list of people.
//
// This is a deliberate asymmetry with the friends scope, and the reason is that
// a nickname falls back to the local part of an email address. Among friends
// that is a name they already know. Published to every player it would hand out
// strangers' addresses one half at a time, and at the time this was written
// only three of eighteen accounts had ever chosen a nickname, so it would have
// been fifteen disclosures against three names. A named global board is a fine
// thing to build later, on an opt-in display name that is nobody's address.
//
// Ranked reports whether the viewer qualified; Of counts only accounts that
// did, so "4th of 11" never quietly ranks against people who cannot be ranked.
type GlobalStanding struct {
	Ranked      bool    `json:"ranked"`
	Rank        int     `json:"rank"`
	Of          int     `json:"of"`
	BestAverage float64 `json:"best_average"`
}

// TopicStanding is one category's section: the viewer's sample, the friends
// ranked within it, and the viewer's place among everyone.
type TopicStanding struct {
	Slug    string         `json:"slug"`
	Name    string         `json:"name"`
	Asked   int            `json:"asked"`
	Ranked  bool           `json:"ranked"`
	Friends []TopicEntry   `json:"friends"`
	Global  GlobalStanding `json:"global"`
}

// AllTime is the whole all-time board.
type AllTime struct {
	MinimumDays      int             `json:"minimum_days"`
	MinimumQuestions int             `json:"minimum_questions"`
	DaysPlayed       int             `json:"days_played"`
	Qualified        bool            `json:"qualified"`
	Friends          []AllTimeEntry  `json:"friends"`
	Global           GlobalStanding  `json:"global"`
	Topics           []TopicStanding `json:"topics"`
}

// allTimeScoresQuery returns one row per answer of every counted run, for every
// account. Scoring and aggregation happen in Go so play.Points stays the only
// place the rule is written -- the same division /api/history makes.
//
// It carries no names and no addresses. Identity is fetched separately, for the
// viewer and their friends only, so this endpoint structurally cannot send a
// stranger's nickname even if the ranking code asked it to.
const allTimeScoresQuery = `
WITH ` + userRunsCTE + `
SELECT ur.user_id, ur.puzzle_date, t.slug, t.name, q.difficulty, ra.outcome
  FROM user_runs ur
  JOIN run_answers ra ON ra.run_id = ur.run_id
  JOIN questions q ON q.id = ra.question_id
  JOIN topics t ON t.id = q.topic_id`

// allTimePeopleQuery names the viewer and their friends, and nobody else.
const allTimePeopleQuery = `
SELECT u.id, u.email, fi.nickname
  FROM users u
  LEFT JOIN friend_invites fi ON fi.user_id = u.id
 WHERE u.id = $1
    OR u.id IN (
        SELECT CASE WHEN f.user_low = $1 THEN f.user_high ELSE f.user_low END
          FROM friendships f
         WHERE f.user_low = $1 OR f.user_high = $1)`

// tally is one account's running totals while the rows are read.
type tally struct {
	days   map[string]bool
	points int
	topics map[string]*topicTally
}

type topicTally struct {
	name   string
	asked  int
	points int
}

func (t *tally) average() float64 {
	if len(t.days) == 0 {
		return 0
	}
	return float64(t.points) / float64(len(t.days))
}

func (tt *topicTally) average() float64 {
	if tt.asked == 0 {
		return 0
	}
	return float64(tt.points) / float64(tt.asked)
}

func (s *Server) allTime(w http.ResponseWriter, r *http.Request) {
	if !s.friendsAvailable(w) {
		return
	}
	user, ok := s.requireSignedIn(w, r)
	if !ok {
		return
	}

	tallies, err := s.allTimeTallies(r)
	if err != nil {
		s.internal(w, err)
		return
	}
	names, friendIDs, err := s.allTimeNames(r, user.ID)
	if err != nil {
		s.internal(w, err)
		return
	}

	me := tallies[user.ID]
	if me == nil {
		me = newTally()
	}
	out := AllTime{
		MinimumDays:      MinimumDays,
		MinimumQuestions: MinimumTopicQuestions,
		DaysPlayed:       len(me.days),
		Qualified:        len(me.days) >= MinimumDays,
		Friends:          []AllTimeEntry{},
		Topics:           []TopicStanding{},
	}

	// The friends list is the viewer plus their friends, which is what makes it
	// a standing rather than a list of other people.
	circle := append([]int64{user.ID}, friendIDs...)
	for _, id := range circle {
		t := tallies[id]
		if t == nil || len(t.days) < MinimumDays {
			continue
		}
		out.Friends = append(out.Friends, AllTimeEntry{
			UserID:        id,
			Nickname:      names[id],
			DaysPlayed:    len(t.days),
			AveragePoints: round2(t.average()),
			You:           id == user.ID,
		})
	}
	sort.SliceStable(out.Friends, func(i, j int) bool {
		if out.Friends[i].AveragePoints != out.Friends[j].AveragePoints {
			return out.Friends[i].AveragePoints > out.Friends[j].AveragePoints
		}
		// More days at the same average is the stronger record, then name so
		// the list is stable between identical requests.
		if out.Friends[i].DaysPlayed != out.Friends[j].DaysPlayed {
			return out.Friends[i].DaysPlayed > out.Friends[j].DaysPlayed
		}
		return out.Friends[i].Nickname < out.Friends[j].Nickname
	})
	for i := range out.Friends {
		out.Friends[i].Rank = rankAt(i, func(n int) float64 { return out.Friends[n].AveragePoints },
			func(n int) int { return out.Friends[n].Rank })
	}

	out.Global = globalStanding(tallies, user.ID, func(t *tally) (float64, bool) {
		return t.average(), len(t.days) >= MinimumDays
	})
	out.Topics = s.topicStandings(tallies, names, circle, user.ID)
	s.write(w, http.StatusOK, out)
}

func newTally() *tally {
	return &tally{days: map[string]bool{}, topics: map[string]*topicTally{}}
}

// allTimeTallies reads every counted answer and folds it into one tally per
// account.
func (s *Server) allTimeTallies(r *http.Request) (map[int64]*tally, error) {
	rows, err := s.Pool.Query(r.Context(), allTimeScoresQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]*tally{}
	for rows.Next() {
		var userID int64
		var date clock.Date
		var slug, name string
		var difficulty content.Difficulty
		var outcome *play.Outcome
		if err := rows.Scan(&userID, &date, &slug, &name, &difficulty, &outcome); err != nil {
			return nil, err
		}
		t := out[userID]
		if t == nil {
			t = newTally()
			out[userID] = t
		}
		// A finished run resolves every question, so a day is counted from the
		// row rather than from the outcome being non-null.
		t.days[date.String()] = true
		topic := t.topics[slug]
		if topic == nil {
			topic = &topicTally{name: name}
			t.topics[slug] = topic
		}
		topic.asked++
		if outcome == nil {
			continue
		}
		points := play.Points(difficulty, *outcome)
		t.points += points
		topic.points += points
	}
	return out, rows.Err()
}

// allTimeNames resolves display names for the viewer and their friends. It
// returns the friend ids in a stable order so the caller's list does not depend
// on map iteration.
func (s *Server) allTimeNames(r *http.Request, viewer int64) (map[int64]string, []int64, error) {
	rows, err := s.Pool.Query(r.Context(), allTimePeopleQuery, viewer)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	names := map[int64]string{}
	var friendIDs []int64
	for rows.Next() {
		var id int64
		var email string
		var nickname *string
		if err := rows.Scan(&id, &email, &nickname); err != nil {
			return nil, nil, err
		}
		name := friends.DefaultNickname(email)
		if nickname != nil {
			name = *nickname
		}
		names[id] = name
		if id != viewer {
			friendIDs = append(friendIDs, id)
		}
	}
	sort.Slice(friendIDs, func(i, j int) bool { return friendIDs[i] < friendIDs[j] })
	return names, friendIDs, rows.Err()
}

// topicStandings builds one section per category the viewer has seen, strongest
// first, with the categories they cannot be ranked in yet at the end.
func (s *Server) topicStandings(tallies map[int64]*tally, names map[int64]string, circle []int64, viewer int64) []TopicStanding {
	me := tallies[viewer]
	if me == nil {
		return []TopicStanding{}
	}
	out := make([]TopicStanding, 0, len(me.topics))
	for slug, mine := range me.topics {
		standing := TopicStanding{
			Slug:    slug,
			Name:    mine.name,
			Asked:   mine.asked,
			Ranked:  mine.asked >= MinimumTopicQuestions,
			Friends: []TopicEntry{},
		}
		for _, id := range circle {
			t := tallies[id]
			if t == nil {
				continue
			}
			theirs := t.topics[slug]
			if theirs == nil || theirs.asked < MinimumTopicQuestions {
				continue
			}
			standing.Friends = append(standing.Friends, TopicEntry{
				UserID:        id,
				Nickname:      names[id],
				Asked:         theirs.asked,
				AveragePoints: round2(theirs.average()),
				You:           id == viewer,
			})
		}
		sort.SliceStable(standing.Friends, func(i, j int) bool {
			if standing.Friends[i].AveragePoints != standing.Friends[j].AveragePoints {
				return standing.Friends[i].AveragePoints > standing.Friends[j].AveragePoints
			}
			if standing.Friends[i].Asked != standing.Friends[j].Asked {
				return standing.Friends[i].Asked > standing.Friends[j].Asked
			}
			return standing.Friends[i].Nickname < standing.Friends[j].Nickname
		})
		for i := range standing.Friends {
			standing.Friends[i].Rank = rankAt(i, func(n int) float64 { return standing.Friends[n].AveragePoints },
				func(n int) int { return standing.Friends[n].Rank })
		}
		standing.Global = globalStanding(tallies, viewer, func(t *tally) (float64, bool) {
			tt := t.topics[slug]
			if tt == nil || tt.asked < MinimumTopicQuestions {
				return 0, false
			}
			return tt.average(), true
		})
		out = append(out, standing)
	}

	// Strongest first, which is the interesting read of the section. A category
	// the viewer cannot be ranked in yet sorts last however good the sample
	// looks, then by name so the order is stable.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Ranked != b.Ranked {
			return a.Ranked
		}
		am, bm := me.topics[a.Slug].average(), me.topics[b.Slug].average()
		if am != bm {
			return am > bm
		}
		return a.Name < b.Name
	})
	return out
}

// globalStanding places the viewer among every account that qualifies, by
// whatever measure the caller supplies. It returns counts and a position, never
// an identity.
func globalStanding(tallies map[int64]*tally, viewer int64, measure func(*tally) (float64, bool)) GlobalStanding {
	mine, ok := 0.0, false
	if t := tallies[viewer]; t != nil {
		mine, ok = measure(t)
	}
	out := GlobalStanding{Ranked: ok}
	best := 0.0
	for id, t := range tallies {
		value, counts := measure(t)
		if !counts {
			continue
		}
		out.Of++
		if value > best {
			best = value
		}
		// Strictly greater, so equal averages share a rank rather than being
		// ordered by whichever account id happened to come first.
		if ok && value > mine && id != viewer {
			out.Rank++
		}
	}
	out.BestAverage = round2(best)
	if ok {
		out.Rank++ // a rank is a position, not a count of people ahead
	}
	return out
}

// rankAt gives equal scores equal rank and leaves the gap after them: 1, 2, 2,
// 4. Ranking ties apart would invent a difference the numbers do not support.
//
// The list must already be sorted by value. It reads the previous entry's rank
// rather than recomputing, so a run of ties all carry the first one's number.
func rankAt(i int, value func(int) float64, rank func(int) int) int {
	if i > 0 && value(i) == value(i-1) {
		return rank(i - 1)
	}
	return i + 1
}

// round2 keeps averages to two decimals. They are compared for equality when
// ranking, so they are rounded once here rather than differently by each
// client.
func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
