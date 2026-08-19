package grading

import "testing"

func TestToleranceFor(t *testing.T) {
	tests := []struct {
		alias string
		want  int
	}{
		{"1945", 0},
		{"apollo 11", 0},
		{"cat", 0},
		{"bird", 0},
		{"paris", 1},
		{"napoleon", 1},
		{"jupiter", 1},
		{"washington", 2},
		{"mississippi", 2},
	}
	for _, tt := range tests {
		if got := toleranceFor(tt.alias); got != tt.want {
			t.Errorf("toleranceFor(%q) = %d, want %d", tt.alias, got, tt.want)
		}
	}
}

func TestGrade(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		aliases      []string
		wantCorrect  bool
		wantNearMiss bool
	}{
		{"exact match", "Paris", []string{"paris"}, true, false},
		{"normalizes before comparing", "  The PARIS ", []string{"paris"}, true, false},
		{"matches a secondary alias", "Bonaparte", []string{"napoleon", "bonaparte"}, true, false},
		{"accepts a transposition", "Napolean", []string{"napoleon"}, true, false},
		{"accepts one edit in a long answer", "Missisippi", []string{"mississippi"}, true, false},
		{"accepts two edits in a long answer", "Massisippi", []string{"mississippi"}, true, false},
		{"rejects three edits", "Missippi", []string{"mississippi"}, false, true},
		{"rejects a wrong year", "1946", []string{"1945"}, false, true},
		{"rejects a near-miss short word", "bat", []string{"cat"}, false, true},
		{"rejects an unrelated answer", "elephant", []string{"paris"}, false, false},
		{"rejects empty input", "", []string{"paris"}, false, false},
		{"rejects punctuation-only input", "???", []string{"paris"}, false, false},
		{"rejects a wrong number in a long phrase", "world war 3", []string{"world war 2"}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Grade(tt.input, tt.aliases)
			if got.Correct != tt.wantCorrect {
				t.Errorf("Grade(%q, %v).Correct = %v, want %v", tt.input, tt.aliases, got.Correct, tt.wantCorrect)
			}
			if got.NearMiss != tt.wantNearMiss {
				t.Errorf("Grade(%q, %v).NearMiss = %v, want %v", tt.input, tt.aliases, got.NearMiss, tt.wantNearMiss)
			}
		})
	}
}

// TestGradeNearMissOrderIndependent pins that NearMiss depends on the best
// slack (distance minus that alias's own tolerance), not on which alias
// happens to come first in the slice. "abcd" (4 chars, tolerance 0) and
// "abxyzq" (6 chars, tolerance 1) both sit at raw distance 2 from "abxy", but
// their slack differs (2 vs. 1), so the alias with the smaller slack must
// win regardless of order.
func TestGradeNearMissOrderIndependent(t *testing.T) {
	forward := Grade("abxy", []string{"abcd", "abxyzq"})
	backward := Grade("abxy", []string{"abxyzq", "abcd"})

	if forward.NearMiss != backward.NearMiss {
		t.Errorf("NearMiss depends on alias order: forward=%v backward=%v", forward.NearMiss, backward.NearMiss)
	}
	if !forward.NearMiss {
		t.Errorf("Grade(%q, %v).NearMiss = %v, want true (slack 1 via %q)", "abxy", []string{"abcd", "abxyzq"}, forward.NearMiss, "abxyzq")
	}
}
