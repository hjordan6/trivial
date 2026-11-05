# Play Reminders Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Email lapsed players a reminder (3, then 7, then 14 days after they stop playing) built around today's topics, with a one-click unsubscribe.

**Architecture:** A new `internal/reminders` package holds the SQL (who is due, recording sends, unsubscribe), the message builder and a `Mailer.Run` orchestrator, all against `db.DBTX` with an injected clock. `trivial reminders send [--dry-run]` wires it to config, `puzzle.Ensure` and the Resend sender, and runs daily from cron. `internal/httpapi` gains three small server-rendered unsubscribe routes.

**Tech Stack:** Go 1.26, Postgres via pgx/v5, goose migrations (embedded), `html/template`, Resend HTTP API.

**Spec:** `docs/superpowers/specs/2026-10-07-play-reminders-design.md`

## Global Constraints

- Default schedule `REMINDER_DAYS=3,7,14`; its length is the per-lapse cap.
- "Played" means a `runs` row exists for the date (started, not necessarily finished), across every `players` row with that `user_id`.
- Never-played users anchor on the date of `users.created_at` in `PUZZLE_TIMEZONE`.
- All date arithmetic happens in `PUZZLE_TIMEZONE`.
- Throttle: 2 messages/second (`Pause = 500ms`) in production.
- `reminders send` refuses to run without `APP_BASE_URL` and `MAIL_POSTAL_ADDRESS`.
- Unsubscribe token: `replace(gen_random_uuid()::text, '-', '')`, permanent, unique, never derived from `APP_SECRET`.
- `GET /unsubscribe/{token}` never changes state. Unknown tokens get the same page and status as known ones. Handlers read no cookies.
- Every reminder carries `List-Unsubscribe` and `List-Unsubscribe-Post: List-Unsubscribe=One-Click`.
- No new module dependencies.
- Match surrounding style: explanatory comments on *why*, `t.Fatalf`/`t.Errorf` tests, table tests where the codebase uses them.
- Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Other tests' users in the shared test DB.** `SelectDue` scans all users, and HTTP suites commit real `users` rows. Every selection test must filter results to the IDs it created (`dueFor` helper, Task 2), never assert on total counts.
2. **Timezone boundary.** A send at 23:30 local is "today" in `PUZZLE_TIMEZONE` even though it is tomorrow in UTC; same-day re-run must still be a no-op. Test pinned in Task 2 (`TestSelectDueUsesThePuzzleTimezone`).
3. **A send that succeeds but whose record fails** would resend tomorrow. `Run` stops on a record error instead of continuing (Task 5, `TestRunStopsWhenRecordingFails`).
4. **Provider failure for one address** must not stop the batch, and must not be recorded as sent (Task 5, `TestRunSkipsAFailedSendAndCarriesOn`).
5. **Malformed `APP_BASE_URL`** (missing scheme, trailing slash) would produce broken links in every email. Config rejects non-http(s) and trims the trailing slash (Task 6).

## File map

| File | Responsibility |
|---|---|
| `internal/db/migrations/00009_reminders.sql` (new) | Token + unsubscribed columns, `reminder_sends` table |
| `internal/reminders/store.go` (new) | `SelectDue`, `RecordSend`, `Unsubscribe`, `Resubscribe` |
| `internal/reminders/store_test.go` (new) | Selection and subscription tests |
| `internal/reminders/topics.go` (new) | `Topic`, `TopicsOf`, `Emoji` (Go port of `web/src/stores/run.ts` `topicEmoji`) |
| `internal/reminders/message.go` (new) | `Build`, `UnsubscribeURL`, text + HTML templates |
| `internal/reminders/message_test.go`, `topics_test.go` (new) | Builder and emoji tests |
| `internal/reminders/run.go` / `run_test.go` (new) | `Mailer`, `Report`, `Run` |
| `internal/mail/mail.go`, `resend.go`, `resend_test.go` | `Message.Headers`, passed to Resend |
| `internal/config/config.go`, `config_test.go` | `AppBaseURL`, `MailPostalAddress`, `ReminderDays`, `ValidateForReminders` |
| `internal/cli/cli.go`, `cli_test.go` | `reminders send [--dry-run]` |
| `internal/httpapi/unsubscribe.go` / `unsubscribe_test.go` (new), `server.go` | Unsubscribe routes |
| `.env.example`, `README.md` | Settings and operations docs |

---

### Task 1: Schema and subscription store

**Files:**
- Create: `internal/db/migrations/00009_reminders.sql`
- Create: `internal/reminders/store.go`
- Test: `internal/reminders/store_test.go`

**Interfaces:**
- Produces: `reminders.Unsubscribe(ctx, q db.DBTX, token string, at time.Time) error`, `reminders.Resubscribe(ctx, q db.DBTX, token string) error`; columns `users.unsubscribe_token text NOT NULL UNIQUE`, `users.reminders_unsubscribed_at timestamptz`; table `reminder_sends(id, user_id, step, sent_at)`.
- Test helpers produced (in `store_test.go`, reused by Tasks 2 and 5): `newUser(t, tx, email string, created time.Time) (id int64, token string)`.

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up

-- Play reminders: a nudge by email when a signed-in player stops playing.
--
-- unsubscribe_token is the credential in every reminder's unsubscribe link and
-- List-Unsubscribe header. It is random and stored, never derived from
-- APP_SECRET: rotating that secret would otherwise break every unsubscribe link
-- already sitting in an inbox, and an opt-out link has to keep working.
--
-- The default is volatile, so ADD COLUMN evaluates it once per existing row:
-- every current user is backfilled with their own token by this one statement.
ALTER TABLE users
    ADD COLUMN reminders_unsubscribed_at timestamptz,
    ADD COLUMN unsubscribe_token text NOT NULL DEFAULT replace(gen_random_uuid()::text, '-', '');

CREATE UNIQUE INDEX users_unsubscribe_token_key ON users (unsubscribe_token);

-- One row per reminder actually accepted by the provider. It is both the
-- schedule's memory (which step comes next, and when the last one went) and the
-- idempotency guard: a second run on the same day finds today's row and sends
-- nothing. CASCADE because a send record means nothing once the user is gone.
CREATE TABLE reminder_sends (
    id      bigserial PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    step    smallint NOT NULL CHECK (step > 0),
    sent_at timestamptz NOT NULL
);

CREATE INDEX reminder_sends_user_sent_idx ON reminder_sends (user_id, sent_at);

-- +goose Down
DROP TABLE reminder_sends;
DROP INDEX users_unsubscribe_token_key;
ALTER TABLE users
    DROP COLUMN unsubscribe_token,
    DROP COLUMN reminders_unsubscribed_at;
```

- [ ] **Step 2: Write the failing tests**

```go
package reminders_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/reminders"
	"github.com/hjordan6/trivial/internal/testsupport"
)

// newUser inserts a user and returns its id and unsubscribe token. Addresses
// must be unique across the shared test database, so callers pass one built
// from t.Name().
func newUser(t *testing.T, tx pgx.Tx, email string, created time.Time) (int64, string) {
	t.Helper()
	var id int64
	var token string
	if err := tx.QueryRow(context.Background(),
		`INSERT INTO users (email, created_at) VALUES ($1, $2) RETURNING id, unsubscribe_token`,
		email, created).Scan(&id, &token); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id, token
}

func unsubscribedAt(t *testing.T, tx pgx.Tx, id int64) *time.Time {
	t.Helper()
	var at *time.Time
	if err := tx.QueryRow(context.Background(),
		`SELECT reminders_unsubscribed_at FROM users WHERE id = $1`, id).Scan(&at); err != nil {
		t.Fatalf("read unsubscribed_at: %v", err)
	}
	return at
}

func TestNewUsersGetDistinctTokens(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	now := time.Date(2041, 3, 1, 12, 0, 0, 0, time.UTC)
	_, a := newUser(t, tx, "tokens-a@reminders.test", now)
	_, b := newUser(t, tx, "tokens-b@reminders.test", now)
	if len(a) != 32 || len(b) != 32 {
		t.Fatalf("token lengths = %d, %d, want 32 hex characters", len(a), len(b))
	}
	if a == b {
		t.Fatal("two users share an unsubscribe token")
	}
}

func TestUnsubscribeAndResubscribe(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	now := time.Date(2041, 3, 1, 12, 0, 0, 0, time.UTC)
	id, token := newUser(t, tx, "unsub@reminders.test", now)

	if err := reminders.Unsubscribe(ctx, tx, token, now); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if got := unsubscribedAt(t, tx, id); got == nil || !got.Equal(now) {
		t.Fatalf("unsubscribed_at = %v, want %v", got, now)
	}

	// A second unsubscribe keeps the original moment rather than moving it.
	if err := reminders.Unsubscribe(ctx, tx, token, now.Add(time.Hour)); err != nil {
		t.Fatalf("Unsubscribe again: %v", err)
	}
	if got := unsubscribedAt(t, tx, id); got == nil || !got.Equal(now) {
		t.Fatalf("unsubscribed_at after repeat = %v, want %v", got, now)
	}

	if err := reminders.Resubscribe(ctx, tx, token); err != nil {
		t.Fatalf("Resubscribe: %v", err)
	}
	if got := unsubscribedAt(t, tx, id); got != nil {
		t.Fatalf("unsubscribed_at after resubscribe = %v, want nil", got)
	}
}

func TestUnsubscribeWithAnUnknownTokenIsANoOp(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	if err := reminders.Unsubscribe(ctx, tx, "not-a-real-token", time.Now()); err != nil {
		t.Fatalf("Unsubscribe(unknown) = %v, want nil", err)
	}
	if err := reminders.Resubscribe(ctx, tx, "not-a-real-token"); err != nil {
		t.Fatalf("Resubscribe(unknown) = %v, want nil", err)
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `make db-up && TEST_DATABASE_URL="postgres://trivial:trivial@localhost:5433/trivial_test?sslmode=disable" go test ./internal/reminders/ -count=1`
Expected: FAIL — package `reminders` does not exist.

- [ ] **Step 4: Implement `store.go` (subscription half)**

```go
// Package reminders emails signed-in players who have stopped playing.
//
// Everything that touches the database takes a db.DBTX and a time passed in by
// the caller, never time.Now, so the whole schedule is testable inside a
// rolled-back transaction. Sending lives in run.go; this file is SQL only.
package reminders

import (
	"context"
	"fmt"
	"time"

	"github.com/hjordan6/trivial/internal/db"
)

// Unsubscribe stops reminders for the user holding token.
//
// An unknown token is not an error and does nothing: the HTTP layer answers it
// exactly as it answers a real one, so the endpoint cannot be used to discover
// which tokens exist. An already-unsubscribed user keeps the original moment.
func Unsubscribe(ctx context.Context, q db.DBTX, token string, at time.Time) error {
	if _, err := q.Exec(ctx, `
		UPDATE users SET reminders_unsubscribed_at = $2
		WHERE unsubscribe_token = $1 AND reminders_unsubscribed_at IS NULL`,
		token, at); err != nil {
		return fmt.Errorf("unsubscribe: %w", err)
	}
	return nil
}

// Resubscribe undoes Unsubscribe. Like it, an unknown token is a silent no-op.
func Resubscribe(ctx context.Context, q db.DBTX, token string) error {
	if _, err := q.Exec(ctx, `
		UPDATE users SET reminders_unsubscribed_at = NULL
		WHERE unsubscribe_token = $1`, token); err != nil {
		return fmt.Errorf("resubscribe: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Run to verify pass**

Run: same command as Step 3. Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/00009_reminders.sql internal/reminders/
git commit -m "feat: add unsubscribe tokens and the reminder send log

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Who is due

**Files:**
- Modify: `internal/reminders/store.go`
- Test: `internal/reminders/store_test.go`

**Interfaces:**
- Consumes: Task 1 schema, `newUser`.
- Produces:
  ```go
  type Due struct {
      UserID int64
      Email  string
      Token  string
      Step   int  // 1-based position in the schedule
      Last   bool // Step == len(gaps): the final reminder of this lapse
  }
  func SelectDue(ctx context.Context, q db.DBTX, today clock.Date, loc *time.Location, gaps []int) ([]Due, error)
  func RecordSend(ctx context.Context, q db.DBTX, userID int64, step int, at time.Time) error
  ```
- Test helpers produced: `play(t, tx, userID int64, date clock.Date)`, `sent(t, tx, userID int64, step int, at time.Time)`, `dueFor(ds []reminders.Due, id int64) *reminders.Due`, `day(t, s string) clock.Date`, `denver(t) *time.Location`, `noon(t, d clock.Date) time.Time` (noon Denver on that date).

- [ ] **Step 1: Write the failing tests** (append to `store_test.go`; add imports `fmt`, `strings`, `internal/clock`)

```go
var gaps = []int{3, 7, 14}

func denver(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func day(t *testing.T, s string) clock.Date {
	t.Helper()
	d, err := clock.ParseDate(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// noon is midday in Denver on d, safely inside the puzzle day either way.
func noon(t *testing.T, d clock.Date) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 12, 0, 0, 0, denver(t))
}

// play records that userID started the puzzle on date, from a fresh device.
// Puzzle dates live in 2041 so no other suite's committed boards collide.
func play(t *testing.T, tx pgx.Tx, userID int64, date clock.Date) {
	t.Helper()
	ctx := context.Background()
	if _, err := tx.Exec(ctx,
		`INSERT INTO daily_puzzles (puzzle_date) VALUES ($1) ON CONFLICT DO NOTHING`, date); err != nil {
		t.Fatalf("insert puzzle: %v", err)
	}
	var playerID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO players (user_id) VALUES ($1) RETURNING id`, userID).Scan(&playerID); err != nil {
		t.Fatalf("insert player: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO runs (player_id, puzzle_date, started_at, expires_at, option_seed)
		VALUES ($1, $2, now(), now() + interval '240 seconds', 1)`, playerID, date); err != nil {
		t.Fatalf("insert run: %v", err)
	}
}

func sent(t *testing.T, tx pgx.Tx, userID int64, step int, at time.Time) {
	t.Helper()
	if err := reminders.RecordSend(context.Background(), tx, userID, step, at); err != nil {
		t.Fatalf("RecordSend: %v", err)
	}
}

// dueFor picks one user out of a selection. Selections are never asserted on
// as a whole: the test database is shared, and other suites commit users.
func dueFor(ds []reminders.Due, id int64) *reminders.Due {
	for i := range ds {
		if ds[i].UserID == id {
			return &ds[i]
		}
	}
	return nil
}

func selectOn(t *testing.T, tx pgx.Tx, today clock.Date) []reminders.Due {
	t.Helper()
	ds, err := reminders.SelectDue(context.Background(), tx, today, denver(t), gaps)
	if err != nil {
		t.Fatalf("SelectDue: %v", err)
	}
	return ds
}

func email(t *testing.T) string {
	return fmt.Sprintf("%s@reminders.test", strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")))
}

func TestSelectDueWalksTheSchedule(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	id, token := newUser(t, tx, email(t), noon(t, day(t, "2041-01-01")))
	last := day(t, "2041-01-10")
	play(t, tx, id, last)

	// Day 2 after the last play: too soon.
	if d := dueFor(selectOn(t, tx, last.AddDays(2)), id); d != nil {
		t.Fatalf("due on day 2: %+v", d)
	}
	// Day 3: step 1.
	d := dueFor(selectOn(t, tx, last.AddDays(3)), id)
	if d == nil || d.Step != 1 || d.Last || d.Token != token || d.Email != email(t) {
		t.Fatalf("day 3 = %+v, want step 1 for %s", d, email(t))
	}
	sent(t, tx, id, 1, noon(t, last.AddDays(3)))

	// Step 2 is 7 days after step 1, not 7 after the play.
	if d := dueFor(selectOn(t, tx, last.AddDays(9)), id); d != nil {
		t.Fatalf("due 6 days after step 1: %+v", d)
	}
	d = dueFor(selectOn(t, tx, last.AddDays(10)), id)
	if d == nil || d.Step != 2 || d.Last {
		t.Fatalf("day 10 = %+v, want step 2", d)
	}
	sent(t, tx, id, 2, noon(t, last.AddDays(10)))

	d = dueFor(selectOn(t, tx, last.AddDays(24)), id)
	if d == nil || d.Step != 3 || !d.Last {
		t.Fatalf("day 24 = %+v, want final step 3", d)
	}
	sent(t, tx, id, 3, noon(t, last.AddDays(24)))

	// Never a fourth.
	if d := dueFor(selectOn(t, tx, last.AddDays(400)), id); d != nil {
		t.Fatalf("due after the last step: %+v", d)
	}
}

func TestSelectDuePlayingResetsTheCycle(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	id, _ := newUser(t, tx, email(t), noon(t, day(t, "2041-01-01")))
	first := day(t, "2041-02-01")
	play(t, tx, id, first)
	sent(t, tx, id, 1, noon(t, first.AddDays(3)))
	sent(t, tx, id, 2, noon(t, first.AddDays(10)))

	back := first.AddDays(12)
	play(t, tx, id, back)

	if d := dueFor(selectOn(t, tx, back.AddDays(2)), id); d != nil {
		t.Fatalf("due 2 days after returning: %+v", d)
	}
	d := dueFor(selectOn(t, tx, back.AddDays(3)), id)
	if d == nil || d.Step != 1 {
		t.Fatalf("3 days after returning = %+v, want step 1 again", d)
	}
}

func TestSelectDueSkipsAnyoneWhoPlayedToday(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	id, _ := newUser(t, tx, email(t), noon(t, day(t, "2041-01-01")))
	today := day(t, "2041-03-01")
	play(t, tx, id, today)
	if d := dueFor(selectOn(t, tx, today), id); d != nil {
		t.Fatalf("due on a day they played: %+v", d)
	}
}

func TestSelectDueSkipsTheUnsubscribed(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	id, token := newUser(t, tx, email(t), noon(t, day(t, "2041-01-01")))
	if err := reminders.Unsubscribe(context.Background(), tx, token, time.Now()); err != nil {
		t.Fatal(err)
	}
	if d := dueFor(selectOn(t, tx, day(t, "2041-04-01")), id); d != nil {
		t.Fatalf("unsubscribed user is due: %+v", d)
	}
}

func TestSelectDueAnchorsNeverPlayedUsersOnSignUp(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	joined := day(t, "2041-05-01")
	id, _ := newUser(t, tx, email(t), noon(t, joined))
	if d := dueFor(selectOn(t, tx, joined.AddDays(2)), id); d != nil {
		t.Fatalf("due 2 days after sign-up: %+v", d)
	}
	if d := dueFor(selectOn(t, tx, joined.AddDays(3)), id); d == nil || d.Step != 1 {
		t.Fatalf("3 days after sign-up = %+v, want step 1", d)
	}
}

func TestSelectDueCountsPlaysFromEveryDevice(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	id, _ := newUser(t, tx, email(t), noon(t, day(t, "2041-01-01")))
	play(t, tx, id, day(t, "2041-06-01")) // phone
	play(t, tx, id, day(t, "2041-06-05")) // laptop: a second players row
	if d := dueFor(selectOn(t, tx, day(t, "2041-06-07")), id); d != nil {
		t.Fatalf("due 2 days after the laptop play: %+v", d)
	}
}

func TestSelectDueIsANoOpTheSecondTimeInADay(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	id, _ := newUser(t, tx, email(t), noon(t, day(t, "2041-01-01")))
	last := day(t, "2041-07-01")
	play(t, tx, id, last)
	today := last.AddDays(30)
	sent(t, tx, id, 1, noon(t, today))
	// Step 2's gap is long past measured from the play, but step 1 went today.
	if d := dueFor(selectOn(t, tx, today), id); d != nil {
		t.Fatalf("due twice in one day: %+v", d)
	}
}

// A send late in the Denver evening is already tomorrow in UTC. It must still
// count as today's send, or a re-run that evening would send step 2.
func TestSelectDueUsesThePuzzleTimezone(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	id, _ := newUser(t, tx, email(t), noon(t, day(t, "2041-01-01")))
	last := day(t, "2041-08-01")
	play(t, tx, id, last)
	today := last.AddDays(30)
	late := time.Date(today.Year, today.Month, today.Day, 23, 30, 0, 0, denver(t))
	sent(t, tx, id, 1, late)
	if d := dueFor(selectOn(t, tx, today), id); d != nil {
		t.Fatalf("a 23:30 local send was not counted as today: %+v", d)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `TEST_DATABASE_URL=... go test ./internal/reminders/ -count=1`
Expected: FAIL — `undefined: reminders.SelectDue`, `reminders.RecordSend`.

- [ ] **Step 3: Implement** (append to `store.go`; add imports `clock`)

```go
// Due is one user owed a reminder today.
type Due struct {
	UserID int64
	Email  string
	Token  string
	// Step is the 1-based position in the schedule this reminder fills.
	Step int
	// Last is true for the final reminder of a lapse, which says so.
	Last bool
}

// selectDueSQL finds every subscribed user who has not played today and has
// room left in this lapse, along with how many days it has been since the
// schedule's anchor. Go applies the per-step gap, which keeps the gap list out
// of SQL.
//
// last_played is the latest puzzle date started on any of the user's devices,
// or the sign-up date for someone who never played. Sends dated after it are
// this lapse's; earlier ones belonged to a lapse that playing already ended.
// Every timestamp is converted to a date in the puzzle timezone ($2) before it
// is compared, so a late-evening send is still "today".
const selectDueSQL = `
WITH last_play AS (
    SELECT u.id, u.email, u.unsubscribe_token,
           COALESCE(max(r.puzzle_date), (u.created_at AT TIME ZONE $2)::date) AS last_played
    FROM users u
    LEFT JOIN players p ON p.user_id = u.id
    LEFT JOIN runs r ON r.player_id = p.id
    WHERE u.reminders_unsubscribed_at IS NULL
    GROUP BY u.id
), lapse AS (
    SELECT lp.id, lp.email, lp.unsubscribe_token, lp.last_played,
           count(s.id) AS sends,
           max((s.sent_at AT TIME ZONE $2)::date) AS last_sent
    FROM last_play lp
    LEFT JOIN reminder_sends s
           ON s.user_id = lp.id AND (s.sent_at AT TIME ZONE $2)::date > lp.last_played
    GROUP BY lp.id, lp.email, lp.unsubscribe_token, lp.last_played
)
SELECT id, email, unsubscribe_token, sends + 1,
       $1::date - COALESCE(last_sent, last_played)
FROM lapse
WHERE last_played < $1::date
  AND sends < $3
  AND NOT EXISTS (
      SELECT 1 FROM reminder_sends t
      WHERE t.user_id = lapse.id AND (t.sent_at AT TIME ZONE $2)::date = $1::date)
ORDER BY id`

// SelectDue returns everyone owed a reminder on today, in user id order.
//
// gaps[i] is how many days must separate reminder i+1 from its anchor: the
// last play for the first, the previous reminder for the rest.
func SelectDue(ctx context.Context, q db.DBTX, today clock.Date, loc *time.Location, gaps []int) ([]Due, error) {
	rows, err := q.Query(ctx, selectDueSQL, today, loc.String(), len(gaps))
	if err != nil {
		return nil, fmt.Errorf("select due reminders: %w", err)
	}
	defer rows.Close()

	var out []Due
	for rows.Next() {
		var d Due
		var days int
		if err := rows.Scan(&d.UserID, &d.Email, &d.Token, &d.Step, &days); err != nil {
			return nil, fmt.Errorf("scan due reminder: %w", err)
		}
		if days < gaps[d.Step-1] {
			continue
		}
		d.Last = d.Step == len(gaps)
		out = append(out, d)
	}
	return out, rows.Err()
}

// RecordSend notes that step went to userID at at. Run calls it immediately
// after the provider accepts each message, so a run that dies partway leaves
// an accurate log and the next run neither skips nor repeats anyone.
func RecordSend(ctx context.Context, q db.DBTX, userID int64, step int, at time.Time) error {
	if _, err := q.Exec(ctx,
		`INSERT INTO reminder_sends (user_id, step, sent_at) VALUES ($1, $2, $3)`,
		userID, step, at); err != nil {
		return fmt.Errorf("record reminder for user %d: %w", userID, err)
	}
	return nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: same as Step 2. Expected: PASS (all store tests). If `count(...)` scanning into `int` fails (`bigint`), cast in SQL: `(count(s.id) + 1)::int` and `($1::date - ...)::int`.

- [ ] **Step 5: Commit**

```bash
git add internal/reminders/
git commit -m "feat: select who is owed a play reminder today

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Custom headers on outbound mail

**Files:**
- Modify: `internal/mail/mail.go` (Message struct)
- Modify: `internal/mail/resend.go:45-52`
- Test: `internal/mail/resend_test.go`

**Interfaces:**
- Produces: `mail.Message.Headers map[string]string`.

- [ ] **Step 1: Write the failing tests** (append to `resend_test.go`)

```go
func TestResendSendsCustomHeaders(t *testing.T) {
	var got struct {
		Headers map[string]string `json:"headers"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := Resend{APIKey: "re_test", From: "Trivial <play@example.test>", Endpoint: srv.URL}
	err := sender.Send(context.Background(), Message{
		To: "player@example.test", Subject: "s", Text: "t",
		Headers: map[string]string{
			"List-Unsubscribe":      "<https://example.test/unsubscribe/abc>",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if got.Headers["List-Unsubscribe"] != "<https://example.test/unsubscribe/abc>" ||
		got.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Errorf("headers = %v", got.Headers)
	}
}

func TestResendOmitsTheHeadersFieldWhenThereAreNone(t *testing.T) {
	var raw map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	sender := Resend{APIKey: "re_test", From: "x@example.test", Endpoint: srv.URL}
	if err := sender.Send(context.Background(), Message{To: "a@example.test", Subject: "s", Text: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["headers"]; ok {
		t.Error("request carries a headers field for a message with none")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/mail/ -count=1`
Expected: FAIL — `unknown field Headers in struct literal`.

- [ ] **Step 3: Implement**

In `mail.go`, add to `Message` after `HTML`:

```go
	// Headers are extra message headers, such as List-Unsubscribe on a
	// reminder. Nil for the sign-in email, which is transactional and has
	// nothing to unsubscribe from.
	Headers map[string]string
```

In `resend.go`, extend the anonymous request struct and literal:

```go
		HTML    string            `json:"html,omitempty"`
		Headers map[string]string `json:"headers,omitempty"`
	}{From: r.From, To: []string{m.To}, Subject: m.Subject, Text: m.Text, HTML: m.HTML, Headers: m.Headers})
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/mail/ -count=1`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mail/
git commit -m "feat: let outbound mail carry extra headers

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Topics and the reminder message

**Files:**
- Create: `internal/reminders/topics.go`, `internal/reminders/topics_test.go`
- Create: `internal/reminders/message.go`, `internal/reminders/message_test.go`

**Interfaces:**
- Consumes: `Due` (Task 2), `mail.Message.Headers` (Task 3), `puzzle.Puzzle`/`puzzle.Entry` (`TopicSlug`, `TopicName`, `TopicPosition`).
- Produces:
  ```go
  type Topic struct{ Name, Emoji string }
  func TopicsOf(p *puzzle.Puzzle) []Topic
  func Emoji(slug, name string) string
  func UnsubscribeURL(baseURL, token string) string
  func Build(d Due, topics []Topic, baseURL, postalAddress string) mail.Message
  ```

- [ ] **Step 1: Write the failing tests**

`topics_test.go`:

```go
package reminders

import (
	"testing"

	"github.com/hjordan6/trivial/internal/puzzle"
)

func TestEmojiMatchesTheFrontEnd(t *testing.T) {
	tests := []struct{ slug, name, want string }{
		{"geography", "Geography", "🌍"},
		{"general-knowledge", "General Knowledge", "🧠"},
		{"world-history", "World History", "🏛️"},
		{"cooking-basics", "Cooking Basics", "🍽️"}, // keyword fallback
		{"general-science", "General Science", "🔬"}, // subject beats "general"
		{"zzz", "Zzz", "❓"},
	}
	for _, tt := range tests {
		if got := Emoji(tt.slug, tt.name); got != tt.want {
			t.Errorf("Emoji(%q, %q) = %q, want %q", tt.slug, tt.name, got, tt.want)
		}
	}
}

func TestTopicsOfKeepsBoardOrderAndDedupes(t *testing.T) {
	p := &puzzle.Puzzle{Entries: []puzzle.Entry{
		{TopicSlug: "music", TopicName: "Music", TopicPosition: 2},
		{TopicSlug: "geography", TopicName: "Geography", TopicPosition: 1},
		{TopicSlug: "music", TopicName: "Music", TopicPosition: 2},
		{TopicSlug: "sports", TopicName: "Sports", TopicPosition: 3},
	}}
	got := TopicsOf(p)
	want := []Topic{{"Geography", "🌍"}, {"Music", "🎵"}, {"Sports", "⚽"}}
	if len(got) != len(want) {
		t.Fatalf("TopicsOf = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("TopicsOf[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}
```

`message_test.go`:

```go
package reminders

import (
	"strings"
	"testing"
)

var testTopics = []Topic{{"History", "🏛️"}, {"Science", "🔬"}, {"Movies", "🎬"}}

func TestBuildSubjectListsTodaysTopics(t *testing.T) {
	m := Build(Due{Email: "a@example.test", Token: "tok", Step: 1}, testTopics, "https://trivial.test", "PO Box 1, Denver CO")
	if m.Subject != "Today's Trivial: History, Science & Movies" {
		t.Errorf("Subject = %q", m.Subject)
	}
	if m.To != "a@example.test" {
		t.Errorf("To = %q", m.To)
	}
}

func TestBuildJoinsShortTopicLists(t *testing.T) {
	if got := joinTopics([]Topic{{"A", ""}}); got != "A" {
		t.Errorf("one = %q", got)
	}
	if got := joinTopics([]Topic{{"A", ""}, {"B", ""}}); got != "A & B" {
		t.Errorf("two = %q", got)
	}
}

func TestBuildOpeningDependsOnTheStep(t *testing.T) {
	tests := []struct {
		d    Due
		want string
	}{
		{Due{Step: 1}, "Haven't seen you in a few days."},
		{Due{Step: 2}, "It's been over a week."},
		{Due{Step: 3, Last: true}, "Last nudge from us"},
	}
	for _, tt := range tests {
		tt.d.Email, tt.d.Token = "a@example.test", "tok"
		m := Build(tt.d, testTopics, "https://trivial.test", "PO Box 1")
		if !strings.Contains(m.Text, tt.want) {
			t.Errorf("step %d text missing %q:\n%s", tt.d.Step, tt.want, m.Text)
		}
	}
}

func TestBuildCarriesLinksAddressAndHeaders(t *testing.T) {
	m := Build(Due{Email: "a@example.test", Token: "tok123", Step: 1}, testTopics, "https://trivial.test", "PO Box 1, Denver CO")
	unsub := "https://trivial.test/unsubscribe/tok123"
	for _, part := range []struct{ name, body string }{{"text", m.Text}, {"html", m.HTML}} {
		for _, want := range []string{"https://trivial.test/", unsub, "PO Box 1, Denver CO", "History", "🔬"} {
			if !strings.Contains(part.body, want) {
				t.Errorf("%s part missing %q", part.name, want)
			}
		}
	}
	if m.Headers["List-Unsubscribe"] != "<"+unsub+">" {
		t.Errorf("List-Unsubscribe = %q", m.Headers["List-Unsubscribe"])
	}
	if m.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Errorf("List-Unsubscribe-Post = %q", m.Headers["List-Unsubscribe-Post"])
	}
}

func TestBuildEscapesTopicNamesInHTML(t *testing.T) {
	m := Build(Due{Email: "a@example.test", Token: "t", Step: 1},
		[]Topic{{"<b>Rock & Roll</b>", "🎵"}}, "https://trivial.test", "PO Box 1")
	if strings.Contains(m.HTML, "<b>Rock") {
		t.Error("topic name reached the HTML unescaped")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `TEST_DATABASE_URL=... go test ./internal/reminders/ -count=1`
Expected: FAIL — undefined `Emoji`, `TopicsOf`, `Build`, `joinTopics`.

- [ ] **Step 3: Implement `topics.go`**

```go
package reminders

import (
	"regexp"
	"sort"
	"strings"

	"github.com/hjordan6/trivial/internal/puzzle"
)

// Topic is one of today's topics as the reminder shows it.
type Topic struct {
	Name  string
	Emoji string
}

// TopicsOf returns a board's topics once each, in board order.
func TopicsOf(p *puzzle.Puzzle) []Topic {
	entries := append([]puzzle.Entry(nil), p.Entries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].TopicPosition < entries[j].TopicPosition })
	seen := map[string]bool{}
	var out []Topic
	for _, e := range entries {
		if seen[e.TopicSlug] {
			continue
		}
		seen[e.TopicSlug] = true
		out = append(out, Topic{Name: e.TopicName, Emoji: Emoji(e.TopicSlug, e.TopicName)})
	}
	return out
}

// topicEmoji and topicKeywordEmoji are a port of TOPIC_EMOJI and
// TOPIC_KEYWORD_EMOJI in web/src/stores/run.ts. Keep the two in step: a topic
// should wear the same emoji in the inbox as on the board.
var topicEmoji = map[string]string{
	"geography":           "🌍",
	"world-geography":     "🌍",
	"science-nature":      "🔬",
	"science-and-nature":  "🔬",
	"movies-tv":           "🎬",
	"film-and-television": "🎬",
	"music":               "🎵",
	"sports":              "⚽",
	"sport":               "⚽",
	"u-s-history":         "🗽",
	"world-history":       "🏛️",
	"history":             "🏛️",
	"art-culture":         "🎨",
	"literature-language": "📚",
	"technology-internet": "💻",
	"modern-pop-culture":  "✨",
	"general-knowledge":   "🧠",
}

var topicKeywordEmoji = []struct {
	pattern *regexp.Regexp
	emoji   string
}{
	{regexp.MustCompile(`(?i)geograph|\bworld\b|map|travel|countr|capital`), "🌍"},
	{regexp.MustCompile(`(?i)scien|nature|biolog|chemi|physic|space|astronom`), "🔬"},
	{regexp.MustCompile(`(?i)film|movie|televis|\btv\b|cinema`), "🎬"},
	{regexp.MustCompile(`(?i)\bu\.?s\.?\b|america`), "🗽"},
	{regexp.MustCompile(`(?i)histor|ancient|war`), "🏛️"},
	{regexp.MustCompile(`(?i)music|song|band|album`), "🎵"},
	{regexp.MustCompile(`(?i)sport|football|soccer|olymp`), "⚽"},
	{regexp.MustCompile(`(?i)pop culture|celebrit|trend|viral|meme`), "✨"},
	{regexp.MustCompile(`(?i)tech|comput|internet|softwar|game|gaming`), "💻"},
	{regexp.MustCompile(`(?i)literat|languag|book|author|poet|word`), "📚"},
	{regexp.MustCompile(`(?i)\bart\b|culture|paint|sculpt|museum`), "🎨"},
	{regexp.MustCompile(`(?i)food|drink|cook|cuisine`), "🍽️"},
	{regexp.MustCompile(`(?i)politic|govern|law|electio`), "🗳️"},
	{regexp.MustCompile(`(?i)animal|wildlife|nature`), "🐾"},
	{regexp.MustCompile(`(?i)myth|religio|folklore`), "🔮"},
	{regexp.MustCompile(`(?i)business|econom|money|financ`), "💰"},
	// Last, as on the front end, so "General Science" matches its subject.
	{regexp.MustCompile(`(?i)general|trivia|miscellan|assorted|potpourri|grab bag`), "🧠"},
}

// Emoji picks a topic's emoji the way the front end's topicEmoji does.
func Emoji(slug, name string) string {
	if e, ok := topicEmoji[slug]; ok {
		return e
	}
	haystack := strings.ReplaceAll(slug, "-", " ") + " " + name
	for _, k := range topicKeywordEmoji {
		if k.pattern.MatchString(haystack) {
			return k.emoji
		}
	}
	return "❓"
}
```

- [ ] **Step 4: Implement `message.go`**

```go
package reminders

import (
	"html/template"
	"net/url"
	"strings"

	"github.com/hjordan6/trivial/internal/mail"
)

// UnsubscribeURL is the link in the footer and the List-Unsubscribe target.
func UnsubscribeURL(baseURL, token string) string {
	return baseURL + "/unsubscribe/" + url.PathEscape(token)
}

// Build renders one reminder.
//
// The subject carries the day's topics because they are the only part of the
// message that changes from one day to the next: it gives the player a reason
// to open it, and it keeps a series of reminders from reading to a filter like
// the same message sent over and over.
func Build(d Due, topics []Topic, baseURL, postalAddress string) mail.Message {
	data := messageData{
		Opening:        opening(d),
		Topics:         topics,
		PlayURL:        baseURL + "/",
		UnsubscribeURL: UnsubscribeURL(baseURL, d.Token),
		PostalAddress:  postalAddress,
		Last:           d.Last,
	}
	m := mail.Message{
		To:      d.Email,
		Subject: "Today's Trivial: " + joinTopics(topics),
		Text:    reminderText(data),
		// RFC 8058 one-click: a mail client's own Unsubscribe button POSTs to
		// this URL, which the unsubscribe handler accepts without a cookie.
		Headers: map[string]string{
			"List-Unsubscribe":      "<" + data.UnsubscribeURL + ">",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
	}
	var html strings.Builder
	// A template that cannot render its own literal is a programming error; the
	// text part alone still delivers a working reminder, so fall back to it.
	if err := reminderHTML.Execute(&html, data); err == nil {
		m.HTML = html.String()
	}
	return m
}

type messageData struct {
	Opening        string
	Topics         []Topic
	PlayURL        string
	UnsubscribeURL string
	PostalAddress  string
	Last           bool
}

func opening(d Due) string {
	switch {
	case d.Last:
		return "Last nudge from us — we'll stop after this one."
	case d.Step == 1:
		return "Haven't seen you in a few days."
	default:
		return "It's been over a week."
	}
}

// joinTopics writes "A", "A & B", or "A, B & C".
func joinTopics(topics []Topic) string {
	names := make([]string, len(topics))
	for i, t := range topics {
		names[i] = t.Name
	}
	if len(names) <= 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " & " + names[len(names)-1]
}

func reminderText(d messageData) string {
	var b strings.Builder
	b.WriteString(d.Opening + " Today's puzzle is up:\n\n")
	for _, t := range d.Topics {
		b.WriteString("  " + t.Emoji + " " + t.Name + "\n")
	}
	b.WriteString("\nPlay today's puzzle: " + d.PlayURL + "\n\n")
	b.WriteString("--\nYou're getting this because you signed in to Trivial with this address.\n")
	b.WriteString("Unsubscribe: " + d.UnsubscribeURL + "\n")
	b.WriteString(d.PostalAddress + "\n")
	return b.String()
}

// reminderHTML follows loginCodeHTML in internal/httpapi/logincodemail.go:
// nested tables and inline styles in the site palette, because Gmail mobile
// drops <style> blocks and Outlook renders through Word.
var reminderHTML = template.Must(template.New("reminder").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light">
<title>Today's Trivial</title>
</head>
<body style="margin:0;padding:0;background:#f4f1e8;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">{{.Opening}} Today's puzzle is up.</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#f4f1e8;">
<tr><td align="center" style="padding:32px 12px;">
<table role="presentation" width="460" cellpadding="0" cellspacing="0" border="0" style="width:460px;max-width:100%;background:#fffdf7;border:1px solid #cbc7bc;border-radius:6px;">

<tr><td style="padding:30px 30px 0;font-family:'Courier New',Courier,monospace;font-size:11px;font-weight:500;letter-spacing:.16em;text-transform:uppercase;color:#6a6b65;">Trivial &middot; daily trivia</td></tr>

<tr><td style="padding:10px 30px 0;font-family:system-ui,-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:25px;font-weight:700;letter-spacing:-.02em;line-height:1.2;color:#1b1d1b;">{{.Opening}}</td></tr>

<tr><td style="padding:14px 30px 0;font-family:system-ui,-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:15px;line-height:1.6;color:#55564f;">Today&rsquo;s puzzle is up:</td></tr>

<tr><td style="padding:10px 30px 0;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0">
  {{range .Topics}}<tr><td style="padding:6px 0;font-family:system-ui,-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:18px;font-weight:600;color:#1b1d1b;">{{.Emoji}}&nbsp;&nbsp;{{.Name}}</td></tr>{{end}}
  </table>
</td></tr>

<tr><td style="padding:22px 30px 30px;">
  <table role="presentation" cellpadding="0" cellspacing="0" border="0">
    <tr><td style="background:#d9ff55;border:2px solid #1b1d1b;border-radius:6px;">
      <a href="{{.PlayURL}}" style="display:inline-block;padding:14px 22px;font-family:system-ui,-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:16px;font-weight:700;color:#1b1d1b;text-decoration:none;">Play today&rsquo;s puzzle</a>
    </td></tr>
  </table>
</td></tr>

<tr><td style="padding:20px 30px 28px;border-top:1px solid #cbc7bc;font-family:system-ui,-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:13px;line-height:1.55;color:#6a6b65;">
  You&rsquo;re getting this because you signed in to Trivial with this address.{{if .Last}} This is the last reminder until you play again.{{end}}
  <a href="{{.UnsubscribeURL}}" style="color:#6a6b65;">Unsubscribe</a>.<br>
  {{.PostalAddress}}
</td></tr>

</table>
</td></tr>
</table>
</body>
</html>
`))
```

- [ ] **Step 5: Run to verify pass**

Run: `TEST_DATABASE_URL=... go test ./internal/reminders/ -count=1`. Expected: PASS.

Note: `html/template` escapes `&` in the URL attributes and text; the test for the unsubscribe URL in the HTML part uses a token with no special characters, so the literal URL appears. If `TestBuildCarriesLinksAddressAndHeaders` fails only on the HTML part for `PO Box 1, Denver CO`, check the template rendered at all (Build swallows the error) by temporarily asserting `m.HTML != ""`.

- [ ] **Step 6: Commit**

```bash
git add internal/reminders/
git commit -m "feat: build the play reminder email around today's topics

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The send loop

**Files:**
- Create: `internal/reminders/run.go`
- Test: `internal/reminders/run_test.go`

**Interfaces:**
- Consumes: `SelectDue`, `RecordSend`, `Build`, `Topic`; `mail.Sender`, `mail.ErrRejected`; `clock.Clock`; test helpers from `store_test.go` (same package `reminders_test`).
- Produces:
  ```go
  type Mailer struct {
      DB            db.DBTX
      Sender        mail.Sender
      Clock         clock.Clock
      Timezone      *time.Location
      Gaps          []int
      BaseURL       string
      PostalAddress string
      Pause         time.Duration // between sends; 0 in tests
      Out           io.Writer     // progress lines; nil discards
  }
  type Report struct{ Due, Sent, Failed int }
  func (m Mailer) Run(ctx context.Context, topics []Topic, dryRun bool) (Report, error)
  ```

- [ ] **Step 1: Write the failing tests**

```go
package reminders_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/mail"
	"github.com/hjordan6/trivial/internal/reminders"
	"github.com/hjordan6/trivial/internal/testsupport"
)

// recorder is a mail.Sender that remembers every message and can refuse
// chosen addresses.
type recorder struct {
	mu     sync.Mutex
	sent   []mail.Message
	refuse map[string]error
}

func (r *recorder) Send(_ context.Context, m mail.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.refuse[m.To]; err != nil {
		return err
	}
	r.sent = append(r.sent, m)
	return nil
}

func (r *recorder) to(addr string) []mail.Message {
	var out []mail.Message
	for _, m := range r.sent {
		if m.To == addr {
			out = append(out, m)
		}
	}
	return out
}

var runTopics = []reminders.Topic{{Name: "History", Emoji: "🏛️"}}

// execFails passes reads through to a real transaction and fails every Exec,
// which is how RecordSend writes.
type execFails struct{ pgx.Tx }

func (execFails) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("disk full")
}

func TestRunSendsAndRecords(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	addr := email(t)
	id, _ := newUser(t, tx, addr, noon(t, day(t, "2041-01-01")))
	last := day(t, "2041-09-01")
	play(t, tx, id, last)
	today := last.AddDays(3)

	rec := &recorder{}
	m := reminders.Mailer{DB: tx, Sender: rec, Clock: clock.Fake{T: noon(t, today)}, Timezone: denver(t),
		Gaps: gaps, BaseURL: "https://trivial.test", PostalAddress: "PO Box 1"}

	if _, err := m.Run(context.Background(), runTopics, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rec.to(addr); len(got) != 1 || !strings.Contains(got[0].Subject, "History") {
		t.Fatalf("sent to %s = %v, want one reminder", addr, got)
	}
	// Recorded: running again the same day sends nothing more.
	if _, err := m.Run(context.Background(), runTopics, false); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := rec.to(addr); len(got) != 1 {
		t.Fatalf("after a second run, %d reminders went to %s, want 1", len(got), addr)
	}
}

func TestRunDryRunSendsAndRecordsNothing(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	addr := email(t)
	id, _ := newUser(t, tx, addr, noon(t, day(t, "2041-01-01")))
	last := day(t, "2041-09-01")
	play(t, tx, id, last)
	today := last.AddDays(3)

	rec := &recorder{}
	var out bytes.Buffer
	m := reminders.Mailer{DB: tx, Sender: rec, Clock: clock.Fake{T: noon(t, today)}, Timezone: denver(t),
		Gaps: gaps, BaseURL: "https://trivial.test", PostalAddress: "PO Box 1", Out: &out}

	if _, err := m.Run(context.Background(), runTopics, true); err != nil {
		t.Fatalf("Run(dry): %v", err)
	}
	if len(rec.to(addr)) != 0 {
		t.Fatal("dry run sent mail")
	}
	if !strings.Contains(out.String(), addr) {
		t.Errorf("dry run output does not name %s:\n%s", addr, out.String())
	}
	// Still due: nothing was recorded.
	if d := dueFor(selectOn(t, tx, today), id); d == nil {
		t.Fatal("dry run recorded a send")
	}
}

func TestRunSkipsAFailedSendAndCarriesOn(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	bad, good := "bad-"+email(t), "good-"+email(t)
	badID, _ := newUser(t, tx, bad, noon(t, day(t, "2041-01-01")))
	goodID, _ := newUser(t, tx, good, noon(t, day(t, "2041-01-01")))
	last := day(t, "2041-10-01")
	play(t, tx, badID, last)
	play(t, tx, goodID, last)
	today := last.AddDays(3)

	rec := &recorder{refuse: map[string]error{bad: mail.ErrRejected}}
	m := reminders.Mailer{DB: tx, Sender: rec, Clock: clock.Fake{T: noon(t, today)}, Timezone: denver(t),
		Gaps: gaps, BaseURL: "https://trivial.test", PostalAddress: "PO Box 1"}

	rep, err := m.Run(context.Background(), runTopics, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Failed < 1 {
		t.Errorf("Report.Failed = %d, want at least 1", rep.Failed)
	}
	if len(rec.to(good)) != 1 {
		t.Error("the good address was not mailed after the bad one failed")
	}
	// The failure was not recorded, so tomorrow tries again.
	if d := dueFor(selectOn(t, tx, today.AddDays(1)), badID); d == nil || d.Step != 1 {
		t.Fatalf("failed user tomorrow = %+v, want step 1 again", d)
	}
}

func TestRunStopsWhenRecordingFails(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	addr := email(t)
	id, _ := newUser(t, tx, addr, noon(t, day(t, "2041-01-01")))
	last := day(t, "2041-11-01")
	play(t, tx, id, last)

	rec := &recorder{}
	m := reminders.Mailer{DB: execFails{tx}, Sender: rec, Clock: clock.Fake{T: noon(t, last.AddDays(3))},
		Timezone: denver(t), Gaps: gaps, BaseURL: "https://trivial.test", PostalAddress: "PO Box 1"}
	if _, err := m.Run(context.Background(), runTopics, false); err == nil {
		t.Fatal("Run succeeded although every send record failed")
	}
	if len(rec.sent) != 1 {
		t.Fatalf("%d messages sent, want exactly 1 before stopping", len(rec.sent))
	}
}
```

**Why "exactly 1 sent" holds in `TestRunStopsWhenRecordingFails`:** other suites' committed users may also be due on that date, so "exactly 1 sent" holds because `Run` stops at the first record failure regardless of who comes first.

- [ ] **Step 2: Run to verify failure**

Run: `TEST_DATABASE_URL=... go test ./internal/reminders/ -count=1`
Expected: FAIL — undefined `reminders.Mailer`.

- [ ] **Step 3: Implement `run.go`**

```go
package reminders

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/db"
	"github.com/hjordan6/trivial/internal/mail"
)

// Mailer sends one day's reminders.
type Mailer struct {
	DB            db.DBTX
	Sender        mail.Sender
	Clock         clock.Clock
	Timezone      *time.Location
	Gaps          []int
	BaseURL       string
	PostalAddress string
	// Pause is the gap between sends. Production runs at 500ms -- two a second,
	// Resend's default rate limit -- so a first run against every existing user
	// neither trips the provider nor arrives as one suspicious burst.
	Pause time.Duration
	// Out receives one line per user. Nil discards.
	Out io.Writer
}

// Report counts what a run did.
type Report struct {
	Due    int
	Sent   int
	Failed int
}

// Run selects everyone due today and mails them.
//
// A send that fails is logged and skipped, and left unrecorded so tomorrow's
// run tries again; the batch carries on. A send that succeeds but cannot be
// recorded stops the run with an error instead: carrying on would leave a
// player who was mailed with no record of it, and tomorrow would mail them
// again.
func (m Mailer) Run(ctx context.Context, topics []Topic, dryRun bool) (Report, error) {
	out := m.Out
	if out == nil {
		out = io.Discard
	}
	today := clock.PuzzleDateAt(m.Clock.Now(), m.Timezone)
	due, err := SelectDue(ctx, m.DB, today, m.Timezone, m.Gaps)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Due: len(due)}

	for i, d := range due {
		if dryRun {
			fmt.Fprintf(out, "would send step %d to %s\n", d.Step, d.Email)
			continue
		}
		if i > 0 && m.Pause > 0 {
			select {
			case <-ctx.Done():
				return rep, ctx.Err()
			case <-time.After(m.Pause):
			}
		}
		err := m.Sender.Send(ctx, Build(d, topics, m.BaseURL, m.PostalAddress))
		if err != nil {
			rep.Failed++
			reason := "provider error"
			if errors.Is(err, mail.ErrRejected) {
				reason = "rejected"
			}
			fmt.Fprintf(out, "failed step %d to %s (%s): %v\n", d.Step, d.Email, reason, err)
			continue
		}
		if err := RecordSend(ctx, m.DB, d.UserID, d.Step, m.Clock.Now()); err != nil {
			return rep, fmt.Errorf("sent step %d to %s but could not record it; stopping so nobody is mailed twice: %w",
				d.Step, d.Email, err)
		}
		rep.Sent++
		fmt.Fprintf(out, "sent step %d to %s\n", d.Step, d.Email)
	}
	return rep, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: same as Step 2. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/reminders/
git commit -m "feat: send one day's play reminders, recording each as it goes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Configuration

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `Config.AppBaseURL string` (no trailing slash), `Config.MailPostalAddress string`, `Config.ReminderDays []int` (default `[3 7 14]`), `func (c Config) ValidateForReminders() error`.

- [ ] **Step 1: Write the failing tests**

Add `"APP_BASE_URL", "MAIL_POSTAL_ADDRESS", "REMINDER_DAYS"` to `managedEnv`. Then:

```go
func TestLoadReminderSettings(t *testing.T) {
	for _, k := range managedEnv {
		t.Setenv(k, "")
	}
	t.Setenv("DATABASE_URL", "postgres://x/y")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(cfg.ReminderDays) != "[3 7 14]" {
		t.Errorf("default ReminderDays = %v, want [3 7 14]", cfg.ReminderDays)
	}

	t.Setenv("APP_BASE_URL", "https://trivial.example/")
	t.Setenv("REMINDER_DAYS", " 2, 5 ")
	if cfg, err = Load(); err != nil {
		t.Fatal(err)
	}
	if cfg.AppBaseURL != "https://trivial.example" {
		t.Errorf("AppBaseURL = %q, want the trailing slash trimmed", cfg.AppBaseURL)
	}
	if fmt.Sprint(cfg.ReminderDays) != "[2 5]" {
		t.Errorf("ReminderDays = %v, want [2 5]", cfg.ReminderDays)
	}
}

func TestValidateForReminders(t *testing.T) {
	ok := Config{AppBaseURL: "https://trivial.example", MailPostalAddress: "PO Box 1", ReminderDays: []int{3}}
	if err := ok.ValidateForReminders(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	noURL := ok
	noURL.AppBaseURL = ""
	if err := noURL.ValidateForReminders(); err == nil || !strings.Contains(err.Error(), "APP_BASE_URL") {
		t.Errorf("missing base URL: err = %v", err)
	}
	noAddr := ok
	noAddr.MailPostalAddress = ""
	if err := noAddr.ValidateForReminders(); err == nil || !strings.Contains(err.Error(), "MAIL_POSTAL_ADDRESS") {
		t.Errorf("missing postal address: err = %v", err)
	}
}
```

And add rows to `TestLoadErrors`:

```go
		{
			name:    "base url without a scheme",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "APP_BASE_URL": "trivial.example"},
			wantErr: "APP_BASE_URL",
		},
		{
			name:    "reminder days not a number",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "REMINDER_DAYS": "3,soon"},
			wantErr: "REMINDER_DAYS",
		},
		{
			name:    "reminder days zero",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "REMINDER_DAYS": "3,0"},
			wantErr: "REMINDER_DAYS",
		},
```

(add `"fmt"` to the test imports.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/config/ -count=1`. Expected: FAIL — unknown fields.

- [ ] **Step 3: Implement**

Add fields to `Config` after `TrustProxyIP`:

```go
	// AppBaseURL is the site's public origin, with no trailing slash. Reminder
	// links need it because cron has no browser location to borrow one from;
	// the server itself never reads it. Empty is legal until a reminder run.
	AppBaseURL string
	// MailPostalAddress goes in every reminder's footer. CAN-SPAM requires a
	// valid postal address in commercial email; a PO box is fine.
	MailPostalAddress string
	// ReminderDays is the reminder schedule: the first entry is days after the
	// last play, each later one days after the previous reminder. Its length
	// caps reminders per lapse.
	ReminderDays []int
```

In `Load`, before `return cfg, nil`:

```go
	if cfg.AppBaseURL, err = baseURL("APP_BASE_URL"); err != nil {
		return Config{}, err
	}
	cfg.MailPostalAddress = strings.TrimSpace(os.Getenv("MAIL_POSTAL_ADDRESS"))
	if cfg.ReminderDays, err = positiveIntList("REMINDER_DAYS", []int{3, 7, 14}); err != nil {
		return Config{}, err
	}
```

Add helpers and the validator (`net/url` import):

```go
// ValidateForReminders rejects a configuration that cannot send reminders.
// Separate from Load for the same reason as ValidateForServe: only the
// reminder command uses these settings.
func (c Config) ValidateForReminders() error {
	if c.AppBaseURL == "" {
		return fmt.Errorf("APP_BASE_URL is required to send reminders, e.g. https://trivial.example")
	}
	if c.MailPostalAddress == "" {
		return fmt.Errorf("MAIL_POSTAL_ADDRESS is required to send reminders; commercial email must carry a postal address")
	}
	return nil
}

// baseURL reads an absolute http(s) origin and trims any trailing slash, so
// callers can append "/path" without producing "//path".
func baseURL(key string) (string, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%s %q must be an absolute http(s) URL such as https://trivial.example", key, raw)
	}
	return strings.TrimRight(raw, "/"), nil
}

func positiveIntList(key string, fallback []int) ([]int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	var out []int
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("%s %q must be a comma-separated list of positive day counts", key, raw)
		}
		out = append(out, n)
	}
	return out, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/config/ -count=1`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: configure the reminder schedule, base URL and postal address

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: `trivial reminders send` and its docs

**Files:**
- Modify: `internal/cli/cli.go` (usage, dispatch, new `runReminders`)
- Test: `internal/cli/cli_test.go`
- Modify: `.env.example`, `README.md`

**Interfaces:**
- Consumes: `config.Config.ValidateForReminders`, `reminders.Mailer`, `reminders.TopicsOf`, `puzzle.Ensure(ctx, pool, date, puzzle.Settings)`, `mail.Resend`, `mail.Logger`.

- [ ] **Step 1: Write the failing tests**

Add rows to `TestRunArgumentErrors`:

```go
		{"reminders without subcommand", []string{"reminders"}, "usage"},
		{"reminders with an unknown subcommand", []string{"reminders", "blast"}, "usage"},
		{"reminders send with a stray argument", []string{"reminders", "send", "now"}, "usage"},
```

And a config-gate test:

```go
// Reminders must refuse to start without the settings every message needs,
// before touching the database or the provider.
func TestRemindersSendRequiresBaseURLAndPostalAddress(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://unused/unused")
	t.Setenv("APP_BASE_URL", "")
	t.Setenv("MAIL_POSTAL_ADDRESS", "PO Box 1")
	var stdout, stderr bytes.Buffer
	err := cli.Run(context.Background(), []string{"reminders", "send"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "APP_BASE_URL") {
		t.Fatalf("err = %v, want it to name APP_BASE_URL", err)
	}
}
```

Extend `TestRunHelpSucceeds`'s list with `"reminders"`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/cli/ -run 'TestRunArgumentErrors|TestRemindersSend|TestRunHelp' -count=1`
Expected: FAIL — `unknown command "reminders"`.

- [ ] **Step 3: Implement**

Usage gains `  trivial reminders send [--dry-run]` after the `mail test` line. `Run` gains:

```go
	case "reminders":
		return runReminders(ctx, args[1:], stdout)
```

New functions (imports: `log/slog`, `time`, `internal/reminders`):

```go
const remindersUsage = "usage: trivial reminders send [--dry-run]"

// runReminders mails today's play reminders. It is meant for a daily cron
// entry after `puzzles generate`; see README.md ("Play reminders").
func runReminders(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "send" {
		return fmt.Errorf(remindersUsage)
	}
	fs := flag.NewFlagSet("reminders send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "list who would be mailed; send and record nothing")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return fmt.Errorf(remindersUsage)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.ValidateForReminders(); err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// The same safety net the server uses: if the generation cron has not run,
	// make today's board now rather than mail people about a puzzle that is
	// not there.
	today := clock.PuzzleDateAt(nowClock.Now(), cfg.PuzzleTimezone)
	p, err := puzzle.Ensure(ctx, pool, today, puzzle.Settings{
		CooldownDays:       cfg.QuestionCooldownDays,
		AnswerCooldownDays: cfg.AnswerCooldownDays,
		TimeLimitSeconds:   cfg.TimeLimitSeconds,
	})
	if err != nil {
		return fmt.Errorf("today's puzzle: %w", err)
	}

	var sender mail.Sender = mail.Resend{APIKey: cfg.ResendAPIKey, From: cfg.MailFrom}
	if cfg.ResendAPIKey == "" {
		// As with sign-in in development: the messages go to the log.
		fmt.Fprintln(stdout, "no RESEND_API_KEY: reminders will be written to the log, not emailed")
		sender = mail.Logger{Log: slog.Default()}
	}

	m := reminders.Mailer{
		DB:            pool,
		Sender:        sender,
		Clock:         nowClock,
		Timezone:      cfg.PuzzleTimezone,
		Gaps:          cfg.ReminderDays,
		BaseURL:       cfg.AppBaseURL,
		PostalAddress: cfg.MailPostalAddress,
		Pause:         500 * time.Millisecond,
		Out:           stdout,
	}
	rep, err := m.Run(ctx, reminders.TopicsOf(p), *dryRun)
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Fprintf(stdout, "dry run: %d due, nothing sent\n", rep.Due)
		return nil
	}
	fmt.Fprintf(stdout, "%d due, %d sent, %d failed\n", rep.Due, rep.Sent, rep.Failed)
	if rep.Failed > 0 {
		// Non-zero so cron mails the operator; the failed users stay due and
		// tomorrow's run retries them.
		return fmt.Errorf("%d reminder(s) failed to send", rep.Failed)
	}
	return nil
}
```

Note: `mail.Logger.Send` logs "login email (development sender...)". Change its log message in `mail.go` to the neutral `"email (development sender; not actually sent)"` so reminder output is not mislabeled; no test asserts on that string (verify with `grep -rn "login email" internal`).

- [ ] **Step 4: Update `.env.example`** — append after `TRUST_PROXY_IP`:

```
# Play reminders (`trivial reminders send`, run daily from cron). Neither the
# server nor any other command reads these.
#
# The site's public origin. Reminder links are built from it because cron has
# no browser address to borrow one from.
APP_BASE_URL=http://localhost:8080
# Printed in every reminder's footer. US law (CAN-SPAM) requires a valid postal
# address in commercial email; a PO box or registered mailbox is fine.
MAIL_POSTAL_ADDRESS=
# Days before each reminder in a lapse: the first counts from the last play,
# each later one from the previous reminder. Its length caps reminders per lapse.
REMINDER_DAYS=3,7,14
```

- [ ] **Step 5: Update `README.md`** — add `go run ./cmd/trivial reminders send [--dry-run]` to the CLI command list, and a section after "Friend links":

```markdown
## Play reminders

Signed-in players who stop playing get an email nudge: three days after their
last game, then seven days after that, then fourteen, and then nothing until
they play again. Each one lists the day's topics and links to the game, and
every one carries a one-click unsubscribe link (and the `List-Unsubscribe`
headers that put an Unsubscribe button in Gmail and Apple Mail).

Sending is a daily cron job, not part of `serve`, so a restart can never send a
batch twice:

    0 9 * * * cd /path/to/trivial && ./trivial reminders send

It needs `APP_BASE_URL` and `MAIL_POSTAL_ADDRESS` (see `.env.example`); the
schedule is `REMINDER_DAYS`. Run it with `--dry-run` first to see who would be
mailed. Each send is recorded as it happens, so running it twice in a day sends
nothing the second time, and a run that dies partway resumes cleanly. A failed
send is retried by the next day's run, and makes the command exit non-zero.

Unsubscribing happens at `/unsubscribe/<token>`, which needs no sign-in and
offers a resubscribe button.
```

- [ ] **Step 6: Run to verify pass**

Run: `go test ./internal/cli/ -count=1 && go vet ./...`. Expected: PASS, no vet output.

- [ ] **Step 7: Smoke-test against the dev database**

```bash
make seed
APP_BASE_URL=http://localhost:8080 MAIL_POSTAL_ADDRESS="PO Box 1" go run ./cmd/trivial reminders send --dry-run
```

Expected: `dry run: N due, nothing sent` (N may be 0 on a fresh database) and exit 0.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/ internal/mail/mail.go .env.example README.md
git commit -m "feat: add the reminders send command for a daily cron

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Unsubscribe pages

**Files:**
- Create: `internal/httpapi/unsubscribe.go`
- Test: `internal/httpapi/unsubscribe_test.go`
- Modify: `internal/httpapi/server.go` (route registration in `Handler`)

**Interfaces:**
- Consumes: `reminders.Unsubscribe`, `reminders.Resubscribe`; `Server.Pool`, `Server.Clock`, `Server.Logger`; test fixture `newAuthFixture` (`f.pool`, `f.server`, `f.email`, `f.now`; its cleanup deletes `users WHERE email LIKE prefix%`).
- Produces: routes `GET /unsubscribe/{token}`, `POST /unsubscribe/{token}`, `POST /unsubscribe/{token}/resubscribe`.

- [ ] **Step 1: Write the failing tests**

```go
package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// unsubUser commits a user through the fixture's pool, so the fixture's cleanup
// (users WHERE email LIKE prefix%) removes it.
func unsubUser(t *testing.T, f *authFixture) (int64, string) {
	t.Helper()
	var id int64
	var token string
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, created_at) VALUES ($1, $2) RETURNING id, unsubscribe_token`,
		f.email("unsub"), f.now).Scan(&id, &token); err != nil {
		t.Fatal(err)
	}
	return id, token
}

func isUnsubscribed(t *testing.T, f *authFixture, id int64) bool {
	t.Helper()
	var at *time.Time
	if err := f.pool.QueryRow(context.Background(),
		`SELECT reminders_unsubscribed_at FROM users WHERE id = $1`, id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at != nil
}

// form posts the way a browser form or a mail client's one-click button does:
// urlencoded, and with no cookies at all.
func form(f *authFixture, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(res, req)
	return res
}

func TestUnsubscribeGetOnlyAsks(t *testing.T) {
	f := newAuthFixture(t)
	id, token := unsubUser(t, f)
	res := f.do(t, http.MethodGet, "/unsubscribe/"+token, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), `method="post"`) {
		t.Error("confirmation page has no form")
	}
	if isUnsubscribed(t, f, id) {
		t.Fatal("a GET unsubscribed the user; link scanners would unsubscribe everyone")
	}
}

func TestUnsubscribePostAndResubscribe(t *testing.T) {
	f := newAuthFixture(t)
	id, token := unsubUser(t, f)

	res := form(f, "/unsubscribe/"+token, "List-Unsubscribe=One-Click")
	if res.Code != http.StatusOK {
		t.Fatalf("unsubscribe status = %d", res.Code)
	}
	if !isUnsubscribed(t, f, id) {
		t.Fatal("POST did not unsubscribe")
	}
	if !strings.Contains(res.Body.String(), "/unsubscribe/"+token+"/resubscribe") {
		t.Error("unsubscribed page offers no resubscribe")
	}

	res = form(f, "/unsubscribe/"+token+"/resubscribe", "")
	if res.Code != http.StatusOK {
		t.Fatalf("resubscribe status = %d", res.Code)
	}
	if isUnsubscribed(t, f, id) {
		t.Fatal("resubscribe did not clear the flag")
	}
}

func TestUnsubscribeUnknownTokenLooksTheSame(t *testing.T) {
	f := newAuthFixture(t)
	_, token := unsubUser(t, f)
	known := form(f, "/unsubscribe/"+token, "")
	unknown := form(f, "/unsubscribe/nope", "")
	if known.Code != unknown.Code {
		t.Fatalf("status known=%d unknown=%d", known.Code, unknown.Code)
	}
	// Bodies differ only in the token echoed into the resubscribe form.
	if strings.ReplaceAll(known.Body.String(), token, "X") != strings.ReplaceAll(unknown.Body.String(), "nope", "X") {
		t.Error("unknown token renders a different page")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `TEST_DATABASE_URL=... go test ./internal/httpapi/ -run Unsubscribe -count=1`
Expected: FAIL — GET returns the SPA/404 and POST returns 405/404.

- [ ] **Step 3: Implement `unsubscribe.go`**

```go
package httpapi

import (
	"html/template"
	"net/http"

	"github.com/hjordan6/trivial/internal/reminders"
)

// The unsubscribe pages are server-rendered, not part of the Vue app: they must
// work from a mail client's in-app browser with no JavaScript, no cookies and
// no sign-in, because the token in the URL is the whole credential.
//
// GET only asks. Mail security scanners fetch every link in a message, so a GET
// that unsubscribed would unsubscribe players who never clicked. The POST is
// both the form's target and the RFC 8058 one-click target named in each
// reminder's List-Unsubscribe header.
//
// Every response is the same for a token that matches nobody, so the endpoint
// cannot be used to test whether a token is real.

func (s *Server) unsubscribeAsk(w http.ResponseWriter, r *http.Request) {
	s.renderUnsubscribe(w, unsubscribeAskPage, r.PathValue("token"))
}

func (s *Server) unsubscribe(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if err := reminders.Unsubscribe(r.Context(), s.Pool, token, s.Clock.Now()); err != nil {
		s.Logger.Error("unsubscribe", "err", err)
		http.Error(w, "Something went wrong. Please try again.", http.StatusInternalServerError)
		return
	}
	s.renderUnsubscribe(w, unsubscribeDonePage, token)
}

func (s *Server) resubscribe(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if err := reminders.Resubscribe(r.Context(), s.Pool, token); err != nil {
		s.Logger.Error("resubscribe", "err", err)
		http.Error(w, "Something went wrong. Please try again.", http.StatusInternalServerError)
		return
	}
	s.renderUnsubscribe(w, resubscribeDonePage, token)
}

func (s *Server) renderUnsubscribe(w http.ResponseWriter, page *template.Template, token string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The token is a credential; keep it out of caches and referrers.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_ = page.Execute(w, struct{ Token string }{token})
}

const unsubscribeLayout = `{{define "layout"}}<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Trivial reminders</title>
<style>
body{margin:0;background:#f4f1e8;color:#1b1d1b;font:16px/1.6 system-ui,-apple-system,'Segoe UI',Helvetica,Arial,sans-serif}
main{max-width:420px;margin:12vh auto;padding:28px;background:#fffdf7;border:1px solid #cbc7bc;border-radius:6px}
h1{font-size:22px;margin:0 0 10px}
p{color:#55564f;margin:0 0 18px}
button{font:inherit;font-weight:700;padding:10px 18px;background:#d9ff55;border:2px solid #1b1d1b;border-radius:6px;cursor:pointer}
a{color:#55564f}
</style></head><body><main>{{template "body" .}}</main></body></html>{{end}}`

func unsubscribePage(body string) *template.Template {
	return template.Must(template.Must(template.New("layout").Parse(unsubscribeLayout)).Parse(
		`{{define "body"}}` + body + `{{end}}`)).Lookup("layout")
}

var (
	unsubscribeAskPage = unsubscribePage(`<h1>Stop reminder emails?</h1>
<p>We email you when you haven&rsquo;t played in a few days. Unsubscribe and we won&rsquo;t send any more.</p>
<form method="post" action="/unsubscribe/{{.Token}}"><button type="submit">Unsubscribe</button></form>`)

	unsubscribeDonePage = unsubscribePage(`<h1>You&rsquo;re unsubscribed</h1>
<p>You won&rsquo;t get reminders anymore. Clicked by mistake?</p>
<form method="post" action="/unsubscribe/{{.Token}}/resubscribe"><button type="submit">Resubscribe</button></form>
<p style="margin-top:18px"><a href="/">Play today&rsquo;s puzzle</a></p>`)

	resubscribeDonePage = unsubscribePage(`<h1>You&rsquo;re resubscribed</h1>
<p>We&rsquo;ll nudge you again if you&rsquo;re away for a few days.</p>
<p><a href="/">Play today&rsquo;s puzzle</a></p>`)
)
```

In `server.go` `Handler`, after the friends routes:

```go
	mux.HandleFunc("GET /unsubscribe/{token}", s.unsubscribeAsk)
	mux.HandleFunc("POST /unsubscribe/{token}", s.unsubscribe)
	mux.HandleFunc("POST /unsubscribe/{token}/resubscribe", s.resubscribe)
```

If `s.Logger` can be nil on a zero-value `Server` in tests, guard it the way other handlers do (check with `grep -n "s.Logger" internal/httpapi/*.go | head`) and follow that pattern.

- [ ] **Step 4: Run to verify pass**

Run: `TEST_DATABASE_URL=... go test ./internal/httpapi/ -run Unsubscribe -count=1`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat: add the unsubscribe and resubscribe pages for reminders

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Full verification

- [ ] **Step 1:** `gofmt -l .` — expected: no output.
- [ ] **Step 2:** `go vet ./...` — expected: no output.
- [ ] **Step 3:** `make test` — expected: every package `ok`.
- [ ] **Step 4:** Migration round trip on the dev DB: `go run ./cmd/trivial migrate down && go run ./cmd/trivial migrate up` — expected: both succeed (down removes 00009 only).
- [ ] **Step 5:** Manual: `make serve`, open `http://localhost:8080/unsubscribe/<token from psql>`, press Unsubscribe, then Resubscribe; confirm the row in `users` flips each time.
- [ ] **Step 6:** Push and update PR description to cover the implementation.
