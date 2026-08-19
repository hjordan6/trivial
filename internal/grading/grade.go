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
	NearMiss bool
	// Matched is the closest alias considered.
	Matched string
	// Distance is the edit distance to Matched, or -1 when nothing was compared.
	Distance int
}

// Grade compares a player's raw input against a question's already-normalized
// aliases. Input is normalized here; aliases are stored normalized.
func Grade(input string, normalizedAliases []string) Result {
	normalized := Normalize(input)
	if normalized == "" {
		return Result{Distance: -1}
	}

	// best is ranked by slack — how far its distance falls outside its own
	// tolerance — not by raw distance. Two aliases can tie on raw distance
	// while having different tolerances (a short alias vs. a long one), and
	// ranking by raw distance alone made NearMiss depend on slice order.
	// Ties in slack are broken by the smaller raw distance.
	best := Result{Distance: -1}
	haveBest := false
	bestSlack := 0
	for _, alias := range normalizedAliases {
		distance := damerauLevenshtein(normalized, alias)
		tolerance := toleranceFor(alias)
		if distance <= tolerance {
			return Result{Correct: true, Matched: alias, Distance: distance}
		}
		slack := distance - tolerance
		if !haveBest || slack < bestSlack || (slack == bestSlack && distance < best.Distance) {
			best = Result{Matched: alias, Distance: distance}
			haveBest = true
			bestSlack = slack
		}
	}

	if haveBest && bestSlack <= 1 {
		best.NearMiss = true
	}
	return best
}

// toleranceFor returns the edit distance an alias will forgive.
//
// Answers containing digits get no tolerance: 1945 and 1946 are one edit
// apart, and accepting the wrong year is a worse failure than rejecting a
// typo. Very short answers are excluded for the same reason — at four
// characters, one edit reaches too many other valid words.
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
	default:
		return 2
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
