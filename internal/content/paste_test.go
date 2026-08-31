package content

import (
	"encoding/json"
	"strings"
	"testing"
)

// The payload a chat assistant actually produces: curly quotes as delimiters,
// a quotation inside one prompt, and curly apostrophes in the text.
const chatbotPaste = `{
  “questions”: [
    {
      “question”: “This duo took its name from a review calling an earlier band “daft punky thrash,” and later performed as robots.”,
      “category”: “Music”,
      “difficulty”: “hard”,
      “answer”: “Daft Punk”,
      “acceptedAnswers”: [],
      “multipleChoiceOptions”: [“Justice”, “Air”, “The Chemical Brothers”, “Daft Punk”]
    }
  ]
}`

func TestParseSeedAcceptsCurlyQuoteDelimiters(t *testing.T) {
	seed, err := ParseSeed([]byte(chatbotPaste))
	if err != nil {
		t.Fatalf("ParseSeed: %v", err)
	}
	if len(seed.Topics) != 1 || len(seed.Topics[0].Questions) != 1 {
		t.Fatalf("got %d topics", len(seed.Topics))
	}
	q := seed.Topics[0].Questions[0]
	if q.Answer != "Daft Punk" {
		t.Errorf("answer = %q", q.Answer)
	}
	// The quotation inside the prompt is the whole point: a blind replacement
	// would have ended the string at "daft punky thrash," and lost the rest.
	if !strings.Contains(q.Prompt, "“daft punky thrash,”") {
		t.Errorf("prompt lost its inner quotation: %q", q.Prompt)
	}
	if !strings.HasSuffix(q.Prompt, "performed as robots.") {
		t.Errorf("prompt was truncated: %q", q.Prompt)
	}
	if len(q.Distractors) != 3 {
		t.Errorf("distractors = %v", q.Distractors)
	}
}

func TestParseSeedLeavesValidJSONUntouched(t *testing.T) {
	// A prompt whose text ends with a curly quote followed by a comma is the
	// case the repair heuristic would get wrong -- so valid JSON must never
	// reach the repair at all.
	const valid = `[{"question":"He shouted “stop”, then left, and this is the rest.",
		"category":"Music","difficulty":5,"answer":"A",
		"multipleChoiceOptions":["A","b","c","d"]}]`
	seed, err := ParseSeed([]byte(valid))
	if err != nil {
		t.Fatalf("ParseSeed: %v", err)
	}
	got := seed.Topics[0].Questions[0].Prompt
	if !strings.HasSuffix(got, "and this is the rest.") {
		t.Errorf("valid JSON was altered: %q", got)
	}
}

func TestRepairPasteIsIdentityOnValidJSON(t *testing.T) {
	for _, in := range []string{
		`{"a":"b"}`,
		`[{"question":"a “quoted” phrase","answer":"x"}]`,
		`  {"topics": []}  `,
	} {
		if got := string(repairPaste([]byte(in))); got != in {
			t.Errorf("repairPaste(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestStripCodeFence(t *testing.T) {
	for name, in := range map[string]string{
		"tagged": "```json\n{\"questions\": []}\n```",
		"bare":   "```\n{\"questions\": []}\n```",
	} {
		t.Run(name, func(t *testing.T) {
			out := repairPaste([]byte(in))
			if !json.Valid(out) {
				t.Fatalf("fence survived: %q", out)
			}
		})
	}
}

// Curly quotes and a fence together, which is what a copy out of a chat window
// most often looks like.
func TestParseSeedHandlesFencedCurlyPaste(t *testing.T) {
	paste := "```json\n" + chatbotPaste + "\n```"
	seed, err := ParseSeed([]byte(paste))
	if err != nil {
		t.Fatalf("ParseSeed: %v", err)
	}
	if seed.Topics[0].Questions[0].Answer != "Daft Punk" {
		t.Errorf("answer = %q", seed.Topics[0].Questions[0].Answer)
	}
}

// A payload that is broken for a reason the repair cannot fix must still fail,
// and must not be reported as something else.
func TestParseSeedStillRejectsRealGarbage(t *testing.T) {
	if _, err := ParseSeed([]byte(`{"questions": [`)); err == nil {
		t.Fatal("truncated JSON was accepted")
	}
	if _, err := ParseSeed([]byte("not json at all")); err == nil {
		t.Fatal("prose was accepted")
	}
}

func TestStraightenDelimitersKeepsApostrophes(t *testing.T) {
	in := `{“a”: “Darlin’ and Roget’s”}`
	got := string(straightenDelimiters([]byte(in)))
	if !strings.Contains(got, "Darlin’ and Roget’s") {
		t.Errorf("apostrophes were altered: %q", got)
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("not valid JSON: %q", got)
	}
}
