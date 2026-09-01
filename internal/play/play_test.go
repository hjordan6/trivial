package play_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/play"
	"github.com/hjordan6/trivial/internal/puzzle"
	"github.com/hjordan6/trivial/internal/testsupport"
)

func fixture(t *testing.T, tx pgx.Tx, limit int) (*puzzle.Puzzle, time.Time) {
	t.Helper()
	ctx := context.Background()
	date, _ := clock.ParseDate("2026-08-19")
	now := time.Date(2026, 8, 19, 18, 0, 0, 0, time.UTC)
	if _, err := tx.Exec(ctx, `INSERT INTO daily_puzzles(puzzle_date,time_limit_seconds) VALUES($1,$2)`, date, limit); err != nil {
		t.Fatal(err)
	}
	p := &puzzle.Puzzle{Date: date, TimeLimitSeconds: limit}
	for topic := 0; topic < 3; topic++ {
		var tid int64
		if err := tx.QueryRow(ctx, `INSERT INTO topics(slug,name) VALUES($1,$2) RETURNING id`, fmt.Sprintf("play-%d", topic), fmt.Sprintf("Topic %d", topic)).Scan(&tid); err != nil {
			t.Fatal(err)
		}
		// difficulty_rating is NOT NULL and constrained to its band: easy 1-4,
		// medium 5-7, hard 8-10. These are the values migration 00004 backfilled.
		ratings := []int{2, 6, 9}
		for dIndex, d := range []string{"easy", "medium", "hard"} {
			var qid int64
			answer := fmt.Sprintf("answer-%d-%s", topic, d)
			if err := tx.QueryRow(ctx, `INSERT INTO questions(topic_id,difficulty,difficulty_rating,prompt,canonical_answer,status) VALUES($1,$2,$3,$4,$5,'active') RETURNING id`, tid, d, ratings[dIndex], "Prompt?", answer).Scan(&qid); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO question_aliases(question_id,alias,normalized) VALUES($1,$2,$2)`, qid, answer); err != nil {
				t.Fatal(err)
			}
			for n := 0; n < 5; n++ {
				if _, err := tx.Exec(ctx, `INSERT INTO question_distractors(question_id,option_text) VALUES($1,$2)`, qid, fmt.Sprintf("wrong-%d-%d-%d", topic, dIndex, n)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO daily_puzzle_questions(puzzle_date,topic_id,topic_position,difficulty,question_id) VALUES($1,$2,$3,$4,$5)`, date, tid, topic, d, qid); err != nil {
				t.Fatal(err)
			}
			p.Entries = append(p.Entries, puzzle.Entry{TopicID: tid, TopicPosition: topic, Difficulty: content.Difficulty(d), QuestionID: qid, Prompt: "Prompt?"})
		}
	}
	return p, now
}

func TestRunLifecycle(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	p, now := fixture(t, tx, 135)
	playerID, err := play.CreatePlayer(context.Background(), tx, now)
	if err != nil {
		t.Fatal(err)
	}
	run, err := play.Start(context.Background(), tx, play.Viewer{PlayerID: playerID}, p, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := play.Start(context.Background(), tx, play.Viewer{PlayerID: playerID}, p, now.Add(time.Minute), nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != again.ID || !run.ExpiresAt.Equal(again.ExpiresAt) {
		t.Fatal("idempotent start changed the run")
	}
	qid := p.Entries[0].QuestionID
	first, err := play.Reveal(context.Background(), tx, run.ID, play.Viewer{PlayerID: playerID}, qid, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := play.Reveal(context.Background(), tx, run.ID, play.Viewer{PlayerID: playerID}, qid, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("options changed: %v vs %v", first, second)
	}
	if _, _, err := play.AnswerQuestion(context.Background(), tx, run.ID, play.Viewer{PlayerID: playerID}, qid, play.FreeText, "answer-0-easy", now); !errors.Is(err, play.ErrInvalidStage) {
		t.Fatalf("free text after reveal error=%v", err)
	}
	a, _, err := play.AnswerQuestion(context.Background(), tx, run.ID, play.Viewer{PlayerID: playerID}, qid, play.MultipleChoice, "answer-0-easy", now)
	if err != nil {
		t.Fatal(err)
	}
	if a.Outcome == nil || *a.Outcome != play.Circle || a.CanonicalAnswer == "" {
		t.Fatalf("answer=%+v", a)
	}
}

func TestExpiredRunSweepsEveryQuestion(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	p, now := fixture(t, tx, 1)
	playerID, _ := play.CreatePlayer(context.Background(), tx, now)
	run, err := play.Start(context.Background(), tx, play.Viewer{PlayerID: playerID}, p, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := play.Load(context.Background(), tx, run.ID, play.Viewer{PlayerID: playerID}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got.CompletedAt == nil || len(got.Answers) != 9 {
		t.Fatalf("expired run not swept: %+v", got)
	}
	for _, a := range got.Answers {
		if a.Outcome == nil || *a.Outcome != play.Expired {
			t.Fatalf("answer not expired: %+v", a)
		}
	}
}

// signedIn is the viewer for a browser attached to, and currently signed in to,
// an account.
func signedIn(playerID string, userID int64) play.Viewer {
	return play.Viewer{PlayerID: playerID, UserID: &userID}
}

// twoBrowsers builds one account with two browsers signed in on it, which is
// the shape every cross-browser rule is about.
func twoBrowsers(t *testing.T, tx pgx.Tx, now time.Time) (userID int64, first, second string) {
	t.Helper()
	ctx := context.Background()
	email := fmt.Sprintf("two-browsers-%d@example.com", now.UnixNano())
	if err := tx.QueryRow(ctx, `INSERT INTO users(email,created_at) VALUES($1,$2) RETURNING id`, email, now).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []*string{&first, &second} {
		created, err := play.CreatePlayer(ctx, tx, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE players SET user_id=$2 WHERE id=$1`, created, userID); err != nil {
			t.Fatal(err)
		}
		*id = created
	}
	return userID, first, second
}

// A day belongs to the account, not to the browser.
//
// The regression: signing in on a second browser handed back a clean board for
// a day the account had already finished, because the run was looked up by
// player_id and the new browser was a new player. Stats had always counted that
// day, so the game and the streak disagreed about whether it had been played.
func TestASecondBrowserJoinsTheAccountsRunInsteadOfStartingItsOwn(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	p, now := fixture(t, tx, 135)
	userID, first, second := twoBrowsers(t, tx, now)

	run, err := play.Start(ctx, tx, signedIn(first, userID), p, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := play.Finish(ctx, tx, run.ID, signedIn(first, userID), now); err != nil {
		t.Fatal(err)
	}

	// The second browser arrives a minute later, as an incognito window would.
	later := now.Add(time.Minute)

	current, err := play.Current(ctx, tx, signedIn(second, userID), p.Date, later)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil {
		t.Fatal("Current() = nil for the second browser, want the account's finished run")
	}
	if current.ID != run.ID {
		t.Errorf("Current().ID = %s, want the first browser's run %s", current.ID, run.ID)
	}
	if current.CompletedAt == nil {
		t.Error("Current().CompletedAt = nil, want the day to read as already played")
	}
	if current.PlayerID != first {
		t.Errorf("Current().PlayerID = %s, want the browser that actually played it (%s)", current.PlayerID, first)
	}

	// Start is the half that mattered: the front end calls it to begin a game,
	// and it has to hand back the finished run rather than mint a second one.
	started, err := play.Start(ctx, tx, signedIn(second, userID), p, later, nil)
	if err != nil {
		t.Fatal(err)
	}
	if started.ID != run.ID {
		t.Errorf("Start().ID = %s, want the existing run %s -- a second run means the day can be replayed", started.ID, run.ID)
	}

	var runs int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM runs WHERE puzzle_date=$1`, p.Date).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Errorf("runs for %s = %d, want 1", p.Date, runs)
	}
}

// Scope follows the session, not the attachment. players.user_id deliberately
// outlives a sign-out, so reading the account's run off the column instead of
// the session would leave a signed-out browser still bound to it.
func TestSigningOutNarrowsABrowserBackToItsOwnRuns(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	p, now := fixture(t, tx, 135)
	userID, first, second := twoBrowsers(t, tx, now)

	run, err := play.Start(ctx, tx, signedIn(first, userID), p, now, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Same browser, signed out: still attached in the database, but no session.
	signedOut := play.Viewer{PlayerID: second}
	current, err := play.Current(ctx, tx, signedOut, p.Date, now)
	if err != nil {
		t.Fatal(err)
	}
	if current != nil {
		t.Errorf("Current() = run %s for a signed-out browser, want nil", current.ID)
	}
	if _, err := play.Load(ctx, tx, run.ID, signedOut, now); !errors.Is(err, play.ErrNotYourRun) {
		t.Errorf("Load() error = %v, want ErrNotYourRun", err)
	}
}

// Two strangers must never share a run. The user half of the scope is NULL for
// both, and NULL matches no row -- this is the test that says so.
func TestAnonymousBrowsersDoNotShareRuns(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	p, now := fixture(t, tx, 135)

	mine, err := play.CreatePlayer(ctx, tx, now)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := play.CreatePlayer(ctx, tx, now)
	if err != nil {
		t.Fatal(err)
	}

	run, err := play.Start(ctx, tx, play.Viewer{PlayerID: mine}, p, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	current, err := play.Current(ctx, tx, play.Viewer{PlayerID: theirs}, p.Date, now)
	if err != nil {
		t.Fatal(err)
	}
	if current != nil {
		t.Errorf("Current() = run %s for an unrelated browser, want nil", current.ID)
	}
	if _, err := play.Load(ctx, tx, run.ID, play.Viewer{PlayerID: theirs}, now); !errors.Is(err, play.ErrNotYourRun) {
		t.Errorf("Load() error = %v, want ErrNotYourRun", err)
	}
}
