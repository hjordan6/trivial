package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// leaderboardFixture is a stats fixture plus the other people a leaderboard
// needs: friends, strangers, and the friendships between them.
type leaderboardFixture struct {
	*statsFixture
	today string
}

func newLeaderboardFixture(t *testing.T) *leaderboardFixture {
	t.Helper()
	sf := newStatsFixture(t, 1)
	return &leaderboardFixture{statsFixture: sf, today: sf.dates[len(sf.dates)-1]}
}

// user creates an account and returns its id. The address carries the auth
// fixture's prefix, so its cleanup removes it and the friendships cascade.
func (lf *leaderboardFixture) user(t *testing.T, local string) int64 {
	t.Helper()
	var id int64
	err := lf.pool.QueryRow(context.Background(),
		`INSERT INTO users(email,created_at) VALUES($1,$2) RETURNING id`,
		lf.email(local), lf.now).Scan(&id)
	if err != nil {
		t.Fatalf("insert user %s: %v", local, err)
	}
	return id
}

// befriend records the friendship in the canonical order the CHECK demands.
func (lf *leaderboardFixture) befriend(t *testing.T, a, b int64) {
	t.Helper()
	low, high := a, b
	if low > high {
		low, high = high, low
	}
	if _, err := lf.pool.Exec(context.Background(),
		`INSERT INTO friendships(user_low,user_high,created_at) VALUES($1,$2,$3)`,
		low, high, lf.now); err != nil {
		t.Fatalf("insert friendship: %v", err)
	}
}

// playerFor attaches a fresh browser to an arbitrary user, which statsFixture's
// own player() cannot do -- it only knows its own.
func (lf *leaderboardFixture) playerFor(t *testing.T, userID int64) string {
	t.Helper()
	var id string
	if err := lf.pool.QueryRow(context.Background(),
		`INSERT INTO players(created_at,last_seen_at,user_id) VALUES($1,$1,$2) RETURNING id`,
		lf.now, userID).Scan(&id); err != nil {
		t.Fatalf("insert player: %v", err)
	}
	return id
}

// scoreMixed finishes a run on the three mixed-difficulty questions, so points
// and correct-count are not the same number and a test can tell which one the
// server reported.
func (lf *leaderboardFixture) scoreMixed(t *testing.T, playerID string, outcomes ...string) {
	t.Helper()
	answers := make([]answerRow, 0, len(lf.mixed))
	for i, q := range lf.mixed {
		answers = append(answers, answerRow{q.id, outcomes[i]})
	}
	lf.completeRunWith(t, playerID, lf.today, lf.now, answers)
}

func (lf *leaderboardFixture) sessionFor(userID int64) *http.Cookie {
	return &http.Cookie{
		Name:  sessionCookie,
		Value: signExpiring(lf.server.appKey(), sessionCookie, fmt.Sprintf("%d", userID), lf.server.now().Add(time.Hour)),
	}
}

func (lf *leaderboardFixture) get(t *testing.T, cookies ...*http.Cookie) FriendsToday {
	t.Helper()
	res := lf.do(t, http.MethodGet, "/api/friends/today", nil, cookies...)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /api/friends/today status = %d: %s", res.Code, res.Body.String())
	}
	var got FriendsToday
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// The headline case: the viewer sees each friend once, scored by difficulty
// rather than by count, ranked best first.
func TestFriendsTodayRanksFriendsByPoints(t *testing.T) {
	lf := newLeaderboardFixture(t)
	viewer := lf.userID
	ahead := lf.user(t, "ahead")
	behind := lf.user(t, "behind")
	lf.befriend(t, viewer, ahead)
	lf.befriend(t, viewer, behind)

	// easy=3, medium=4, hard=5, plus 2 for typing it.
	lf.scoreMixed(t, lf.player(t, true), "star", "miss", "miss")            // viewer: 5
	lf.scoreMixed(t, lf.playerFor(t, ahead), "star", "star", "star")        // 5+6+7 = 18
	lf.scoreMixed(t, lf.playerFor(t, behind), "circle", "circle", "circle") // 3+4+5 = 12

	got := lf.get(t, lf.playerCookie(lf.player(t, true)), lf.sessionFor(viewer))

	if got.Date.String() != lf.today {
		t.Errorf("date = %s, want %s", got.Date, lf.today)
	}
	if got.You.Points != 5 || got.You.Correct != 1 {
		t.Errorf("you = %d pts / %d correct, want 5/1", got.You.Points, got.You.Correct)
	}
	if len(got.Friends) != 2 {
		t.Fatalf("friends = %d, want 2", len(got.Friends))
	}
	if got.Friends[0].UserID != ahead || got.Friends[0].Points != 18 {
		t.Errorf("first = user %d with %d pts, want user %d with 18", got.Friends[0].UserID, got.Friends[0].Points, ahead)
	}
	if got.Friends[0].Correct != 3 || got.Friends[0].Typed != 3 {
		t.Errorf("first = %d correct / %d typed, want 3/3", got.Friends[0].Correct, got.Friends[0].Typed)
	}
	// Every answer picked off the list, so correct and typed have to differ.
	if got.Friends[1].Points != 12 || got.Friends[1].Correct != 3 || got.Friends[1].Typed != 0 {
		t.Errorf("second = %d pts / %d correct / %d typed, want 12/3/0",
			got.Friends[1].Points, got.Friends[1].Correct, got.Friends[1].Typed)
	}
	if len(got.Friends[0].Answers) != 3 {
		t.Errorf("answers = %d, want 3 for the side-by-side", len(got.Friends[0].Answers))
	}
}

// A friendship is the whole authorization rule, so somebody who played today
// but is not a friend must not appear.
func TestFriendsTodayOmitsStrangers(t *testing.T) {
	lf := newLeaderboardFixture(t)
	stranger := lf.user(t, "stranger")
	lf.scoreMixed(t, lf.playerFor(t, stranger), "star", "star", "star")

	got := lf.get(t, lf.playerCookie(lf.player(t, true)), lf.sessionFor(lf.userID))

	if len(got.Friends) != 0 {
		t.Errorf("friends = %+v, want none", got.Friends)
	}
}

// A friend who has not finished today is listed, not hidden: the point of the
// list is who has played and who has not.
func TestFriendsTodayListsAFriendWhoHasNotPlayed(t *testing.T) {
	lf := newLeaderboardFixture(t)
	idle := lf.user(t, "idle")
	lf.befriend(t, lf.userID, idle)

	got := lf.get(t, lf.playerCookie(lf.player(t, true)), lf.sessionFor(lf.userID))

	if len(got.Friends) != 1 {
		t.Fatalf("friends = %d, want 1", len(got.Friends))
	}
	if got.Friends[0].Played {
		t.Error("played = true, want false for a friend with no completed run")
	}
	if got.Friends[0].Points != 0 || len(got.Friends[0].Answers) != 0 {
		t.Errorf("unplayed friend = %d pts / %d answers, want 0/0", got.Friends[0].Points, len(got.Friends[0].Answers))
	}
}

// An unfinished run is not a result. It must not be scored as a partial one,
// which would let a friend's score go down as they play.
func TestFriendsTodayIgnoresAnUnfinishedRun(t *testing.T) {
	lf := newLeaderboardFixture(t)
	playing := lf.user(t, "playing")
	lf.befriend(t, lf.userID, playing)

	playerID := lf.playerFor(t, playing)
	if _, err := lf.pool.Exec(context.Background(),
		`INSERT INTO runs(player_id,puzzle_date,started_at,expires_at,option_seed) VALUES($1,$2,$3,$4,1)`,
		playerID, lf.today, lf.now, lf.now.Add(240*time.Second)); err != nil {
		t.Fatalf("insert run: %v", err)
	}

	got := lf.get(t, lf.playerCookie(lf.player(t, true)), lf.sessionFor(lf.userID))

	if len(got.Friends) != 1 {
		t.Fatalf("friends = %d, want 1", len(got.Friends))
	}
	if got.Friends[0].Played {
		t.Error("played = true, want false while the run is still open")
	}
}

func TestFriendsTodayRequiresSignIn(t *testing.T) {
	lf := newLeaderboardFixture(t)

	res := lf.do(t, http.MethodGet, "/api/friends/today", nil, lf.playerCookie(lf.player(t, false)))

	if res.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401: %s", res.Code, res.Body.String())
	}
}
