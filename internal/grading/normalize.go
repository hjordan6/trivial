// Package grading turns free-text player answers into a correct/incorrect
// verdict. It has no database and no clock, so it is pure and fully
// table-testable.
package grading

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var apostropheStripper = strings.NewReplacer("'", "", "’", "", "ʼ", "")

var leadingArticles = map[string]bool{"the": true, "a": true, "an": true}

// Normalize reduces an answer to its comparable form: no diacritics, no
// punctuation, lowercase, single-spaced, and without a leading article.
//
// Apostrophes are deleted rather than replaced with a space, so "O'Brien"
// becomes "obrien" rather than "o brien". A leading article is only stripped
// when something follows it, so the answer "The" survives intact.
func Normalize(s string) string {
	s = apostropheStripper.Replace(s)

	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	if folded, _, err := transform.String(t, s); err == nil {
		s = folded
	}

	s = strings.ToLower(s)

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}

	fields := strings.Fields(b.String())
	if len(fields) > 1 && leadingArticles[fields[0]] {
		fields = fields[1:]
	}
	return strings.Join(fields, " ")
}
