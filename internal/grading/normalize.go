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

// specialLetterFolder maps letters that are their own Unicode base — they
// have no canonical decomposition into a base letter plus a combining mark,
// so the NFD/Mn stage below never touches them — to their plain-ASCII
// equivalent. Some map one-to-many (æ, œ, ß), which is why this is a string
// replacer rather than a rune-wise table.
var specialLetterFolder = strings.NewReplacer(
	"ø", "o", "Ø", "o",
	"ł", "l", "Ł", "l",
	"æ", "ae", "Æ", "ae",
	"œ", "oe", "Œ", "oe",
	"ß", "ss",
	"þ", "th", "Þ", "th",
	"ð", "d", "Ð", "d",
	"ı", "i",
	"đ", "d", "Đ", "d",
)

var leadingArticles = map[string]bool{"the": true, "a": true, "an": true}

// Normalize reduces an answer to its comparable form: no diacritics, no
// punctuation, lowercase, single-spaced, and without a leading article.
//
// Apostrophes are deleted rather than replaced with a space, so "O'Brien"
// becomes "obrien" rather than "o brien". A leading article is only stripped
// when something follows it, so the answer "The" survives intact.
//
// Normalize is not idempotent: article stripping runs once per call, so
// Normalize("The The Who") is "the who", and normalizing that result again
// yields "who". Callers must apply it exactly once to any given input —
// never normalize an already-normalized value.
func Normalize(s string) string {
	s = apostropheStripper.Replace(s)
	s = specialLetterFolder.Replace(s)

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
