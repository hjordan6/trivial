package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func (sf *statsFixture) history(t *testing.T, cookies ...*http.Cookie) History {
	t.Helper()
	res := sf.do(t, http.MethodGet, "/api/history", nil, cookies...)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /api/history status = %d: %s", res.Code, res.Body.String())
	}
	var got History
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// The scoring rule is the whole point of the endpoint: a typed answer is worth
// its difficulty plus the bonus, a chosen one the difficulty alone, and a miss
// or an expiry nothing at all.
func TestHistoryScoresEachDay(t *testing.T) {
	sf := newStatsFixture(t, 2)
	laptop := sf.player(t, false)
	base := sf.server.now()

	// easy typed (3+2), medium chosen (4), hard missed (0).
	sf.completeRunWith(t, laptop, sf.dates[0], base, []answerRow{
		{sf.mixed[0].id, "star"},
		{sf.mixed[1].id, "circle"},
		{sf.mixed[2].id, "miss"},
	})
	// easy expired (0), medium typed (4+2), hard typed (5+2).
	sf.completeRunWith(t, laptop, sf.dates[1], base.Add(24*time.Hour), []answerRow{
		{sf.mixed[0].id, "expired"},
		{sf.mixed[1].id, "star"},
		{sf.mixed[2].id, "star"},
	})

	got := sf.history(t, sf.playerCookie(laptop))
	if len(got.Days) != 2 {
		t.Fatalf("Days = %+v, want two days", got.Days)
	}
	// Oldest first: the client reverses for display, and streak-shaped reads
	// depend on the order being the same as /api/stats.
	if got.Days[0].Date.String() != sf.dates[0] || got.Days[1].Date.String() != sf.dates[1] {
		t.Errorf("dates = %s, %s, want %s, %s oldest first",
			got.Days[0].Date, got.Days[1].Date, sf.dates[0], sf.dates[1])
	}
	if got.Days[0].Points != 9 {
		t.Errorf("day one points = %d, want 9 (easy typed 5 + medium chosen 4)", got.Days[0].Points)
	}
	if got.Days[0].Correct != 2 || got.Days[0].Typed != 1 {
		t.Errorf("day one correct/typed = %d/%d, want 2/1", got.Days[0].Correct, got.Days[0].Typed)
	}
	if got.Days[1].Points != 13 {
		t.Errorf("day two points = %d, want 13 (medium typed 6 + hard typed 7)", got.Days[1].Points)
	}
	if got.Days[1].Typed != 2 {
		t.Errorf("day two typed = %d, want 2", got.Days[1].Typed)
	}
}

// The page ranks categories, so every category the viewer has met has to come
// back with its own totals -- and only its own.
func TestHistoryTotalsEachCategorySeparately(t *testing.T) {
	sf := newStatsFixture(t, 2)
	laptop := sf.player(t, false)
	base := sf.server.now()

	// Day one: three easy questions from the nine-question category, two typed.
	sf.completeRunWith(t, laptop, sf.dates[0], base, []answerRow{
		{sf.questionID[0], "star"},
		{sf.questionID[1], "star"},
		{sf.questionID[2], "miss"},
	})
	// Day two: the mixed category, one right.
	sf.completeRunWith(t, laptop, sf.dates[1], base.Add(24*time.Hour), []answerRow{
		{sf.mixed[0].id, "circle"},
		{sf.mixed[1].id, "miss"},
		{sf.mixed[2].id, "expired"},
	})

	got := sf.history(t, sf.playerCookie(laptop))
	if len(got.Topics) != 2 {
		t.Fatalf("Topics = %+v, want one row per category", got.Topics)
	}
	bySlug := map[string]HistoryTopic{}
	for _, topic := range got.Topics {
		bySlug[topic.Slug] = topic
	}
	easy := bySlug[sf.slug]
	if easy.Asked != 3 || easy.Correct != 2 || easy.Points != 10 {
		t.Errorf("easy category = %+v, want asked 3, correct 2, points 10", easy)
	}
	mixed := bySlug[sf.mixedSlug]
	if mixed.Asked != 3 || mixed.Correct != 1 || mixed.Points != 3 {
		t.Errorf("mixed category = %+v, want asked 3, correct 1, points 3", mixed)
	}
}

// History reads days through the same CTE as stats, so it inherits the rule that
// a signed-in person's history spans their browsers and counts a doubled-up day
// once. This guards the wiring, not the CTE.
func TestHistorySpansASignedInUsersPlayers(t *testing.T) {
	sf := newStatsFixture(t, 2)
	laptop := sf.player(t, true)
	phone := sf.player(t, true)
	base := sf.server.now()

	sf.completeRunWith(t, laptop, sf.dates[0], base, []answerRow{{sf.mixed[0].id, "star"}})
	// The same date on two browsers. The run started first is the one that counts.
	sf.completeRunWith(t, laptop, sf.dates[1], base.Add(time.Hour), []answerRow{{sf.mixed[0].id, "circle"}})
	sf.completeRunWith(t, phone, sf.dates[1], base.Add(2*time.Hour), []answerRow{{sf.mixed[2].id, "star"}})

	got := sf.history(t, sf.playerCookie(laptop), sf.sessionCookie())
	if len(got.Days) != 2 {
		t.Fatalf("Days = %+v, want the shared day counted once", got.Days)
	}
	if got.Days[1].Points != 3 {
		t.Errorf("shared day points = %d, want 3 from the earlier run", got.Days[1].Points)
	}

	signedOut := sf.history(t, sf.playerCookie(phone))
	if len(signedOut.Days) != 1 || signedOut.Days[0].Points != 7 {
		t.Errorf("signed out history = %+v, want the phone's day alone", signedOut.Days)
	}
}

// A visitor with no cookie, and one with a junk cookie, both get an empty
// history rather than an error or a null list.
func TestHistoryWithoutAPlayer(t *testing.T) {
	sf := newStatsFixture(t, 1)

	for _, cookies := range [][]*http.Cookie{
		nil,
		{{Name: playerCookie, Value: "not-a-uuid-at-all"}},
	} {
		got := sf.history(t, cookies...)
		if len(got.Days) != 0 || len(got.Topics) != 0 {
			t.Errorf("history = %+v, want empty", got)
		}
	}
	body := sf.do(t, http.MethodGet, "/api/history", nil).Body.String()
	if !strings.Contains(body, `"days":[]`) || !strings.Contains(body, `"topics":[]`) {
		t.Errorf("body = %s, want empty arrays rather than nulls", body)
	}
}
