package grading

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"lowercases", "The Beatles", "beatles"},
		{"strips leading definite article", "The Great Gatsby", "great gatsby"},
		{"strips leading indefinite articles", "A Tale of Two Cities", "tale of two cities"},
		{"strips an", "an apple", "apple"},
		{"keeps a bare article", "the", "the"},
		{"only strips whole-word articles", "Ann Arbor", "ann arbor"},
		{"strips diacritics", "Café", "cafe"},
		{"strips multiple diacritics", "Gabriel García Márquez", "gabriel garcia marquez"},
		{"removes apostrophes without splitting", "O'Brien", "obrien"},
		{"removes curly apostrophes", "O’Brien", "obrien"},
		{"maps other punctuation to a space", "Rock-n-Roll!", "rock n roll"},
		{"collapses whitespace", "  George   Washington  ", "george washington"},
		{"preserves digits", "1945", "1945"},
		{"handles mixed alphanumerics", "Apollo 11", "apollo 11"},
		{"empty stays empty", "", ""},
		{"punctuation only becomes empty", "!?!", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.input); got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
