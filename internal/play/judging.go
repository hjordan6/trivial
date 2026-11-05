package play

import (
	"context"
	"errors"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/db"
	"github.com/hjordan6/trivial/internal/grading"
	"github.com/hjordan6/trivial/internal/judge"
)

// Judging gives a typed answer the grader rejected a second opinion before the
// player sees the result. The zero value is off: no judge, today's grading.
//
// An accepted answer is scored as a star, exactly as if it had been an alias
// all along, and is then saved as one, so every later player who types it is
// graded by the alias path and never waits on the judge.
type Judging struct {
	Judge judge.Judge
	// Thresholds is optional; the zero value means judge.DefaultThresholds.
	Thresholds *judge.Thresholds
	// Log is optional; nil means slog.Default().
	Log *slog.Logger
}

// judgeTimeout bounds how long a player waits on the judge. Jev answered in a
// median of 112ms and at worst ~180ms in testing; past this, the grader's own
// verdict stands. It is a wait for one wrong answer, not for every answer.
const judgeTimeout = 1500 * time.Millisecond

// maxJudgedRunes skips the judge for anything longer than a plausible answer.
// The longest canonical answer in the library is 48 characters; text much
// longer than that is a sentence, not an answer, and not worth sending out.
const maxJudgedRunes = 80

// wouldJudge reports whether a rejected free-text answer is worth a second
// opinion at all.
func (jd Judging) wouldJudge(submission string, gr grading.Result) bool {
	if jd.Judge == nil {
		return false
	}
	// Close to a wrong option means the player chose a known-wrong answer.
	// The grader already decided that on purpose; nothing to reconsider.
	if gr.ReadsAsWrongOption {
		return false
	}
	normalized := grading.Normalize(submission)
	return normalized != "" && utf8.RuneCountInString(submission) <= maxJudgedRunes
}

func (jd Judging) logger() *slog.Logger {
	if jd.Log != nil {
		return jd.Log
	}
	return slog.Default()
}

// secondOpinion asks the judge whether submission should have been accepted,
// records the verdict, and saves an accepted answer as an alias. It reports
// whether to accept.
//
// Every failure answers false and is logged: the judge may only ever turn a
// miss into a hit, so when anything goes wrong the grader's no stands.
func (jd Judging) secondOpinion(ctx context.Context, q db.DBTX, runID string, qid int64, d QuestionData, submission string) bool {
	log := jd.logger().With("question_id", qid, "submission", submission)
	normalized := grading.Normalize(submission)

	// Only a question this run has not touched yet. The insert that records
	// the answer refuses a second one anyway, but it runs after this; without
	// the check, resubmitting to an answered question would put a fresh
	// string in front of the judge every time, and each acceptance becomes an
	// alias for everyone.
	var touched bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM run_answers WHERE run_id=$1 AND question_id=$2)`, runID, qid).Scan(&touched); err != nil {
		log.Error("judge: check whether the question is already answered", "error", err)
		return false
	}
	if touched {
		return false
	}

	// An earlier verdict is final either way. A rejection stays a rejection.
	// An acceptance would already be an alias and never reach here, unless an
	// operator has since deleted that alias -- and then deleting it was the
	// decision, which re-judging must not undo.
	var seen bool
	err := q.QueryRow(ctx, `SELECT true FROM judged_answers WHERE question_id=$1 AND normalized=$2`, qid, normalized).Scan(&seen)
	if err == nil {
		return false
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		log.Error("judge: read earlier verdict", "error", err)
		return false
	}

	judgeCtx, cancel := context.WithTimeout(ctx, judgeTimeout)
	defer cancel()
	started := time.Now()
	scores, err := jd.Judge.Score(judgeCtx, judge.Question{
		Prompt:       d.Prompt,
		Answer:       d.Canonical,
		Accepted:     d.AliasText,
		WrongOptions: d.Distractors,
	}, submission)
	elapsed := time.Since(started)
	if err != nil {
		// Not recorded: an outage is not a verdict, and the next player to
		// type the same thing should get a real one.
		log.Warn("judge unavailable; grader's verdict stands", "error", err, "elapsed_ms", elapsed.Milliseconds())
		return false
	}

	thresholds := judge.DefaultThresholds
	if jd.Thresholds != nil {
		thresholds = *jd.Thresholds
	}
	accepted := scores.Accepts(thresholds)
	log.Info("judged answer", "accepted", accepted,
		"acceptable", scores.Acceptable, "names_other_thing", scores.NamesOtherThing,
		"too_vague", scores.TooVague, "model", scores.Model, "elapsed_ms", elapsed.Milliseconds())

	// The verdict is recorded even when saving fails below, so a judge that
	// said no is not asked again; the player's answer is graded on the
	// verdict regardless, since that is what the judge actually said.
	if _, err := q.Exec(ctx, `INSERT INTO judged_answers(question_id,normalized,submission,acceptable,names_other_thing,too_vague,model,accepted) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`,
		qid, normalized, submission, scores.Acceptable, scores.NamesOtherThing, scores.TooVague, scores.Model, accepted); err != nil {
		log.Error("judge: record verdict", "error", err)
	}
	if accepted {
		if _, err := q.Exec(ctx, `INSERT INTO question_aliases(question_id,alias,normalized) VALUES($1,$2,$3) ON CONFLICT (question_id, normalized) DO NOTHING`,
			qid, submission, normalized); err != nil {
			log.Error("judge: save accepted answer as alias", "error", err)
		}
	}
	return accepted
}
