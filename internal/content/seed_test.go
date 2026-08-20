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
	if seed.Topics[0].Weight != 1 {
		t.Errorf("default topic weight = %d, want 1", seed.Topics[0].Weight)
	}
	q := seed.Topics[0].Questions[0]
	// The canonical answer is always an accepted alias, whether or not the
	// author listed it.
	if !contains(q.Aliases, "Paris") {
		t.Errorf("aliases = %v, want the canonical answer to be included", q.Aliases)
	}
}

func TestParseSeedAcceptsFlatQuestionFormat(t *testing.T) {
	data := `[{
	  "question": "This Serbian star is an all-time great center.",
	  "category": "Sports",
	  "difficulty": 5,
	  "answer": "Nikola Jokić",
	  "acceptedAnswers": ["Nikola Jokic", "Jokic", "Jokić"],
	  "multipleChoiceOptions": ["Nikola Jokić", "Luka Dončić", "Giannis Antetokounmpo", "Joel Embiid"]
	}]`
	seed, err := ParseSeed([]byte(data))
	if err != nil {
		t.Fatalf("ParseSeed() error = %v", err)
	}
	if len(seed.Topics) != 1 || seed.Topics[0].Slug != "sports" {
		t.Fatalf("topics = %+v", seed.Topics)
	}
	question := seed.Topics[0].Questions[0]
	if question.Difficulty != 5 {
		t.Fatalf("difficulty = %d, want 5", question.Difficulty)
	}
	if len(question.Distractors) != 3 {
		t.Fatalf("distractors = %v, want three wrong options", question.Distractors)
	}
	if question.ExternalID == "" {
		t.Fatal("derived external id is empty")
	}
}

func TestParseSeedAcceptsWrappedFlatQuestionFormat(t *testing.T) {
	data := `{"questions":[{
	  "question":"A valid wrapped question?","category":"General Knowledge","difficulty":8,
	  "answer":"Yes","acceptedAnswers":[],
	  "multipleChoiceOptions":["Yes","No","Maybe","Unknown"]
	}]}`
	seed, err := ParseSeed([]byte(data))
	if err != nil {
		t.Fatalf("ParseSeed() error = %v", err)
	}
	if len(seed.Topics) != 1 || len(seed.Topics[0].Questions) != 1 {
		t.Fatalf("seed = %+v", seed)
	}
}

func TestParseSeedRejectsBadFiles(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(string) string
		wantErr string
	}{
		{
			name: "invalid topic weight",
			mutate: func(s string) string {
				return strings.Replace(s, `"name": "World Geography"`, `"name": "World Geography", "weight": -1`, 1)
			},
			wantErr: "weight",
		},
		{
			name:    "unknown difficulty",
			mutate:  func(s string) string { return strings.Replace(s, `"easy"`, `"trivial"`, 1) },
			wantErr: "difficulty",
		},
		{
			name: "too few distractors",
			mutate: func(s string) string {
				return strings.Replace(s, `"Lyon", "Marseille", "Bordeaux", "Nice", "Toulouse"`, `"Lyon", "Marseille"`, 1)
			},
			wantErr: "at least 3 distractors",
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
			name: "duplicate distractor collapses below three distinct",
			mutate: func(s string) string {
				return strings.Replace(s, `"Lyon", "Marseille", "Bordeaux", "Nice", "Toulouse"`, `"Lyon", "Lyon", "Marseille"`, 1)
			},
			wantErr: "at least 3 distractors",
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
