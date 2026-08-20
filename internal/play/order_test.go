package play

import (
	"reflect"
	"testing"

	"github.com/hjordan6/trivial/internal/puzzle"
)

func TestShuffleQuestionsIsStableForRunSeed(t *testing.T) {
	first := testEntries()
	second := testEntries()
	shuffleQuestions(first, 42)
	shuffleQuestions(second, 42)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same run seed produced different question orders")
	}
}

func TestShuffleQuestionsChangesStoredBoardOrder(t *testing.T) {
	entries := testEntries()
	original := append([]puzzle.Entry(nil), entries...)
	shuffleQuestions(entries, 42)
	if reflect.DeepEqual(entries, original) {
		t.Fatal("question order was not shuffled")
	}
	seen := map[int64]bool{}
	for _, entry := range entries {
		seen[entry.QuestionID] = true
	}
	if len(seen) != len(original) {
		t.Fatal("shuffle lost or duplicated questions")
	}
}

func testEntries() []puzzle.Entry {
	entries := make([]puzzle.Entry, 9)
	for i := range entries {
		entries[i].QuestionID = int64(i + 1)
	}
	return entries
}
