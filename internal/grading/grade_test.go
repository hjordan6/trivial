package grading

import "testing"

func TestToleranceFor(t *testing.T) {
	tests := []struct {
		alias string
		want  int
	}{
		{"1945", 0},
		{"apollo 11", 0},
		{"cat", 1},
		{"bird", 1},
		{"paris", 3},
		{"napoleon", 3},
		{"jupiter", 3},
		{"chocolate", 5},
		{"washington", 5},
		{"mississippi", 5},
		{"pennsylvania", 5},
		{"constantinople", 6},
		{"henry viii", 0},
		{"nicholas ii", 0},
		{"louis xiv", 0},
		{"world war ii", 0},
		{"george washington", 6},
	}
	for _, tt := range tests {
		if got := toleranceFor(tt.alias); got != tt.want {
			t.Errorf("toleranceFor(%q) = %d, want %d", tt.alias, got, tt.want)
		}
	}
}

// TestToleranceCeiling pins that the ladder stops at maxTolerance: an alias
// twice the length of the longest rung still forgives six edits and no more.
// Without this, "scales with length" could quietly become "unbounded".
func TestToleranceCeiling(t *testing.T) {
	for _, alias := range []string{"george washington", "the peloponnesian war", "antidisestablishmentarianism"} {
		if got := toleranceFor(alias); got != maxTolerance {
			t.Errorf("toleranceFor(%q) = %d, want %d", alias, got, maxTolerance)
		}
	}
}

func TestAllowedAdditions(t *testing.T) {
	tests := []struct{ distance, want int }{
		{0, 1}, {1, 1}, {2, 1}, {3, 1}, {5, 1}, {6, 2}, {9, 3}, {12, 4},
	}
	for _, tt := range tests {
		if got := allowedAdditions(tt.distance); got != tt.want {
			t.Errorf("allowedAdditions(%d) = %d, want %d", tt.distance, got, tt.want)
		}
	}
}

func TestGrade(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		aliases      []string
		distractors  []string
		wantCorrect  bool
		wantNearMiss bool
	}{
		{"exact match", "Paris", []string{"paris"}, nil, true, false},
		{"normalizes before comparing", "  The PARIS ", []string{"paris"}, nil, true, false},
		{"matches a secondary alias", "Bonaparte", []string{"napoleon", "bonaparte"}, nil, true, false},
		{"accepts a transposition", "Napolean", []string{"napoleon"}, nil, true, false},
		{"accepts one edit in a long answer", "Missisippi", []string{"mississippi"}, nil, true, false},
		{"accepts two edits in a long answer", "Massisippi", []string{"mississippi"}, nil, true, false},
		{"accepts three edits in a long answer", "Missippi", []string{"mississippi"}, nil, true, false},
		{"accepts one edit in a short answer", "Cet", []string{"cat"}, nil, true, false},
		{"accepts six edits in a very long answer", "Gorj Wasinten", []string{"george washington"}, nil, true, false},
		{"rejects seven edits in a very long answer", "Gorj Wasnten", []string{"george washington"}, nil, false, true},

		// The first-letter gate. Both of these sit inside their alias's
		// tolerance and are rejected purely on their opening character.
		{"rejects a wrong first letter in a short answer", "bat", []string{"cat"}, nil, false, true},
		{"rejects a wrong first letter within tolerance", "Zamunda", []string{"wakanda"}, nil, false, true},

		// The addition gate. Both are well inside tolerance and start
		// correctly; what sinks them is spending the budget on extra letters.
		{"accepts a doubled letter", "Misssissippi", []string{"mississippi"}, nil, true, false},
		{"rejects a suffix that outgrows the budget", "Mississippian", []string{"mississippi"}, nil, false, true},
		{"rejects an answer padded onto the alias", "Washingtonian", []string{"washington"}, nil, false, true},

		// The distractor guard.
		{"rejects an exact distractor", "Netball", []string{"basketball"}, []string{"netball", "volleyball"}, false, false},
		{"rejects an answer no further from a distractor", "Fourteen", []string{"fifteen"}, []string{"fourteen", "thirteen"}, false, false},
		{"accepts the same answer when it is not a distractor", "Fourteen", []string{"fifteen"}, nil, true, false},
		{"ignores a distant distractor", "Napolean", []string{"napoleon"}, []string{"churchill", "hitler"}, true, false},

		{"rejects a wrong year", "1946", []string{"1945"}, nil, false, true},
		{"rejects an unrelated short word", "dog", []string{"cat"}, nil, false, false},
		{"rejects an unrelated answer", "elephant", []string{"paris"}, nil, false, false},
		{"rejects an unrelated long answer", "Abraham Lincoln", []string{"george washington"}, nil, false, false},
		{"rejects empty input", "", []string{"paris"}, nil, false, false},
		{"rejects punctuation-only input", "???", []string{"paris"}, nil, false, false},
		{"rejects a wrong number in a long phrase", "world war 3", []string{"world war 2"}, nil, false, true},
		{"rejects a different king with the same trailing numeral length", "Henry VII", []string{"henry viii"}, nil, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Grade(tt.input, tt.aliases, tt.distractors)
			if got.Correct != tt.wantCorrect {
				t.Errorf("Grade(%q, %v, %v).Correct = %v, want %v", tt.input, tt.aliases, tt.distractors, got.Correct, tt.wantCorrect)
			}
			if got.NearMiss != tt.wantNearMiss {
				t.Errorf("Grade(%q, %v, %v).NearMiss = %v, want %v", tt.input, tt.aliases, tt.distractors, got.NearMiss, tt.wantNearMiss)
			}
		})
	}
}

// TestGradePrefersTheNearestAlias pins that Correct is decided by the closest
// accepting alias, not the first one in the slice. A submission that a later
// alias matches outright must not be sunk by an earlier alias that a
// distractor happens to beat.
func TestGradePrefersTheNearestAlias(t *testing.T) {
	// "egypt" is exact; "egyptians" is four edits away and no closer than the
	// distractor. Order must not change the verdict.
	forward := Grade("Egypt", []string{"egyptians", "egypt"}, []string{"mayans"})
	backward := Grade("Egypt", []string{"egypt", "egyptians"}, []string{"mayans"})

	if !forward.Correct || !backward.Correct {
		t.Errorf("Correct depends on alias order: forward=%v backward=%v", forward.Correct, backward.Correct)
	}
	if forward.Distance != 0 {
		t.Errorf("Grade matched %q at distance %d, want the exact alias at distance 0", forward.Matched, forward.Distance)
	}
}

// TestGradeNearMissOrderIndependent pins that NearMiss depends on the best
// slack (distance minus that alias's own tolerance), not on which alias
// happens to come first in the slice. "abcd" (4 chars, tolerance 1) and
// "wxabcd" (6 chars, tolerance 3) both sit at raw distance 4 from "wxyz", but
// their slack differs (3 vs. 1), so the alias with the smaller slack must
// win regardless of order.
func TestGradeNearMissOrderIndependent(t *testing.T) {
	forward := Grade("wxyz", []string{"abcd", "wxabcd"}, nil)
	backward := Grade("wxyz", []string{"wxabcd", "abcd"}, nil)

	if forward.NearMiss != backward.NearMiss {
		t.Errorf("NearMiss depends on alias order: forward=%v backward=%v", forward.NearMiss, backward.NearMiss)
	}
	if !forward.NearMiss {
		t.Errorf("Grade(%q, %v).NearMiss = %v, want true (slack 1 via %q)", "wxyz", []string{"abcd", "wxabcd"}, forward.NearMiss, "wxabcd")
	}
}

// TestGradeNeverNearMissesADistractor pins that a rejected distractor is not
// offered up for alias curation. Promoting it would make the question
// unanswerable, so this is the one rejection that must stay silent.
func TestGradeNeverNearMissesADistractor(t *testing.T) {
	got := Grade("Sixteen", []string{"fifteen"}, []string{"sixteen", "fourteen"})
	if got.Correct || got.NearMiss {
		t.Errorf("Grade(sixteen) = {Correct:%v NearMiss:%v}, want both false", got.Correct, got.NearMiss)
	}
}
