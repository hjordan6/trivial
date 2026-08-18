# Foundation & Content Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the tested, headless core of the daily trivia game — schema, seed content, a deterministic puzzle generator, and an answer-grading engine — exposed through a CLI, with no HTTP layer.

**Architecture:** A single Go module with a thin `cmd/trivial` CLI over `internal/` packages. The two pieces holding real logic — puzzle generation and answer grading — are built and tested in isolation from both the network and, in grading's case, the database. PostgreSQL runs in Docker; a second throwaway Postgres on port 5433 backs the integration tests, each of which runs inside a transaction that is rolled back.

**Tech Stack:** Go 1.24+, PostgreSQL 17, pgx v5, goose (embedded migrations), `golang.org/x/text` for Unicode normalization, Vue 3 + TypeScript + Vite (scaffold only), Docker Compose, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-08-18-daily-trivia-design.md`

## Global Constraints

- Module path is `github.com/hjordan6/trivial`. Every import path in this plan assumes it.
- Go 1.24 or newer. Go is **not currently installed** on this machine; Task 1 installs it.
- `PUZZLE_TIMEZONE` defaults to `America/Denver`. All puzzle-date arithmetic goes through a loaded `*time.Location` — never a hardcoded UTC offset, because the zone observes DST.
- `QUESTION_COOLDOWN_DAYS` defaults to `180`. The cooldown is a **hard constraint** and is never relaxed.
- `TIME_LIMIT_SECONDS` defaults to `135`, stored per-day on the puzzle row.
- There is no topic cooldown. Topics are chosen at random each day and may repeat.
- All instants are `timestamptz`. Puzzle dates are Postgres `date` and are represented in Go by `clock.Date`, never `time.Time`.
- No HTTP server, no Vue application logic in this plan. The Vue scaffold in Task 12 is a build-pipeline placeholder only.
- Integration tests require `TEST_DATABASE_URL`. When it is unset they call `t.Fatal`, never `t.Skip` — a silently skipped suite that reports green is worse than a failing one.
- Commit messages follow Conventional Commits (`feat:`, `test:`, `chore:`, `fix:`).

## File Structure

```
trivial/
├── Makefile                          # db-up, db-down, migrate, seed, test, lint
├── docker-compose.yml                # db (5432, persistent) + testdb (5433, tmpfs)
├── .env.example
├── go.mod / go.sum
├── cmd/trivial/main.go               # thin: parses argv, calls cli.Run, sets exit code
├── internal/
│   ├── cli/cli.go                    # subcommand dispatch, testable via io.Writer
│   ├── config/config.go              # env -> Config, with validation
│   ├── clock/clock.go                # Clock interface, Fake, and the Date type
│   ├── db/
│   │   ├── db.go                     # pgxpool open, DBTX interface, Migrate()
│   │   └── migrations/00001_initial_schema.sql
│   ├── content/
│   │   ├── store.go                  # topic/question/alias/distractor CRUD + eligibility query
│   │   └── seed.go                   # seed-file parsing, validation, idempotent apply
│   ├── grading/
│   │   ├── normalize.go              # Normalize()
│   │   ├── distance.go               # Damerau-Levenshtein (OSA)
│   │   └── grade.go                  # Grade() + toleranceFor()
│   ├── puzzle/
│   │   ├── store.go                  # read/write daily_puzzles + daily_puzzle_questions
│   │   └── generator.go              # GenerateFor(date), deterministic
│   └── testsupport/db.go             # MustPool, Tx helpers for integration tests
├── seed/questions.json               # hand-authored starter content
├── web/                              # Vite + Vue 3 + TS scaffold
└── .github/workflows/ci.yml
```

Boundaries worth naming: `grading` touches no database and no clock, so it is pure and exhaustively table-testable. `clock` owns the only place a timezone is interpreted. `puzzle.Generator` depends on `content` for its candidate pools but never queries directly, so its selection logic can be reasoned about without SQL in view.

---

### Task 1: Toolchain, module, and Docker Postgres

**Files:**
- Create: `go.mod`, `Makefile`, `docker-compose.yml`, `.env.example`, `.gitignore`

**Interfaces:**
- Consumes: nothing.
- Produces: a working `go` toolchain, the module path `github.com/hjordan6/trivial`, and two Postgres instances — application on `localhost:5432`, tests on `localhost:5433`.

- [ ] **Step 1: Install Go**

```bash
brew install go
go version
```

Expected: `go version go1.24` or newer. If Homebrew installs an older Go, stop and install from https://go.dev/dl/ instead — the `tool` directive used later requires 1.24.

- [ ] **Step 2: Initialize the module**

```bash
cd /Users/jordan/developer/trivial
go mod init github.com/hjordan6/trivial
```

- [ ] **Step 3: Write `.gitignore`**

```
/trivial
/web/node_modules
/web/dist
.env
```

- [ ] **Step 4: Write `docker-compose.yml`**

```yaml
services:
  db:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: trivial
      POSTGRES_PASSWORD: trivial
      POSTGRES_DB: trivial
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U trivial"]
      interval: 2s
      timeout: 3s
      retries: 30

  testdb:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: trivial
      POSTGRES_PASSWORD: trivial
      POSTGRES_DB: trivial_test
    ports:
      - "5433:5432"
    tmpfs:
      - /var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U trivial"]
      interval: 2s
      timeout: 3s
      retries: 30

volumes:
  pgdata:
```

`testdb` keeps its data in `tmpfs`, so the test database is fast and vanishes with the container. Never point `TEST_DATABASE_URL` at the `db` service — the test helper truncates and migrates freely.

- [ ] **Step 5: Write `.env.example`**

```
DATABASE_URL=postgres://trivial:trivial@localhost:5432/trivial?sslmode=disable
TEST_DATABASE_URL=postgres://trivial:trivial@localhost:5433/trivial_test?sslmode=disable
PUZZLE_TIMEZONE=America/Denver
QUESTION_COOLDOWN_DAYS=180
TIME_LIMIT_SECONDS=135
```

- [ ] **Step 6: Write the `Makefile`**

Note: this machine has GNU Make 3.81, so avoid `.ONESHELL` and other 4.x features. Recipe lines must be indented with **tabs**.

```make
DATABASE_URL ?= postgres://trivial:trivial@localhost:5432/trivial?sslmode=disable
TEST_DATABASE_URL ?= postgres://trivial:trivial@localhost:5433/trivial_test?sslmode=disable

.PHONY: db-up db-down migrate seed test lint fmt

db-up:
	docker compose up -d db testdb
	docker compose exec -T db sh -c 'until pg_isready -U trivial -q; do sleep 0.5; done'
	docker compose exec -T testdb sh -c 'until pg_isready -U trivial -q; do sleep 0.5; done'

db-down:
	docker compose down

migrate: db-up
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/trivial migrate up

seed: migrate
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/trivial seed apply

test: db-up
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./... -count=1

fmt:
	gofmt -w .

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
```

- [ ] **Step 7: Verify both databases come up**

```bash
make db-up
docker compose ps
```

Expected: both `db` and `testdb` listed as `running (healthy)`.

- [ ] **Step 8: Commit**

```bash
git add go.mod Makefile docker-compose.yml .env.example .gitignore
git commit -m "chore: scaffold go module and docker postgres"
```

---

### Task 2: Config package

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.Config{DatabaseURL string, PuzzleTimezone *time.Location, QuestionCooldownDays int, TimeLimitSeconds int}` and `config.Load() (Config, error)`.

- [ ] **Step 1: Write the failing test**

```go
package config

import (
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("PUZZLE_TIMEZONE", "")
	t.Setenv("QUESTION_COOLDOWN_DAYS", "")
	t.Setenv("TIME_LIMIT_SECONDS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.PuzzleTimezone.String(); got != "America/Denver" {
		t.Errorf("PuzzleTimezone = %q, want America/Denver", got)
	}
	if cfg.QuestionCooldownDays != 180 {
		t.Errorf("QuestionCooldownDays = %d, want 180", cfg.QuestionCooldownDays)
	}
	if cfg.TimeLimitSeconds != 135 {
		t.Errorf("TimeLimitSeconds = %d, want 135", cfg.TimeLimitSeconds)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "missing database url",
			env:     map[string]string{"DATABASE_URL": ""},
			wantErr: "DATABASE_URL",
		},
		{
			name:    "unknown timezone",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "PUZZLE_TIMEZONE": "Mars/Olympus"},
			wantErr: "Mars/Olympus",
		},
		{
			name:    "non numeric cooldown",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "QUESTION_COOLDOWN_DAYS": "soon"},
			wantErr: "QUESTION_COOLDOWN_DAYS",
		},
		{
			name:    "zero cooldown",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "QUESTION_COOLDOWN_DAYS": "0"},
			wantErr: "must be positive",
		},
		{
			name:    "negative time limit",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "TIME_LIMIT_SECONDS": "-5"},
			wantErr: "must be positive",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"DATABASE_URL", "PUZZLE_TIMEZONE", "QUESTION_COOLDOWN_DAYS", "TIME_LIMIT_SECONDS"} {
				t.Setenv(k, tt.env[k])
			}
			_, err := Load()
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load() error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Write the implementation**

```go
// Package config loads application settings from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds every environment-driven setting the application needs.
type Config struct {
	DatabaseURL          string
	PuzzleTimezone       *time.Location
	QuestionCooldownDays int
	TimeLimitSeconds     int
}

// Load reads configuration from the environment, applying defaults and
// rejecting values that would fail later in less obvious ways.
func Load() (Config, error) {
	var cfg Config

	cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}

	tzName := envOr("PUZZLE_TIMEZONE", "America/Denver")
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return Config{}, fmt.Errorf("PUZZLE_TIMEZONE %q is not a known timezone: %w", tzName, err)
	}
	cfg.PuzzleTimezone = loc

	if cfg.QuestionCooldownDays, err = positiveInt("QUESTION_COOLDOWN_DAYS", 180); err != nil {
		return Config{}, err
	}
	if cfg.TimeLimitSeconds, err = positiveInt("TIME_LIMIT_SECONDS", 135); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func positiveInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a number: %w", key, raw, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be positive, got %d", key, n)
	}
	return n, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS, all subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat: add environment configuration loading"
```

---

### Task 3: Clock and the Date type

**Files:**
- Create: `internal/clock/clock.go`
- Test: `internal/clock/clock_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `clock.Clock` interface with `Now() time.Time`; `clock.Real{}` and `clock.Fake{T time.Time}` implementations.
  - `clock.Date{Year int, Month time.Month, Day int}` with methods `String() string` (`2006-01-02`), `AddDays(n int) Date`, `Before(Date) bool`, `Equal(Date) bool`, and database plumbing `Scan(any) error` / `Value() (driver.Value, error)`.
  - `clock.ParseDate(s string) (Date, error)`.
  - `clock.PuzzleDateAt(t time.Time, loc *time.Location) Date`.

`Date` is a distinct type rather than a `time.Time` so that a date can never accidentally carry a time-of-day or a zone. Every later task uses `clock.Date` for puzzle dates.

- [ ] **Step 1: Write the failing test**

The DST cases are the point of this task. America/Denver moves to MDT on 2026-03-08, so a hardcoded `-07:00` offset would place the second case on the wrong day.

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/clock/ -v`
Expected: FAIL — `undefined: PuzzleDateAt`.

- [ ] **Step 3: Write the implementation**

```go
// Package clock provides an injectable time source and a timezone-aware
// calendar date type used for puzzle dates.
package clock

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// Clock is a time source. Production code uses Real; tests use Fake.
type Clock interface {
	Now() time.Time
}

// Real reports the actual current time.
type Real struct{}

// Now implements Clock.
func (Real) Now() time.Time { return time.Now() }

// Fake reports a fixed time.
type Fake struct{ T time.Time }

// Now implements Clock.
func (f Fake) Now() time.Time { return f.T }

const dateLayout = "2006-01-02"

// Date is a calendar date with no time-of-day and no timezone. It is the type
// used for puzzle dates everywhere in the application.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// PuzzleDateAt returns the calendar date that the instant t falls on in loc.
func PuzzleDateAt(t time.Time, loc *time.Location) Date {
	local := t.In(loc)
	return Date{Year: local.Year(), Month: local.Month(), Day: local.Day()}
}

// ParseDate parses an ISO-8601 date such as "2026-08-18".
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("parse date %q: %w", s, err)
	}
	return Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}, nil
}

// String renders the date as "2006-01-02".
func (d Date) String() string {
	return d.time().Format(dateLayout)
}

// AddDays returns the date n days after d. n may be negative.
func (d Date) AddDays(n int) Date {
	t := d.time().AddDate(0, 0, n)
	return Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}
}

// Before reports whether d falls before other.
func (d Date) Before(other Date) bool { return d.time().Before(other.time()) }

// Equal reports whether d and other are the same calendar date.
func (d Date) Equal(other Date) bool { return d == other }

// time renders the date as midnight UTC. It is only ever used for arithmetic
// and formatting, never as a real instant.
func (d Date) time() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

// Value implements driver.Valuer so a Date can be written to a Postgres date
// column.
func (d Date) Value() (driver.Value, error) { return d.time(), nil }

// Scan implements sql.Scanner so a Postgres date column can be read into a Date.
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		*d = Date{Year: v.Year(), Month: v.Month(), Day: v.Day()}
		return nil
	case string:
		parsed, err := ParseDate(v)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case nil:
		return fmt.Errorf("cannot scan NULL into clock.Date")
	default:
		return fmt.Errorf("cannot scan %T into clock.Date", src)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/clock/ -v`
Expected: PASS, including all five DST subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/clock
git commit -m "feat: add injectable clock and timezone-aware Date type"
```

---

### Task 4: Database connection, schema migration, and the test harness

**Files:**
- Create: `internal/db/db.go`, `internal/db/migrations/00001_initial_schema.sql`, `internal/testsupport/db.go`
- Test: `internal/db/db_test.go`

**Interfaces:**
- Consumes: `clock.Date` (Task 3).
- Produces:
  - `db.Open(ctx context.Context, url string) (*pgxpool.Pool, error)`
  - `db.Migrate(ctx context.Context, pool *pgxpool.Pool, direction db.Direction) error` with `db.Up` and `db.Down`
  - `db.DBTX` interface — satisfied by both `*pgxpool.Pool` and `pgx.Tx`, and accepted by every store function in later tasks
  - `testsupport.MustPool(t *testing.T) *pgxpool.Pool` and `testsupport.Tx(t *testing.T, pool *pgxpool.Pool) pgx.Tx`

The schema in this task covers only content and puzzles. Player, run, and share tables belong to Plan 2 and get their own migration there.

- [ ] **Step 1: Add dependencies**

```bash
go get github.com/jackc/pgx/v5@latest
go get github.com/pressly/goose/v3@latest
```

- [ ] **Step 2: Write the migration**

Create `internal/db/migrations/00001_initial_schema.sql`:

```sql
-- +goose Up
CREATE TYPE difficulty AS ENUM ('easy', 'medium', 'hard');
CREATE TYPE question_status AS ENUM ('draft', 'active', 'retired');

CREATE TABLE topics (
    id         bigserial PRIMARY KEY,
    slug       text NOT NULL UNIQUE,
    name       text NOT NULL,
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE questions (
    id               bigserial PRIMARY KEY,
    topic_id         bigint NOT NULL REFERENCES topics (id) ON DELETE RESTRICT,
    difficulty       difficulty NOT NULL,
    prompt           text NOT NULL,
    canonical_answer text NOT NULL,
    status           question_status NOT NULL DEFAULT 'draft',
    source           text,
    external_id      text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX questions_source_external_id_key
    ON questions (source, external_id)
    WHERE external_id IS NOT NULL;

CREATE INDEX questions_active_pool_idx
    ON questions (topic_id, difficulty)
    WHERE status = 'active';

CREATE TABLE question_aliases (
    id          bigserial PRIMARY KEY,
    question_id bigint NOT NULL REFERENCES questions (id) ON DELETE CASCADE,
    alias       text NOT NULL,
    normalized  text NOT NULL
);

CREATE UNIQUE INDEX question_aliases_normalized_key
    ON question_aliases (question_id, normalized);

CREATE TABLE question_distractors (
    id          bigserial PRIMARY KEY,
    question_id bigint NOT NULL REFERENCES questions (id) ON DELETE CASCADE,
    option_text text NOT NULL
);

CREATE UNIQUE INDEX question_distractors_option_key
    ON question_distractors (question_id, option_text);

CREATE TABLE daily_puzzles (
    puzzle_date        date PRIMARY KEY,
    time_limit_seconds integer NOT NULL DEFAULT 135 CHECK (time_limit_seconds > 0),
    created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE daily_puzzle_questions (
    puzzle_date    date NOT NULL REFERENCES daily_puzzles (puzzle_date) ON DELETE CASCADE,
    topic_id       bigint NOT NULL REFERENCES topics (id) ON DELETE RESTRICT,
    topic_position smallint NOT NULL CHECK (topic_position BETWEEN 0 AND 2),
    difficulty     difficulty NOT NULL,
    question_id    bigint NOT NULL REFERENCES questions (id) ON DELETE RESTRICT,
    PRIMARY KEY (puzzle_date, topic_id, difficulty)
);

CREATE UNIQUE INDEX daily_puzzle_questions_position_key
    ON daily_puzzle_questions (puzzle_date, topic_position, difficulty);

CREATE UNIQUE INDEX daily_puzzle_questions_once_per_day_key
    ON daily_puzzle_questions (question_id, puzzle_date);

CREATE INDEX daily_puzzle_questions_cooldown_idx
    ON daily_puzzle_questions (question_id, puzzle_date DESC);

-- +goose Down
DROP TABLE daily_puzzle_questions;
DROP TABLE daily_puzzles;
DROP TABLE question_distractors;
DROP TABLE question_aliases;
DROP TABLE questions;
DROP TABLE topics;
DROP TYPE question_status;
DROP TYPE difficulty;
```

- [ ] **Step 3: Write `internal/db/db.go`**

```go
// Package db owns the PostgreSQL connection pool and schema migrations.
package db

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DBTX is the subset of pgx used by store functions. Both *pgxpool.Pool and
// pgx.Tx satisfy it, which lets every store function run either against the
// pool or inside a test transaction that is rolled back.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Direction selects which way Migrate moves the schema.
type Direction int

const (
	// Up applies all pending migrations.
	Up Direction = iota
	// Down rolls back the most recent migration.
	Down
)

// Open creates a connection pool and verifies it can reach the database.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate applies or rolls back schema migrations. It takes a Postgres
// advisory lock first so concurrent callers — several test binaries starting
// at once, or two application instances booting together — serialize instead
// of racing goose's version table.
func Migrate(ctx context.Context, pool *pgxpool.Pool, direction Direction) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()

	const lockID = 4815162342
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockID)
	}()

	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	switch direction {
	case Up:
		if err := goose.UpContext(ctx, sqlDB, "migrations"); err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
	case Down:
		if err := goose.DownContext(ctx, sqlDB, "migrations"); err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
	default:
		return fmt.Errorf("unknown migration direction %d", direction)
	}
	return nil
}
```

- [ ] **Step 4: Write `internal/testsupport/db.go`**

```go
// Package testsupport provides database helpers for integration tests.
package testsupport

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hjordan6/trivial/internal/db"
)

var (
	once     sync.Once
	pool     *pgxpool.Pool
	initErr  error
)

// MustPool returns a migrated connection pool against TEST_DATABASE_URL.
//
// It fails the test when TEST_DATABASE_URL is unset rather than skipping. A
// skipped integration suite still reports green, which is exactly how a broken
// database layer reaches production.
func MustPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL is not set; run tests with `make test`")
	}

	once.Do(func() {
		ctx := context.Background()
		pool, initErr = db.Open(ctx, url)
		if initErr != nil {
			return
		}
		initErr = db.Migrate(ctx, pool, db.Up)
	})
	if initErr != nil {
		t.Fatalf("prepare test database: %v", initErr)
	}
	return pool
}

// Tx begins a transaction that is rolled back when the test finishes, so tests
// share one database without sharing state.
func Tx(t *testing.T, p *pgxpool.Pool) pgx.Tx {
	t.Helper()

	tx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(context.Background())
	})
	return tx
}
```

- [ ] **Step 5: Write the failing test**

This test also proves that `clock.Date` round-trips through a Postgres `date` column via its `Valuer`/`Scanner`, which every later task depends on.

```go
package db_test

import (
	"context"
	"testing"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/testsupport"
)

func TestMigrationsCreateSchema(t *testing.T) {
	pool := testsupport.MustPool(t)
	ctx := context.Background()

	for _, table := range []string{
		"topics", "questions", "question_aliases", "question_distractors",
		"daily_puzzles", "daily_puzzle_questions",
	} {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                WHERE table_schema = 'public' AND table_name = $1)`,
			table).Scan(&exists)
		if err != nil {
			t.Fatalf("query for table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s does not exist after migration", table)
		}
	}
}

func TestDateRoundTripsThroughPostgres(t *testing.T) {
	pool := testsupport.MustPool(t)
	tx := testsupport.Tx(t, pool)
	ctx := context.Background()

	want, err := clock.ParseDate("2026-03-08")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO daily_puzzles (puzzle_date, time_limit_seconds) VALUES ($1, $2)`,
		want, 135); err != nil {
		t.Fatalf("insert puzzle: %v", err)
	}

	var got clock.Date
	if err := tx.QueryRow(ctx,
		`SELECT puzzle_date FROM daily_puzzles WHERE puzzle_date = $1`, want).Scan(&got); err != nil {
		t.Fatalf("select puzzle: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("round-tripped date = %s, want %s", got, want)
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `make test`
Expected: FAIL — the `db` package does not compile yet, or the tables do not exist.

- [ ] **Step 7: Run test to verify it passes**

Run: `make test`
Expected: PASS.

If `TestDateRoundTripsThroughPostgres` fails with an encoding error, pgx is not picking up `clock.Date`'s `driver.Valuer`. Fix it by registering the type explicitly rather than by changing call sites — every later task passes `clock.Date` directly as a query argument.

- [ ] **Step 8: Commit**

```bash
git add internal/db internal/testsupport go.mod go.sum
git commit -m "feat: add postgres pool, embedded migrations, and test harness"
```

---

### Task 5: Answer normalization

**Files:**
- Create: `internal/grading/normalize.go`
- Test: `internal/grading/normalize_test.go`

**Interfaces:**
- Consumes: nothing. This package touches neither the database nor the clock.
- Produces: `grading.Normalize(s string) string`.

Normalization runs on both sides of every comparison: player input at answer time, and alias text at the moment it is written to `question_aliases.normalized`. The two must use this one function, or stored aliases will never match live input.

- [ ] **Step 1: Add the dependency**

```bash
go get golang.org/x/text@latest
```

- [ ] **Step 2: Write the failing test**

```go
package grading

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"lowercases", "The Beatles", "beatles"},
		{"strips leading definite article", "The Great Gatsby", "great gatsby"},
		{"strips leading indefinite articles", "A Tale of Two Cities", "tale of two cities"},
		{"strips an", "an apple", "apple"},
		{"keeps a bare article", "the", "the"},
		{"only strips whole-word articles", "Ann Arbor", "ann arbor"},
		{"strips diacritics", "Café", "cafe"},
		{"strips multiple diacritics", "Gabriel García Márquez", "gabriel garcia marquez"},
		{"removes apostrophes without splitting", "O'Brien", "obrien"},
		{"removes curly apostrophes", "O’Brien", "obrien"},
		{"maps other punctuation to a space", "Rock-n-Roll!", "rock n roll"},
		{"collapses whitespace", "  George   Washington  ", "george washington"},
		{"preserves digits", "1945", "1945"},
		{"handles mixed alphanumerics", "Apollo 11", "apollo 11"},
		{"empty stays empty", "", ""},
		{"punctuation only becomes empty", "!?!", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.input); got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/grading/ -run TestNormalize -v`
Expected: FAIL — `undefined: Normalize`.

- [ ] **Step 4: Write the implementation**

```go
// Package grading turns free-text player answers into a correct/incorrect
// verdict. It has no database and no clock, so it is pure and fully
// table-testable.
package grading

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var apostropheStripper = strings.NewReplacer("'", "", "’", "", "ʼ", "")

var leadingArticles = map[string]bool{"the": true, "a": true, "an": true}

// Normalize reduces an answer to its comparable form: no diacritics, no
// punctuation, lowercase, single-spaced, and without a leading article.
//
// Apostrophes are deleted rather than replaced with a space, so "O'Brien"
// becomes "obrien" rather than "o brien". A leading article is only stripped
// when something follows it, so the answer "The" survives intact.
func Normalize(s string) string {
	s = apostropheStripper.Replace(s)

	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	if folded, _, err := transform.String(t, s); err == nil {
		s = folded
	}

	s = strings.ToLower(s)

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}

	fields := strings.Fields(b.String())
	if len(fields) > 1 && leadingArticles[fields[0]] {
		fields = fields[1:]
	}
	return strings.Join(fields, " ")
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/grading/ -run TestNormalize -v`
Expected: PASS, all 16 subtests.

- [ ] **Step 6: Commit**

```bash
git add internal/grading go.mod go.sum
git commit -m "feat: add answer normalization"
```

---

### Task 6: Edit distance and grading

**Files:**
- Create: `internal/grading/distance.go`, `internal/grading/grade.go`
- Test: `internal/grading/distance_test.go`, `internal/grading/grade_test.go`

**Interfaces:**
- Consumes: `grading.Normalize` (Task 5).
- Produces:
  - `grading.Result{Correct bool, NearMiss bool, Matched string, Distance int}`
  - `grading.Grade(input string, normalizedAliases []string) Result`

`Grade` takes aliases **already normalized** — they are stored that way in `question_aliases.normalized`. It normalizes only the player's input.

Tolerance, per the spec: an alias containing any digit or shorter than five characters must match exactly; five to eight characters allow one edit; nine or more allow two. Distance is Damerau-Levenshtein (optimal string alignment), so a transposition costs one edit rather than two — swapped letters are the most common typing error, and `napolean` for `napoleon` should not be rejected.

- [ ] **Step 1: Write the failing distance test**

```go
package grading

import "testing"

func TestDamerauLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"kitten", "kitten", 0},
		{"kitten", "sitten", 1},
		{"kitten", "sitting", 3},
		{"napoleon", "napolean", 1},
		{"form", "from", 1},
		{"1945", "1946", 1},
		{"mississippi", "missisippi", 1},
		{"café", "cafe", 1},
	}
	for _, tt := range tests {
		if got := damerauLevenshtein(tt.a, tt.b); got != tt.want {
			t.Errorf("damerauLevenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/grading/ -run TestDamerauLevenshtein -v`
Expected: FAIL — `undefined: damerauLevenshtein`.

- [ ] **Step 3: Write `internal/grading/distance.go`**

```go
package grading

// damerauLevenshtein returns the optimal string alignment distance between a
// and b, counting a transposition of two adjacent characters as a single edit.
//
// It operates on runes, not bytes, so multi-byte characters count as one.
func damerauLevenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	twoBack := make([]int, lb+1)
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				if transposed := twoBack[j-2] + 1; transposed < curr[j] {
					curr[j] = transposed
				}
			}
		}
		twoBack, prev, curr = prev, curr, twoBack
	}
	return prev[lb]
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/grading/ -run TestDamerauLevenshtein -v`
Expected: PASS.

- [ ] **Step 5: Write the failing grade test**

```go
package grading

import "testing"

func TestToleranceFor(t *testing.T) {
	tests := []struct {
		alias string
		want  int
	}{
		{"1945", 0},
		{"apollo 11", 0},
		{"cat", 0},
		{"bird", 0},
		{"paris", 1},
		{"napoleon", 1},
		{"jupiter", 1},
		{"washington", 2},
		{"mississippi", 2},
	}
	for _, tt := range tests {
		if got := toleranceFor(tt.alias); got != tt.want {
			t.Errorf("toleranceFor(%q) = %d, want %d", tt.alias, got, tt.want)
		}
	}
}

func TestGrade(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		aliases      []string
		wantCorrect  bool
		wantNearMiss bool
	}{
		{"exact match", "Paris", []string{"paris"}, true, false},
		{"normalizes before comparing", "  The PARIS ", []string{"paris"}, true, false},
		{"matches a secondary alias", "Bonaparte", []string{"napoleon", "bonaparte"}, true, false},
		{"accepts a transposition", "Napolean", []string{"napoleon"}, true, false},
		{"accepts one edit in a long answer", "Missisippi", []string{"mississippi"}, true, false},
		{"accepts two edits in a long answer", "Massisippi", []string{"mississippi"}, true, false},
		{"rejects three edits", "Missippi", []string{"mississippi"}, false, false},
		{"rejects a wrong year", "1946", []string{"1945"}, false, true},
		{"rejects a near-miss short word", "bat", []string{"cat"}, false, true},
		{"rejects an unrelated answer", "elephant", []string{"paris"}, false, false},
		{"rejects empty input", "", []string{"paris"}, false, false},
		{"rejects punctuation-only input", "???", []string{"paris"}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Grade(tt.input, tt.aliases)
			if got.Correct != tt.wantCorrect {
				t.Errorf("Grade(%q, %v).Correct = %v, want %v", tt.input, tt.aliases, got.Correct, tt.wantCorrect)
			}
			if got.NearMiss != tt.wantNearMiss {
				t.Errorf("Grade(%q, %v).NearMiss = %v, want %v", tt.input, tt.aliases, got.NearMiss, tt.wantNearMiss)
			}
		})
	}
}
```

`Massisippi` against `mississippi` is two edits, which a nine-plus-character alias allows. `Missippi` is three, which it does not.

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/grading/ -run 'TestGrade|TestToleranceFor' -v`
Expected: FAIL — `undefined: Grade`.

- [ ] **Step 7: Write `internal/grading/grade.go`**

```go
package grading

import (
	"unicode"
	"unicode/utf8"
)

// Result describes how a submitted answer compared against a question's
// accepted aliases.
type Result struct {
	// Correct reports whether the answer was accepted.
	Correct bool
	// NearMiss reports a rejected answer that came within one edit of being
	// accepted. These are logged so aliases can be curated from real misses.
	NearMiss bool
	// Matched is the closest alias considered.
	Matched string
	// Distance is the edit distance to Matched, or -1 when nothing was compared.
	Distance int
}

// Grade compares a player's raw input against a question's already-normalized
// aliases. Input is normalized here; aliases are stored normalized.
func Grade(input string, normalizedAliases []string) Result {
	normalized := Normalize(input)
	if normalized == "" {
		return Result{Distance: -1}
	}

	best := Result{Distance: -1}
	for _, alias := range normalizedAliases {
		distance := damerauLevenshtein(normalized, alias)
		if distance <= toleranceFor(alias) {
			return Result{Correct: true, Matched: alias, Distance: distance}
		}
		if best.Distance < 0 || distance < best.Distance {
			best = Result{Matched: alias, Distance: distance}
		}
	}

	if best.Distance >= 0 && best.Distance <= toleranceFor(best.Matched)+1 {
		best.NearMiss = true
	}
	return best
}

// toleranceFor returns the edit distance an alias will forgive.
//
// Answers containing digits get no tolerance: 1945 and 1946 are one edit
// apart, and accepting the wrong year is a worse failure than rejecting a
// typo. Very short answers are excluded for the same reason — at four
// characters, one edit reaches too many other valid words.
func toleranceFor(alias string) int {
	for _, r := range alias {
		if unicode.IsDigit(r) {
			return 0
		}
	}
	switch n := utf8.RuneCountInString(alias); {
	case n <= 4:
		return 0
	case n <= 8:
		return 1
	default:
		return 2
	}
}
```

- [ ] **Step 8: Run the whole grading package**

Run: `go test ./internal/grading/ -v`
Expected: PASS, every subtest across all three test files.

- [ ] **Step 9: Commit**

```bash
git add internal/grading
git commit -m "feat: add damerau-levenshtein grading with digit-aware tolerance"
```

---

### Task 7: Content store

**Files:**
- Create: `internal/content/store.go`
- Test: `internal/content/store_test.go`

**Interfaces:**
- Consumes: `db.DBTX` (Task 4), `clock.Date` (Task 3), `grading.Normalize` (Task 5).
- Produces:
  - `content.Difficulty` (`content.Easy`, `content.Medium`, `content.Hard`, `content.AllDifficulties`)
  - `content.Topic{ID int64, Slug, Name string, Active bool}`
  - `content.Question{ID, TopicID int64, Difficulty Difficulty, Prompt, CanonicalAnswer string}`
  - `content.UpsertTopic(ctx, db, slug, name string) (int64, error)`
  - `content.UpsertQuestion(ctx, db, in QuestionInput) (int64, error)`
  - `content.ReplaceAliases(ctx, db, questionID int64, aliases []string) error`
  - `content.ReplaceDistractors(ctx, db, questionID int64, options []string) error`
  - `content.ActiveTopics(ctx, db) ([]Topic, error)`
  - `content.EligibleQuestions(ctx, db, topicID int64, d Difficulty, target clock.Date, cooldownDays int) ([]Question, error)`

`EligibleQuestions` is the single place the cooldown rule lives. It excludes any question used within `cooldownDays` on **either side** of the target date, so regenerating an old day cannot collide with days already generated after it — and it excludes the target date itself, which is what makes regeneration idempotent rather than self-blocking.

- [ ] **Step 1: Write the failing test**

```go
package content_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/testsupport"
)

func target(t *testing.T) clock.Date {
	t.Helper()
	d, err := clock.ParseDate("2026-08-18")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	return d
}

// makeQuestion inserts an active question with one alias and five distractors,
// which is the minimum for eligibility.
func makeQuestion(t *testing.T, tx pgx.Tx, topicID int64, d content.Difficulty, externalID string) int64 {
	t.Helper()
	ctx := context.Background()

	id, err := content.UpsertQuestion(ctx, tx, content.QuestionInput{
		TopicID:         topicID,
		Difficulty:      d,
		Prompt:          "Prompt " + externalID,
		CanonicalAnswer: "Answer " + externalID,
		Status:          "active",
		Source:          "test",
		ExternalID:      externalID,
	})
	if err != nil {
		t.Fatalf("UpsertQuestion(%s): %v", externalID, err)
	}
	if err := content.ReplaceAliases(ctx, tx, id, []string{"Answer " + externalID}); err != nil {
		t.Fatalf("ReplaceAliases: %v", err)
	}
	if err := content.ReplaceDistractors(ctx, tx, id, []string{"w1", "w2", "w3", "w4", "w5"}); err != nil {
		t.Fatalf("ReplaceDistractors: %v", err)
	}
	return id
}

// useQuestion records the question as having appeared on the given date.
func useQuestion(t *testing.T, tx pgx.Tx, questionID, topicID int64, d content.Difficulty, on clock.Date) {
	t.Helper()
	ctx := context.Background()

	if _, err := tx.Exec(ctx,
		`INSERT INTO daily_puzzles (puzzle_date) VALUES ($1) ON CONFLICT DO NOTHING`, on); err != nil {
		t.Fatalf("insert puzzle for %s: %v", on, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO daily_puzzle_questions
		   (puzzle_date, topic_id, topic_position, difficulty, question_id)
		 VALUES ($1, $2, 0, $3::difficulty, $4)`, on, topicID, string(d), questionID); err != nil {
		t.Fatalf("insert puzzle question: %v", err)
	}
}

func TestUpsertTopicIsIdempotent(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	first, err := content.UpsertTopic(ctx, tx, "geography", "Geography")
	if err != nil {
		t.Fatalf("first UpsertTopic: %v", err)
	}
	second, err := content.UpsertTopic(ctx, tx, "geography", "World Geography")
	if err != nil {
		t.Fatalf("second UpsertTopic: %v", err)
	}
	if first != second {
		t.Errorf("UpsertTopic returned %d then %d, want the same id", first, second)
	}

	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM topics WHERE id = $1`, first).Scan(&name); err != nil {
		t.Fatalf("select topic: %v", err)
	}
	if name != "World Geography" {
		t.Errorf("topic name = %q, want the updated %q", name, "World Geography")
	}
}

func TestUpsertQuestionUpdatesByExternalID(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	topicID, err := content.UpsertTopic(ctx, tx, "geography", "Geography")
	if err != nil {
		t.Fatalf("UpsertTopic: %v", err)
	}

	in := content.QuestionInput{
		TopicID: topicID, Difficulty: content.Easy,
		Prompt: "Capital of France?", CanonicalAnswer: "Paris",
		Status: "active", Source: "seed", ExternalID: "geo-easy-1",
	}
	firstID, err := content.UpsertQuestion(ctx, tx, in)
	if err != nil {
		t.Fatalf("first UpsertQuestion: %v", err)
	}

	in.Prompt = "What is the capital of France?"
	secondID, err := content.UpsertQuestion(ctx, tx, in)
	if err != nil {
		t.Fatalf("second UpsertQuestion: %v", err)
	}
	if firstID != secondID {
		t.Errorf("UpsertQuestion returned %d then %d, want the same id", firstID, secondID)
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM questions`).Scan(&count); err != nil {
		t.Fatalf("count questions: %v", err)
	}
	if count != 1 {
		t.Errorf("question count = %d, want 1", count)
	}
}

func TestReplaceAliasesNormalizesAndDeduplicates(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	topicID, err := content.UpsertTopic(ctx, tx, "geography", "Geography")
	if err != nil {
		t.Fatalf("UpsertTopic: %v", err)
	}
	questionID := makeQuestion(t, tx, topicID, content.Easy, "geo-easy-1")

	// "The Paris" and "paris" normalize to the same string and must collapse.
	if err := content.ReplaceAliases(ctx, tx, questionID, []string{"The Paris", "paris", "Ville Lumière", "   "}); err != nil {
		t.Fatalf("ReplaceAliases: %v", err)
	}

	rows, err := tx.Query(ctx, `SELECT normalized FROM question_aliases WHERE question_id = $1 ORDER BY normalized`, questionID)
	if err != nil {
		t.Fatalf("select aliases: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan alias: %v", err)
		}
		got = append(got, n)
	}
	want := []string{"paris", "ville lumiere"}
	if len(got) != len(want) {
		t.Fatalf("aliases = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("alias[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestEligibleQuestionsFiltersIneligibleContent(t *testing.T) {
	pool := testsupport.MustPool(t)
	ctx := context.Background()
	when := target(t)

	t.Run("excludes draft questions", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id, err := content.UpsertQuestion(ctx, tx, content.QuestionInput{
			TopicID: topicID, Difficulty: content.Easy, Prompt: "p", CanonicalAnswer: "a",
			Status: "draft", Source: "test", ExternalID: "draft-1",
		})
		if err != nil {
			t.Fatalf("UpsertQuestion: %v", err)
		}
		_ = content.ReplaceAliases(ctx, tx, id, []string{"a"})
		_ = content.ReplaceDistractors(ctx, tx, id, []string{"1", "2", "3", "4", "5"})

		got, err := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if err != nil {
			t.Fatalf("EligibleQuestions: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d eligible questions, want 0", len(got))
		}
	})

	t.Run("excludes questions with fewer than five distractors", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id := makeQuestion(t, tx, topicID, content.Easy, "thin-1")
		if err := content.ReplaceDistractors(ctx, tx, id, []string{"1", "2", "3", "4"}); err != nil {
			t.Fatalf("ReplaceDistractors: %v", err)
		}

		got, _ := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if len(got) != 0 {
			t.Errorf("got %d eligible questions, want 0", len(got))
		}
	})

	t.Run("excludes questions with no aliases", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id := makeQuestion(t, tx, topicID, content.Easy, "noalias-1")
		if _, err := tx.Exec(ctx, `DELETE FROM question_aliases WHERE question_id = $1`, id); err != nil {
			t.Fatalf("delete aliases: %v", err)
		}

		got, _ := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if len(got) != 0 {
			t.Errorf("got %d eligible questions, want 0", len(got))
		}
	})

	t.Run("excludes a question used inside the cooldown", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id := makeQuestion(t, tx, topicID, content.Easy, "recent-1")
		useQuestion(t, tx, id, topicID, content.Easy, when.AddDays(-179))

		got, _ := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if len(got) != 0 {
			t.Errorf("got %d eligible questions, want 0", len(got))
		}
	})

	t.Run("includes a question used exactly at the cooldown boundary", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id := makeQuestion(t, tx, topicID, content.Easy, "boundary-1")
		useQuestion(t, tx, id, topicID, content.Easy, when.AddDays(-180))

		got, _ := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if len(got) != 1 {
			t.Fatalf("got %d eligible questions, want 1", len(got))
		}
		if got[0].ID != id {
			t.Errorf("eligible question id = %d, want %d", got[0].ID, id)
		}
	})

	t.Run("excludes a question already used on a future date", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id := makeQuestion(t, tx, topicID, content.Easy, "future-1")
		useQuestion(t, tx, id, topicID, content.Easy, when.AddDays(30))

		got, _ := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if len(got) != 0 {
			t.Errorf("got %d eligible questions, want 0", len(got))
		}
	})

	t.Run("includes a question already used on the target date itself", func(t *testing.T) {
		tx := testsupport.Tx(t, pool)
		topicID, _ := content.UpsertTopic(ctx, tx, "geography", "Geography")
		id := makeQuestion(t, tx, topicID, content.Easy, "same-day-1")
		useQuestion(t, tx, id, topicID, content.Easy, when)

		got, _ := content.EligibleQuestions(ctx, tx, topicID, content.Easy, when, 180)
		if len(got) != 1 {
			t.Errorf("got %d eligible questions, want 1 (regeneration must not block itself)", len(got))
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: FAIL — package `content` does not exist.

- [ ] **Step 3: Write `internal/content/store.go`**

```go
// Package content stores and queries the trivia question library.
package content

import (
	"context"
	"fmt"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/db"
	"github.com/hjordan6/trivial/internal/grading"
)

// Difficulty is one of the three tiers every topic contributes each day.
type Difficulty string

const (
	Easy   Difficulty = "easy"
	Medium Difficulty = "medium"
	Hard   Difficulty = "hard"
)

// AllDifficulties lists the tiers in the order they appear on the board.
var AllDifficulties = []Difficulty{Easy, Medium, Hard}

// Topic is a subject area questions belong to.
type Topic struct {
	ID     int64
	Slug   string
	Name   string
	Active bool
}

// Question is a single trivia question.
type Question struct {
	ID              int64
	TopicID         int64
	Difficulty      Difficulty
	Prompt          string
	CanonicalAnswer string
}

// QuestionInput is the writable shape of a question.
type QuestionInput struct {
	TopicID         int64
	Difficulty      Difficulty
	Prompt          string
	CanonicalAnswer string
	Status          string // "draft", "active", or "retired"
	Source          string
	ExternalID      string
}

// UpsertTopic inserts a topic or updates its name, returning its id.
func UpsertTopic(ctx context.Context, q db.DBTX, slug, name string) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO topics (slug, name) VALUES ($1, $2)
		ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
		RETURNING id`, slug, name).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert topic %q: %w", slug, err)
	}
	return id, nil
}

// UpsertQuestion inserts a question, or updates the existing one with the same
// (source, external_id). This is what makes re-running an importer safe.
func UpsertQuestion(ctx context.Context, q db.DBTX, in QuestionInput) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO questions
			(topic_id, difficulty, prompt, canonical_answer, status, source, external_id)
		VALUES ($1, $2::difficulty, $3, $4, $5::question_status, $6, $7)
		ON CONFLICT (source, external_id) WHERE external_id IS NOT NULL
		DO UPDATE SET
			topic_id         = EXCLUDED.topic_id,
			difficulty       = EXCLUDED.difficulty,
			prompt           = EXCLUDED.prompt,
			canonical_answer = EXCLUDED.canonical_answer,
			status           = EXCLUDED.status,
			updated_at       = now()
		RETURNING id`,
		in.TopicID, string(in.Difficulty), in.Prompt, in.CanonicalAnswer,
		in.Status, in.Source, in.ExternalID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert question %q: %w", in.ExternalID, err)
	}
	return id, nil
}

// ReplaceAliases rewrites a question's accepted answers. Each alias is stored
// alongside its normalized form, produced by the same function that normalizes
// player input, so the two can never drift apart.
func ReplaceAliases(ctx context.Context, q db.DBTX, questionID int64, aliases []string) error {
	if _, err := q.Exec(ctx, `DELETE FROM question_aliases WHERE question_id = $1`, questionID); err != nil {
		return fmt.Errorf("clear aliases for question %d: %w", questionID, err)
	}

	seen := make(map[string]bool, len(aliases))
	for _, alias := range aliases {
		normalized := grading.Normalize(alias)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		if _, err := q.Exec(ctx,
			`INSERT INTO question_aliases (question_id, alias, normalized) VALUES ($1, $2, $3)`,
			questionID, alias, normalized); err != nil {
			return fmt.Errorf("insert alias %q for question %d: %w", alias, questionID, err)
		}
	}
	return nil
}

// ReplaceDistractors rewrites a question's wrong multiple-choice options.
func ReplaceDistractors(ctx context.Context, q db.DBTX, questionID int64, options []string) error {
	if _, err := q.Exec(ctx, `DELETE FROM question_distractors WHERE question_id = $1`, questionID); err != nil {
		return fmt.Errorf("clear distractors for question %d: %w", questionID, err)
	}

	seen := make(map[string]bool, len(options))
	for _, option := range options {
		if option == "" || seen[option] {
			continue
		}
		seen[option] = true
		if _, err := q.Exec(ctx,
			`INSERT INTO question_distractors (question_id, option_text) VALUES ($1, $2)`,
			questionID, option); err != nil {
			return fmt.Errorf("insert distractor %q for question %d: %w", option, questionID, err)
		}
	}
	return nil
}

// ActiveTopics returns every selectable topic, ordered by id so that callers
// shuffling them from a seeded RNG get reproducible results.
func ActiveTopics(ctx context.Context, q db.DBTX) ([]Topic, error) {
	rows, err := q.Query(ctx, `SELECT id, slug, name, active FROM topics WHERE active ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query active topics: %w", err)
	}
	defer rows.Close()

	var topics []Topic
	for rows.Next() {
		var t Topic
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.Active); err != nil {
			return nil, fmt.Errorf("scan topic: %w", err)
		}
		topics = append(topics, t)
	}
	return topics, rows.Err()
}

// EligibleQuestions returns the questions that may be used for a topic and
// difficulty on the target date.
//
// A question is eligible when it is active, has at least one alias and at
// least five distractors, and has not been used within cooldownDays of the
// target date in either direction. Uses on the target date itself are ignored,
// so regenerating a day is not blocked by its own existing rows.
func EligibleQuestions(
	ctx context.Context,
	q db.DBTX,
	topicID int64,
	difficulty Difficulty,
	target clock.Date,
	cooldownDays int,
) ([]Question, error) {
	rows, err := q.Query(ctx, `
		SELECT q.id, q.topic_id, q.difficulty, q.prompt, q.canonical_answer
		FROM questions q
		WHERE q.topic_id = $1
		  AND q.difficulty = $2::difficulty
		  AND q.status = 'active'
		  AND EXISTS (SELECT 1 FROM question_aliases a WHERE a.question_id = q.id)
		  AND (SELECT count(*) FROM question_distractors d WHERE d.question_id = q.id) >= 5
		  AND NOT EXISTS (
		        SELECT 1 FROM daily_puzzle_questions dpq
		        WHERE dpq.question_id = q.id
		          AND dpq.puzzle_date <> $3::date
		          AND abs(dpq.puzzle_date - $3::date) < $4
		      )
		ORDER BY q.id`,
		topicID, string(difficulty), target, cooldownDays)
	if err != nil {
		return nil, fmt.Errorf("query eligible questions for topic %d %s: %w", topicID, difficulty, err)
	}
	defer rows.Close()

	var questions []Question
	for rows.Next() {
		var qn Question
		if err := rows.Scan(&qn.ID, &qn.TopicID, &qn.Difficulty, &qn.Prompt, &qn.CanonicalAnswer); err != nil {
			return nil, fmt.Errorf("scan question: %w", err)
		}
		questions = append(questions, qn)
	}
	return questions, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS, including all seven `TestEligibleQuestions` subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/content
git commit -m "feat: add content store with cooldown-aware eligibility query"
```

---

### Task 8: Seed content and loader

**Files:**
- Create: `internal/content/seed.go`, `seed/questions.json`
- Test: `internal/content/seed_test.go` (package `content`, validation) and `internal/content/seed_apply_test.go` (package `content_test`, database round trip)

Two files because the validation tests live inside the package while the apply test uses the public API from outside it. Go allows both a `content` and a `content_test` package in one directory, but not in one file.

**Interfaces:**
- Consumes: everything from Task 7.
- Produces:
  - `content.SeedFile{Topics []SeedTopic}`, `content.SeedTopic{Slug, Name string, Questions []SeedQuestion}`, `content.SeedQuestion{ExternalID string, Difficulty Difficulty, Prompt, Answer string, Aliases, Distractors []string}`
  - `content.ParseSeed(data []byte) (SeedFile, error)` — parses and validates
  - `content.ApplySeed(ctx, db, seed SeedFile) (SeedStats, error)` where `SeedStats{Topics, Questions int}`

- [ ] **Step 1: Write the failing validation test**

```go
package content

import (
	"strings"
	"testing"
)

const validSeed = `{
  "topics": [
    {
      "slug": "geography",
      "name": "World Geography",
      "questions": [
        {
          "external_id": "geography-easy-1",
          "difficulty": "easy",
          "prompt": "What is the capital of France?",
          "answer": "Paris",
          "aliases": ["Paris, France"],
          "distractors": ["Lyon", "Marseille", "Bordeaux", "Nice", "Toulouse"]
        }
      ]
    }
  ]
}`

func TestParseSeedAcceptsValidFile(t *testing.T) {
	seed, err := ParseSeed([]byte(validSeed))
	if err != nil {
		t.Fatalf("ParseSeed() error = %v", err)
	}
	if len(seed.Topics) != 1 {
		t.Fatalf("got %d topics, want 1", len(seed.Topics))
	}
	q := seed.Topics[0].Questions[0]
	// The canonical answer is always an accepted alias, whether or not the
	// author listed it.
	if !contains(q.Aliases, "Paris") {
		t.Errorf("aliases = %v, want the canonical answer to be included", q.Aliases)
	}
}

func TestParseSeedRejectsBadFiles(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(string) string
		wantErr string
	}{
		{
			name:    "unknown difficulty",
			mutate:  func(s string) string { return strings.Replace(s, `"easy"`, `"trivial"`, 1) },
			wantErr: "difficulty",
		},
		{
			name:    "too few distractors",
			mutate:  func(s string) string { return strings.Replace(s, `, "Toulouse"`, ``, 1) },
			wantErr: "at least 5 distractors",
		},
		{
			name:    "distractor duplicates the answer",
			mutate:  func(s string) string { return strings.Replace(s, `"Lyon"`, `"Paris"`, 1) },
			wantErr: "distractor",
		},
		{
			name:    "empty prompt",
			mutate:  func(s string) string { return strings.Replace(s, `"What is the capital of France?"`, `""`, 1) },
			wantErr: "prompt",
		},
		{
			name:    "missing external id",
			mutate:  func(s string) string { return strings.Replace(s, `"geography-easy-1"`, `""`, 1) },
			wantErr: "external_id",
		},
		{
			name:    "duplicate external id",
			mutate:  func(s string) string { return strings.Replace(s, `"questions": [`, `"questions": [`+duplicateQuestion+`,`, 1) },
			wantErr: "duplicate",
		},
		{
			name:    "malformed json",
			mutate:  func(s string) string { return s[:len(s)-1] },
			wantErr: "parse seed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseSeed([]byte(tt.mutate(validSeed)))
			if err == nil {
				t.Fatal("ParseSeed() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ParseSeed() error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

const duplicateQuestion = `{
  "external_id": "geography-easy-1",
  "difficulty": "easy",
  "prompt": "Duplicate",
  "answer": "Paris",
  "aliases": [],
  "distractors": ["a", "b", "c", "d", "e"]
}`

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Write the failing apply test**

Create this as `internal/content/seed_apply_test.go`:

```go
package content_test

import (
	"context"
	"os"
	"testing"

	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/testsupport"
)

func TestApplySeedIsIdempotent(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	data, err := os.ReadFile("../../seed/questions.json")
	if err != nil {
		t.Fatalf("read seed file: %v", err)
	}
	seed, err := content.ParseSeed(data)
	if err != nil {
		t.Fatalf("ParseSeed: %v", err)
	}

	first, err := content.ApplySeed(ctx, tx, seed)
	if err != nil {
		t.Fatalf("first ApplySeed: %v", err)
	}
	second, err := content.ApplySeed(ctx, tx, seed)
	if err != nil {
		t.Fatalf("second ApplySeed: %v", err)
	}
	if first != second {
		t.Errorf("ApplySeed stats differ across runs: %+v then %+v", first, second)
	}

	var questions int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM questions`).Scan(&questions); err != nil {
		t.Fatalf("count questions: %v", err)
	}
	if questions != first.Questions {
		t.Errorf("question rows = %d after two applies, want %d", questions, first.Questions)
	}
}

func TestSeedFileIsRichEnoughToGenerate(t *testing.T) {
	data, err := os.ReadFile("../../seed/questions.json")
	if err != nil {
		t.Fatalf("read seed file: %v", err)
	}
	seed, err := content.ParseSeed(data)
	if err != nil {
		t.Fatalf("ParseSeed: %v", err)
	}
	if len(seed.Topics) < 6 {
		t.Errorf("seed has %d topics, want at least 6 so a day can pick 3 with room to skip", len(seed.Topics))
	}
	for _, topic := range seed.Topics {
		counts := map[content.Difficulty]int{}
		for _, q := range topic.Questions {
			counts[q.Difficulty]++
		}
		for _, d := range content.AllDifficulties {
			if counts[d] < 3 {
				t.Errorf("topic %s has %d %s questions, want at least 3", topic.Slug, counts[d], d)
			}
		}
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `make test`
Expected: FAIL — `undefined: ParseSeed`, and the seed file does not exist.

- [ ] **Step 4: Write `internal/content/seed.go`**

```go
package content

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hjordan6/trivial/internal/db"
	"github.com/hjordan6/trivial/internal/grading"
)

// SeedFile is the on-disk format for hand-authored and imported content.
type SeedFile struct {
	Topics []SeedTopic `json:"topics"`
}

// SeedTopic is one subject area and its questions.
type SeedTopic struct {
	Slug      string         `json:"slug"`
	Name      string         `json:"name"`
	Questions []SeedQuestion `json:"questions"`
}

// SeedQuestion is one question with everything needed to make it eligible.
type SeedQuestion struct {
	ExternalID  string     `json:"external_id"`
	Difficulty  Difficulty `json:"difficulty"`
	Prompt      string     `json:"prompt"`
	Answer      string     `json:"answer"`
	Aliases     []string   `json:"aliases"`
	Distractors []string   `json:"distractors"`
}

// SeedStats reports what a seed application touched.
type SeedStats struct {
	Topics    int
	Questions int
}

// seedSource marks rows written by the seed loader so re-running it updates
// them rather than inserting duplicates.
const seedSource = "seed"

// ParseSeed decodes and validates a seed file. Validation is strict: content
// that could never become eligible is a bug in the file, not something to
// discover later when a puzzle fails to generate.
func ParseSeed(data []byte) (SeedFile, error) {
	var seed SeedFile
	if err := json.Unmarshal(data, &seed); err != nil {
		return SeedFile{}, fmt.Errorf("parse seed: %w", err)
	}

	seenExternalIDs := map[string]bool{}
	seenSlugs := map[string]bool{}

	for i, topic := range seed.Topics {
		if topic.Slug == "" {
			return SeedFile{}, fmt.Errorf("topic %d: slug is required", i)
		}
		if seenSlugs[topic.Slug] {
			return SeedFile{}, fmt.Errorf("topic %q: duplicate slug", topic.Slug)
		}
		seenSlugs[topic.Slug] = true
		if topic.Name == "" {
			return SeedFile{}, fmt.Errorf("topic %q: name is required", topic.Slug)
		}

		for j := range topic.Questions {
			q := &seed.Topics[i].Questions[j]
			where := fmt.Sprintf("topic %q question %d", topic.Slug, j)

			if q.ExternalID == "" {
				return SeedFile{}, fmt.Errorf("%s: external_id is required", where)
			}
			if seenExternalIDs[q.ExternalID] {
				return SeedFile{}, fmt.Errorf("%s: duplicate external_id %q", where, q.ExternalID)
			}
			seenExternalIDs[q.ExternalID] = true

			switch q.Difficulty {
			case Easy, Medium, Hard:
			default:
				return SeedFile{}, fmt.Errorf("%s: unknown difficulty %q", where, q.Difficulty)
			}
			if q.Prompt == "" {
				return SeedFile{}, fmt.Errorf("%s: prompt is required", where)
			}
			if q.Answer == "" {
				return SeedFile{}, fmt.Errorf("%s: answer is required", where)
			}
			if len(q.Distractors) < 5 {
				return SeedFile{}, fmt.Errorf("%s: has %d distractors, want at least 5 distractors", where, len(q.Distractors))
			}

			answerKey := grading.Normalize(q.Answer)
			for _, d := range q.Distractors {
				if grading.Normalize(d) == answerKey {
					return SeedFile{}, fmt.Errorf("%s: distractor %q is the correct answer", where, d)
				}
			}

			// The canonical answer is always accepted, whether or not it was
			// listed among the aliases.
			hasAnswer := false
			for _, a := range q.Aliases {
				if grading.Normalize(a) == answerKey {
					hasAnswer = true
					break
				}
			}
			if !hasAnswer {
				q.Aliases = append([]string{q.Answer}, q.Aliases...)
			}
		}
	}
	return seed, nil
}

// ApplySeed writes a parsed seed file to the database, upserting by
// (source, external_id) so it can be run repeatedly.
func ApplySeed(ctx context.Context, q db.DBTX, seed SeedFile) (SeedStats, error) {
	var stats SeedStats

	for _, topic := range seed.Topics {
		topicID, err := UpsertTopic(ctx, q, topic.Slug, topic.Name)
		if err != nil {
			return SeedStats{}, err
		}
		stats.Topics++

		for _, question := range topic.Questions {
			questionID, err := UpsertQuestion(ctx, q, QuestionInput{
				TopicID:         topicID,
				Difficulty:      question.Difficulty,
				Prompt:          question.Prompt,
				CanonicalAnswer: question.Answer,
				Status:          "active",
				Source:          seedSource,
				ExternalID:      question.ExternalID,
			})
			if err != nil {
				return SeedStats{}, err
			}
			if err := ReplaceAliases(ctx, q, questionID, question.Aliases); err != nil {
				return SeedStats{}, err
			}
			if err := ReplaceDistractors(ctx, q, questionID, question.Distractors); err != nil {
				return SeedStats{}, err
			}
			stats.Questions++
		}
	}
	return stats, nil
}
```

- [ ] **Step 5: Author `seed/questions.json`**

This step is content authoring, not coding. Write **6 topics × 3 difficulties × 3 questions = 54 questions**, which is what `TestSeedFileIsRichEnoughToGenerate` checks and enough for the generator to produce a month of puzzles with a short development cooldown.

Use exactly these six topic slugs and names:

| slug | name |
|------|------|
| `world-geography` | World Geography |
| `science-and-nature` | Science & Nature |
| `film-and-television` | Film & Television |
| `history` | History |
| `music` | Music |
| `sport` | Sport |

Rules for every question:
- `external_id` follows `<topic-slug>-<difficulty>-<n>`, e.g. `history-hard-2`.
- Exactly 5 or more distractors, all plausible and all the same *kind* of thing as the answer — for a "which year" question, other years; for a "which city", other cities. A distractor that is obviously the wrong category makes the six-option stage free.
- No distractor may equal the answer after normalization; `ParseSeed` rejects the file if one does.
- `aliases` lists genuine alternative spellings and forms only. Do not pad it — `Normalize` already handles case, accents, punctuation, and leading articles, so `"the beatles"` is not a useful alias for `"The Beatles"`.
- Prefer answers that are one or two words. A question whose answer is a sentence cannot be graded fairly.

Two complete worked examples to copy the shape from:

```json
{
  "topics": [
    {
      "slug": "world-geography",
      "name": "World Geography",
      "questions": [
        {
          "external_id": "world-geography-easy-1",
          "difficulty": "easy",
          "prompt": "What is the capital city of Japan?",
          "answer": "Tokyo",
          "aliases": [],
          "distractors": ["Osaka", "Kyoto", "Nagoya", "Sapporo", "Yokohama"]
        },
        {
          "external_id": "world-geography-hard-1",
          "difficulty": "hard",
          "prompt": "Which river flows through the Angel Falls basin in Venezuela?",
          "answer": "Churun",
          "aliases": ["Churun River", "Río Churún"],
          "distractors": ["Caroni", "Orinoco", "Caura", "Apure", "Meta"]
        }
      ]
    }
  ]
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `make test`
Expected: PASS. `TestSeedFileIsRichEnoughToGenerate` failing means the file is short on topics or on questions at some difficulty; its message names which.

- [ ] **Step 7: Commit**

```bash
git add internal/content/seed.go internal/content/seed_test.go seed/questions.json
git commit -m "feat: add seed file format, validation, and starter content"
```

---

### Task 9: Puzzle store

**Files:**
- Create: `internal/puzzle/store.go`
- Test: `internal/puzzle/store_test.go`

**Interfaces:**
- Consumes: `db.DBTX`, `clock.Date`, `content.Difficulty`.
- Produces:
  - `puzzle.Entry{TopicID int64, TopicSlug, TopicName string, TopicPosition int, Difficulty content.Difficulty, QuestionID int64, Prompt string}`
  - `puzzle.Puzzle{Date clock.Date, TimeLimitSeconds int, Entries []Entry}`
  - `puzzle.Get(ctx, db, date clock.Date) (*Puzzle, error)` — returns `nil, nil` when the date has no puzzle
  - `puzzle.Insert(ctx, db, p *Puzzle) error`

`Entries` is always ordered by topic position, then easy, medium, hard. That ordering is what makes the shared 3×3 grid read the same way for everybody, so it is enforced in SQL rather than left to the caller.

- [ ] **Step 1: Write the failing test**

```go
package puzzle_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/puzzle"
	"github.com/hjordan6/trivial/internal/testsupport"
)

func mustDate(t *testing.T, s string) clock.Date {
	t.Helper()
	d, err := clock.ParseDate(s)
	if err != nil {
		t.Fatalf("ParseDate(%s): %v", s, err)
	}
	return d
}

// seedTopic creates a topic with three eligible questions per difficulty.
func seedTopic(t *testing.T, tx pgx.Tx, slug string) content.Topic {
	t.Helper()
	ctx := context.Background()

	id, err := content.UpsertTopic(ctx, tx, slug, slug)
	if err != nil {
		t.Fatalf("UpsertTopic(%s): %v", slug, err)
	}
	for _, d := range content.AllDifficulties {
		for n := 1; n <= 3; n++ {
			externalID := fmt.Sprintf("%s-%s-%d", slug, d, n)
			qid, err := content.UpsertQuestion(ctx, tx, content.QuestionInput{
				TopicID: id, Difficulty: d,
				Prompt: "Prompt " + externalID, CanonicalAnswer: "Answer " + externalID,
				Status: "active", Source: "test", ExternalID: externalID,
			})
			if err != nil {
				t.Fatalf("UpsertQuestion(%s): %v", externalID, err)
			}
			if err := content.ReplaceAliases(ctx, tx, qid, []string{"Answer " + externalID}); err != nil {
				t.Fatalf("ReplaceAliases: %v", err)
			}
			if err := content.ReplaceDistractors(ctx, tx, qid, []string{"w1", "w2", "w3", "w4", "w5"}); err != nil {
				t.Fatalf("ReplaceDistractors: %v", err)
			}
		}
	}
	return content.Topic{ID: id, Slug: slug, Name: slug, Active: true}
}

func TestGetReturnsNilForMissingPuzzle(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))

	got, err := puzzle.Get(context.Background(), tx, mustDate(t, "2030-01-01"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != nil {
		t.Errorf("Get() = %+v, want nil for a date with no puzzle", got)
	}
}

func TestInsertAndGetPreserveBoardOrder(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-08-18")

	topics := []content.Topic{
		seedTopic(t, tx, "alpha"),
		seedTopic(t, tx, "beta"),
		seedTopic(t, tx, "gamma"),
	}

	var entries []puzzle.Entry
	for position, topic := range topics {
		// Insert difficulties out of order to prove the read path sorts them.
		for _, d := range []content.Difficulty{content.Hard, content.Easy, content.Medium} {
			candidates, err := content.EligibleQuestions(ctx, tx, topic.ID, d, date, 180)
			if err != nil {
				t.Fatalf("EligibleQuestions: %v", err)
			}
			if len(candidates) == 0 {
				t.Fatalf("no eligible %s questions for topic %s", d, topic.Slug)
			}
			entries = append(entries, puzzle.Entry{
				TopicID: topic.ID, TopicSlug: topic.Slug, TopicName: topic.Name,
				TopicPosition: position, Difficulty: d,
				QuestionID: candidates[0].ID, Prompt: candidates[0].Prompt,
			})
		}
	}

	want := &puzzle.Puzzle{Date: date, TimeLimitSeconds: 135, Entries: entries}
	if err := puzzle.Insert(ctx, tx, want); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}

	got, err := puzzle.Get(ctx, tx, date)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got == nil {
		t.Fatal("Get() = nil, want the inserted puzzle")
	}
	if len(got.Entries) != 9 {
		t.Fatalf("got %d entries, want 9", len(got.Entries))
	}
	if got.TimeLimitSeconds != 135 {
		t.Errorf("TimeLimitSeconds = %d, want 135", got.TimeLimitSeconds)
	}

	wantOrder := []struct {
		position   int
		difficulty content.Difficulty
	}{
		{0, content.Easy}, {0, content.Medium}, {0, content.Hard},
		{1, content.Easy}, {1, content.Medium}, {1, content.Hard},
		{2, content.Easy}, {2, content.Medium}, {2, content.Hard},
	}
	for i, w := range wantOrder {
		e := got.Entries[i]
		if e.TopicPosition != w.position || e.Difficulty != w.difficulty {
			t.Errorf("entry %d = (position %d, %s), want (position %d, %s)",
				i, e.TopicPosition, e.Difficulty, w.position, w.difficulty)
		}
		if e.TopicSlug == "" || e.Prompt == "" {
			t.Errorf("entry %d has empty topic slug or prompt: %+v", i, e)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: FAIL — package `puzzle` does not exist.

- [ ] **Step 3: Write `internal/puzzle/store.go`**

```go
// Package puzzle builds and stores the daily nine-question board.
package puzzle

import (
	"context"
	"fmt"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/db"
)

// Entry is one cell of the board: a question at a difficulty within a topic.
type Entry struct {
	TopicID       int64
	TopicSlug     string
	TopicName     string
	TopicPosition int
	Difficulty    content.Difficulty
	QuestionID    int64
	Prompt        string
}

// Puzzle is one day's board of nine questions.
type Puzzle struct {
	Date             clock.Date
	TimeLimitSeconds int
	Entries          []Entry
}

// Get returns the puzzle for a date, or nil when none has been generated.
func Get(ctx context.Context, q db.DBTX, date clock.Date) (*Puzzle, error) {
	rows, err := q.Query(ctx, `
		SELECT p.time_limit_seconds,
		       dpq.topic_id, t.slug, t.name, dpq.topic_position,
		       dpq.difficulty, dpq.question_id, qn.prompt
		FROM daily_puzzles p
		JOIN daily_puzzle_questions dpq ON dpq.puzzle_date = p.puzzle_date
		JOIN topics t ON t.id = dpq.topic_id
		JOIN questions qn ON qn.id = dpq.question_id
		WHERE p.puzzle_date = $1
		ORDER BY dpq.topic_position,
		         array_position(ARRAY['easy','medium','hard']::difficulty[], dpq.difficulty)`,
		date)
	if err != nil {
		return nil, fmt.Errorf("query puzzle for %s: %w", date, err)
	}
	defer rows.Close()

	p := &Puzzle{Date: date}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(
			&p.TimeLimitSeconds,
			&e.TopicID, &e.TopicSlug, &e.TopicName, &e.TopicPosition,
			&e.Difficulty, &e.QuestionID, &e.Prompt,
		); err != nil {
			return nil, fmt.Errorf("scan puzzle entry: %w", err)
		}
		p.Entries = append(p.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read puzzle rows: %w", err)
	}
	if len(p.Entries) == 0 {
		return nil, nil
	}
	return p, nil
}

// Insert writes a puzzle and its entries. Callers that need atomicity — the
// generator does — should pass a transaction.
func Insert(ctx context.Context, q db.DBTX, p *Puzzle) error {
	if _, err := q.Exec(ctx,
		`INSERT INTO daily_puzzles (puzzle_date, time_limit_seconds) VALUES ($1, $2)`,
		p.Date, p.TimeLimitSeconds); err != nil {
		return fmt.Errorf("insert puzzle %s: %w", p.Date, err)
	}
	for _, e := range p.Entries {
		if _, err := q.Exec(ctx, `
			INSERT INTO daily_puzzle_questions
				(puzzle_date, topic_id, topic_position, difficulty, question_id)
			VALUES ($1, $2, $3, $4::difficulty, $5)`,
			p.Date, e.TopicID, e.TopicPosition, string(e.Difficulty), e.QuestionID); err != nil {
			return fmt.Errorf("insert puzzle entry %s/%s: %w", e.TopicSlug, e.Difficulty, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/puzzle
git commit -m "feat: add puzzle store with deterministic board ordering"
```

---

### Task 10: Puzzle generator

**Files:**
- Create: `internal/puzzle/generator.go`
- Test: `internal/puzzle/generator_test.go`

**Interfaces:**
- Consumes: `puzzle.Get`, `puzzle.Insert` (Task 9), `content.ActiveTopics`, `content.EligibleQuestions` (Task 7).
- Produces:
  - `puzzle.Generator{DB db.DBTX, CooldownDays int, TimeLimitSeconds int}`
  - `(Generator).GenerateFor(ctx context.Context, date clock.Date) (*Puzzle, error)`
  - `puzzle.InsufficientContentError{Date clock.Date, Accepted int, Starved []StarvedTopic}` and `puzzle.StarvedTopic{Slug string, Difficulty content.Difficulty}`

The generator never relaxes the cooldown. When a topic cannot fill all three difficulties it is skipped and the next shuffled topic is tried; only when fewer than three topics in the whole library can fill a board does generation fail.

- [ ] **Step 1: Write the failing test**

```go
package puzzle_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/puzzle"
	"github.com/hjordan6/trivial/internal/testsupport"
)

func TestGenerateForProducesNineQuestionsAcrossThreeTopics(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-08-18")

	for _, slug := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"} {
		seedTopic(t, tx, slug)
	}

	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}
	got, err := g.GenerateFor(ctx, date)
	if err != nil {
		t.Fatalf("GenerateFor() error = %v", err)
	}
	if len(got.Entries) != 9 {
		t.Fatalf("got %d entries, want 9", len(got.Entries))
	}

	topics := map[string]map[content.Difficulty]bool{}
	for _, e := range got.Entries {
		if topics[e.TopicSlug] == nil {
			topics[e.TopicSlug] = map[content.Difficulty]bool{}
		}
		topics[e.TopicSlug][e.Difficulty] = true
	}
	if len(topics) != 3 {
		t.Errorf("got %d distinct topics, want 3", len(topics))
	}
	for slug, difficulties := range topics {
		if len(difficulties) != 3 {
			t.Errorf("topic %s has %d difficulties, want 3", slug, len(difficulties))
		}
	}
}

func TestGenerateForIsDeterministic(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-08-18")

	for _, slug := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"} {
		seedTopic(t, tx, slug)
	}
	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}

	first, err := g.GenerateFor(ctx, date)
	if err != nil {
		t.Fatalf("first GenerateFor: %v", err)
	}
	firstIDs := questionIDs(first)

	// Discard the puzzle and regenerate from the same library and date.
	if _, err := tx.Exec(ctx, `DELETE FROM daily_puzzles WHERE puzzle_date = $1`, date); err != nil {
		t.Fatalf("delete puzzle: %v", err)
	}

	second, err := g.GenerateFor(ctx, date)
	if err != nil {
		t.Fatalf("second GenerateFor: %v", err)
	}
	secondIDs := questionIDs(second)

	if len(firstIDs) != len(secondIDs) {
		t.Fatalf("entry counts differ: %d then %d", len(firstIDs), len(secondIDs))
	}
	for i := range firstIDs {
		if firstIDs[i] != secondIDs[i] {
			t.Errorf("entry %d: question %d then %d, want the same puzzle for the same date",
				i, firstIDs[i], secondIDs[i])
		}
	}
}

func TestGenerateForDifferentDatesDiffer(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()

	for _, slug := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"} {
		seedTopic(t, tx, slug)
	}
	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}

	first, err := g.GenerateFor(ctx, mustDate(t, "2026-08-18"))
	if err != nil {
		t.Fatalf("GenerateFor day one: %v", err)
	}
	second, err := g.GenerateFor(ctx, mustDate(t, "2026-08-19"))
	if err != nil {
		t.Fatalf("GenerateFor day two: %v", err)
	}

	overlap := 0
	firstSet := map[int64]bool{}
	for _, id := range questionIDs(first) {
		firstSet[id] = true
	}
	for _, id := range questionIDs(second) {
		if firstSet[id] {
			overlap++
		}
	}
	if overlap != 0 {
		t.Errorf("%d questions repeated on consecutive days, want 0 inside the cooldown", overlap)
	}
}

func TestGenerateForIsIdempotent(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-08-18")

	for _, slug := range []string{"alpha", "beta", "gamma", "delta"} {
		seedTopic(t, tx, slug)
	}
	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}

	if _, err := g.GenerateFor(ctx, date); err != nil {
		t.Fatalf("first GenerateFor: %v", err)
	}
	if _, err := g.GenerateFor(ctx, date); err != nil {
		t.Fatalf("second GenerateFor: %v", err)
	}

	var rows int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM daily_puzzle_questions WHERE puzzle_date = $1`, date).Scan(&rows); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	if rows != 9 {
		t.Errorf("entry rows = %d after two generations, want 9", rows)
	}
}

func TestGenerateForSkipsStarvedTopics(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-08-18")

	for _, slug := range []string{"alpha", "beta", "gamma"} {
		seedTopic(t, tx, slug)
	}
	starved := seedTopic(t, tx, "starved")
	// Retire every hard question in this topic so it can never fill a board.
	if _, err := tx.Exec(ctx,
		`UPDATE questions SET status = 'retired' WHERE topic_id = $1 AND difficulty = 'hard'`,
		starved.ID); err != nil {
		t.Fatalf("retire hard questions: %v", err)
	}

	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}
	got, err := g.GenerateFor(ctx, date)
	if err != nil {
		t.Fatalf("GenerateFor() error = %v", err)
	}
	for _, e := range got.Entries {
		if e.TopicSlug == "starved" {
			t.Errorf("puzzle used topic %q, which cannot fill all three difficulties", e.TopicSlug)
		}
	}
}

func TestGenerateForFailsWhenLibraryIsTooThin(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	date := mustDate(t, "2026-08-18")

	seedTopic(t, tx, "alpha")
	seedTopic(t, tx, "beta")

	g := puzzle.Generator{DB: tx, CooldownDays: 180, TimeLimitSeconds: 135}
	_, err := g.GenerateFor(ctx, date)
	if err == nil {
		t.Fatal("GenerateFor() error = nil, want InsufficientContentError")
	}

	var insufficient *puzzle.InsufficientContentError
	if !errors.As(err, &insufficient) {
		t.Fatalf("GenerateFor() error = %T (%v), want *puzzle.InsufficientContentError", err, err)
	}
	if insufficient.Accepted != 2 {
		t.Errorf("Accepted = %d, want 2", insufficient.Accepted)
	}

	var puzzles int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM daily_puzzles WHERE puzzle_date = $1`, date).Scan(&puzzles); err != nil {
		t.Fatalf("count puzzles: %v", err)
	}
	if puzzles != 0 {
		t.Errorf("wrote %d puzzle rows on failure, want 0", puzzles)
	}
}

func questionIDs(p *puzzle.Puzzle) []int64 {
	ids := make([]int64, 0, len(p.Entries))
	for _, e := range p.Entries {
		ids = append(ids, e.QuestionID)
	}
	return ids
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: FAIL — `undefined: puzzle.Generator`.

- [ ] **Step 3: Write `internal/puzzle/generator.go`**

```go
package puzzle

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"strings"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/db"
)

// topicsPerDay is how many topics appear on a board.
const topicsPerDay = 3

// Generator builds the daily board.
type Generator struct {
	DB               db.DBTX
	CooldownDays     int
	TimeLimitSeconds int
}

// StarvedTopic records a topic that was skipped and the difficulty that
// starved it. It is the signal that the library needs content in a specific
// place.
type StarvedTopic struct {
	Slug       string
	Difficulty content.Difficulty
}

// InsufficientContentError means the library cannot fill a board without
// violating the question cooldown. The cooldown is never relaxed to avoid it.
type InsufficientContentError struct {
	Date         clock.Date
	Accepted     int
	CooldownDays int
	Starved      []StarvedTopic
}

func (e *InsufficientContentError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "cannot generate puzzle for %s: only %d of %d topics could fill a board without breaking the %d-day cooldown",
		e.Date, e.Accepted, topicsPerDay, e.CooldownDays)
	if len(e.Starved) > 0 {
		b.WriteString("; starved topics:")
		for _, s := range e.Starved {
			fmt.Fprintf(&b, " %s(%s)", s.Slug, s.Difficulty)
		}
	}
	return b.String()
}

// GenerateFor builds and stores the puzzle for a date.
//
// It is idempotent: if a puzzle already exists for the date it is returned
// unchanged. It is deterministic: the same date and the same library always
// produce the same board, because the shuffle and every pick come from an RNG
// seeded off the date.
func (g Generator) GenerateFor(ctx context.Context, date clock.Date) (*Puzzle, error) {
	existing, err := Get(ctx, g.DB, date)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	topics, err := content.ActiveTopics(ctx, g.DB)
	if err != nil {
		return nil, err
	}

	rng := rand.New(rand.NewPCG(seedFor(date), 0x9E3779B97F4A7C15))
	rng.Shuffle(len(topics), func(i, j int) { topics[i], topics[j] = topics[j], topics[i] })

	var (
		entries  []Entry
		starved  []StarvedTopic
		accepted int
	)
	for _, topic := range topics {
		if accepted == topicsPerDay {
			break
		}
		picked, missing, err := g.fillTopic(ctx, topic, date, accepted, rng)
		if err != nil {
			return nil, err
		}
		if missing != nil {
			starved = append(starved, StarvedTopic{Slug: topic.Slug, Difficulty: *missing})
			continue
		}
		entries = append(entries, picked...)
		accepted++
	}

	if accepted < topicsPerDay {
		return nil, &InsufficientContentError{
			Date:         date,
			Accepted:     accepted,
			CooldownDays: g.CooldownDays,
			Starved:      starved,
		}
	}

	p := &Puzzle{Date: date, TimeLimitSeconds: g.TimeLimitSeconds, Entries: entries}
	if err := Insert(ctx, g.DB, p); err != nil {
		return nil, err
	}
	return p, nil
}

// fillTopic picks one question at each difficulty for a topic. It returns a
// non-nil difficulty when the topic has nothing eligible at that tier, which
// tells the caller to skip the topic entirely.
func (g Generator) fillTopic(
	ctx context.Context,
	topic content.Topic,
	date clock.Date,
	position int,
	rng *rand.Rand,
) ([]Entry, *content.Difficulty, error) {
	entries := make([]Entry, 0, len(content.AllDifficulties))
	for _, difficulty := range content.AllDifficulties {
		candidates, err := content.EligibleQuestions(ctx, g.DB, topic.ID, difficulty, date, g.CooldownDays)
		if err != nil {
			return nil, nil, err
		}
		if len(candidates) == 0 {
			missing := difficulty
			return nil, &missing, nil
		}
		chosen := candidates[rng.IntN(len(candidates))]
		entries = append(entries, Entry{
			TopicID:       topic.ID,
			TopicSlug:     topic.Slug,
			TopicName:     topic.Name,
			TopicPosition: position,
			Difficulty:    difficulty,
			QuestionID:    chosen.ID,
			Prompt:        chosen.Prompt,
		})
	}
	return entries, nil, nil
}

// seedFor derives a stable RNG seed from a date, so regenerating a day
// reproduces it exactly.
func seedFor(date clock.Date) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(date.String()))
	return h.Sum64()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `make test`
Expected: PASS, all six generator tests.

`TestGenerateForDifferentDatesDiffer` is the one that proves the cooldown is actually wired in — two consecutive days drawing from the same six topics must share no questions.

- [ ] **Step 5: Commit**

```bash
git add internal/puzzle
git commit -m "feat: add deterministic puzzle generator with hard cooldown"
```

---

### Task 11: CLI

**Files:**
- Create: `cmd/trivial/main.go`, `internal/cli/cli.go`
- Test: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: `config.Load`, `db.Open`, `db.Migrate`, `content.ParseSeed`, `content.ApplySeed`, `puzzle.Generator`, `puzzle.Get`.
- Produces: `cli.Run(ctx context.Context, args []string, stdout, stderr io.Writer) error`.

Commands:

| Command | Effect |
|---------|--------|
| `trivial migrate up` | Apply pending migrations |
| `trivial migrate down` | Roll back the most recent migration |
| `trivial seed apply [--file seed/questions.json]` | Load and upsert seed content |
| `trivial puzzles generate --from YYYY-MM-DD --days N` | Generate N days starting at `--from` (default: today, 1 day) |
| `trivial puzzles show YYYY-MM-DD` | Print a day's board |

`Run` takes its writers as parameters so argument handling can be tested without a subprocess. The database-touching commands are covered by the integration tests of the packages they call; this task tests the shell around them and verifies the real commands by hand in Step 6.

- [ ] **Step 1: Write the failing test**

```go
package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hjordan6/trivial/internal/cli"
)

func TestRunArgumentErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"no arguments", []string{}, "usage"},
		{"unknown command", []string{"dance"}, "unknown command"},
		{"migrate without direction", []string{"migrate"}, "usage"},
		{"migrate with bad direction", []string{"migrate", "sideways"}, "up or down"},
		{"puzzles without subcommand", []string{"puzzles"}, "usage"},
		{"puzzles show without a date", []string{"puzzles", "show"}, "usage"},
		{"puzzles show with a bad date", []string{"puzzles", "show", "tomorrow"}, "parse date"},
		{"puzzles generate with zero days", []string{"puzzles", "generate", "--days", "0"}, "must be positive"},
		{"seed without subcommand", []string{"seed"}, "usage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := cli.Run(context.Background(), tt.args, &stdout, &stderr)
			if err == nil {
				t.Fatal("Run() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Run() error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestRunHelpSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"help"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run(help) error = %v", err)
	}
	for _, want := range []string{"migrate", "seed", "puzzles"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("help output missing %q; got:\n%s", want, stdout.String())
		}
	}
}
```

Argument validation must happen before any database connection, or these tests will need a database. Parse and validate first, connect second.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -v`
Expected: FAIL — package `cli` does not exist.

- [ ] **Step 3: Write `internal/cli/cli.go`**

```go
// Package cli implements the trivial command line interface.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/config"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/db"
	"github.com/hjordan6/trivial/internal/puzzle"
)

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

	stats, err := content.ApplySeed(ctx, pool, seed)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "seeded %d topics and %d questions from %s\n", stats.Topics, stats.Questions, *path)
	return nil
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
		start = clock.PuzzleDateAt(time.Now(), cfg.PuzzleTimezone)
	}

	g := puzzle.Generator{
		DB:               pool,
		CooldownDays:     cfg.QuestionCooldownDays,
		TimeLimitSeconds: cfg.TimeLimitSeconds,
	}
	for i := 0; i < *days; i++ {
		date := start.AddDays(i)
		p, err := g.GenerateFor(ctx, date)
		if err != nil {
			return fmt.Errorf("generate %s: %w", date, err)
		}
		fmt.Fprintf(stdout, "%s: %s\n", date, topicSummary(p))
	}
	return nil
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
```

- [ ] **Step 4: Write `cmd/trivial/main.go`**

```go
// Command trivial administers the daily trivia content library and puzzles.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hjordan6/trivial/internal/cli"
)

func main() {
	if err := cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 6: Verify the real commands end to end**

```bash
make db-up
make seed
go run ./cmd/trivial puzzles generate --from 2026-09-01 --days 5
go run ./cmd/trivial puzzles show 2026-09-01
```

Expected: `seeded 6 topics and 54 questions`, then five lines each naming three topics, then a nine-line board for 2026-09-01 with three topics at three difficulties each.

Then confirm the cooldown holds across the generated week:

```bash
docker compose exec -T db psql -U trivial -d trivial -c \
  "SELECT count(*) AS reused FROM (
     SELECT question_id FROM daily_puzzle_questions GROUP BY question_id HAVING count(*) > 1
   ) t;"
```

Expected: `reused | 0`.

- [ ] **Step 7: Commit**

```bash
git add cmd internal/cli
git commit -m "feat: add administration CLI for migrations, seeding, and puzzles"
```

---

### Task 12: Vue scaffold and CI

**Files:**
- Create: `web/` (Vite scaffold), `.github/workflows/ci.yml`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: nothing.
- Produces: a building Vue 3 + TypeScript application at `web/`, and CI that runs the Go suite against Postgres plus the frontend build. Plan 2 builds the actual UI on this scaffold.

- [ ] **Step 1: Scaffold the frontend**

```bash
npm create vite@latest web -- --template vue-ts
cd web && npm install && npm install -D vitest && cd ..
```

- [ ] **Step 2: Add the test script**

In `web/package.json`, add to `scripts`:

```json
"test": "vitest run --passWithNoTests"
```

`--passWithNoTests` is needed because this plan adds no frontend tests; Plan 2 removes the flag when it adds the first one.

- [ ] **Step 3: Verify the frontend builds**

```bash
cd web && npm run build && npm run test && cd ..
```

Expected: a `web/dist` directory, and vitest exiting zero with no test files.

- [ ] **Step 4: Write `.github/workflows/ci.yml`**

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

jobs:
  go:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:17-alpine
        env:
          POSTGRES_USER: trivial
          POSTGRES_PASSWORD: trivial
          POSTGRES_DB: trivial_test
        ports:
          - 5433:5432
        options: >-
          --health-cmd "pg_isready -U trivial"
          --health-interval 2s
          --health-timeout 3s
          --health-retries 30
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - name: Check formatting
        run: test -z "$(gofmt -l .)" || { gofmt -l .; exit 1; }
      - name: Vet
        run: go vet ./...
      - name: Test
        run: go test ./... -count=1
        env:
          TEST_DATABASE_URL: postgres://trivial:trivial@localhost:5433/trivial_test?sslmode=disable

  web:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: web
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: npm
          cache-dependency-path: web/package-lock.json
      - run: npm ci
      - run: npm run test
      - run: npm run build
```

CI pins Node 22 LTS while this machine runs Node 25. If a build succeeds locally and fails in CI, that gap is the first thing to check.

- [ ] **Step 5: Verify the full suite passes locally**

```bash
make lint
make test
```

Expected: no formatting or vet output, and every package passing.

- [ ] **Step 6: Commit**

```bash
git add web .github .gitignore
git commit -m "chore: scaffold vue frontend and add CI"
```

---

## Definition of Done

Plan 1 is complete when all of the following hold:

- `make test` passes from a clean checkout after `make db-up`.
- `make lint` reports nothing.
- `make seed` loads 6 topics and 54 questions, and is safe to run twice.
- `trivial puzzles generate --from <date> --days 5` produces five boards with no question repeated across them.
- `trivial puzzles show <date>` prints nine questions across three topics.
- Regenerating an already-generated date returns the identical board.
- CI is green on `main`.

## What Plan 2 Picks Up

Plan 2 (`Playable game`, spec phases 4–6) starts from this foundation and adds: the `players`, `runs`, and `run_answers` tables; the run lifecycle API with the server-authoritative clock; option-order seeding for the six-choice stage; the Vue play UI; and the results grid with share text. It will reuse `grading.Grade` unchanged — that is the point of building it standalone here — and it is where `Result.NearMiss` is finally logged, since Plan 1 has no request to log it against.
