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
		for dIndex, d := range []string{"easy", "medium", "hard"} {
			var qid int64
			answer := fmt.Sprintf("answer-%d-%s", topic, d)
			if err := tx.QueryRow(ctx, `INSERT INTO questions(topic_id,difficulty,prompt,canonical_answer,status) VALUES($1,$2,$3,$4,'active') RETURNING id`, tid, d, "Prompt?", answer).Scan(&qid); err != nil {
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
	run, err := play.Start(context.Background(), tx, playerID, p, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := play.Start(context.Background(), tx, playerID, p, now.Add(time.Minute), nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != again.ID || !run.ExpiresAt.Equal(again.ExpiresAt) {
		t.Fatal("idempotent start changed the run")
	}
	qid := p.Entries[0].QuestionID
	first, err := play.Reveal(context.Background(), tx, run.ID, playerID, qid, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := play.Reveal(context.Background(), tx, run.ID, playerID, qid, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("options changed: %v vs %v", first, second)
	}
	if _, _, err := play.AnswerQuestion(context.Background(), tx, run.ID, playerID, qid, play.FreeText, "answer-0-easy", now); !errors.Is(err, play.ErrInvalidStage) {
		t.Fatalf("free text after reveal error=%v", err)
	}
	a, _, err := play.AnswerQuestion(context.Background(), tx, run.ID, playerID, qid, play.MultipleChoice, "answer-0-easy", now)
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
	run, err := play.Start(context.Background(), tx, playerID, p, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := play.Load(context.Background(), tx, run.ID, playerID, now.Add(2*time.Second))
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
