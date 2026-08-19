package cli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/testsupport"
)

// TestRunPuzzlesGenerateUsesInjectedClockWhenFromIsOmitted covers the one
// path spec §9 claims is "tested with a fake clock" but, before nowClock
// existed, was not: `puzzles generate` with no --from, which is the path a
// cron invocation takes every day. It substitutes nowClock with a
// clock.Fake and checks that the resolved puzzle date — which surfaces in
// the InsufficientContentError message when the (unseeded) test database
// has no topics — matches the fake time rather than the real one.
//
// This is a white-box test (package cli, not cli_test) because nowClock is
// deliberately unexported: the smallest fix for testability, per the final
// review, is a package variable a test can substitute, not a public API.
func TestRunPuzzlesGenerateUsesInjectedClockWhenFromIsOmitted(t *testing.T) {
	testDBURL := os.Getenv("TEST_DATABASE_URL")
	if testDBURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; run with `make test`")
	}

	// Ensure the test database's schema is migrated. This also guarantees
	// (by virtue of every other integration test using a rolled-back
	// transaction) that no topics exist, so GenerateFor fails with
	// InsufficientContentError instead of needing seeded content — all this
	// test cares about is which date that error names.
	testsupport.MustPool(t)

	restoreClock := nowClock
	restoreDatabaseURL, hadDatabaseURL := os.LookupEnv("DATABASE_URL")
	restoreTimezone, hadTimezone := os.LookupEnv("PUZZLE_TIMEZONE")
	t.Cleanup(func() {
		nowClock = restoreClock
		if hadDatabaseURL {
			os.Setenv("DATABASE_URL", restoreDatabaseURL)
		} else {
			os.Unsetenv("DATABASE_URL")
		}
		if hadTimezone {
			os.Setenv("PUZZLE_TIMEZONE", restoreTimezone)
		} else {
			os.Unsetenv("PUZZLE_TIMEZONE")
		}
	})

	fakeNow := time.Date(2027, time.May, 1, 10, 0, 0, 0, time.UTC)
	nowClock = clock.Fake{T: fakeNow}
	os.Setenv("DATABASE_URL", testDBURL)
	os.Setenv("PUZZLE_TIMEZONE", "UTC")

	var stdout, stderr strings.Builder
	err := Run(context.Background(), []string{"puzzles", "generate"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Run() error = nil, want *puzzle.InsufficientContentError naming the fake clock's date")
	}
	if !strings.Contains(err.Error(), "2027-05-01") {
		t.Errorf("Run() error = %q, want it to name 2027-05-01 (the fake clock's date, UTC)", err)
	}
}
