// Package judge gives a typed answer that the alias grader rejected a second
// opinion: would a fair trivia host have accepted it anyway?
//
// The grader is deliberately literal. It knows the spellings someone wrote
// down, and nothing else, so "civil rights" misses "The Civil Rights
// Movement" and "28" misses "28 days" although both are plainly right. A
// judge reads the question and decides.
//
// A judge only ever turns a miss into a hit. It is asked after the grader has
// already said no, and every failure -- a timeout, an outage, a malformed
// reply -- leaves that no in place. The player sees exactly today's grading
// whenever the judge is unavailable.
package judge

import "context"

// Question is what the judge needs to know about the question being answered.
type Question struct {
	Prompt string
	Answer string
	// Accepted lists the spellings the grader already takes, so the judge
	// sees the house style for this answer.
	Accepted []string
	// WrongOptions are the multiple-choice distractors. An answer that names
	// one of these is wrong by construction.
	WrongOptions []string
}

// Scores are the judge's four probabilities, each between 0 and 1.
//
// Acceptable and Misspelling are the two ways to a yes; NamesOtherThing and
// TooVague are vetoes on either. Acceptable alone is not enough to accept on:
// in testing it scored "Mary" for "Typhoid Mary" at 0.76 and "Berlin" for
// "The Berlin Wall" at 0.88. The model reads both as what the player meant,
// and both are wrong. The vetoes are what catch those.
type Scores struct {
	// Acceptable: would a fair host accept the answer for this question?
	Acceptable float64
	// Misspelling: is the answer the correct one, or an accepted spelling of
	// it, written wrong? Acceptable lumps misspellings in with nicknames and
	// abbreviations and scores a badly mangled one low -- "pallendrom" for
	// "Palindrome" at 0.41 -- where this scores it at 0.97.
	Misspelling float64
	// NamesOtherThing: read on its own, does the answer name something real
	// and different -- "Berlin", a city, for the Berlin Wall?
	NamesOtherThing float64
	// TooVague: did the player keep only a generic part of the answer and
	// drop what identifies it -- "Mary" for "Typhoid Mary"?
	TooVague float64
	// Model is the model that produced the scores, as the provider reports it.
	Model string
}

// Thresholds are the cut-offs Accepts applies.
type Thresholds struct {
	MinAcceptable      float64
	MinMisspelling     float64
	MaxNamesOtherThing float64
	MaxTooVague        float64
}

// DefaultThresholds were chosen by scoring 116 answers three times each: 43
// correct answers the grader missed, and 73 wrong ones, most of them real
// misses from play. These accepted 117 of the 129 correct judgments with no
// wrong acceptance. Each limit sits about 0.14 below the nearest wrong answer,
// clear of the 0.11 the same answer moved by between runs: "Berlin" at 0.49
// on NamesOtherThing, "Protestant" for "The Protestant Reformation" at 0.59 on
// TooVague.
//
// MinMisspelling was added with the grader's tolerance cut to plain typos, and
// measured on the 92 cases in scripts/jev-judge-test.py, three runs each: the
// original set plus every answer typed in production that the old tolerance
// accepted and the new one does not. It took right answers from 141 to 150 of
// 168 judgments, wrong ones still 0 of 108. The highest-scoring wrong answer
// to get past both vetoes was "Stein" for "John Steinbeck" at 0.41, 0.24
// below the limit; one answer moved by at most 0.21 between runs.
var DefaultThresholds = Thresholds{
	MinAcceptable:      0.65,
	MinMisspelling:     0.65,
	MaxNamesOtherThing: 0.35,
	MaxTooVague:        0.45,
}

// Accepts reports whether the scores clear the thresholds: Acceptable or
// Misspelling high enough, and neither veto raised.
func (s Scores) Accepts(t Thresholds) bool {
	return (s.Acceptable >= t.MinAcceptable || s.Misspelling >= t.MinMisspelling) &&
		s.NamesOtherThing <= t.MaxNamesOtherThing &&
		s.TooVague <= t.MaxTooVague
}

// Judge scores one typed answer to one question.
type Judge interface {
	Score(ctx context.Context, q Question, typed string) (Scores, error)
}
