package grading

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Result describes how a submitted answer compared against a question's
// accepted aliases.
type Result struct {
	// Correct reports whether the answer was accepted.
	Correct bool
	// NearMiss reports a rejected answer that came within one edit of being
	// accepted. These are logged so aliases can be curated from real misses.
	// An answer rejected for sitting closer to a wrong option is never a
	// NearMiss: promoting it to an alias would make the question unanswerable.
	NearMiss bool
	// Matched is the closest alias considered.
	Matched string
	// Distance is the edit distance to Matched, or -1 when nothing was compared.
	Distance int
	// ReadsAsWrongOption reports that the input would pass as a spelling of
	// one of the wrong options, by the same rules an alias is matched with.
	// Such an answer is settled -- the player picked a known-wrong answer -- so
	// it is never worth a second opinion on whether it might be right.
	ReadsAsWrongOption bool
}

// Grade compares a player's raw input against a question's already-normalized
// aliases and distractors. Input is normalized here; aliases and distractors
// are passed already normalized.
//
// The bar is deliberately narrow: an exact alias, or a small typo of one. A
// player who knows the answer but mangles it worse than that is not lost --
// the rejection goes to the answer judge (internal/judge), which reads the
// question and can tell "chlorafil" from "Finding Dory". The grader's job is
// to settle the obvious cases instantly and never accept a different answer,
// so where the two pull apart it gives up forgiveness for precision. See
// acceptable for the gates.
func Grade(input string, normalizedAliases, normalizedDistractors []string) Result {
	normalized := Normalize(input)
	if normalized == "" {
		return Result{Distance: -1}
	}

	// A wrong multiple-choice option is a real answer to some other question,
	// not a misspelling of this one, so proximity to one disqualifies the
	// submission outright: "Nigera" is one edit from "Nigeria" and would
	// otherwise be accepted, but it is just as close to "Niger". Distance is
	// measured raw here — a distractor gets no tolerance of its own, it just
	// has to be no further away than the alias that would otherwise have
	// matched.
	nearestDistractor := -1
	readsAsWrongOption := false
	for _, distractor := range normalizedDistractors {
		if distractor == "" {
			continue
		}
		d := damerauLevenshtein(normalized, distractor)
		if nearestDistractor < 0 || d < nearestDistractor {
			nearestDistractor = d
		}
		if acceptable(normalized, distractor, d) {
			readsAsWrongOption = true
		}
	}
	closerToWrongAnswer := func(aliasDistance int) bool {
		return nearestDistractor >= 0 && nearestDistractor <= aliasDistance
	}

	// Two passes in one loop: the best accepting alias (ranked by raw
	// distance) decides Correct, while the best rejecting alias decides
	// NearMiss. The latter is ranked by slack — how far its distance falls
	// outside its own tolerance — not by raw distance. Two aliases can tie on
	// raw distance while having different tolerances (a short alias vs. a long
	// one), and ranking by raw distance alone made NearMiss depend on slice
	// order. Ties in slack are broken by the smaller raw distance.
	accepted := Result{Distance: -1}
	haveAccepted := false
	best := Result{Distance: -1}
	haveBest := false
	bestSlack := 0
	for _, alias := range normalizedAliases {
		distance := damerauLevenshtein(normalized, alias)
		if acceptable(normalized, alias, distance) {
			if !haveAccepted || distance < accepted.Distance {
				accepted = Result{Matched: alias, Distance: distance}
				haveAccepted = true
			}
			continue
		}
		slack := distance - toleranceFor(alias)
		if !haveBest || slack < bestSlack || (slack == bestSlack && distance < best.Distance) {
			best = Result{Matched: alias, Distance: distance}
			haveBest = true
			bestSlack = slack
		}
	}

	if haveAccepted {
		// Report the alias that would have matched either way, so a rejection
		// here is still legible in logs; only Correct differs.
		accepted.Correct = !closerToWrongAnswer(accepted.Distance)
		accepted.ReadsAsWrongOption = readsAsWrongOption
		return accepted
	}

	if haveBest && bestSlack <= 1 && !closerToWrongAnswer(best.Distance) {
		best.NearMiss = true
	}
	best.ReadsAsWrongOption = readsAsWrongOption
	return best
}

// WrongOptionFor reports which wrong option input reads as a spelling of, by
// the same rules an alias is matched with -- the test behind
// ReadsAsWrongOption. It returns the index into normalizedDistractors of the
// nearest such option, or -1 when input reads as none of them.
//
// This is what lets the choices that follow a wrong typed answer strike out
// the option the player already gave: they knew it, spelt it well enough to
// count, and it was wrong.
func WrongOptionFor(input string, normalizedDistractors []string) int {
	normalized := Normalize(input)
	if normalized == "" {
		return -1
	}
	best, bestDistance := -1, 0
	for i, distractor := range normalizedDistractors {
		if distractor == "" {
			continue
		}
		d := damerauLevenshtein(normalized, distractor)
		if acceptable(normalized, distractor, d) && (best < 0 || d < bestDistance) {
			best, bestDistance = i, d
		}
	}
	return best
}

// acceptable reports whether input is close enough to alias to be read as a
// misspelling of it. Distance is their already-computed edit distance.
//
// Three gates, all of which must pass:
//
//   - Distance is within the alias's length-scaled tolerance (see toleranceFor).
//   - Each word starts with the right letter (see sameInitials).
//   - At most one character was added (see maxAdditions).
//
// An exact match short-circuits: the gates exist to police approximate
// matches and must never reject a literal one.
func acceptable(input, alias string, distance int) bool {
	if distance == 0 {
		return true
	}
	if distance > toleranceFor(alias) {
		return false
	}
	if !sameInitials(input, alias) {
		return false
	}
	return netAdditions(input, alias) <= maxAdditions
}

// sameInitials reports whether input starts with the same letter as alias
// and, when both have the same number of words, whether every word does.
//
// Players misspell the middles of words, not their beginnings -- nobody who
// knows the answer is "Wakanda" types a leading Z. A changed first letter is
// what most short wrong answers look like instead: "Kate Moss" for "Kate
// Bush", "Little Rock Five" for "Little Rock Nine", "Jets" for "Mets". Each is
// a couple of edits inside a long answer, and the whole-answer first letter
// alone let them all through.
//
// Words are compared one to one only when the counts agree, because a space
// typed or dropped ("radio head", "wutang clan") shifts every word after it
// and says nothing about whether the player knew the answer.
func sameInitials(input, alias string) bool {
	if !sameFirstRune(input, alias) {
		return false
	}
	in, al := strings.Fields(input), strings.Fields(alias)
	if len(in) != len(al) {
		return true
	}
	for i := range in {
		if !sameFirstRune(in[i], al[i]) {
			return false
		}
	}
	return true
}

func sameFirstRune(input, alias string) bool {
	a, _ := utf8.DecodeRuneInString(input)
	b, _ := utf8.DecodeRuneInString(alias)
	return a != utf8.RuneError && a == b
}

// netAdditions is how many more characters the player typed than the alias
// has. It is a floor on the additions any alignment of the two must make, not
// a traceback of one particular alignment: an answer cannot be six characters
// longer than the alias without having added six characters somewhere.
func netAdditions(input, alias string) int {
	extra := utf8.RuneCountInString(input) - utf8.RuneCountInString(alias)
	if extra < 0 {
		return 0
	}
	return extra
}

// maxAdditions caps how many characters the player may have added rather than
// mistyped.
//
// Additions are the dangerous edit. Dropping or fumbling letters produces a
// misspelling of the same answer; adding letters produces a different answer,
// which is why "Irish Grand National" is six edits from "Grand National" and
// is emphatically not it. Substitutions and omissions can use the whole
// budget.
//
// One rather than none, because a doubled letter -- "misssissippi" -- is the
// single most common typo there is.
const maxAdditions = 1

// maxTolerance is the most edits any alias will forgive. It is the ceiling of
// the ladder in toleranceFor.
const maxTolerance = 3

// toleranceFor returns the edit distance an alias will forgive.
//
// The ladder covers plain typos and nothing more: a dropped, doubled, swapped
// or fumbled letter or two. It used to run to six edits, and replaying every
// answer typed in production showed what the upper rungs were buying: three
// to six edits accepted "Berlin" for the Berlin Wall, "Gold" for the
// California Gold Rush, "Stein" for Steinbeck, "Finding Dory" for Finding
// Nemo and "Dallas Stars" for the Dallas Cowboys, alongside real misspellings
// like "chlorafil". The grader cannot tell those apart; the answer judge can,
// so everything past a typo is now its call.
//
// Tolerance scales with length because the same budget means very different
// things at either end: three edits off "george washington" is still plainly
// that answer, while one edit off "han" is "hun", a different people. Answers
// of four characters or fewer get none.
//
// The initials and addition gates in acceptable, and the distractor check in
// Grade, still apply within the budget.
//
// Answers containing digits get no tolerance at all: 1945 and 1946 are one
// edit apart, and accepting the wrong year is a worse failure than rejecting
// a typo. No amount of generosity elsewhere makes a wrong number right.
//
// The same reasoning applies to Roman numerals: "Henry VIII" and "Henry
// VII", or "Nicholas II" and "Nicholas I", are one edit apart but name
// different people, so a trailing numeral token also gets no tolerance. The
// check only looks at the alias's final whitespace-separated token, and only
// when the alias has more than one token, so a bare one-word answer that
// happens to be spelled from the letters i, v, x, l, c, d, m — "mix",
// "civil" — is unaffected and keeps its length-based tolerance.
func toleranceFor(alias string) int {
	for _, r := range alias {
		if unicode.IsDigit(r) {
			return 0
		}
	}
	if tokens := strings.Fields(alias); len(tokens) > 1 && isRomanNumeralToken(tokens[len(tokens)-1]) {
		return 0
	}
	switch n := utf8.RuneCountInString(alias); {
	case n <= 4:
		return 0
	case n <= 8:
		return 1
	case n <= 12:
		return 2
	default:
		return maxTolerance
	}
}

// isRomanNumeralToken reports whether token consists entirely of the letters
// used in Roman numerals. It does not validate that the letters form a
// well-formed numeral (e.g. "vix" would pass) — toleranceFor only calls it on
// a trailing token of a multi-word alias, where a false positive merely
// tightens tolerance rather than loosening it, so an overly permissive match
// is the safe direction to err in.
func isRomanNumeralToken(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		switch r {
		case 'i', 'v', 'x', 'l', 'c', 'd', 'm':
		default:
			return false
		}
	}
	return true
}
