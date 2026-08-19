// Package cli implements the trivial command line interface.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/config"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/db"
	"github.com/hjordan6/trivial/internal/puzzle"
)

// nowClock is the time source used to resolve "today" when a command (such
// as `puzzles generate` with no --from) needs the current date. Production
// uses the real clock; tests substitute a clock.Fake so the no-date ("cron")
// path is testable without depending on the real clock.
var nowClock clock.Clock = clock.Real{}

const usage = `trivial — daily trivia administration

Usage:
  trivial migrate up|down
  trivial seed apply [--file seed/questions.json]
  trivial puzzles generate [--from YYYY-MM-DD] [--days N]
  trivial puzzles show YYYY-MM-DD
  trivial help
`

// Run dispatches a command. Arguments are validated before any database
// connection is opened, so argument handling is testable without a database.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage:\n%s", usage)
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	case "migrate":
		return runMigrate(ctx, args[1:], stdout)
	case "seed":
		return runSeed(ctx, args[1:], stdout)
	case "puzzles":
		return runPuzzles(ctx, args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
}

func runMigrate(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: trivial migrate up|down")
	}
	var direction db.Direction
	switch args[0] {
	case "up":
		direction = db.Up
	case "down":
		direction = db.Down
	default:
		return fmt.Errorf("migrate direction must be up or down, got %q", args[0])
	}

	_, pool, err := connect(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, direction); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "migrations %s complete\n", args[0])
	return nil
}

func runSeed(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "apply" {
		return fmt.Errorf("usage: trivial seed apply [--file path]")
	}

	fs := flag.NewFlagSet("seed apply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("file", "seed/questions.json", "path to the seed file")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("usage: trivial seed apply [--file path]: %w", err)
	}

	data, err := os.ReadFile(*path)
	if err != nil {
		return fmt.Errorf("read seed file: %w", err)
	}
	seed, err := content.ParseSeed(data)
	if err != nil {
		return err
	}

	_, pool, err := connect(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	// content.ApplySeed issues roughly 380 statements with no transaction of
	// its own. ReplaceAliases in particular deletes a question's aliases
	// before reinserting them, so a mid-run failure against the bare pool
	// could leave an existing question with zero aliases — silently dropping
	// it out of EligibleQuestions rather than raising anything. Running the
	// whole seed inside one transaction makes the apply all-or-nothing.
	stats, err := applySeedInTx(ctx, pool, seed)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "seeded %d topics and %d questions from %s\n", stats.Topics, stats.Questions, *path)
	return nil
}

// applySeedInTx runs content.ApplySeed inside its own transaction, committing
// only on success and rolling back on any error so a failed seed leaves no
// partial writes behind.
func applySeedInTx(ctx context.Context, pool *pgxpool.Pool, seed content.SeedFile) (content.SeedStats, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return content.SeedStats{}, fmt.Errorf("begin seed transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	stats, err := content.ApplySeed(ctx, tx, seed)
	if err != nil {
		return content.SeedStats{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return content.SeedStats{}, fmt.Errorf("commit seed transaction: %w", err)
	}
	return stats, nil
}

func runPuzzles(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: trivial puzzles generate|show")
	}
	switch args[0] {
	case "generate":
		return runPuzzlesGenerate(ctx, args[1:], stdout)
	case "show":
		return runPuzzlesShow(ctx, args[1:], stdout)
	default:
		return fmt.Errorf("usage: trivial puzzles generate|show")
	}
}

func runPuzzlesGenerate(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("puzzles generate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	from := fs.String("from", "", "first date to generate, defaults to today")
	days := fs.Int("days", 1, "how many consecutive days to generate")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("usage: trivial puzzles generate [--from YYYY-MM-DD] [--days N]: %w", err)
	}
	if *days <= 0 {
		return fmt.Errorf("--days must be positive, got %d", *days)
	}

	var start clock.Date
	if *from != "" {
		parsed, err := clock.ParseDate(*from)
		if err != nil {
			return err
		}
		start = parsed
	}

	cfg, pool, err := connect(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	if *from == "" {
		start = clock.PuzzleDateAt(nowClock.Now(), cfg.PuzzleTimezone)
	}

	for i := 0; i < *days; i++ {
		date := start.AddDays(i)
		p, err := generateOneInTx(ctx, pool, cfg, date)
		if err != nil {
			return fmt.Errorf("generate %s: %w", date, err)
		}
		fmt.Fprintf(stdout, "%s: %s\n", date, topicSummary(p))
	}
	return nil
}

// generateOneInTx generates the puzzle for a single date inside its own
// transaction.
//
// puzzle.Insert writes one daily_puzzles row and then nine
// daily_puzzle_questions rows as separate statements. Against the bare pool,
// a failure partway through would leave a puzzle row with fewer than nine
// entries, and a later run's puzzle.Get would return that partial board as if
// it were finished — a corrupt day that regeneration could never repair.
// Running each date's generation in its own transaction, committed only on
// success, keeps the puzzle and its nine rows atomic. The transaction is
// scoped to this helper (rather than the whole loop) so its deferred
// rollback fires once per date instead of accumulating across every
// iteration.
func generateOneInTx(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, date clock.Date) (*puzzle.Puzzle, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin generate transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	g := puzzle.Generator{
		DB:               tx,
		CooldownDays:     cfg.QuestionCooldownDays,
		TimeLimitSeconds: cfg.TimeLimitSeconds,
	}
	p, err := g.GenerateFor(ctx, date)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit generate transaction: %w", err)
	}
	return p, nil
}

func runPuzzlesShow(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: trivial puzzles show YYYY-MM-DD")
	}
	date, err := clock.ParseDate(args[0])
	if err != nil {
		return err
	}

	_, pool, err := connect(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	p, err := puzzle.Get(ctx, pool, date)
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("no puzzle generated for %s", date)
	}

	fmt.Fprintf(stdout, "%s (%d seconds)\n", p.Date, p.TimeLimitSeconds)
	for _, e := range p.Entries {
		fmt.Fprintf(stdout, "  [%d] %-20s %-6s  %s\n", e.TopicPosition, e.TopicName, e.Difficulty, e.Prompt)
	}
	return nil
}

func topicSummary(p *puzzle.Puzzle) string {
	seen := map[string]bool{}
	var names []string
	for _, e := range p.Entries {
		if !seen[e.TopicSlug] {
			seen[e.TopicSlug] = true
			names = append(names, e.TopicName)
		}
	}
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

func connect(ctx context.Context) (config.Config, *pgxpool.Pool, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, nil, err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return config.Config{}, nil, err
	}
	return cfg, pool, nil
}
