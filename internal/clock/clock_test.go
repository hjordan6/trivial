package clock

import (
	"testing"
	"time"
)

func TestPuzzleDateAtHandlesDST(t *testing.T) {
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}

	tests := []struct {
		name string
		utc  string
		want string
	}{
		{"one minute before winter rollover", "2026-01-15T06:59:00Z", "2026-01-14"},
		{"exactly at winter rollover", "2026-01-15T07:00:00Z", "2026-01-15"},
		{"one minute before summer rollover", "2026-07-01T05:59:00Z", "2026-06-30"},
		{"exactly at summer rollover", "2026-07-01T06:00:00Z", "2026-07-01"},
		{"winter offset does not apply in summer", "2026-07-01T06:30:00Z", "2026-07-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instant, err := time.Parse(time.RFC3339, tt.utc)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := PuzzleDateAt(instant, loc).String(); got != tt.want {
				t.Errorf("PuzzleDateAt(%s) = %s, want %s", tt.utc, got, tt.want)
			}
		})
	}
}

func TestDateRoundTrip(t *testing.T) {
	d, err := ParseDate("2026-08-18")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	if d.String() != "2026-08-18" {
		t.Errorf("String() = %s, want 2026-08-18", d.String())
	}
	if _, err := ParseDate("18/08/2026"); err == nil {
		t.Error("ParseDate accepted a non ISO date, want error")
	}
}

func TestDateAddDaysCrossesMonthAndYear(t *testing.T) {
	tests := []struct {
		start string
		days  int
		want  string
	}{
		{"2026-08-18", 1, "2026-08-19"},
		{"2026-08-31", 1, "2026-09-01"},
		{"2026-12-31", 1, "2027-01-01"},
		{"2026-03-01", -1, "2026-02-28"},
		{"2028-03-01", -1, "2028-02-29"},
	}
	for _, tt := range tests {
		d, err := ParseDate(tt.start)
		if err != nil {
			t.Fatalf("ParseDate(%s): %v", tt.start, err)
		}
		if got := d.AddDays(tt.days).String(); got != tt.want {
			t.Errorf("%s.AddDays(%d) = %s, want %s", tt.start, tt.days, got, tt.want)
		}
	}
}

func TestFakeClock(t *testing.T) {
	want := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	var c Clock = Fake{T: want}
	if !c.Now().Equal(want) {
		t.Errorf("Fake.Now() = %v, want %v", c.Now(), want)
	}
}
