package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// statsFixture builds the smallest world the stats query needs: some dates, some
// questions to hang answers off, and players whose runs can be scored.
//
// It deliberately does not build boards (daily_puzzle_questions), because stats
// reads runs and run_answers only.
type statsFixture struct {
	*authFixture
	dates      []string
	questionID []int64
	// mixed is a second category asked at all three difficulties. Ranking
	// categories against each other, and scoring a day at anything but the easy
	// rate, both need more than the nine easy questions above.
	mixed []mixedQuestion
	// The two category slugs, in the same order as the question lists above.
	slug, mixedSlug string
	userID          int64
}

type mixedQuestion struct {
	id         int64
	difficulty string
}

func newStatsFixture(t *testing.T, days int) *statsFixture {
	t.Helper()
	f := newAuthFixture(t)
	f.server.Timezone = time.UTC
	ctx := context.Background()

	// Dates unique to this test, so nothing collides on daily_puzzles' primary
	// key, and consecutive, so streaks are meaningful. The clock is moved to the
	// last of them, which makes that day "today".
	last := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, int(f.seq)*(days+2))
	sf := &statsFixture{authFixture: f}
	for i := days - 1; i >= 0; i-- {
		sf.dates = append(sf.dates, last.AddDate(0, 0, -i).Format("2006-01-02"))
	}
	f.at(last)

	for _, d := range sf.dates {
		if _, err := f.pool.Exec(ctx, `INSERT INTO daily_puzzles(puzzle_date) VALUES($1)`, d); err != nil {
			t.Fatalf("insert daily_puzzles %s: %v", d, err)
		}
	}

	newQuestion := func(topicID int64, difficulty, prompt string) int64 {
		var qid int64
		rating := map[string]int{"easy": 2, "medium": 6, "hard": 9}[difficulty]
		err := f.pool.QueryRow(ctx,
			`INSERT INTO questions(topic_id,difficulty,difficulty_rating,prompt,canonical_answer,status)
			 VALUES($1,$2,$3,$4,'answer','active') RETURNING id`,
			topicID, difficulty, rating, prompt).Scan(&qid)
		if err != nil {
			t.Fatalf("insert question: %v", err)
		}
		return qid
	}
	newTopic := func(slug string) int64 {
		var id int64
		if err := f.pool.QueryRow(ctx, `INSERT INTO topics(slug,name) VALUES($1,$1) RETURNING id`, slug).Scan(&id); err != nil {
			t.Fatalf("insert topic: %v", err)
		}
		return id
	}

	sf.slug = fmt.Sprintf("stats-%d", f.seq)
	topicID := newTopic(sf.slug)
	// Nine questions, so a run can be scored anywhere from 0 to 9.
	for i := 0; i < 9; i++ {
		sf.questionID = append(sf.questionID, newQuestion(topicID, "easy", fmt.Sprintf("q%d?", i)))
	}

	sf.mixedSlug = fmt.Sprintf("stats-mixed-%d", f.seq)
	mixedTopicID := newTopic(sf.mixedSlug)
	for _, difficulty := range []string{"easy", "medium", "hard"} {
		sf.mixed = append(sf.mixed, mixedQuestion{
			id:         newQuestion(mixedTopicID, difficulty, fmt.Sprintf("mixed-%s?", difficulty)),
			difficulty: difficulty,
		})
	}
	topicIDs := []int64{topicID, mixedTopicID}

	if err := f.pool.QueryRow(ctx,
		`INSERT INTO users(email,created_at) VALUES($1,$2) RETURNING id`,
		f.email("player"), f.now).Scan(&sf.userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	t.Cleanup(func() {
		// Registered after the auth fixture's cleanup, so this runs first.
		// runs and run_answers cascade from players, but daily_puzzles is
		// RESTRICT, so the runs have to go before the dates they point at.
		if _, err := f.pool.Exec(ctx, `DELETE FROM runs WHERE puzzle_date = ANY($1)`, sf.dates); err != nil {
			t.Errorf("cleanup runs: %v", err)
		}
		if _, err := f.pool.Exec(ctx, `DELETE FROM daily_puzzles WHERE puzzle_date = ANY($1)`, sf.dates); err != nil {
			t.Errorf("cleanup daily_puzzles: %v", err)
		}
		if _, err := f.pool.Exec(ctx, `DELETE FROM questions WHERE topic_id = ANY($1)`, topicIDs); err != nil {
			t.Errorf("cleanup questions: %v", err)
		}
		if _, err := f.pool.Exec(ctx, `DELETE FROM topics WHERE id = ANY($1)`, topicIDs); err != nil {
			t.Errorf("cleanup topics: %v", err)
		}
	})
	return sf
}

// player creates a browser, optionally already attached to the fixture's user.
func (sf *statsFixture) player(t *testing.T, attached bool) string {
	t.Helper()
	var id string
	if err := sf.pool.QueryRow(context.Background(),
		`INSERT INTO players(created_at,last_seen_at) VALUES($1,$1) RETURNING id`, sf.now).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if attached {
		if _, err := sf.pool.Exec(context.Background(), `UPDATE players SET user_id=$2 WHERE id=$1`, id, sf.userID); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// answerRow is one resolved question of a finished run.
type answerRow struct {
	questionID int64
	outcome    string
}

// completeRun records a finished run scoring `correct` out of nine, every right
// answer typed.
func (sf *statsFixture) completeRun(t *testing.T, playerID, date string, correct int, startedAt time.Time) {
	t.Helper()
	answers := make([]answerRow, 0, len(sf.questionID))
	for i, qid := range sf.questionID {
		outcome := "miss"
		if i < correct {
			outcome = "star"
		}
		answers = append(answers, answerRow{qid, outcome})
	}
	sf.completeRunWith(t, playerID, date, startedAt, answers)
}

// completeRunWith records a finished run answer by answer, for tests that care
// which question got which outcome.
func (sf *statsFixture) completeRunWith(t *testing.T, playerID, date string, startedAt time.Time, answers []answerRow) {
	t.Helper()
	ctx := context.Background()
	var runID string
	err := sf.pool.QueryRow(ctx,
		`INSERT INTO runs(player_id,puzzle_date,started_at,expires_at,completed_at,option_seed)
		 VALUES($1,$2,$3,$4,$4,1) RETURNING id`,
		playerID, date, startedAt, startedAt.Add(135*time.Second)).Scan(&runID)
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	for _, a := range answers {
		if _, err := sf.pool.Exec(ctx,
			`INSERT INTO run_answers(run_id,question_id,stage,outcome,first_touched_at,resolved_at)
			 VALUES($1,$2,'free_text',$3,$4,$4)`,
			runID, a.questionID, a.outcome, startedAt); err != nil {
			t.Fatalf("insert run_answer: %v", err)
		}
	}
}

func (sf *statsFixture) sessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:  sessionCookie,
		Value: signExpiring(sf.server.appKey(), sessionCookie, fmt.Sprintf("%d", sf.userID), sf.server.now().Add(time.Hour)),
	}
}

func (sf *statsFixture) playerCookie(id string) *http.Cookie {
	return &http.Cookie{Name: playerCookie, Value: sign(sf.server.appKey(), playerCookie, id)}
}

func (sf *statsFixture) stats(t *testing.T, cookies ...*http.Cookie) Stats {
	t.Helper()
	res := sf.do(t, http.MethodGet, "/api/stats", nil, cookies...)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /api/stats status = %d: %s", res.Code, res.Body.String())
	}
	var got Stats
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// TestStatsAggregateAcrossASignedInUsersPlayers is the heart of the feature: a
// person's history has to span every browser they have signed in on, count a day
// they played twice only once, and fall back to this browser alone when signed
// out.
func TestStatsAggregateAcrossASignedInUsersPlayers(t *testing.T) {
	sf := newStatsFixture(t, 3)
	day1, day2, day3 := sf.dates[0], sf.dates[1], sf.dates[2]
	laptop := sf.player(t, true)
	phone := sf.player(t, true)

	base := sf.server.now()
	sf.completeRun(t, laptop, day1, 3, base)
	// day2 was played on both browsers. The earlier run is the one they actually
	// played first, so it is the one that counts.
	sf.completeRun(t, laptop, day2, 5, base.Add(time.Hour))
	sf.completeRun(t, phone, day2, 9, base.Add(2*time.Hour))
	sf.completeRun(t, phone, day3, 7, base.Add(3*time.Hour))

	t.Run("signed in spans both browsers", func(t *testing.T) {
		got := sf.stats(t, sf.playerCookie(laptop), sf.sessionCookie())
		if got.DaysPlayed != 3 {
			t.Errorf("DaysPlayed = %d, want 3 (the shared day counted once)", got.DaysPlayed)
		}
		if got.ScoreDistribution[9] != 0 {
			t.Errorf("the later run for the shared day was counted: distribution[9] = %d, want 0", got.ScoreDistribution[9])
		}
		for _, score := range []int{3, 5, 7} {
			if got.ScoreDistribution[score] != 1 {
				t.Errorf("distribution[%d] = %d, want 1", score, got.ScoreDistribution[score])
			}
		}
		if got.CurrentStreak != 3 || got.LongestStreak != 3 {
			t.Errorf("streak = (%d, %d), want (3, 3) spanning both browsers", got.CurrentStreak, got.LongestStreak)
		}
	})

	t.Run("signed out sees only this browser", func(t *testing.T) {
		got := sf.stats(t, sf.playerCookie(laptop))
		if got.DaysPlayed != 2 {
			t.Errorf("DaysPlayed = %d, want 2", got.DaysPlayed)
		}
		if got.ScoreDistribution[3] != 1 || got.ScoreDistribution[5] != 1 {
			t.Errorf("distribution = %v, want the laptop's two days", got.ScoreDistribution)
		}
		if got.ScoreDistribution[7] != 0 {
			t.Error("the phone's day leaked into a signed-out view")
		}
	})

	t.Run("an unattached browser is unaffected", func(t *testing.T) {
		stranger := sf.player(t, false)
		got := sf.stats(t, sf.playerCookie(stranger), sf.sessionCookie())
		if got.DaysPlayed != 0 {
			t.Errorf("DaysPlayed = %d, want 0: a session must not apply to a browser it does not own", got.DaysPlayed)
		}
	})
}

// An in-flight run must not be counted until it is finished, signed in or not.
func TestStatsIgnoreUnfinishedRuns(t *testing.T) {
	sf := newStatsFixture(t, 1)
	laptop := sf.player(t, true)

	if _, err := sf.pool.Exec(context.Background(),
		`INSERT INTO runs(player_id,puzzle_date,started_at,expires_at,option_seed) VALUES($1,$2,$3,$4,1)`,
		laptop, sf.dates[0], sf.server.now(), sf.server.now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := sf.stats(t, sf.playerCookie(laptop), sf.sessionCookie()); got.DaysPlayed != 0 {
		t.Errorf("DaysPlayed = %d, want 0", got.DaysPlayed)
	}
}

// A junk player cookie used to be interpolated into a uuid column and crash the
// query with a 500. It must read as no cookie at all.
func TestStatsToleratesAJunkPlayerCookie(t *testing.T) {
	sf := newStatsFixture(t, 1)

	res := sf.do(t, http.MethodGet, "/api/stats", nil, &http.Cookie{Name: playerCookie, Value: "not-a-uuid-at-all"})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	var got Stats
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DaysPlayed != 0 {
		t.Errorf("DaysPlayed = %d, want 0", got.DaysPlayed)
	}
}

// The dev reset has to clear the day everywhere the signed-in person played it,
// or "play again" leaves the streak and the score behind.
func TestDevelopmentResetClearsTheDayAcrossAUsersPlayers(t *testing.T) {
	sf := newStatsFixture(t, 1)
	sf.server.DevelopmentMode = true
	today := sf.dates[0]
	laptop, phone := sf.player(t, true), sf.player(t, true)

	base := sf.server.now()
	sf.completeRun(t, laptop, today, 4, base)
	sf.completeRun(t, phone, today, 6, base.Add(time.Hour))

	if got := sf.stats(t, sf.playerCookie(laptop), sf.sessionCookie()); got.DaysPlayed != 1 {
		t.Fatalf("before reset DaysPlayed = %d, want 1", got.DaysPlayed)
	}
	res := sf.do(t, http.MethodPost, "/api/dev/reset", nil, sf.playerCookie(laptop), sf.sessionCookie())
	if res.Code != http.StatusNoContent {
		t.Fatalf("reset status = %d, want 204: %s", res.Code, res.Body.String())
	}
	if got := sf.stats(t, sf.playerCookie(laptop), sf.sessionCookie()); got.DaysPlayed != 0 {
		t.Errorf("after reset DaysPlayed = %d, want 0: the other browser's run survived", got.DaysPlayed)
	}
}

// ?accounts=1 exists so the sign-in flow can be replayed locally. It must throw
// the account away without taking any history with it.
func TestDevelopmentResetCanDropTheAccountWithoutLosingRuns(t *testing.T) {
	sf := newStatsFixture(t, 2)
	sf.server.DevelopmentMode = true
	yesterday, today := sf.dates[0], sf.dates[1]
	laptop := sf.player(t, true)

	base := sf.server.now()
	sf.completeRun(t, laptop, yesterday, 4, base)
	sf.completeRun(t, laptop, today, 6, base.Add(time.Hour))

	res := sf.do(t, http.MethodPost, "/api/dev/reset?accounts=1", nil, sf.playerCookie(laptop), sf.sessionCookie())
	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	if c := cookieNamed(res, sessionCookie); c == nil || c.MaxAge >= 0 {
		t.Errorf("the session cookie was not cleared: %+v", c)
	}

	ctx := context.Background()
	var users int
	if err := sf.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id=$1`, sf.userID).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 0 {
		t.Error("the user row survived")
	}
	var userID *int64
	if err := sf.pool.QueryRow(ctx, `SELECT user_id FROM players WHERE id=$1`, laptop).Scan(&userID); err != nil {
		t.Fatalf("the player row did not survive: %v", err)
	}
	if userID != nil {
		t.Errorf("user_id = %d, want NULL", *userID)
	}
	// Today went with the reset; yesterday must not have.
	var runs int
	if err := sf.pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE player_id=$1`, laptop).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Errorf("runs = %d, want 1: dropping an account must not delete history", runs)
	}
}
