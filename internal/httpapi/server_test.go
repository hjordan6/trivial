package httpapi

import (
	"testing"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/puzzle"
)

func date(t *testing.T, value string) clock.Date {
	t.Helper()
	d, err := clock.ParseDate(value)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestStreaks(t *testing.T) {
	tests := []struct {
		name             string
		dates            []string
		today            string
		current, longest int
	}{
		{"empty", nil, "2026-08-19", 0, 0},
		{"today may be unplayed", []string{"2026-08-16", "2026-08-17", "2026-08-18"}, "2026-08-19", 3, 3},
		{"missed past day breaks current", []string{"2026-08-15", "2026-08-16"}, "2026-08-19", 0, 2},
		{"calendar dates cross DST", []string{"2026-10-31", "2026-11-01", "2026-11-02"}, "2026-11-02", 3, 3},
		{"longest preserved", []string{"2026-08-01", "2026-08-02", "2026-08-03", "2026-08-18"}, "2026-08-19", 1, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dates := make([]clock.Date, len(tt.dates))
			for i, v := range tt.dates {
				dates[i] = date(t, v)
			}
			current, longest := streaks(dates, date(t, tt.today))
			if current != tt.current || longest != tt.longest {
				t.Fatalf("streaks = (%d,%d), want (%d,%d)", current, longest, tt.current, tt.longest)
			}
		})
	}
}

func TestStartPuzzleContainsTopicsButNoQuestions(t *testing.T) {
	p := &puzzle.Puzzle{Date: date(t, "2026-08-19"), TimeLimitSeconds: 135}
	for topic := 0; topic < 3; topic++ {
		for q := 0; q < 3; q++ {
			p.Entries = append(p.Entries, puzzle.Entry{TopicName: string(rune('A' + topic)), TopicPosition: topic, QuestionID: int64(topic*3 + q + 1), Prompt: "secret"})
		}
	}
	got := startPuzzle(p)
	if len(got.Entries) != 3 {
		t.Fatalf("got %d topic summaries, want 3", len(got.Entries))
	}
	for _, entry := range got.Entries {
		if entry.QuestionID != 0 || entry.Prompt != "" {
			t.Fatalf("start response leaked question: %+v", entry)
		}
	}
}
