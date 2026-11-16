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
		{"chocolate", 2},
		{"washington", 2},
		{"mississippi", 2},
		{"pennsylvania", 2},
		{"constantinople", 3},
		{"henry viii", 0},
		{"nicholas ii", 0},
		{"louis xiv", 0},
		{"world war ii", 0},
		{"george washington", 3},
	}
	for _, tt := range tests {
		if got := toleranceFor(tt.alias); got != tt.want {
			t.Errorf("toleranceFor(%q) = %d, want %d", tt.alias, got, tt.want)
		}
	}
}

// TestToleranceCeiling pins that the ladder stops at maxTolerance: an alias
// twice the length of the longest rung still forgives three edits and no
// more. Without this, "scales with length" could quietly become "unbounded".
func TestToleranceCeiling(t *testing.T) {
	for _, alias := range []string{"george washington", "the peloponnesian war", "antidisestablishmentarianism"} {
		if got := toleranceFor(alias); got != maxTolerance {
			t.Errorf("toleranceFor(%q) = %d, want %d", alias, got, maxTolerance)
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
		{"rejects two edits in a mid-length answer", "Napolian", []string{"napoleon"}, nil, false, true},
		{"accepts one edit in a long answer", "Missisippi", []string{"mississippi"}, nil, true, false},
		{"accepts two edits in a long answer", "Massisippi", []string{"mississippi"}, nil, true, false},
		{"rejects three edits in a long answer", "Missippi", []string{"mississippi"}, nil, false, true},
		{"rejects any edit in a short answer", "Cet", []string{"cat"}, nil, false, true},
		{"accepts three edits in a very long answer", "Gorge Wahsingtn", []string{"george washington"}, nil, true, false},
		{"rejects four edits in a very long answer", "Gorge Wahsngtn", []string{"george washington"}, nil, false, true},

		// The initials gate. Each of these sits inside its alias's tolerance
		// and is rejected purely on the letter a word opens with.
		{"rejects a wrong first letter within tolerance", "Baris", []string{"paris"}, nil, false, true},
		{"rejects a wrong first letter in a long answer", "Nississippi", []string{"mississippi"}, nil, false, true},
		{"rejects a later word that opens wrong", "Little Rock Five", []string{"little rock nine"}, nil, false, true},
		{"rejects a different team from the same city", "New York Jets", []string{"new york mets"}, nil, false, true},
		{"accepts a typo inside a later word", "Little Rock Nien", []string{"little rock nine"}, nil, true, false},
		{"compares words only when the counts agree", "Radio Head", []string{"radiohead"}, nil, true, false},

		// The addition gate. Both are inside tolerance and start correctly;
		// what sinks them is the second extra letter.
		{"accepts a doubled letter", "Misssissippi", []string{"mississippi"}, nil, true, false},
		{"rejects a suffix that outgrows the budget", "Mississippian", []string{"mississippi"}, nil, false, true},
		{"rejects an answer padded onto the alias", "Pennsylvanians", []string{"pennsylvania"}, nil, false, true},

		// The distractor guard.
		{"rejects an exact distractor", "Netball", []string{"basketball"}, []string{"netball", "volleyball"}, false, false},
		{"rejects an answer no further from a distractor", "Nigera", []string{"nigeria"}, []string{"niger", "chad"}, false, false},
		{"accepts the same answer when it is not a distractor", "Nigera", []string{"nigeria"}, nil, true, false},
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
	// "prussia" is exact; "prussian" accepts at one edit, which is no closer
	// than the distractor "russia". Order must not change the verdict.
	forward := Grade("Prussia", []string{"prussian", "prussia"}, []string{"russia"})
	backward := Grade("Prussia", []string{"prussia", "prussian"}, []string{"russia"})

	if !forward.Correct || !backward.Correct {
		t.Errorf("Correct depends on alias order: forward=%v backward=%v", forward.Correct, backward.Correct)
	}
	if forward.Distance != 0 {
		t.Errorf("Grade matched %q at distance %d, want the exact alias at distance 0", forward.Matched, forward.Distance)
	}
}

// TestGradeNearMissOrderIndependent pins that NearMiss depends on the best
// slack (distance minus that alias's own tolerance), not on which alias
// happens to come first in the slice. "wxab" (4 chars, tolerance 0) and
// "wxyzab" (6 chars, tolerance 1) both sit at raw distance 2 from "wxyz", but
// their slack differs (2 vs. 1), so the alias with the smaller slack must
// win regardless of order.
func TestGradeNearMissOrderIndependent(t *testing.T) {
	forward := Grade("wxyz", []string{"wxab", "wxyzab"}, nil)
	backward := Grade("wxyz", []string{"wxyzab", "wxab"}, nil)

	if forward.NearMiss != backward.NearMiss {
		t.Errorf("NearMiss depends on alias order: forward=%v backward=%v", forward.NearMiss, backward.NearMiss)
	}
	if !forward.NearMiss {
		t.Errorf("Grade(%q, %v).NearMiss = %v, want true (slack 1 via %q)", "wxyz", []string{"wxab", "wxyzab"}, forward.NearMiss, "wxyzab")
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

// TestGradeReadsAsWrongOption pins which rejections are settled by a wrong
// option. A misspelt distractor is; a wrong answer that merely isn't an alias
// is not, because that is exactly the case a second opinion exists for.
func TestGradeReadsAsWrongOption(t *testing.T) {
	aliases := []string{"mariana trench"}
	distractors := []string{"tonga trench", "java trench"}
	tests := []struct {
		input string
		want  bool
	}{
		{"Tonga Trench", true},
		{"tonga trensh", true},
		{"mariana", false},
		{"pacific", false},
		{"Mariana Trench", false},
	}
	for _, tt := range tests {
		if got := Grade(tt.input, aliases, distractors).ReadsAsWrongOption; got != tt.want {
			t.Errorf("Grade(%q).ReadsAsWrongOption = %v, want %v", tt.input, got, tt.want)
		}
	}
}
