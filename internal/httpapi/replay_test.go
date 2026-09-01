package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// replayFixture is an account with two browsers and a real board for today,
// which is the smallest world in which "can I play this day twice" is a
// question the HTTP surface can answer.
type replayFixture struct {
	*authFixture
	userID   int64
	date     string
	topicIDs []int64
}

func newReplayFixture(t *testing.T) *replayFixture {
	t.Helper()
	f := newAuthFixture(t)
	f.server.Timezone = time.UTC
	ctx := context.Background()

	// A date of this test's own, so nothing collides with another test's board.
	today := time.Date(2027, 6, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, int(f.seq))
	f.at(today)
	date := today.Format("2006-01-02")

	rf := &replayFixture{authFixture: f, date: date}
	if _, err := f.pool.Exec(ctx, `INSERT INTO daily_puzzles(puzzle_date,time_limit_seconds) VALUES($1,240)`, date); err != nil {
		t.Fatalf("insert daily_puzzles: %v", err)
	}
	// Three topics by three difficulties: the board's uniqueness constraints are
	// per (date, topic, difficulty), so a real board is the only shape that fits.
	for position := 0; position < 3; position++ {
		slug := fmt.Sprintf("replay-%d-%d", f.seq, position)
		var topicID int64
		if err := f.pool.QueryRow(ctx, `INSERT INTO topics(slug,name) VALUES($1,$1) RETURNING id`, slug).Scan(&topicID); err != nil {
			t.Fatalf("insert topic: %v", err)
		}
		rf.topicIDs = append(rf.topicIDs, topicID)
		for i, difficulty := range []string{"easy", "medium", "hard"} {
			rating := []int{2, 6, 9}[i]
			var qid int64
			err := f.pool.QueryRow(ctx,
				`INSERT INTO questions(topic_id,difficulty,difficulty_rating,prompt,canonical_answer,status)
				 VALUES($1,$2,$3,$4,'answer','active') RETURNING id`,
				topicID, difficulty, rating, fmt.Sprintf("prompt %d-%s?", position, difficulty)).Scan(&qid)
			if err != nil {
				t.Fatalf("insert question: %v", err)
			}
			if _, err := f.pool.Exec(ctx,
				`INSERT INTO daily_puzzle_questions(puzzle_date,topic_id,topic_position,difficulty,question_id)
				 VALUES($1,$2,$3,$4,$5)`, date, topicID, position, difficulty, qid); err != nil {
				t.Fatalf("insert board entry: %v", err)
			}
		}
	}

	email := f.email("replay")
	if err := f.pool.QueryRow(ctx, `INSERT INTO users(email,created_at) VALUES($1,$2) RETURNING id`, email, f.now).Scan(&rf.userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	t.Cleanup(func() {
		// Registered after the auth fixture's, so this runs first. runs and
		// run_answers cascade from players, but daily_puzzles is RESTRICT, so
		// the runs have to go before the date they point at -- and the board
		// before the questions it references.
		for _, stmt := range []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM runs WHERE puzzle_date = $1`, []any{rf.date}},
			{`DELETE FROM daily_puzzle_questions WHERE puzzle_date = $1`, []any{rf.date}},
			{`DELETE FROM daily_puzzles WHERE puzzle_date = $1`, []any{rf.date}},
			{`DELETE FROM questions WHERE topic_id = ANY($1)`, []any{rf.topicIDs}},
			{`DELETE FROM topics WHERE id = ANY($1)`, []any{rf.topicIDs}},
		} {
			if _, err := f.pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
				t.Errorf("cleanup (%s): %v", stmt.sql, err)
			}
		}
	})
	return rf
}

// browser creates a player row already attached to the account, the state a
// browser is left in by signing in.
func (rf *replayFixture) browser(t *testing.T) *http.Cookie {
	t.Helper()
	var id string
	if err := rf.pool.QueryRow(context.Background(),
		`INSERT INTO players(created_at,last_seen_at,user_id) VALUES($1,$1,$2) RETURNING id`,
		rf.now, rf.userID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: playerCookie, Value: sign(rf.server.appKey(), playerCookie, id)}
}

func (rf *replayFixture) session() *http.Cookie {
	return &http.Cookie{
		Name:  sessionCookie,
		Value: signExpiring(rf.server.appKey(), sessionCookie, fmt.Sprintf("%d", rf.userID), rf.server.now().Add(time.Hour)),
	}
}

// runID pulls the run out of an envelope, or "" when there is none.
func runID(t *testing.T, body []byte) (id string, completed bool) {
	t.Helper()
	var env struct {
		Run *struct {
			ID          string     `json:"id"`
			CompletedAt *time.Time `json:"completed_at"`
		} `json:"run"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v (%s)", err, body)
	}
	if env.Run == nil {
		return "", false
	}
	return env.Run.ID, env.Run.CompletedAt != nil
}

// The bug as a player met it: finish the day, open an incognito window, sign in
// to the same account, and get a clean board for a day already played.
func TestSigningInOnANewBrowserCannotReplayTheDay(t *testing.T) {
	rf := newReplayFixture(t)
	first, second, session := rf.browser(t), rf.browser(t), rf.session()

	res := rf.do(t, http.MethodPost, "/api/runs", nil, first, session)
	if res.Code != http.StatusOK {
		t.Fatalf("POST /api/runs status = %d: %s", res.Code, res.Body)
	}
	played, _ := runID(t, res.Body.Bytes())

	res = rf.do(t, http.MethodPost, "/api/runs/"+played+"/finish", nil, first, session)
	if res.Code != http.StatusOK {
		t.Fatalf("finish status = %d: %s", res.Code, res.Body)
	}

	// The incognito window, signed in to the same account.
	res = rf.do(t, http.MethodGet, "/api/runs/current", nil, second, session)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /api/runs/current status = %d: %s", res.Code, res.Body)
	}
	got, completed := runID(t, res.Body.Bytes())
	if got != played {
		t.Errorf("current run = %q, want the day already played (%q)", got, played)
	}
	if !completed {
		t.Error("current run is not marked completed, so the second browser would offer a fresh game")
	}

	// And pressing play must not mint a second run.
	res = rf.do(t, http.MethodPost, "/api/runs", nil, second, session)
	if res.Code != http.StatusOK {
		t.Fatalf("POST /api/runs status = %d: %s", res.Code, res.Body)
	}
	if got, _ = runID(t, res.Body.Bytes()); got != played {
		t.Errorf("start on the second browser = %q, want the existing run %q", got, played)
	}

	var runs int
	if err := rf.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM runs WHERE puzzle_date=$1`, rf.date).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Errorf("runs recorded for %s = %d, want 1", rf.date, runs)
	}
}

// Signed out, the same browser is on its own again -- the attachment in the
// database must not keep it bound to the account's run.
func TestASignedOutBrowserGetsItsOwnRun(t *testing.T) {
	rf := newReplayFixture(t)
	first, second, session := rf.browser(t), rf.browser(t), rf.session()

	res := rf.do(t, http.MethodPost, "/api/runs", nil, first, session)
	if res.Code != http.StatusOK {
		t.Fatalf("POST /api/runs status = %d: %s", res.Code, res.Body)
	}
	played, _ := runID(t, res.Body.Bytes())

	// No session cookie: attached in the database, signed out in this browser.
	res = rf.do(t, http.MethodGet, "/api/runs/current", nil, second)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /api/runs/current status = %d: %s", res.Code, res.Body)
	}
	if got, _ := runID(t, res.Body.Bytes()); got != "" {
		t.Errorf("current run = %q for a signed-out browser, want none (%q belongs to the account)", got, played)
	}
}
