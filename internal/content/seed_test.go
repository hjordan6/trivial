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
			name:    "empty prompt",
			mutate:  func(s string) string { return strings.Replace(s, `"What is the capital of France?"`, `""`, 1) },
			wantErr: "prompt",
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

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
