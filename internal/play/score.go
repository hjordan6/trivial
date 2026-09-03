package play

import "github.com/hjordan6/trivial/internal/content"

// The scoring rule lives here: harder questions pay more, and typing the answer
// instead of picking it off the list earns a bonus on top of the question's
// points.
//
// web/src/stores/run.ts carries the same three numbers, because the browser has
// to score the run in progress before the server is asked for anything. The two
// copies must move together.
const FreeTextBonus = 2

var DifficultyPoints = map[content.Difficulty]int{
	content.Easy:   3,
	content.Medium: 4,
	content.Hard:   5,
}

// MaxPoints is a flawless board: three topics at each difficulty, every answer
// typed.
var MaxPoints = 3*(DifficultyPoints[content.Easy]+DifficultyPoints[content.Medium]+DifficultyPoints[content.Hard]) + 9*FreeTextBonus

// Points scores one resolved answer. A miss, an expiry, an unresolved question
// and a difficulty the enum does not know are all worth nothing.
func Points(difficulty content.Difficulty, outcome Outcome) int {
	switch outcome {
	case Star:
		return DifficultyPoints[difficulty] + FreeTextBonus
	case Circle:
		return DifficultyPoints[difficulty]
	default:
		return 0
	}
}
