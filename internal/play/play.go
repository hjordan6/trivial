// Package play owns player identity and the lifecycle of a daily run.
package play

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	mathrand "math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/db"
	"github.com/hjordan6/trivial/internal/grading"
	"github.com/hjordan6/trivial/internal/puzzle"
)

type Stage string
type Outcome string

const (
	FreeText       Stage   = "free_text"
	MultipleChoice Stage   = "multiple_choice"
	Star           Outcome = "star"
	Circle         Outcome = "circle"
	Miss           Outcome = "miss"
	Expired        Outcome = "expired"
)

var (
	ErrNotYourRun      = errors.New("not your run")
	ErrExpired         = errors.New("run expired")
	ErrAlreadyAnswered = errors.New("question already answered")
	ErrInvalidStage    = errors.New("invalid answer stage")
	ErrQuestion        = errors.New("question is not in run")
)

type Answer struct {
	QuestionID         int64    `json:"question_id"`
	Stage              Stage    `json:"stage"`
	FreeTextSubmission *string  `json:"free_text_submission,omitempty"`
	ChosenOption       *string  `json:"chosen_option,omitempty"`
	Outcome            *Outcome `json:"outcome,omitempty"`
	CanonicalAnswer    string   `json:"canonical_answer,omitempty"`
}

type Run struct {
	ID          string         `json:"id"`
	PlayerID    string         `json:"-"`
	Puzzle      *puzzle.Puzzle `json:"puzzle"`
	StartedAt   time.Time      `json:"started_at"`
	ExpiresAt   time.Time      `json:"expires_at"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
	OptionSeed  int64          `json:"-"`
	Answers     []Answer       `json:"answers"`
}

func CreatePlayer(ctx context.Context, q db.DBTX, now time.Time) (string, error) {
	var id string
	err := q.QueryRow(ctx, `INSERT INTO players (created_at, last_seen_at) VALUES ($1,$1) RETURNING id`, now).Scan(&id)
	return id, err
}

func TouchPlayer(ctx context.Context, q db.DBTX, id string, now time.Time) error {
	ct, err := q.Exec(ctx, `UPDATE players SET last_seen_at=$2 WHERE id=$1 AND last_seen_at < $2::date`, id, now)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM players WHERE id=$1)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
	}
	return nil
}

func Start(ctx context.Context, q db.DBTX, playerID string, p *puzzle.Puzzle, now time.Time, referredBy *string) (*Run, error) {
	seed, err := randomInt64()
	if err != nil {
		return nil, fmt.Errorf("option seed: %w", err)
	}
	var id string
	var started, expires time.Time
	err = q.QueryRow(ctx, `
		INSERT INTO runs (player_id,puzzle_date,started_at,expires_at,option_seed,referred_by_run_id)
		VALUES ($1,$2,$3::timestamptz,$3::timestamptz + make_interval(secs => $4::double precision),$5,$6)
		ON CONFLICT (player_id,puzzle_date) DO UPDATE SET player_id=EXCLUDED.player_id
		RETURNING id,started_at,expires_at`, playerID, p.Date, now, p.TimeLimitSeconds, seed, referredBy).Scan(&id, &started, &expires)
	if err != nil {
		return nil, fmt.Errorf("start run: %w", err)
	}
	return Load(ctx, q, id, playerID, now)
}

func Current(ctx context.Context, q db.DBTX, playerID string, date clock.Date, now time.Time) (*Run, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM runs WHERE player_id=$1 AND puzzle_date=$2`, playerID, date).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return Load(ctx, q, id, playerID, now)
}

func Load(ctx context.Context, q db.DBTX, id, playerID string, now time.Time) (*Run, error) {
	r := &Run{ID: id, PlayerID: playerID}
	var date clock.Date
	err := q.QueryRow(ctx, `SELECT puzzle_date,started_at,expires_at,completed_at,option_seed FROM runs WHERE id=$1 AND player_id=$2`, id, playerID).Scan(&date, &r.StartedAt, &r.ExpiresAt, &r.CompletedAt, &r.OptionSeed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotYourRun
	}
	if err != nil {
		return nil, err
	}
	if r.CompletedAt == nil && !now.Before(r.ExpiresAt) {
		if err := Finish(ctx, q, id, playerID, now); err != nil {
			return nil, err
		}
		return Load(ctx, q, id, playerID, now)
	}
	r.Puzzle, err = puzzle.Get(ctx, q, date)
	if err != nil {
		return nil, err
	}
	shuffleQuestions(r.Puzzle.Entries, r.OptionSeed)
	rows, err := q.Query(ctx, `SELECT ra.question_id,ra.stage,ra.free_text_submission,ra.chosen_option,ra.outcome,
		CASE WHEN r.completed_at IS NOT NULL OR ra.outcome IS NOT NULL THEN qu.canonical_answer ELSE '' END
		FROM run_answers ra JOIN runs r ON r.id=ra.run_id JOIN questions qu ON qu.id=ra.question_id WHERE ra.run_id=$1 ORDER BY ra.question_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Answer
		if err := rows.Scan(&a.QuestionID, &a.Stage, &a.FreeTextSubmission, &a.ChosenOption, &a.Outcome, &a.CanonicalAnswer); err != nil {
			return nil, err
		}
		r.Answers = append(r.Answers, a)
	}
	return r, rows.Err()
}

func Finish(ctx context.Context, q db.DBTX, id, playerID string, now time.Time) error {
	var owned bool
	err := q.QueryRow(ctx, `
		WITH finished AS (
			UPDATE runs SET completed_at=COALESCE(completed_at,$3::timestamptz)
			WHERE id=$1 AND player_id=$2 RETURNING id,puzzle_date
		), swept AS (
			INSERT INTO run_answers(run_id,question_id,stage,outcome,first_touched_at,resolved_at)
			SELECT f.id,dpq.question_id,'free_text','expired',$3::timestamptz,$3::timestamptz
			FROM finished f JOIN daily_puzzle_questions dpq ON dpq.puzzle_date=f.puzzle_date
			ON CONFLICT (run_id,question_id) DO UPDATE SET outcome='expired',resolved_at=$3::timestamptz
			WHERE run_answers.outcome IS NULL RETURNING 1
		)
		SELECT EXISTS(SELECT 1 FROM finished)`, id, playerID, now).Scan(&owned)
	if err != nil {
		return err
	}
	if !owned {
		return ErrNotYourRun
	}
	return nil
}

type QuestionData struct {
	Canonical            string
	Aliases, Distractors []string
}

func questionData(ctx context.Context, q db.DBTX, runID string, qid int64) (QuestionData, error) {
	var d QuestionData
	err := q.QueryRow(ctx, `SELECT qu.canonical_answer FROM runs r JOIN daily_puzzle_questions dpq ON dpq.puzzle_date=r.puzzle_date JOIN questions qu ON qu.id=dpq.question_id WHERE r.id=$1 AND qu.id=$2`, runID, qid).Scan(&d.Canonical)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrQuestion
	}
	if err != nil {
		return d, err
	}
	rows, err := q.Query(ctx, `SELECT normalized FROM question_aliases WHERE question_id=$1`, qid)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return d, err
		}
		d.Aliases = append(d.Aliases, s)
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT option_text FROM question_distractors WHERE question_id=$1`, qid)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return d, err
		}
		d.Distractors = append(d.Distractors, s)
	}
	return d, rows.Err()
}

func Reveal(ctx context.Context, q db.DBTX, runID, playerID string, qid int64, now time.Time) ([]string, error) {
	r, err := Load(ctx, q, runID, playerID, now)
	if err != nil {
		return nil, err
	}
	if r.CompletedAt != nil {
		return nil, ErrExpired
	}
	d, err := questionData(ctx, q, runID, qid)
	if err != nil {
		return nil, err
	}
	ct, err := q.Exec(ctx, `INSERT INTO run_answers(run_id,question_id,stage,first_touched_at) VALUES($1,$2,'multiple_choice',$3) ON CONFLICT(run_id,question_id) DO UPDATE SET stage='multiple_choice' WHERE run_answers.outcome IS NULL AND run_answers.stage='free_text'`, runID, qid, now)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		var stage Stage
		var outcome *Outcome
		if err := q.QueryRow(ctx, `SELECT stage,outcome FROM run_answers WHERE run_id=$1 AND question_id=$2`, runID, qid).Scan(&stage, &outcome); err != nil {
			return nil, err
		}
		if stage != MultipleChoice || outcome != nil {
			return nil, ErrAlreadyAnswered
		}
	}
	options := append([]string{d.Canonical}, d.Distractors...)
	shuffle(options, r.OptionSeed, qid)
	return options, nil
}

func AnswerQuestion(ctx context.Context, q db.DBTX, runID, playerID string, qid int64, stage Stage, submission string, now time.Time) (Answer, *grading.Result, error) {
	r, err := Load(ctx, q, runID, playerID, now.Add(-2*time.Second))
	if err != nil {
		return Answer{}, nil, err
	}
	if r.CompletedAt != nil {
		return Answer{}, nil, ErrExpired
	}
	d, err := questionData(ctx, q, runID, qid)
	if err != nil {
		return Answer{}, nil, err
	}
	var outcome *Outcome
	var result *grading.Result
	if stage == FreeText {
		// Distractors are stored raw and normalized here; aliases are already
		// normalized in the database. Grade rejects a submission that lands on
		// a distractor -- exactly, or closer than to any alias -- so the check
		// no longer needs to happen out here.
		normalizedDistractors := make([]string, len(d.Distractors))
		for i, distractor := range d.Distractors {
			normalizedDistractors[i] = grading.Normalize(distractor)
		}
		gr := grading.Grade(submission, d.Aliases, normalizedDistractors)
		result = &gr
		if gr.Correct {
			o := Star
			outcome = &o
		}
	} else if stage == MultipleChoice {
		if submission == d.Canonical {
			o := Circle
			outcome = &o
		} else {
			o := Miss
			outcome = &o
		}
	} else {
		return Answer{}, nil, ErrInvalidStage
	}
	var a Answer
	if stage == FreeText {
		err = q.QueryRow(ctx, `INSERT INTO run_answers(run_id,question_id,stage,free_text_submission,outcome,first_touched_at,resolved_at) VALUES($1,$2,'free_text',$3,$4,$5::timestamptz,CASE WHEN $4::answer_outcome IS NULL THEN NULL ELSE $5::timestamptz END) ON CONFLICT DO NOTHING RETURNING question_id,stage,free_text_submission,chosen_option,outcome`, runID, qid, submission, outcome, now).Scan(&a.QuestionID, &a.Stage, &a.FreeTextSubmission, &a.ChosenOption, &a.Outcome)
	} else {
		err = q.QueryRow(ctx, `UPDATE run_answers SET chosen_option=$3,outcome=$4,resolved_at=$5 WHERE run_id=$1 AND question_id=$2 AND stage='multiple_choice' AND outcome IS NULL RETURNING question_id,stage,free_text_submission,chosen_option,outcome`, runID, qid, submission, outcome, now).Scan(&a.QuestionID, &a.Stage, &a.FreeTextSubmission, &a.ChosenOption, &a.Outcome)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Answer{}, result, ErrInvalidStage
	}
	if err != nil {
		return Answer{}, result, err
	}
	if a.Outcome != nil {
		a.CanonicalAnswer = d.Canonical
		_, err = q.Exec(ctx, `UPDATE runs SET completed_at=$3 WHERE id=$1 AND player_id=$2 AND completed_at IS NULL AND 9=(SELECT count(*) FROM run_answers WHERE run_id=$1 AND outcome IS NOT NULL)`, runID, playerID, now)
		if err != nil {
			return Answer{}, result, err
		}
	}
	return a, result, nil
}

func randomInt64() (int64, error) {
	n, err := cryptorand.Int(cryptorand.Reader, new(big.Int).SetUint64(1<<63))
	if err != nil {
		return 0, err
	}
	return n.Int64(), nil
}
func shuffle(values []string, seed int64, qid int64) {
	var b [16]byte
	binary.LittleEndian.PutUint64(b[:8], uint64(seed))
	binary.LittleEndian.PutUint64(b[8:], uint64(qid))
	r := mathrand.New(mathrand.NewPCG(binary.LittleEndian.Uint64(b[:8]), binary.LittleEndian.Uint64(b[8:])))
	r.Shuffle(len(values), func(i, j int) { values[i], values[j] = values[j], values[i] })
}

// shuffleQuestions gives each run a stable presentation order. It uses a
// separate PCG stream from option shuffling so changing board presentation
// cannot change any question's multiple-choice order.
func shuffleQuestions(entries []puzzle.Entry, seed int64) {
	r := mathrand.New(mathrand.NewPCG(uint64(seed), 0xD1B54A32D192ED03))
	r.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
}
