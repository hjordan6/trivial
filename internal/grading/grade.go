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
}

// Grade compares a player's raw input against a question's already-normalized
// aliases and distractors. Input is normalized here; aliases and distractors
// are passed already normalized.
//
// The bar an answer has to clear is deliberately generous about spelling and
// deliberately strict about everything else: a player who knows the answer
// but mangles it should score, while a player who typed a different answer
// should not, however few edits separate the two words. See acceptable for
// the three gates that split those cases apart.
func Grade(input string, normalizedAliases, normalizedDistractors []string) Result {
	normalized := Normalize(input)
	if normalized == "" {
		return Result{Distance: -1}
	}

	// A wrong multiple-choice option is a real answer to some other question,
	// not a misspelling of this one, so proximity to one disqualifies the
	// submission outright. This is what makes the generous tolerance safe:
	// "Netball" is four edits from "Basketball" and would otherwise be
	// accepted, but it is zero edits from a distractor. Distance is measured
	// raw here — a distractor gets no tolerance of its own, it just has to be
	// no further away than the alias that would otherwise have matched.
	nearestDistractor := -1
	for _, distractor := range normalizedDistractors {
		if distractor == "" {
			continue
		}
		if d := damerauLevenshtein(normalized, distractor); nearestDistractor < 0 || d < nearestDistractor {
			nearestDistractor = d
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
		return accepted
	}

	if haveBest && bestSlack <= 1 && !closerToWrongAnswer(best.Distance) {
		best.NearMiss = true
	}
	return best
}

// acceptable reports whether input is close enough to alias to be read as a
// misspelling of it. Distance is their already-computed edit distance.
//
// Three gates, all of which must pass:
//
//   - Distance is within the alias's length-scaled tolerance (see toleranceFor).
//   - The first letter matches. Players misspell the middles of words, not
//     their beginnings — nobody who knows the answer is "Wakanda" types a
//     leading Z. The first letter is also what separates most same-length
//     wrong answers from the right one, so this gate costs almost nothing in
//     forgiveness and buys most of the precision back.
//   - At most a third of the edits are additions (see allowedAdditions).
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
	if !sameFirstRune(input, alias) {
		return false
	}
	return netAdditions(input, alias) <= allowedAdditions(distance)
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

// allowedAdditions caps how much of an edit budget may be spent on characters
// the player added rather than mistyped.
//
// Additions are the dangerous edit. Dropping or fumbling letters produces a
// misspelling of the same answer; adding letters produces a different answer,
// which is why "Irish Grand National" is six edits from "Grand National" and
// is emphatically not it. Substitutions and omissions can use the whole
// budget; additions get a third of it.
//
// The floor of one is deliberate. A third of a one-edit budget is zero, which
// would reject a doubled letter — "misssissippi" — and that is the single
// most common typo there is. Forgiving it is the entire point of the
// tolerance, so one addition is always allowed and the ratio governs from
// there.
func allowedAdditions(distance int) int {
	if allowed := distance / 3; allowed > 1 {
		return allowed
	}
	return 1
}

// maxTolerance is the most edits any alias will forgive. It is the ceiling of
// the ladder in toleranceFor, reached only by aliases long enough that six
// edits still leave the answer recognizable.
const maxTolerance = 6

// toleranceFor returns the edit distance an alias will forgive.
//
// The ladder is deliberately generous — a player who knows the answer but
// cannot spell it should score, and mangled spellings of long proper nouns
// are the common case this exists for. Tolerance scales with alias length
// rather than being flat, because the same edit budget means very different
// things at either end: six edits off "george washington" is still plainly
// that answer, while six edits off "paris" reaches every other five-letter
// city on the board. Length is the only thing separating "forgiving" from
// "accepts anything", so short answers stay tight even here.
//
// Tolerance alone is not what keeps a generous ladder honest — the first
// letter and addition gates in acceptable do most of that work, and the
// distractor check in Grade catches what they miss.
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
		return 1
	case n <= 8:
		return 3
	case n <= 12:
		return 5
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
