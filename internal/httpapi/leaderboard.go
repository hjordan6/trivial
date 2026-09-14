package httpapi

import (
	"net/http"
	"sort"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/friends"
	"github.com/hjordan6/trivial/internal/play"
)

// FriendAnswer is one question of somebody's board as a friend may see it: the
// outcome, and nothing else.
//
// Deliberately not the submission and not the canonical answer. The board is
// the same nine questions for everyone, so a viewer who has not played yet can
// open this; returning what a friend typed, or what the right answer was, would
// turn the leaderboard into a way to read today's answers off someone who
// finished earlier.
type FriendAnswer struct {
	QuestionID int64        `json:"question_id"`
	Outcome    play.Outcome `json:"outcome"`
}

// FriendToday is one person's finished board for a date.
//
// Played distinguishes "no completed run" from "a completed run that scored
// zero", which Points alone cannot: both are 0. The list renders them
// differently, so the distinction has to survive the wire.
type FriendToday struct {
	UserID   int64          `json:"user_id"`
	Nickname string         `json:"nickname"`
	Played   bool           `json:"played"`
	Correct  int            `json:"correct"`
	Typed    int            `json:"typed"`
	Points   int            `json:"points"`
	Answers  []FriendAnswer `json:"answers"`
	// You marks the viewer's own row. They are ranked inside the list rather
	// than pinned above it, the same way the all-time board places them: a
	// friend who beat you today belongs above you, which is the whole point of
	// looking.
	You bool `json:"you"`
}

// FriendsToday is the whole board: the viewer, and everyone they are friends
// with, for one date.
//
// You is sent even though the client could score its own run, so that every
// number on the leaderboard comes from one scorer. A client-scored "you" beside
// server-scored friends is a disagreement waiting to happen the first time the
// two implementations drift.
//
// Friends carries each person's per-question outcomes, not just their totals,
// so opening the side-by-side costs no second request. Nine outcomes per friend
// is small enough that fetching them up front beats a round trip per tap.
//
// It includes the viewer, marked with You. The separate You field is kept
// because the side-by-side needs the viewer's own board without searching the
// list for it.
type FriendsToday struct {
	Date    clock.Date    `json:"date"`
	You     FriendToday   `json:"you"`
	Friends []FriendToday `json:"friends"`
}

// friendsTodayQuery returns one row per answer for the viewer and each of their
// friends, plus a single null-answer row for anyone with no completed run.
//
// `people` is the viewer unioned with the other side of every friendship they
// appear in. friendships stores one row per pair in canonical order, so the
// CASE is what turns that into "the other person" regardless of which column
// the viewer landed in.
//
// The run each person is scored on comes from userRunsCTE, so a friend who
// played on two browsers is scored on the run their own history page also
// counts, and today's board can never disagree with the all-time one.
//
// Scoring stays in Go rather than a CASE expression here, so play.Points
// remains the only place the game's scoring rule is written down.
const friendsTodayQuery = `
WITH ` + userRunsCTE + `, people AS (
    SELECT $1::bigint AS user_id
    UNION
    SELECT CASE WHEN f.user_low = $1 THEN f.user_high ELSE f.user_low END
      FROM friendships f
     WHERE f.user_low = $1 OR f.user_high = $1
), chosen AS (
    SELECT ur.user_id, ur.run_id
      FROM user_runs ur
     WHERE ur.puzzle_date = $2
       AND ur.user_id IN (SELECT user_id FROM people)
)
SELECT pe.user_id,
       u.email,
       fi.nickname,
       c.run_id IS NOT NULL,
       ra.question_id,
       q.difficulty,
       ra.outcome
  FROM people pe
  JOIN users u ON u.id = pe.user_id
  LEFT JOIN friend_invites fi ON fi.user_id = pe.user_id
  LEFT JOIN chosen c ON c.user_id = pe.user_id
  LEFT JOIN run_answers ra ON ra.run_id = c.run_id
  LEFT JOIN questions q ON q.id = ra.question_id
 ORDER BY pe.user_id, ra.question_id`

// friendsToday serves today's leaderboard.
//
// Sign-in is required rather than optional: a leaderboard is a list of people
// you know, and an anonymous browser has no friendships to read. That also
// keeps the authorization trivial -- the query only ever walks out from the
// caller's own user id, so there is no id in the request that could be
// tampered with to read a stranger's board.
func (s *Server) friendsToday(w http.ResponseWriter, r *http.Request) {
	if !s.friendsAvailable(w) {
		return
	}
	user, ok := s.requireSignedIn(w, r)
	if !ok {
		return
	}
	date := s.date(s.now())

	rows, err := s.Pool.Query(r.Context(), friendsTodayQuery, user.ID, date)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()

	// Rows arrive grouped by user id, so a new id is always a new person.
	byUser := map[int64]*FriendToday{}
	var order []int64
	for rows.Next() {
		var userID int64
		var email string
		var nickname *string
		var played bool
		var questionID *int64
		var difficulty *content.Difficulty
		var outcome *play.Outcome
		if err := rows.Scan(&userID, &email, &nickname, &played, &questionID, &difficulty, &outcome); err != nil {
			s.internal(w, err)
			return
		}

		person := byUser[userID]
		if person == nil {
			// The name a person chose for themselves when they last minted a
			// friend link, falling back to the same local-part rule the invite
			// page uses. Nobody's address is ever sent.
			name := friends.DefaultNickname(email)
			if nickname != nil {
				name = *nickname
			}
			person = &FriendToday{UserID: userID, Nickname: name, Played: played, Answers: []FriendAnswer{}}
			byUser[userID] = person
			order = append(order, userID)
		}
		if questionID == nil || outcome == nil || difficulty == nil {
			continue
		}
		person.Answers = append(person.Answers, FriendAnswer{QuestionID: *questionID, Outcome: *outcome})
		person.Points += play.Points(*difficulty, *outcome)
		if *outcome == play.Star || *outcome == play.Circle {
			person.Correct++
		}
		if *outcome == play.Star {
			person.Typed++
		}
	}
	if err := rows.Err(); err != nil {
		s.internal(w, err)
		return
	}

	out := FriendsToday{Date: date, Friends: []FriendToday{}}
	// A viewer with no completed run still has a row in `people`, so this is
	// only nil if the caller's own user row vanished mid-request.
	if you := byUser[user.ID]; you != nil {
		you.You = true
		out.You = *you
	} else {
		out.You = FriendToday{UserID: user.ID, Answers: []FriendAnswer{}, You: true}
	}
	for _, id := range order {
		out.Friends = append(out.Friends, *byUser[id])
	}
	if byUser[user.ID] == nil {
		out.Friends = append(out.Friends, out.You)
	}

	// Ranked here rather than in the client because the ordering is part of
	// what a leaderboard means. Anyone who has not played sorts last whatever
	// their zero looks like, then points, then the tiebreaks, then name so the
	// list is stable between identical requests.
	sort.SliceStable(out.Friends, func(i, j int) bool {
		a, b := out.Friends[i], out.Friends[j]
		if a.Played != b.Played {
			return a.Played
		}
		if a.Points != b.Points {
			return a.Points > b.Points
		}
		if a.Correct != b.Correct {
			return a.Correct > b.Correct
		}
		return a.Nickname < b.Nickname
	})
	s.write(w, http.StatusOK, out)
}
