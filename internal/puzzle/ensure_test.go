package puzzle_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/puzzle"
	"github.com/hjordan6/trivial/internal/testsupport"
)

// ensureSettings mirrors production: the answer cooldown on, the question
// cooldown long.
var ensureSettings = puzzle.Settings{CooldownDays: 180, AnswerCooldownDays: 14, TimeLimitSeconds: 135}

// Ensure's tests cannot use the rolled-back transaction the rest of this
// package shares. Ensure takes a pool, because the advisory lock it relies on
// only serializes separate connections, and the committed board is the thing
// being counted. Each test therefore writes to the shared test database under
// a slug prefix and a puzzle date of its own, and cleans up both before and
// after it runs so a previously crashed run cannot poison it.
func ensureFixture(t *testing.T, topics int) (*pgxpool.Pool, clock.Date) {
	t.Helper()
	pool := testsupport.MustPool(t)
	ctx := context.Background()
	prefix := "ensure-" + t.Name()

	cleanup := func() {
		if _, err := pool.Exec(ctx,
			`DELETE FROM daily_puzzles WHERE puzzle_date IN (
			   SELECT puzzle_date FROM daily_puzzle_questions dpq
			   JOIN topics t ON t.id = dpq.topic_id WHERE t.slug LIKE $1)`, prefix+"%"); err != nil {
			t.Fatalf("clean puzzles: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`DELETE FROM questions WHERE topic_id IN (SELECT id FROM topics WHERE slug LIKE $1)`,
			prefix+"%"); err != nil {
			t.Fatalf("clean questions: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM topics WHERE slug LIKE $1`, prefix+"%"); err != nil {
			t.Fatalf("clean topics: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	for i := 0; i < topics; i++ {
		seedTopicWithQuestionsPerDifficulty(t, pool, fmt.Sprintf("%s-%d", prefix, i), 2)
	}

	// Ensure reads every active topic, so a board could be filled by rows
	// this fixture did not create. Fail loudly rather than silently testing
	// something else.
	var active int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM topics WHERE active`).Scan(&active); err != nil {
		t.Fatalf("count active topics: %v", err)
	}
	if active != topics {
		t.Fatalf("test database has %d active topics, want the %d this test seeded: another run left rows behind",
			active, topics)
	}

	date, err := clock.ParseDate("2031-03-03")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM daily_puzzles WHERE puzzle_date = $1`, date); err != nil {
		t.Fatalf("clear the target date: %v", err)
	}
	return pool, date
}

func TestEnsureGeneratesAMissingDay(t *testing.T) {
	pool, date := ensureFixture(t, 3)
	ctx := context.Background()

	got, err := puzzle.Ensure(ctx, pool, date, ensureSettings)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if got == nil || len(got.Entries) != 9 {
		t.Fatalf("Ensure() returned %v entries, want 9", got)
	}

	// The board has to be committed, not just returned: the next request has
	// to find it rather than generate a second one.
	stored, err := puzzle.Get(ctx, pool, date)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored == nil {
		t.Fatal("Ensure() returned a board but committed nothing")
	}
	if len(stored.Entries) != 9 {
		t.Errorf("stored entries = %d, want 9", len(stored.Entries))
	}
}

func TestEnsureLeavesAnExistingDayAlone(t *testing.T) {
	pool, date := ensureFixture(t, 3)
	ctx := context.Background()

	first, err := puzzle.Ensure(ctx, pool, date, ensureSettings)
	if err != nil {
		t.Fatalf("first Ensure(): %v", err)
	}
	second, err := puzzle.Ensure(ctx, pool, date, ensureSettings)
	if err != nil {
		t.Fatalf("second Ensure(): %v", err)
	}
	if got, want := questionIDs(second), questionIDs(first); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("second Ensure() = %v, want the first board %v", got, want)
	}
	if n := countPuzzles(t, pool, date); n != 1 {
		t.Errorf("puzzle rows = %d after two Ensure calls, want 1", n)
	}
}

// TestEnsureGeneratesOnceForConcurrentFirstRequests is why Ensure takes an
// advisory lock. Without it every caller reads no puzzle, every caller tries
// to insert one, and all but the winner fail on daily_puzzles' primary key —
// so the burst of traffic that arrives the moment a missing day is first
// requested would mostly be served errors.
func TestEnsureGeneratesOnceForConcurrentFirstRequests(t *testing.T) {
	pool, date := ensureFixture(t, 3)
	ctx := context.Background()

	const callers = 6
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		boards  []string
		failure error
	)
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			p, err := puzzle.Ensure(ctx, pool, date, ensureSettings)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failure = err
				return
			}
			boards = append(boards, fmt.Sprint(questionIDs(p)))
		}()
	}
	wg.Wait()

	if failure != nil {
		t.Fatalf("a concurrent Ensure() failed: %v", failure)
	}
	if len(boards) != callers {
		t.Fatalf("got %d boards, want %d", len(boards), callers)
	}
	for i, board := range boards {
		if board != boards[0] {
			t.Errorf("caller %d saw board %s, want the same board as caller 0 (%s)", i, board, boards[0])
		}
	}
	if n := countPuzzles(t, pool, date); n != 1 {
		t.Errorf("puzzle rows = %d after %d concurrent calls, want 1", n, callers)
	}
}

// TestEnsureRefusesToFillADayTheLibraryCannot pins that the safety net does
// not become a way around the cooldowns: a day that cannot be filled fails,
// and fails without leaving a partial board behind.
func TestEnsureRefusesToFillADayTheLibraryCannot(t *testing.T) {
	pool, date := ensureFixture(t, 2) // a board needs three topics
	ctx := context.Background()

	_, err := puzzle.Ensure(ctx, pool, date, ensureSettings)
	var insufficient *puzzle.InsufficientContentError
	if !errors.As(err, &insufficient) {
		t.Fatalf("Ensure() error = %T (%v), want *puzzle.InsufficientContentError", err, err)
	}
	if n := countPuzzles(t, pool, date); n != 0 {
		t.Errorf("puzzle rows = %d after a failed Ensure, want 0", n)
	}
}

func countPuzzles(t *testing.T, pool *pgxpool.Pool, date clock.Date) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM daily_puzzles WHERE puzzle_date = $1`, date).Scan(&n); err != nil {
		t.Fatalf("count puzzles: %v", err)
	}
	return n
}
