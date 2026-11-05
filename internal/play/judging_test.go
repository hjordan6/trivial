package play_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/hjordan6/trivial/internal/judge"
	"github.com/hjordan6/trivial/internal/play"
	"github.com/hjordan6/trivial/internal/testsupport"
)

// fakeJudge returns fixed scores, or an error, and counts its calls.
type fakeJudge struct {
	mu     sync.Mutex
	scores judge.Scores
	err    error
	calls  []string
}

func (f *fakeJudge) Score(_ context.Context, _ judge.Question, typed string) (judge.Scores, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, typed)
	return f.scores, f.err
}

var (
	yes = judge.Scores{Acceptable: 0.95, NamesOtherThing: 0.05, TooVague: 0.05, Model: "fake"}
	// Acceptable on its own, vetoed as too vague: the "Mary" case.
	vague = judge.Scores{Acceptable: 0.9, NamesOtherThing: 0.05, TooVague: 0.9, Model: "fake"}
)

func TestJudgeAcceptsAMissAndSavesItAsAnAlias(t *testing.T) {
	ctx := context.Background()
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	p, now := fixture(t, tx, 135)
	qid := p.Entries[0].QuestionID
	j := &fakeJudge{scores: yes}

	first, _ := play.CreatePlayer(ctx, tx, now)
	run, err := play.Start(ctx, tx, play.Viewer{PlayerID: first}, p, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, result, err := play.AnswerQuestion(ctx, tx, play.Judging{Judge: j}, run.ID, play.Viewer{PlayerID: first}, qid, play.FreeText, "Answer Zero", now)
	if err != nil {
		t.Fatal(err)
	}
	if a.Outcome == nil || *a.Outcome != play.Star {
		t.Fatalf("outcome = %v, want star: the player should not be able to tell", a.Outcome)
	}
	if !result.Correct || result.NearMiss {
		t.Errorf("result = %+v, want Correct and not NearMiss", result)
	}

	var alias string
	if err := tx.QueryRow(ctx, `SELECT alias FROM question_aliases WHERE question_id=$1 AND normalized='answer zero'`, qid).Scan(&alias); err != nil {
		t.Fatalf("accepted answer not saved as an alias: %v", err)
	}
	var accepted bool
	if err := tx.QueryRow(ctx, `SELECT accepted FROM judged_answers WHERE question_id=$1 AND normalized='answer zero'`, qid).Scan(&accepted); err != nil || !accepted {
		t.Fatalf("verdict not recorded as accepted: accepted=%v err=%v", accepted, err)
	}

	// The next player who types it is graded by the alias, not the judge.
	second, _ := play.CreatePlayer(ctx, tx, now)
	run2, err := play.Start(ctx, tx, play.Viewer{PlayerID: second}, p, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, _, err = play.AnswerQuestion(ctx, tx, play.Judging{Judge: j}, run2.ID, play.Viewer{PlayerID: second}, qid, play.FreeText, "answer zero", now)
	if err != nil {
		t.Fatal(err)
	}
	if a.Outcome == nil || *a.Outcome != play.Star {
		t.Fatalf("second player outcome = %v, want star", a.Outcome)
	}
	if len(j.calls) != 1 {
		t.Errorf("judge called %d times, want once", len(j.calls))
	}
}

func TestJudgeRejectionIsRememberedAndNotAskedAgain(t *testing.T) {
	ctx := context.Background()
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	p, now := fixture(t, tx, 135)
	qid := p.Entries[0].QuestionID
	j := &fakeJudge{scores: vague}

	for i := 0; i < 2; i++ {
		player, _ := play.CreatePlayer(ctx, tx, now)
		run, err := play.Start(ctx, tx, play.Viewer{PlayerID: player}, p, now, nil)
		if err != nil {
			t.Fatal(err)
		}
		a, _, err := play.AnswerQuestion(ctx, tx, play.Judging{Judge: j}, run.ID, play.Viewer{PlayerID: player}, qid, play.FreeText, "answer", now)
		if err != nil {
			t.Fatal(err)
		}
		if a.Outcome != nil {
			t.Fatalf("player %d: outcome = %v, want none (on to multiple choice)", i, *a.Outcome)
		}
	}
	if len(j.calls) != 1 {
		t.Errorf("judge called %d times, want once: a rejection should be cached", len(j.calls))
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM question_aliases WHERE question_id=$1 AND normalized='answer'`, qid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected answer saved as alias: n=%d err=%v", n, err)
	}
}

// An outage must leave today's grading in place, and must not be cached as a
// verdict: the next player deserves a real answer.
func TestJudgeFailureFallsBackAndIsNotCached(t *testing.T) {
	ctx := context.Background()
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	p, now := fixture(t, tx, 135)
	qid := p.Entries[0].QuestionID
	j := &fakeJudge{err: errors.New("jev: status 529")}

	player, _ := play.CreatePlayer(ctx, tx, now)
	run, err := play.Start(ctx, tx, play.Viewer{PlayerID: player}, p, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := play.AnswerQuestion(ctx, tx, play.Judging{Judge: j}, run.ID, play.Viewer{PlayerID: player}, qid, play.FreeText, "answer zero", now)
	if err != nil {
		t.Fatalf("a judge outage failed the request: %v", err)
	}
	if a.Outcome != nil {
		t.Fatalf("outcome = %v, want none", *a.Outcome)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM judged_answers WHERE question_id=$1`, qid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("outage recorded as a verdict: n=%d err=%v", n, err)
	}
}

func TestJudgeIsNotAskedAboutAWrongOption(t *testing.T) {
	ctx := context.Background()
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	p, now := fixture(t, tx, 135)
	qid := p.Entries[0].QuestionID
	j := &fakeJudge{scores: yes}

	player, _ := play.CreatePlayer(ctx, tx, now)
	run, err := play.Start(ctx, tx, play.Viewer{PlayerID: player}, p, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's first question has distractors wrong-0-0-0 .. wrong-0-0-4.
	a, _, err := play.AnswerQuestion(ctx, tx, play.Judging{Judge: j}, run.ID, play.Viewer{PlayerID: player}, qid, play.FreeText, "wrong-0-0-2", now)
	if err != nil {
		t.Fatal(err)
	}
	if a.Outcome != nil || len(j.calls) != 0 {
		t.Fatalf("outcome=%v judge calls=%d, want no outcome and no call", a.Outcome, len(j.calls))
	}
}

// Resubmitting to an answered question must not reach the judge: each
// acceptance becomes an alias for every player, so one run must not be able
// to try string after string.
func TestJudgeIsNotAskedTwiceForOneQuestionInOneRun(t *testing.T) {
	ctx := context.Background()
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	p, now := fixture(t, tx, 135)
	qid := p.Entries[0].QuestionID
	j := &fakeJudge{scores: vague}

	player, _ := play.CreatePlayer(ctx, tx, now)
	run, err := play.Start(ctx, tx, play.Viewer{PlayerID: player}, p, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := play.Viewer{PlayerID: player}
	if _, _, err := play.AnswerQuestion(ctx, tx, play.Judging{Judge: j}, run.ID, v, qid, play.FreeText, "first try", now); err != nil {
		t.Fatal(err)
	}
	j.scores = yes
	if _, _, err := play.AnswerQuestion(ctx, tx, play.Judging{Judge: j}, run.ID, v, qid, play.FreeText, "second try", now); !errors.Is(err, play.ErrInvalidStage) {
		t.Fatalf("second submission err = %v, want ErrInvalidStage", err)
	}
	if len(j.calls) != 1 {
		t.Fatalf("judge called %d times, want once", len(j.calls))
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM question_aliases WHERE question_id=$1 AND normalized='second try'`, qid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("resubmission became an alias: n=%d err=%v", n, err)
	}
}
