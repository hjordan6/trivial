package play_test

import (
	"testing"

	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/play"
)

func TestPoints(t *testing.T) {
	cases := []struct {
		difficulty content.Difficulty
		outcome    play.Outcome
		want       int
	}{
		{content.Easy, play.Star, 5},
		{content.Medium, play.Star, 6},
		{content.Hard, play.Star, 7},
		{content.Easy, play.Circle, 3},
		{content.Medium, play.Circle, 4},
		{content.Hard, play.Circle, 5},
		{content.Hard, play.Miss, 0},
		{content.Hard, play.Expired, 0},
		// A difficulty outside the enum cannot score, bonus or no bonus.
		{content.Difficulty("impossible"), play.Star, play.FreeTextBonus},
	}
	for _, c := range cases {
		if got := play.Points(c.difficulty, c.outcome); got != c.want {
			t.Errorf("Points(%q, %q) = %d, want %d", c.difficulty, c.outcome, got, c.want)
		}
	}
}

// The web client shows this total on the start screen and the results page. If
// the two ever disagree, the game is telling players a score is out of a number
// it cannot reach.
func TestMaxPointsIsAFlawlessBoard(t *testing.T) {
	if play.MaxPoints != 54 {
		t.Errorf("MaxPoints = %d, want 54 -- keep MAX_POINTS in web/src/stores/run.ts in step", play.MaxPoints)
	}
}
