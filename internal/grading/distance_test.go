package grading

import "testing"

func TestDamerauLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"kitten", "kitten", 0},
		{"kitten", "sitten", 1},
		{"kitten", "sitting", 3},
		{"napoleon", "napolean", 1},
		{"form", "from", 1},
		{"1945", "1946", 1},
		{"mississippi", "missisippi", 1},
		{"café", "cafe", 1},
	}
	for _, tt := range tests {
		if got := damerauLevenshtein(tt.a, tt.b); got != tt.want {
			t.Errorf("damerauLevenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
