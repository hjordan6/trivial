package content

import (
	"strings"
	"testing"
)

const validSeed = `{
  "topics": [
    {
      "slug": "geography",
      "name": "World Geography",
      "questions": [
        {
          "external_id": "geography-easy-1",
          "difficulty": "easy",
          "prompt": "What is the capital of France?",
          "answer": "Paris",
          "aliases": ["Paris, France"],
          "distractors": ["Lyon", "Marseille", "Bordeaux", "Nice", "Toulouse"]
        }
      ]
    }
  ]
}`

func TestParseSeedAcceptsValidFile(t *testing.T) {
	seed, err := ParseSeed([]byte(validSeed))
	if err != nil {
		t.Fatalf("ParseSeed() error = %v", err)
	}
	if len(seed.Topics) != 1 {
		t.Fatalf("got %d topics, want 1", len(seed.Topics))
	}
	q := seed.Topics[0].Questions[0]
	// The canonical answer is always an accepted alias, whether or not the
	// author listed it.
	if !contains(q.Aliases, "Paris") {
		t.Errorf("aliases = %v, want the canonical answer to be included", q.Aliases)
	}
}

func TestParseSeedRejectsBadFiles(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(string) string
		wantErr string
	}{
		{
			name:    "unknown difficulty",
			mutate:  func(s string) string { return strings.Replace(s, `"easy"`, `"trivial"`, 1) },
			wantErr: "difficulty",
		},
		{
			name:    "too few distractors",
			mutate:  func(s string) string { return strings.Replace(s, `, "Toulouse"`, ``, 1) },
			wantErr: "at least 5 distractors",
		},
		{
			name:    "distractor duplicates the answer",
			mutate:  func(s string) string { return strings.Replace(s, `"Lyon"`, `"Paris"`, 1) },
			wantErr: "distractor",
		},
		{
			name:    "distractor duplicates an alias",
			mutate:  func(s string) string { return strings.Replace(s, `"Marseille"`, `"Paris, France"`, 1) },
			wantErr: "distractor",
		},
		{
			// "Pariss" is not an exact match for the accepted answer "paris",
			// but it is one edit away, and grading.Grade would accept it: the
			// validator must run the real grader rather than exact string
			// comparison, or a distractor this close would be graded correct
			// at play time.
			name:    "distractor is a fuzzy match for an accepted answer",
			mutate:  func(s string) string { return strings.Replace(s, `"Lyon"`, `"Pariss"`, 1) },
			wantErr: "would be graded correct",
		},
		{
			name: "alias normalizes to empty",
			mutate: func(s string) string {
				return strings.Replace(s, `"aliases": ["Paris, France"]`, `"aliases": ["Paris, France", "???"]`, 1)
			},
			wantErr: "normalizes to empty",
		},
		{
			name:    "answer normalizes to empty",
			mutate:  func(s string) string { return strings.Replace(s, `"answer": "Paris"`, `"answer": "???"`, 1) },
			wantErr: "normalizes to empty",
		},
		{
			name: "duplicate distractor collapses below five distinct",
			mutate: func(s string) string {
				// Two raw entries, "Toulouse" replaced with a repeat of
				// "Lyon", so the file still lists 5 distractors but only 4
				// are distinct after normalization.
				return strings.Replace(s, `"Nice", "Toulouse"`, `"Nice", "Lyon"`, 1)
			},
			wantErr: "at least 5 distractors",
		},
		{
			name:    "empty prompt",
			mutate:  func(s string) string { return strings.Replace(s, `"What is the capital of France?"`, `""`, 1) },
			wantErr: "prompt",
		},
		{
			name:    "empty answer",
			mutate:  func(s string) string { return strings.Replace(s, `"answer": "Paris"`, `"answer": ""`, 1) },
			wantErr: "answer",
		},
		{
			name:    "missing external id",
			mutate:  func(s string) string { return strings.Replace(s, `"geography-easy-1"`, `""`, 1) },
			wantErr: "external_id",
		},
		{
			name: "duplicate external id",
			mutate: func(s string) string {
				return strings.Replace(s, `"questions": [`, `"questions": [`+duplicateQuestion+`,`, 1)
			},
			wantErr: "duplicate",
		},
		{
			name:    "empty topic slug",
			mutate:  func(s string) string { return strings.Replace(s, `"slug": "geography"`, `"slug": ""`, 1) },
			wantErr: "slug",
		},
		{
			name:    "empty topic name",
			mutate:  func(s string) string { return strings.Replace(s, `"name": "World Geography"`, `"name": ""`, 1) },
			wantErr: "name",
		},
		{
			name: "duplicate topic slug",
			mutate: func(s string) string {
				return strings.Replace(s, `"topics": [`, `"topics": [`+duplicateTopic+`,`, 1)
			},
			wantErr: "duplicate",
		},
		{
			name:    "malformed json",
			mutate:  func(s string) string { return s[:len(s)-1] },
			wantErr: "parse seed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseSeed([]byte(tt.mutate(validSeed)))
			if err == nil {
				t.Fatal("ParseSeed() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ParseSeed() error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

const duplicateQuestion = `{
  "external_id": "geography-easy-1",
  "difficulty": "easy",
  "prompt": "Duplicate",
  "answer": "Paris",
  "aliases": [],
  "distractors": ["a", "b", "c", "d", "e"]
}`

const duplicateTopic = `{
  "slug": "geography",
  "name": "Geography Two",
  "questions": []
}`

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
