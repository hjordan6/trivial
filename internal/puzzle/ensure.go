package puzzle

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hjordan6/trivial/internal/clock"
)

// Settings are the generator knobs that come from configuration, bundled so
// the request path can carry them without depending on the config package.
type Settings struct {
	CooldownDays       int
	AnswerCooldownDays int
	TimeLimitSeconds   int
}

// lazyGenerationLockClass namespaces Ensure's advisory locks. Postgres keeps
// two-argument advisory locks in a key space separate from the
// single-argument lock db.Migrate takes, and the class distinguishes this use
// from any other two-argument lock the application might add later.
const lazyGenerationLockClass = 1

// Ensure returns the puzzle for a date, generating and storing it first if it
// is missing. On a nil error the puzzle is always non-nil.
//
// It exists so a lapsed generation cron cannot take the game down: without
// it, a day nobody pre-generated has no board, and every request for that
// day is served an error until an operator notices. Pre-generating remains
// the intended path — this is the safety net under it, not a replacement,
// because a board generated on the first request of the day cannot be
// reviewed or hand-edited before players see it.
//
// Ensure does not relax any constraint to produce a board. A day the library
// cannot fill still fails, with the same InsufficientContentError the CLI
// reports, so the caller can tell "nobody generated this day" (recoverable
// here) from "the library cannot fill this day" (not).
func Ensure(ctx context.Context, pool *pgxpool.Pool, date clock.Date, s Settings) (*Puzzle, error) {
	// The overwhelmingly common case is a board the cron already wrote. Read
	// it straight from the pool, taking no lock and opening no write
	// transaction, so the safety net costs a single query on the happy path.
	if p, err := Get(ctx, pool, date); err != nil || p != nil {
		return p, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin lazy generation for %s: %w", date, err)
	}
	defer tx.Rollback(ctx)

	// Serialize the first requests to arrive for a missing day. Without this
	// lock they all read no puzzle and all try to write one, and every loser
	// fails on daily_puzzles' primary key — turning a recoverable gap into an
	// error for most of the burst. The lock is transaction scoped, so the
	// commit or the deferred rollback below releases it and it can never
	// outlive the request. Whoever waits then finds the winner's board,
	// because GenerateFor re-reads the puzzle inside this transaction and
	// returns an existing one unchanged.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`,
		lazyGenerationLockClass, dateLockKey(date)); err != nil {
		return nil, fmt.Errorf("lock lazy generation for %s: %w", date, err)
	}

	g := Generator{
		DB:                 tx,
		CooldownDays:       s.CooldownDays,
		AnswerCooldownDays: s.AnswerCooldownDays,
		TimeLimitSeconds:   s.TimeLimitSeconds,
	}
	p, err := g.GenerateFor(ctx, date)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit lazy generation for %s: %w", date, err)
	}
	return p, nil
}

// dateLockKey packs a date into the second half of the advisory lock key, so
// two different days can generate concurrently while one day cannot generate
// twice. Postgres advisory lock keys are int32; a date as YYYYMMDD fits with
// room to spare.
func dateLockKey(date clock.Date) int32 {
	return int32(date.Year*10000 + int(date.Month)*100 + date.Day)
}
