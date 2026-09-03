package httpapi

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanNicknameTruncatesOnRuneBoundaries(t *testing.T) {
	// Each of these is multi-byte, so a byte-wise s[:40] lands mid-rune and
	// yields invalid UTF-8 -- which Postgres rejects outright on a text column.
	long := strings.Repeat("é", 45)
	got := cleanNickname(&long)
	if got == nil {
		t.Fatal("cleanNickname() = nil, want a value")
	}
	if !utf8.ValidString(*got) {
		t.Errorf("cleanNickname() = %q, which is not valid UTF-8", *got)
	}
	if n := utf8.RuneCountInString(*got); n != 40 {
		t.Errorf("rune count = %d, want 40", n)
	}
}

func TestCleanNicknameKeepsShortNamesWhole(t *testing.T) {
	for _, in := range []string{"jordan", "Ana María", "🎯 quizmaster"} {
		v := in
		got := cleanNickname(&v)
		if got == nil || *got != in {
			t.Errorf("cleanNickname(%q) = %v, want %q", in, got, in)
		}
	}
}

func TestCleanNicknameRejectsBlank(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n"} {
		v := in
		if got := cleanNickname(&v); got != nil {
			t.Errorf("cleanNickname(%q) = %q, want nil", in, *got)
		}
	}
}
