package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// allTime drives the endpoint as the fixture's own user.
func (lf *leaderboardFixture) allTime(t *testing.T, cookies ...*http.Cookie) AllTime {
	t.Helper()
	res := lf.do(t, http.MethodGet, "/api/friends/all-time", nil, cookies...)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /api/friends/all-time status = %d: %s", res.Code, res.Body.String())
	}
	var got AllTime
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// playDays finishes `days` consecutive dates for one player, every answer typed
// on the three mixed-difficulty questions -- 5+6+7 = 18 points a day.
func (lf *leaderboardFixture) playDays(t *testing.T, playerID string, days int) {
	t.Helper()
	for i := 0; i < days; i++ {
		answers := make([]answerRow, 0, len(lf.mixed))
		for _, q := range lf.mixed {
			answers = append(answers, answerRow{q.id, "star"})
		}
		lf.completeRunWith(t, playerID, lf.dates[i], lf.now.Add(time.Duration(i)*time.Minute), answers)
	}
}

// newAllTimeFixture gives the fixture enough consecutive dates to cross the
// three-day floor and then some.
func newAllTimeFixture(t *testing.T) *leaderboardFixture {
	t.Helper()
	sf := newStatsFixture(t, 6)
	return &leaderboardFixture{statsFixture: sf, today: sf.dates[len(sf.dates)-1]}
}

// The floor is the whole reason the average means anything: one big day must
// not outrank a sustained record.
func TestAllTimeExcludesAccountsUnderTheDayFloor(t *testing.T) {
	lf := newAllTimeFixture(t)
	steady := lf.user(t, "steady")
	oneday := lf.user(t, "oneday")
	lf.befriend(t, lf.userID, steady)
	lf.befriend(t, lf.userID, oneday)

	lf.playDays(t, lf.player(t, true), 4)      // viewer: 4 days
	lf.playDays(t, lf.playerFor(t, steady), 3) // friend: exactly at the floor
	lf.playDays(t, lf.playerFor(t, oneday), 2) // friend: one short of it

	got := lf.allTime(t, lf.playerCookie(lf.player(t, true)), lf.sessionFor(lf.userID))

	if got.MinimumDays != 3 {
		t.Errorf("minimum_days = %d, want 3", got.MinimumDays)
	}
	if !got.Qualified || got.DaysPlayed != 4 {
		t.Errorf("viewer qualified=%v days=%d, want true/4", got.Qualified, got.DaysPlayed)
	}
	for _, e := range got.Friends {
		if e.UserID == oneday {
			t.Errorf("an account with 2 days appeared: %+v", e)
		}
	}
	if len(got.Friends) != 2 {
		t.Fatalf("friends = %d, want the viewer and the one friend at the floor", len(got.Friends))
	}
}

// The viewer is in the list, not beside it: the question is where they place.
func TestAllTimeRanksTheViewerInsideTheFriendsList(t *testing.T) {
	lf := newAllTimeFixture(t)
	rival := lf.user(t, "rival")
	lf.befriend(t, lf.userID, rival)

	lf.playDays(t, lf.player(t, true), 3)
	// Same three days, but one is a wipeout, so the average is lower.
	rivalPlayer := lf.playerFor(t, rival)
	for i := 0; i < 3; i++ {
		outcome := "star"
		if i == 0 {
			outcome = "miss"
		}
		answers := make([]answerRow, 0, len(lf.mixed))
		for _, q := range lf.mixed {
			answers = append(answers, answerRow{q.id, outcome})
		}
		lf.completeRunWith(t, rivalPlayer, lf.dates[i], lf.now.Add(time.Duration(i)*time.Minute), answers)
	}

	got := lf.allTime(t, lf.playerCookie(lf.player(t, true)), lf.sessionFor(lf.userID))

	if len(got.Friends) != 2 {
		t.Fatalf("friends = %d, want 2", len(got.Friends))
	}
	if !got.Friends[0].You || got.Friends[0].Rank != 1 {
		t.Errorf("first = %+v, want the viewer at rank 1", got.Friends[0])
	}
	if got.Friends[0].AveragePoints != 18 {
		t.Errorf("viewer average = %v, want 18", got.Friends[0].AveragePoints)
	}
	if got.Friends[1].Rank != 2 || got.Friends[1].You {
		t.Errorf("second = %+v, want the rival at rank 2", got.Friends[1])
	}
}

// Equal averages share a rank, and the next place is left empty: 1, 1, 3.
func TestAllTimeGivesTiedAveragesTheSameRank(t *testing.T) {
	lf := newAllTimeFixture(t)
	twin := lf.user(t, "twin")
	lf.befriend(t, lf.userID, twin)

	lf.playDays(t, lf.player(t, true), 3)
	lf.playDays(t, lf.playerFor(t, twin), 3)

	got := lf.allTime(t, lf.playerCookie(lf.player(t, true)), lf.sessionFor(lf.userID))

	if len(got.Friends) != 2 {
		t.Fatalf("friends = %d, want 2", len(got.Friends))
	}
	if got.Friends[0].Rank != 1 || got.Friends[1].Rank != 1 {
		t.Errorf("ranks = %d,%d, want 1,1 for identical averages", got.Friends[0].Rank, got.Friends[1].Rank)
	}
}

// The everyone scope reports a position and a field size. It must never carry a
// name: the nickname fallback is the local part of an email address.
func TestAllTimeGlobalStandingNamesNobody(t *testing.T) {
	lf := newAllTimeFixture(t)
	stranger := lf.user(t, "stranger")
	lf.playDays(t, lf.playerFor(t, stranger), 3)
	lf.playDays(t, lf.player(t, true), 3)

	res := lf.do(t, http.MethodGet, "/api/friends/all-time", nil,
		lf.playerCookie(lf.player(t, true)), lf.sessionFor(lf.userID))
	body := res.Body.String()

	var got AllTime
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Global.Ranked || got.Global.Of < 2 {
		t.Errorf("global = %+v, want the viewer ranked in a field of at least 2", got.Global)
	}
	// The stranger is counted but never named, and is not in the friends list.
	for _, e := range got.Friends {
		if e.UserID == stranger {
			t.Error("a stranger appeared in the friends list")
		}
	}
	if name := lf.email("stranger"); containsAddressPart(body, name) {
		t.Errorf("the response carries a non-friend's address part: %s", name)
	}
}

// containsAddressPart reports whether the local part of an address appears
// anywhere in the payload.
func containsAddressPart(body, email string) bool {
	local := email
	for i, c := range email {
		if c == '@' {
			local = email[:i]
			break
		}
	}
	return local != "" && jsonContains(body, local)
}

func jsonContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestAllTimeRequiresSignIn(t *testing.T) {
	lf := newAllTimeFixture(t)
	res := lf.do(t, http.MethodGet, "/api/friends/all-time", nil, lf.playerCookie(lf.player(t, false)))
	if res.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.Code)
	}
}
