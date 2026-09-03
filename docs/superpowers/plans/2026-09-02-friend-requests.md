# Friend Requests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A signed-in player can press **Send friend request**, name themselves, and share a reusable link; whoever opens it and accepts becomes their friend, signing in or creating an account along the way.

**Architecture:** Two new tables behind a new pure-SQL `internal/friends` package shaped exactly like `internal/accounts` — every function takes a `db.DBTX`, none reads the environment or calls `time.Now`, so the whole package tests inside a rolled-back transaction. Three HTTP routes reuse the existing session helpers. The frontend gets one store, one component, and one new route; the recipient's sign-in reuses the existing `SignIn` component untouched.

**Tech Stack:** Go 1.26, PostgreSQL 17, pgx v5, goose (embedded migrations), Vue 3 + TypeScript + Pinia + vue-router, Vitest.

**Spec:** `docs/superpowers/specs/2026-09-02-friend-requests-design.md`

## Global Constraints

- Module path is `github.com/hjordan6/trivial`. Every import path below assumes it.
- Run Go tests with `make test` (it starts the test database and sets `TEST_DATABASE_URL`). A bare `go test ./...` fails by design — `testsupport.MustPool` calls `t.Fatal` when `TEST_DATABASE_URL` is unset rather than skipping, because a silently skipped integration suite that reports green is worse than a failing one.
- Run frontend tests with `cd web && npm test`.
- Commit messages follow Conventional Commits (`feat:`, `test:`, `chore:`, `fix:`) and end with the `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>` trailer.
- Migrations are embedded and append-only. Add `00008_friends.sql`; never edit an existing migration.
- **A friendship is between two `users`, never two `players`.** A player is a browser, a user is a person. Anything keyed to a player evaporates when someone plays on a second device.
- **No friend route may ever return an email address.** The invite token is a bearer credential that will end up in group chats; a route that echoes the sender's address turns it into an email oracle. Task 7 asserts this directly.
- Nothing reads `friendships` in this plan. No friend list, no count, no UI that displays a friendship. The rows accumulate for a later design.
- `internal/friends` never imports `net/http` and never reads `os.Getenv`. If a function needs the current time, it takes a `time.Time` parameter.
- Go: `gofmt` clean and `go vet ./...` clean. Check with `make lint`.

## File Structure

```
trivial/
├── internal/
│   ├── db/migrations/
│   │   └── 00008_friends.sql          # NEW: friend_invites + friendships
│   ├── friends/                       # NEW package, pure SQL over db.DBTX
│   │   ├── friends.go                 # types, errors, DefaultNickname
│   │   ├── friends_test.go            # DefaultNickname table test (no DB)
│   │   ├── store.go                   # UpsertInvite, InviteByToken, Accept
│   │   └── store_test.go              # integration tests, rolled-back txns
│   └── httpapi/
│       ├── friends.go                 # NEW: three handlers + requireSignedIn
│       ├── friends_test.go            # NEW: endpoint tests
│       └── server.go                  # MODIFY: route registration, cleanNickname fix
└── web/src/
    ├── types.ts                       # MODIFY: FriendInvite, PublicInvite, AcceptResult
    ├── main.ts                        # MODIFY: /f/:token route
    ├── style.css                      # MODIFY: .friends__*, .invite__* rules
    ├── stores/
    │   ├── friends.ts                 # NEW: sender + recipient state
    │   └── friends.test.ts            # NEW
    ├── components/
    │   ├── SendFriendRequest.vue      # NEW: nickname panel + share button
    │   └── ResultsView.vue            # MODIFY: mount the button
    └── views/
        └── FriendInviteView.vue       # NEW: the /f/:token landing page
```

Boundaries worth naming: `internal/friends` holds every SQL statement and no HTTP concern, so it is exhaustively testable in a transaction. `internal/httpapi/friends.go` holds every HTTP concern and no SQL. The frontend store holds all fetch calls; the two components hold none, which is what keeps them testable by reading the store.

---

### Task 1: Make `cleanNickname` truncate on rune boundaries

`cleanNickname` truncates with `s[:40]`, which slices **bytes**. A nickname of 40+ multi-byte characters gets cut mid-rune, producing invalid UTF-8 that Postgres refuses to store in a `text` column. Today that is a latent 500 on the share route; this feature gives it a second, far more reachable home on a `NOT NULL` column, so it gets fixed first. This is a targeted repair to code the feature depends on, not unrelated refactoring — the share route gets the fix for free.

**Files:**
- Modify: `internal/httpapi/server.go:652-664`
- Test: `internal/httpapi/friends_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: `cleanNickname(v *string) *string` — unchanged signature, now rune-safe. Task 6 relies on it never returning invalid UTF-8.

- [ ] **Step 1: Write the failing test**

Create `internal/httpapi/friends_test.go`:

```go
package httpapi

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanNicknameTruncatesOnRuneBoundaries(t *testing.T) {
	// Each of these is multi-byte, so a byte-wise s[:40] lands mid-rune and
	// yields invalid UTF-8 -- which Postgres rejects outright on a text column.
	long := strings.Repeat("é", 45)
	got := cleanNickname(&long)
	if got == nil {
		t.Fatal("cleanNickname() = nil, want a value")
	}
	if !utf8.ValidString(*got) {
		t.Errorf("cleanNickname() = %q, which is not valid UTF-8", *got)
	}
	if n := utf8.RuneCountInString(*got); n != 40 {
		t.Errorf("rune count = %d, want 40", n)
	}
}

func TestCleanNicknameKeepsShortNamesWhole(t *testing.T) {
	for _, in := range []string{"jordan", "Ana María", "🎯 quizmaster"} {
		v := in
		got := cleanNickname(&v)
		if got == nil || *got != in {
			t.Errorf("cleanNickname(%q) = %v, want %q", in, got, in)
		}
	}
}

func TestCleanNicknameRejectsBlank(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n"} {
		v := in
		if got := cleanNickname(&v); got != nil {
			t.Errorf("cleanNickname(%q) = %q, want nil", in, *got)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `make test 2>&1 | grep -A 5 CleanNickname`
Expected: `TestCleanNicknameTruncatesOnRuneBoundaries` FAILS — the result is invalid UTF-8 and the rune count is under 40.

- [ ] **Step 3: Write the implementation**

Replace `cleanNickname` in `internal/httpapi/server.go`:

```go
// cleanNickname trims, bounds, and normalises a caller-supplied display name,
// returning nil for anything that is empty once trimmed.
//
// The bound is 40 *runes*, counted with a range loop rather than sliced at
// s[:40]. Slicing by bytes cuts a multi-byte character in half and produces
// invalid UTF-8, which Postgres rejects on a text column -- a 500 on what
// should be a naming choice.
func cleanNickname(v *string) *string {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(*v)
	if s == "" {
		return nil
	}
	count := 0
	for i := range s {
		if count == maxNicknameRunes {
			s = s[:i]
			break
		}
		count++
	}
	return &s
}
```

And add the constant beside it:

```go
// maxNicknameRunes bounds every caller-supplied display name: share nicknames
// and friend-invite nicknames both. It matches the length CHECK on
// friend_invites.nickname.
const maxNicknameRunes = 40
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test 2>&1 | tail -20`
Expected: PASS, with no other test regressing.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add internal/httpapi/server.go internal/httpapi/friends_test.go
git commit -m "fix: truncate a nickname on a rune boundary, not a byte one"
```

---

### Task 2: The migration

**Files:**
- Create: `internal/db/migrations/00008_friends.sql`

**Interfaces:**
- Consumes: the `users` table from `00007_accounts.sql`.
- Produces: tables `friend_invites (token, user_id, nickname, created_at)` and `friendships (user_low, user_high, created_at)`. Tasks 3–5 query these exact column names.

- [ ] **Step 1: Write the migration**

Create `internal/db/migrations/00008_friends.sql`:

```sql
-- +goose Up

-- A sender's reusable friend link.
--
-- UNIQUE (user_id) is what makes the link stable: minting is an upsert that
-- returns the token already on the row and only updates the nickname, so
-- pressing the button a second time yields the same URL and a link already
-- sent to somebody never goes dead.
--
-- The token is a bearer credential -- anyone holding it can befriend this user,
-- and it does not expire. It lives in its own column rather than being derived
-- from a signed cookie value precisely so it can be rotated with one UPDATE if
-- a revoke feature is ever wanted; deriving it would tie rotation to APP_SECRET
-- and sign every player out.
--
-- nickname is NOT NULL here, unlike share_tokens.nickname. A share can
-- legitimately be anonymous; an invite cannot, because the nickname is the only
-- thing the landing page has to say who is asking. The length CHECK matches
-- maxNicknameRunes in internal/httpapi.
CREATE TABLE friend_invites (
    token      text PRIMARY KEY,
    user_id    bigint NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    nickname   text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT friend_invites_nickname_length CHECK (length(nickname) BETWEEN 1 AND 40)
);

-- One row per friendship, not two.
--
-- CHECK (user_low < user_high) forces canonical order, and the primary key then
-- buys three properties outright: no duplicates, no direction-dependent
-- duplicates, and no way to record a half-friendship where A is B's friend but
-- not the reverse. Inserting with least()/greatest() and ON CONFLICT DO NOTHING
-- therefore makes accepting idempotent without a uniqueness check in Go.
--
-- The strict < also makes self-friendship unrepresentable. The HTTP layer
-- rejects it a step earlier so the player reads a sentence rather than a 500.
--
-- ON DELETE CASCADE, deliberately unlike players.user_id, which is SET NULL.
-- That column is SET NULL because runs belong to the player and must outlive
-- the account; a friendship has no meaning once either user is gone.
CREATE TABLE friendships (
    user_low   bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_high  bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_low, user_high),
    CONSTRAINT friendships_ordered CHECK (user_low < user_high)
);

-- The primary key orders on user_low and therefore serves only half the
-- lookups. Nothing reads this yet; the first thing that lists a person's
-- friends needs it, and it costs nothing on an empty table.
CREATE INDEX friendships_high_idx ON friendships (user_high);

-- +goose Down
DROP TABLE friendships;
DROP TABLE friend_invites;
```

- [ ] **Step 2: Verify it applies and rolls back**

```bash
make migrate
go run ./cmd/trivial migrate down
make migrate
```

Expected: all three succeed. The `down` must not error on the foreign keys — `friendships` is dropped before `friend_invites`, and neither references the other.

- [ ] **Step 3: Verify the constraints actually bite**

```bash
docker compose exec -T db psql -U trivial -d trivial -c \
  "INSERT INTO friendships (user_low, user_high) VALUES (5, 5);"
```

Expected: `ERROR: new row for relation "friendships" violates check constraint "friendships_ordered"`. (A foreign-key error instead means user 5 does not exist — that is also a pass for this check; the point is that the statement is refused.)

- [ ] **Step 4: Commit**

```bash
git add internal/db/migrations/00008_friends.sql
git commit -m "feat: add friend_invites and friendships tables"
```

---

### Task 3: `friends.DefaultNickname`

A pure function with no database, so it gets its own fast test file.

**Files:**
- Create: `internal/friends/friends.go`
- Test: `internal/friends/friends_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `friends.DefaultNickname(email string) string`; types `friends.Invite{Token string; UserID int64; Nickname string}` and `friends.Outcome string` with constants `friends.OutcomeAdded` / `friends.OutcomeAlreadyFriends`; errors `friends.ErrNoInvite`, `friends.ErrSelfInvite`, `friends.ErrEmptyNickname`. Tasks 4–7 use all of these by these exact names.

- [ ] **Step 1: Write the failing test**

Create `internal/friends/friends_test.go`:

```go
package friends_test

import (
	"testing"

	"github.com/hjordan6/trivial/internal/friends"
)

func TestDefaultNickname(t *testing.T) {
	tests := []struct {
		name  string
		email string
		want  string
	}{
		{"local part", "jordan@example.com", "jordan"},
		{"dots are kept", "ana.maria@example.com", "ana.maria"},
		{"plus tag is kept", "jordan+trivia@example.com", "jordan+trivia"},
		{"no at sign falls back", "nonsense", "A player"},
		{"empty local part falls back", "@example.com", "A player"},
		{"blank falls back", "   ", "A player"},
		{"whitespace-only local part falls back", "  @example.com", "A player"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := friends.DefaultNickname(tt.email); got != tt.want {
				t.Errorf("DefaultNickname(%q) = %q, want %q", tt.email, got, tt.want)
			}
		})
	}
}

// A long local part must come back inside the length CHECK on
// friend_invites.nickname, and still be valid UTF-8.
func TestDefaultNicknameIsBounded(t *testing.T) {
	long := ""
	for i := 0; i < 60; i++ {
		long += "é"
	}
	got := friends.DefaultNickname(long + "@example.com")
	if n := len([]rune(got)); n > 40 {
		t.Errorf("rune count = %d, want at most 40", n)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `make test 2>&1 | grep -E "friends|FAIL" | head`
Expected: FAIL — the package `internal/friends` does not exist yet.

- [ ] **Step 3: Write the implementation**

Create `internal/friends/friends.go`:

```go
// Package friends turns a reusable invite link into a mutual friendship.
//
// A friendship is between two users -- people -- never between two players.
// A player is a browser, so a friendship keyed to one would evaporate the
// moment somebody played on a second device.
//
// Everything here takes a db.DBTX and neither reads the environment nor calls
// time.Now, so every function is testable inside a rolled-back transaction.
// That is the same shape internal/accounts has, and for the same reason.
package friends

import (
	"errors"
	"strings"
)

var (
	ErrNoInvite      = errors.New("no such invite")
	ErrSelfInvite    = errors.New("cannot befriend yourself")
	ErrEmptyNickname = errors.New("nickname is required")
)

// maxNicknameRunes matches the length CHECK on friend_invites.nickname and the
// constant of the same name in internal/httpapi. Counted in runes, never bytes:
// slicing a multi-byte name by bytes yields invalid UTF-8, which Postgres
// refuses on a text column.
const maxNicknameRunes = 40

// fallbackNickname is what an address yields when it has no usable local part.
// The landing page always has something to render, so it never has to fall back
// to showing an email address.
const fallbackNickname = "A player"

// Invite is a sender's reusable link. It deliberately carries no email: the
// token is a bearer credential, and anything reachable with it must not
// disclose the sender's address.
type Invite struct {
	Token    string
	UserID   int64
	Nickname string
}

// Outcome says what accepting an invite actually did.
type Outcome string

const (
	OutcomeAdded          Outcome = "added"
	OutcomeAlreadyFriends Outcome = "already_friends"
)

// DefaultNickname derives a display name from an address: the local part, so
// jordan@example.com becomes "jordan".
//
// It lives here rather than only in the frontend for two reasons: the server
// needs the same fallback when a client sends a blank nickname, and having one
// implementation means one place to test the rule.
func DefaultNickname(email string) string {
	local, _, found := strings.Cut(strings.TrimSpace(email), "@")
	local = strings.TrimSpace(local)
	if !found || local == "" {
		return fallbackNickname
	}
	return truncateRunes(local)
}

// truncateRunes bounds a name at maxNicknameRunes without splitting a
// character. The range loop yields byte offsets at rune boundaries, so slicing
// at one can never produce invalid UTF-8.
func truncateRunes(s string) string {
	count := 0
	for i := range s {
		if count == maxNicknameRunes {
			return s[:i]
		}
		count++
	}
	return s
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test 2>&1 | grep -E "ok.*friends|FAIL"`
Expected: `ok github.com/hjordan6/trivial/internal/friends`

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add internal/friends/
git commit -m "feat: derive a default friend nickname from an address"
```

---

### Task 4: `UpsertInvite` and `InviteByToken`

**Files:**
- Create: `internal/friends/store.go`
- Test: `internal/friends/store_test.go`

**Interfaces:**
- Consumes: `friends.Invite`, `friends.ErrNoInvite`, `friends.ErrEmptyNickname` from Task 3; the tables from Task 2.
- Produces:
  - `friends.UpsertInvite(ctx context.Context, q db.DBTX, userID int64, nickname, token string, now time.Time) (Invite, error)`
  - `friends.InviteByToken(ctx context.Context, q db.DBTX, token string) (Invite, error)`

  `UpsertInvite` takes the token as a parameter rather than drawing one, for the same reason `accounts.RequestCode` returns its code instead of sending it: the package stays pure SQL and a test can pin the value. On a conflict the passed token is discarded and the stored one returned — that is what makes an existing link stable.

- [ ] **Step 1: Write the failing test**

Create `internal/friends/store_test.go`:

```go
package friends_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/friends"
	"github.com/hjordan6/trivial/internal/testsupport"
)

var now = time.Date(2026, 9, 2, 18, 0, 0, 0, time.UTC)

func setup(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()
	return context.Background(), testsupport.Tx(t, testsupport.MustPool(t))
}

// user inserts a bare user row and returns its id. Every test runs inside a
// transaction that is rolled back, so the addresses need not be unique across
// the suite -- only within one test.
func user(t *testing.T, ctx context.Context, tx pgx.Tx, email string) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO users(email, created_at) VALUES($1, $2) RETURNING id`,
		email, now).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUpsertInviteCreatesALink(t *testing.T) {
	ctx, tx := setup(t)
	uid := user(t, ctx, tx, "sender@example.com")

	inv, err := friends.UpsertInvite(ctx, tx, uid, "sender", "tok-one", now)
	if err != nil {
		t.Fatalf("UpsertInvite() error = %v", err)
	}
	if inv.Token != "tok-one" {
		t.Errorf("token = %q, want tok-one", inv.Token)
	}
	if inv.UserID != uid {
		t.Errorf("user id = %d, want %d", inv.UserID, uid)
	}
	if inv.Nickname != "sender" {
		t.Errorf("nickname = %q, want sender", inv.Nickname)
	}
}

// The link is reusable and stable: pressing the button again renames the sender
// but must not mint a second token, or a link already sent to somebody would go
// dead.
func TestUpsertInviteKeepsTheFirstTokenAndTakesTheNewName(t *testing.T) {
	ctx, tx := setup(t)
	uid := user(t, ctx, tx, "sender@example.com")

	first, err := friends.UpsertInvite(ctx, tx, uid, "sender", "tok-one", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := friends.UpsertInvite(ctx, tx, uid, "Sam", "tok-two", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("second UpsertInvite() error = %v", err)
	}
	if second.Token != first.Token {
		t.Errorf("token = %q, want the original %q", second.Token, first.Token)
	}
	if second.Nickname != "Sam" {
		t.Errorf("nickname = %q, want Sam", second.Nickname)
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM friend_invites WHERE user_id=$1`, uid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("invite rows = %d, want 1", count)
	}
}

func TestUpsertInviteRejectsABlankNickname(t *testing.T) {
	ctx, tx := setup(t)
	uid := user(t, ctx, tx, "sender@example.com")

	if _, err := friends.UpsertInvite(ctx, tx, uid, "   ", "tok-one", now); !errors.Is(err, friends.ErrEmptyNickname) {
		t.Errorf("error = %v, want ErrEmptyNickname", err)
	}
}

func TestInviteByTokenReadsItBack(t *testing.T) {
	ctx, tx := setup(t)
	uid := user(t, ctx, tx, "sender@example.com")
	if _, err := friends.UpsertInvite(ctx, tx, uid, "Sam", "tok-one", now); err != nil {
		t.Fatal(err)
	}

	inv, err := friends.InviteByToken(ctx, tx, "tok-one")
	if err != nil {
		t.Fatalf("InviteByToken() error = %v", err)
	}
	if inv.Nickname != "Sam" || inv.UserID != uid {
		t.Errorf("invite = %+v, want Sam and user %d", inv, uid)
	}
}

func TestInviteByTokenRejectsAnUnknownToken(t *testing.T) {
	ctx, tx := setup(t)

	if _, err := friends.InviteByToken(ctx, tx, "nope"); !errors.Is(err, friends.ErrNoInvite) {
		t.Errorf("error = %v, want ErrNoInvite", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `make test 2>&1 | grep -E "friends|FAIL" | head`
Expected: FAIL — `UpsertInvite` and `InviteByToken` are undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/friends/store.go`:

```go
package friends

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/db"
)

// UpsertInvite returns the caller's reusable invite, creating it on first use.
//
// token is supplied rather than drawn here so this package stays pure SQL and a
// test can pin the value -- the same division accounts.RequestCode makes by
// returning its code instead of sending it. On a conflict the supplied token is
// discarded and the stored one returned, which is exactly what makes a link
// already shared with somebody keep working.
//
// The no-op-shaped DO UPDATE is what gets a RETURNING row whether the insert
// landed or conflicted, the same trick accounts.upsertUser uses.
func UpsertInvite(ctx context.Context, q db.DBTX, userID int64, nickname, token string, now time.Time) (Invite, error) {
	nickname = truncateRunes(strings.TrimSpace(nickname))
	if nickname == "" {
		// Caught here rather than left to the NOT NULL and the length CHECK, so
		// a direct caller gets a sentence instead of a constraint violation.
		// The HTTP layer always supplies a fallback, so this is unreachable
		// from a request.
		return Invite{}, ErrEmptyNickname
	}

	var inv Invite
	err := q.QueryRow(ctx,
		`INSERT INTO friend_invites (token, user_id, nickname, created_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id) DO UPDATE SET nickname = EXCLUDED.nickname
		 RETURNING token, user_id, nickname`,
		token, userID, nickname, now).Scan(&inv.Token, &inv.UserID, &inv.Nickname)
	if err != nil {
		return Invite{}, fmt.Errorf("upsert friend invite: %w", err)
	}
	return inv, nil
}

// InviteByToken resolves a link to its sender.
//
// It returns no email, and there is no variant of it that does: the token is a
// bearer credential that will end up forwarded into group chats, and a route
// that echoed the sender's address would turn every such link into a way to
// read it off the server.
func InviteByToken(ctx context.Context, q db.DBTX, token string) (Invite, error) {
	var inv Invite
	err := q.QueryRow(ctx,
		`SELECT token, user_id, nickname FROM friend_invites WHERE token = $1`,
		token).Scan(&inv.Token, &inv.UserID, &inv.Nickname)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invite{}, ErrNoInvite
	}
	if err != nil {
		return Invite{}, fmt.Errorf("look up friend invite: %w", err)
	}
	return inv, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test 2>&1 | grep -E "ok.*friends|FAIL"`
Expected: `ok github.com/hjordan6/trivial/internal/friends`

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add internal/friends/store.go internal/friends/store_test.go
git commit -m "feat: mint a stable, reusable friend invite link"
```

---

### Task 5: `friends.Accept`

**Files:**
- Modify: `internal/friends/store.go`
- Test: `internal/friends/store_test.go`

**Interfaces:**
- Consumes: `Invite`, `Outcome`, `ErrNoInvite`, `ErrSelfInvite` from Task 3; `UpsertInvite` from Task 4.
- Produces: `friends.Accept(ctx context.Context, q db.DBTX, token string, userID int64, now time.Time) (Invite, Outcome, error)`. It returns the `Invite` alongside the outcome so the handler can name the sender without a second query.

- [ ] **Step 1: Write the failing test**

Append to `internal/friends/store_test.go`:

```go
func TestAcceptMakesAMutualFriendship(t *testing.T) {
	ctx, tx := setup(t)
	sender := user(t, ctx, tx, "sender@example.com")
	recipient := user(t, ctx, tx, "recipient@example.com")
	if _, err := friends.UpsertInvite(ctx, tx, sender, "Sam", "tok-one", now); err != nil {
		t.Fatal(err)
	}

	inv, outcome, err := friends.Accept(ctx, tx, "tok-one", recipient, now)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if outcome != friends.OutcomeAdded {
		t.Errorf("outcome = %q, want added", outcome)
	}
	if inv.Nickname != "Sam" {
		t.Errorf("nickname = %q, want Sam", inv.Nickname)
	}

	// Canonical order is the whole point of the CHECK: one row, low id first.
	var low, high int64
	if err := tx.QueryRow(ctx, `SELECT user_low, user_high FROM friendships`).Scan(&low, &high); err != nil {
		t.Fatal(err)
	}
	wantLow, wantHigh := sender, recipient
	if wantLow > wantHigh {
		wantLow, wantHigh = wantHigh, wantLow
	}
	if low != wantLow || high != wantHigh {
		t.Errorf("row = (%d,%d), want (%d,%d)", low, high, wantLow, wantHigh)
	}
}

// The direction the invite runs must not change which row is written, or the
// same pair could be stored twice.
func TestAcceptIsCanonicalInBothDirections(t *testing.T) {
	ctx, tx := setup(t)
	a := user(t, ctx, tx, "a@example.com")
	b := user(t, ctx, tx, "b@example.com")

	// a invites b, b accepts.
	if _, err := friends.UpsertInvite(ctx, tx, a, "A", "tok-a", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := friends.Accept(ctx, tx, "tok-a", b, now); err != nil {
		t.Fatal(err)
	}
	// Now b invites a, and a accepts. Same pair, opposite direction.
	if _, err := friends.UpsertInvite(ctx, tx, b, "B", "tok-b", now); err != nil {
		t.Fatal(err)
	}
	_, outcome, err := friends.Accept(ctx, tx, "tok-b", a, now)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if outcome != friends.OutcomeAlreadyFriends {
		t.Errorf("outcome = %q, want already_friends", outcome)
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM friendships`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("friendship rows = %d, want 1", count)
	}
}

// A double-tap on Accept must be a no-op, not a duplicate or an error.
func TestAcceptTwiceIsIdempotent(t *testing.T) {
	ctx, tx := setup(t)
	sender := user(t, ctx, tx, "sender@example.com")
	recipient := user(t, ctx, tx, "recipient@example.com")
	if _, err := friends.UpsertInvite(ctx, tx, sender, "Sam", "tok-one", now); err != nil {
		t.Fatal(err)
	}

	if _, first, err := friends.Accept(ctx, tx, "tok-one", recipient, now); err != nil || first != friends.OutcomeAdded {
		t.Fatalf("first Accept() = %q, %v; want added, nil", first, err)
	}
	_, second, err := friends.Accept(ctx, tx, "tok-one", recipient, now)
	if err != nil {
		t.Fatalf("second Accept() error = %v", err)
	}
	if second != friends.OutcomeAlreadyFriends {
		t.Errorf("outcome = %q, want already_friends", second)
	}
}

func TestAcceptRejectsYourOwnInvite(t *testing.T) {
	ctx, tx := setup(t)
	sender := user(t, ctx, tx, "sender@example.com")
	if _, err := friends.UpsertInvite(ctx, tx, sender, "Sam", "tok-one", now); err != nil {
		t.Fatal(err)
	}

	if _, _, err := friends.Accept(ctx, tx, "tok-one", sender, now); !errors.Is(err, friends.ErrSelfInvite) {
		t.Errorf("error = %v, want ErrSelfInvite", err)
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM friendships`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("friendship rows = %d, want 0", count)
	}
}

func TestAcceptRejectsAnUnknownToken(t *testing.T) {
	ctx, tx := setup(t)
	recipient := user(t, ctx, tx, "recipient@example.com")

	if _, _, err := friends.Accept(ctx, tx, "nope", recipient, now); !errors.Is(err, friends.ErrNoInvite) {
		t.Errorf("error = %v, want ErrNoInvite", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `make test 2>&1 | grep -E "friends|FAIL" | head`
Expected: FAIL — `Accept` is undefined.

- [ ] **Step 3: Write the implementation**

Append to `internal/friends/store.go`:

```go
// Accept records a mutual friendship between the invite's sender and userID.
//
// One statement, in three parts. The lookup, the insert, and the report of what
// the insert did all have to agree, and splitting them would open a window
// where a concurrent accept makes the reported outcome wrong.
//
// least()/greatest() is what satisfies the CHECK (user_low < user_high), so the
// primary key rejects a duplicate in either direction, and ON CONFLICT DO
// NOTHING turns that rejection into the already_friends outcome rather than an
// error. Together they mean a double-tap costs nothing and needs no prior
// existence check.
//
// This is a single data-modifying CTE read by the outer SELECT, not two of them
// touching one row -- that second shape is the one Postgres documents as
// unpredictable, and accounts.VerifyCode avoids it for the same reason.
func Accept(ctx context.Context, q db.DBTX, token string, userID int64, now time.Time) (Invite, Outcome, error) {
	var inv Invite
	var inserted bool
	err := q.QueryRow(ctx,
		`WITH inv AS (
		     SELECT token, user_id, nickname FROM friend_invites WHERE token = $1
		 ), ins AS (
		     INSERT INTO friendships (user_low, user_high, created_at)
		     SELECT least(inv.user_id, $2), greatest(inv.user_id, $2), $3
		       FROM inv
		      WHERE inv.user_id <> $2
		     ON CONFLICT DO NOTHING
		     RETURNING 1
		 )
		 SELECT inv.token, inv.user_id, inv.nickname, EXISTS (SELECT 1 FROM ins)
		   FROM inv`,
		token, userID, now).Scan(&inv.Token, &inv.UserID, &inv.Nickname, &inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invite{}, "", ErrNoInvite
	}
	if err != nil {
		return Invite{}, "", fmt.Errorf("accept friend invite: %w", err)
	}
	// The WHERE in the CTE already declined to insert this one; reporting it as
	// its own error is what lets the page say "this is your own link" instead of
	// claiming a friendship that was never made.
	if inv.UserID == userID {
		return inv, "", ErrSelfInvite
	}
	if inserted {
		return inv, OutcomeAdded, nil
	}
	return inv, OutcomeAlreadyFriends, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test 2>&1 | grep -E "ok.*friends|FAIL"`
Expected: `ok github.com/hjordan6/trivial/internal/friends`

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add internal/friends/store.go internal/friends/store_test.go
git commit -m "feat: accept a friend invite into a canonical friendship row"
```

---

### Task 6: Mint and read an invite over HTTP

**Files:**
- Create: `internal/httpapi/friends.go`
- Modify: `internal/httpapi/server.go` (route registration, in the `Handler` method after the `/api/auth/` block at lines 86–89)
- Test: `internal/httpapi/friends_test.go` (extend Task 1's file)

**Interfaces:**
- Consumes: `friends.UpsertInvite`, `friends.InviteByToken`, `friends.DefaultNickname`, `friends.ErrNoInvite` from Tasks 3–5; the existing `s.accountsEnabled()`, `s.optionalPlayer`, `s.signedInUser`, `s.write`, `s.fail`, `s.internal`, `decodeOptional`, `randomToken`, `cleanNickname`.
- Produces: `POST /api/friends/invite` → `{token, url, nickname}`; `GET /api/friends/invite/{token}` → `{nickname}`; the helper `(*Server).requireSignedIn(w, r) (*accounts.User, bool)` used by Task 7.

- [ ] **Step 1: Write the failing test**

Append to `internal/httpapi/friends_test.go` (it already imports `strings`, `testing`, `unicode/utf8` from Task 1 — add `encoding/json`, `net/http`, `net/http/httptest`):

```go
// signIn takes a fixture through the real sign-in flow and returns the two
// cookies a browser would then be holding.
//
// Exactly one code is requested. Asking twice would not merely spend rate
// budget: RequestCode supersedes every live code for an address, so a second
// request silently invalidates the code the first one returned.
//
// No player cookie is sent. createSession calls requirePlayer, which creates a
// player and sets the cookie on the response when the request carries none --
// so both cookies come back from this one call.
func signIn(t *testing.T, f *authFixture, local string) []*http.Cookie {
	t.Helper()
	email := f.email(local)
	code := f.requestCode(t, email)

	res := f.do(t, http.MethodPost, "/api/auth/session",
		map[string]string{"email": email, "code": code})
	if res.Code != http.StatusOK {
		t.Fatalf("sign-in status = %d, want 200: %s", res.Code, res.Body.String())
	}
	player := cookieNamed(res, playerCookie)
	if player == nil {
		t.Fatal("no player cookie after sign-in")
	}
	session := cookieNamed(res, sessionCookie)
	if session == nil {
		t.Fatal("no session cookie after sign-in")
	}
	return []*http.Cookie{player, session}
}

func TestMintInviteReturnsAStableLink(t *testing.T) {
	f := newAuthFixture(t)
	cookies := signIn(t, f, "sender")

	res := f.do(t, http.MethodPost, "/api/friends/invite", map[string]string{"nickname": "Sam"}, cookies...)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	var first struct{ Token, URL, Nickname string }
	if err := json.Unmarshal(res.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Token == "" {
		t.Error("token is empty")
	}
	if first.URL != "/f/"+first.Token {
		t.Errorf("url = %q, want /f/%s", first.URL, first.Token)
	}
	if first.Nickname != "Sam" {
		t.Errorf("nickname = %q, want Sam", first.Nickname)
	}

	// Pressing the button again renames but must not re-mint.
	res = f.do(t, http.MethodPost, "/api/friends/invite", map[string]string{"nickname": "Sammy"}, cookies...)
	var second struct{ Token, URL, Nickname string }
	if err := json.Unmarshal(res.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.Token != first.Token {
		t.Errorf("token = %q, want the original %q", second.Token, first.Token)
	}
	if second.Nickname != "Sammy" {
		t.Errorf("nickname = %q, want Sammy", second.Nickname)
	}
}

// A blank nickname falls back to the address's local part rather than failing:
// the button's job is to produce a link.
func TestMintInviteFallsBackToTheAddressLocalPart(t *testing.T) {
	f := newAuthFixture(t)
	cookies := signIn(t, f, "sender")

	res := f.do(t, http.MethodPost, "/api/friends/invite", map[string]string{"nickname": "  "}, cookies...)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	var got struct{ Nickname string }
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// f.email("sender") is "<prefix>sender@example.com", so the local part is
	// the whole thing before the @.
	want := strings.Split(f.email("sender"), "@")[0]
	if got.Nickname != want {
		t.Errorf("nickname = %q, want %q", got.Nickname, want)
	}
}

func TestMintInviteRequiresSignIn(t *testing.T) {
	f := newAuthFixture(t)

	res := f.do(t, http.MethodPost, "/api/friends/invite", map[string]string{"nickname": "Sam"})
	if res.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401: %s", res.Code, res.Body.String())
	}
}

func TestReadInviteIsPublicAndNamesTheSender(t *testing.T) {
	f := newAuthFixture(t)
	cookies := signIn(t, f, "sender")
	res := f.do(t, http.MethodPost, "/api/friends/invite", map[string]string{"nickname": "Sam"}, cookies...)
	var minted struct{ Token string }
	if err := json.Unmarshal(res.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}

	// No cookies at all: a recipient who has never visited must be able to read it.
	res = f.do(t, http.MethodGet, "/api/friends/invite/"+minted.Token, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	var got struct{ Nickname string }
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Nickname != "Sam" {
		t.Errorf("nickname = %q, want Sam", got.Nickname)
	}
}

func TestReadInviteRejectsAnUnknownToken(t *testing.T) {
	f := newAuthFixture(t)

	res := f.do(t, http.MethodGet, "/api/friends/invite/nosuchtoken", nil)
	if res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: %s", res.Code, res.Body.String())
	}
}

// Friends are a public feature, so a server with no sign-in says so plainly
// rather than 404ing the way the admin surface does -- the frontend has to be
// able to tell "no accounts here" from "not signed in" in order to hide the
// button.
func TestFriendRoutesAreUnavailableWithoutAMailer(t *testing.T) {
	handler := (&Server{}).Handler()

	// The accept route joins this list in Task 7, when it exists.
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/friends/invite"},
		{http.MethodGet, "/api/friends/invite/anything"},
	} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`)))
		if res.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s status = %d, want 503", tc.method, tc.path, res.Code)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `make test 2>&1 | grep -E "httpapi|FAIL" | head`
Expected: FAIL — the routes 404 (or fall through to the SPA handler), so the status assertions fail.

- [ ] **Step 3: Write the implementation**

Create `internal/httpapi/friends.go`:

```go
package httpapi

import (
	"errors"
	"net/http"

	"github.com/hjordan6/trivial/internal/accounts"
	"github.com/hjordan6/trivial/internal/friends"
)

// inviteResponse is what the sender gets back. url is relative, exactly like
// share's "/c/<token>": the frontend makes it absolute from location.origin, so
// no BASE_URL setting has to exist or be kept correct across environments.
type inviteResponse struct {
	Token    string `json:"token"`
	URL      string `json:"url"`
	Nickname string `json:"nickname"`
}

// publicInviteResponse is what anyone holding the link gets. A nickname and
// nothing else -- see readInvite.
type publicInviteResponse struct {
	Nickname string `json:"nickname"`
}

type acceptResponse struct {
	Nickname string `json:"nickname"`
	Status   string `json:"status"`
}

// friendsAvailable gates all three routes on the same condition sign-in uses,
// because a friendship needs two accounts and there are none without a mailer.
func (s *Server) friendsAvailable(w http.ResponseWriter) bool {
	if s.accountsEnabled() {
		return true
	}
	s.fail(w, http.StatusServiceUnavailable, "accounts_unavailable", "Friends are not available on this server.")
	return false
}

// requireSignedIn resolves the caller, or answers 401 and reports false.
//
// It uses optionalPlayer rather than requirePlayer: these routes must not mint
// a player row for a crawler that wanders onto an invite link. A recipient who
// signs in on the invite page has already been given one by createSession.
func (s *Server) requireSignedIn(w http.ResponseWriter, r *http.Request) (*accounts.User, bool) {
	playerID := s.optionalPlayer(r)
	if playerID != "" {
		user, err := s.signedInUser(r, playerID)
		if err != nil {
			s.internal(w, err)
			return nil, false
		}
		if user != nil {
			return user, true
		}
	}
	s.fail(w, http.StatusUnauthorized, "not_signed_in", "Sign in first.")
	return nil, false
}

// mintInvite returns the caller's reusable friend link, creating it on first use.
//
// The link is stable by design: a second press renames the sender but returns
// the same token, so a link already sent to somebody never goes dead. That does
// make it a bearer credential with no expiry -- anyone holding it can befriend
// the sender -- which is why the token lives in a column that a rotation
// feature could later overwrite in place.
func (s *Server) mintInvite(w http.ResponseWriter, r *http.Request) {
	if !s.friendsAvailable(w) {
		return
	}
	user, ok := s.requireSignedIn(w, r)
	if !ok {
		return
	}
	var body struct {
		Nickname *string `json:"nickname"`
	}
	if err := decodeOptional(r, &body); err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// A blank name falls back rather than failing. The button's job is to
	// produce a link, and DefaultNickname always yields something printable, so
	// the landing page never has to fall back to showing an address.
	nickname := friends.DefaultNickname(user.Email)
	if cleaned := cleanNickname(body.Nickname); cleaned != nil {
		nickname = *cleaned
	}

	token, err := randomToken()
	if err != nil {
		s.internal(w, err)
		return
	}
	inv, err := friends.UpsertInvite(r.Context(), s.Pool, user.ID, nickname, token, s.now())
	if err != nil {
		s.internal(w, err)
		return
	}
	s.write(w, http.StatusOK, inviteResponse{Token: inv.Token, URL: "/f/" + inv.Token, Nickname: inv.Nickname})
}

// readInvite names the sender for whoever opened the link.
//
// This is the only public friend route, and it exists so the landing page can
// say who is asking before the visitor has signed in. It returns the nickname
// and nothing else -- never the email, the user id, or a count. The token will
// end up forwarded into group chats, and a route that echoed the address would
// turn every such link into a way to read it off the server.
func (s *Server) readInvite(w http.ResponseWriter, r *http.Request) {
	if !s.friendsAvailable(w) {
		return
	}
	inv, err := friends.InviteByToken(r.Context(), s.Pool, r.PathValue("token"))
	if errors.Is(err, friends.ErrNoInvite) {
		s.fail(w, http.StatusNotFound, "no_such_invite", "This link doesn’t work any more.")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.write(w, http.StatusOK, publicInviteResponse{Nickname: inv.Nickname})
}
```

Register the routes in `internal/httpapi/server.go`, immediately after the `DELETE /api/auth/session` line:

```go
	mux.HandleFunc("POST /api/friends/invite", s.mintInvite)
	mux.HandleFunc("GET /api/friends/invite/{token}", s.readInvite)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test 2>&1 | tail -20`
Expected: PASS, whole suite green.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add internal/httpapi/friends.go internal/httpapi/friends_test.go internal/httpapi/server.go
git commit -m "feat: mint and read a friend invite over HTTP"
```

---

### Task 7: Accept an invite over HTTP

**Files:**
- Modify: `internal/httpapi/friends.go`
- Modify: `internal/httpapi/server.go` (one route line)
- Test: `internal/httpapi/friends_test.go`

**Interfaces:**
- Consumes: `friends.Accept`, `friends.ErrNoInvite`, `friends.ErrSelfInvite`, `friends.OutcomeAdded`, `friends.OutcomeAlreadyFriends`; `requireSignedIn` from Task 6.
- Produces: `POST /api/friends/invite/{token}/accept` → `{nickname, status}` where status is `"added"` or `"already_friends"`.

- [ ] **Step 1: Write the failing test**

Append to `internal/httpapi/friends_test.go`:

```go
func TestAcceptBefriendsTwoSignedInBrowsers(t *testing.T) {
	f := newAuthFixture(t)
	sender := signIn(t, f, "sender")
	res := f.do(t, http.MethodPost, "/api/friends/invite", map[string]string{"nickname": "Sam"}, sender...)
	var minted struct{ Token string }
	if err := json.Unmarshal(res.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}

	recipient := signIn(t, f, "recipient")
	res = f.do(t, http.MethodPost, "/api/friends/invite/"+minted.Token+"/accept", nil, recipient...)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	var got struct{ Nickname, Status string }
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "added" {
		t.Errorf("status = %q, want added", got.Status)
	}
	if got.Nickname != "Sam" {
		t.Errorf("nickname = %q, want Sam", got.Nickname)
	}

	// A second tap is a no-op, not a duplicate.
	res = f.do(t, http.MethodPost, "/api/friends/invite/"+minted.Token+"/accept", nil, recipient...)
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "already_friends" {
		t.Errorf("second status = %q, want already_friends", got.Status)
	}
}

func TestAcceptRequiresSignIn(t *testing.T) {
	f := newAuthFixture(t)
	sender := signIn(t, f, "sender")
	res := f.do(t, http.MethodPost, "/api/friends/invite", map[string]string{"nickname": "Sam"}, sender...)
	var minted struct{ Token string }
	if err := json.Unmarshal(res.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}

	res = f.do(t, http.MethodPost, "/api/friends/invite/"+minted.Token+"/accept", nil)
	if res.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401: %s", res.Code, res.Body.String())
	}
}

func TestAcceptRejectsYourOwnInvite(t *testing.T) {
	f := newAuthFixture(t)
	sender := signIn(t, f, "sender")
	res := f.do(t, http.MethodPost, "/api/friends/invite", map[string]string{"nickname": "Sam"}, sender...)
	var minted struct{ Token string }
	if err := json.Unmarshal(res.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}

	res = f.do(t, http.MethodPost, "/api/friends/invite/"+minted.Token+"/accept", nil, sender...)
	if res.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409: %s", res.Code, res.Body.String())
	}
}

func TestAcceptRejectsAnUnknownToken(t *testing.T) {
	f := newAuthFixture(t)
	recipient := signIn(t, f, "recipient")

	res := f.do(t, http.MethodPost, "/api/friends/invite/nosuchtoken/accept", nil, recipient...)
	if res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: %s", res.Code, res.Body.String())
	}
}

// The guard that matters most. The invite token is a bearer credential that
// will end up forwarded into group chats, so no friend route may ever echo an
// address back -- that would make every shared link a way to read the sender's
// email off the server. Asserted directly, because a promise like this erodes
// quietly as handlers are edited.
func TestNoFriendRouteEverReturnsAnEmailAddress(t *testing.T) {
	f := newAuthFixture(t)
	sender := signIn(t, f, "sender")
	recipient := signIn(t, f, "recipient")

	res := f.do(t, http.MethodPost, "/api/friends/invite", map[string]string{"nickname": "Sam"}, sender...)
	bodies := []string{res.Body.String()}
	var minted struct{ Token string }
	if err := json.Unmarshal(res.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}

	bodies = append(bodies,
		f.do(t, http.MethodGet, "/api/friends/invite/"+minted.Token, nil).Body.String(),
		f.do(t, http.MethodPost, "/api/friends/invite/"+minted.Token+"/accept", nil, recipient...).Body.String(),
		f.do(t, http.MethodPost, "/api/friends/invite/"+minted.Token+"/accept", nil, sender...).Body.String(),
		f.do(t, http.MethodGet, "/api/friends/invite/nosuchtoken", nil).Body.String(),
	)
	for i, body := range bodies {
		if strings.Contains(body, "@") {
			t.Errorf("response %d contains an address: %s", i, body)
		}
		if strings.Contains(body, "example.com") {
			t.Errorf("response %d contains a domain: %s", i, body)
		}
	}
}
```

And add the accept route to the table in `TestFriendRoutesAreUnavailableWithoutAMailer`, which Task 6 left covering only the two routes that existed then:

```go
		{http.MethodPost, "/api/friends/invite/anything/accept"},
```

- [ ] **Step 2: Run it to verify it fails**

Run: `make test 2>&1 | grep -E "httpapi|FAIL" | head`
Expected: FAIL — the accept route does not exist.

- [ ] **Step 3: Write the implementation**

Append to `internal/httpapi/friends.go`:

```go
// acceptInvite records the friendship.
//
// Not wrapped in a transaction: friends.Accept is one idempotent statement, so
// there is nothing to make atomic with anything else. Contrast createSession,
// which is deliberately un-transactional for the opposite reason -- rolling
// back there would refund a brute-force budget.
//
// added and already_friends are both 200 and read identically in the UI.
// Distinguishing them for the visitor would tell them something about a
// friendship they may not remember making, for no benefit.
func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	if !s.friendsAvailable(w) {
		return
	}
	user, ok := s.requireSignedIn(w, r)
	if !ok {
		return
	}

	inv, outcome, err := friends.Accept(r.Context(), s.Pool, r.PathValue("token"), user.ID, s.now())
	switch {
	case errors.Is(err, friends.ErrNoInvite):
		s.fail(w, http.StatusNotFound, "no_such_invite", "This link doesn’t work any more.")
		return
	case errors.Is(err, friends.ErrSelfInvite):
		s.fail(w, http.StatusConflict, "self_invite", "That’s your own friend link.")
		return
	case err != nil:
		s.internal(w, err)
		return
	}
	s.write(w, http.StatusOK, acceptResponse{Nickname: inv.Nickname, Status: string(outcome)})
}
```

Register the route in `internal/httpapi/server.go`, beside the other two:

```go
	mux.HandleFunc("POST /api/friends/invite/{token}/accept", s.acceptInvite)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test 2>&1 | tail -20`
Expected: PASS, whole suite green.

Note on cleanup: the fixture's `t.Cleanup` deletes `users`, and both new tables reference `users` with `ON DELETE CASCADE`, so invite and friendship rows are removed with them. No change to `newAuthFixture` is needed.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add internal/httpapi/friends.go internal/httpapi/friends_test.go internal/httpapi/server.go
git commit -m "feat: accept a friend invite over HTTP"
```

---

### Task 8: The frontend store

**Files:**
- Modify: `web/src/types.ts`
- Create: `web/src/stores/friends.ts`
- Test: `web/src/stores/friends.test.ts`

**Interfaces:**
- Consumes: the three endpoints from Tasks 6–7; the existing `api<T>()` from `web/src/api.ts`.
- Produces: `useFriendsStore()` with state `invite: FriendInvite|null`, `nickname: string`, `senderName: string`, `status: InviteStatus`, `loading: boolean`, `error: string`, getter `shareUrl: string`, and actions `defaultNickname(email)`, `mint(nickname)`, `saveNickname(nickname)`, `load(token)`, `accept(token)`. Tasks 9 and 10 call exactly these.

- [ ] **Step 1: Write the failing test**

Add to `web/src/types.ts`:

```ts
export interface FriendInvite { token:string; url:string; nickname:string }
export interface PublicInvite { nickname:string }
export interface AcceptResult { nickname:string; status:'added'|'already_friends' }
```

Create `web/src/stores/friends.test.ts`:

```ts
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useFriendsStore } from './friends'

// Same shape as account.test.ts: stub fetch so these stay store-level tests,
// which is where every other frontend test in this project sits.
function stubFetch(responses: { ok?: boolean; body?: unknown }[]) {
  const calls: { path: string; init?: RequestInit }[] = []
  let i = 0
  vi.stubGlobal('fetch', vi.fn(async (path: string, init?: RequestInit) => {
    calls.push({ path, init })
    const r = responses[Math.min(i++, responses.length - 1)]
    return { ok: r.ok !== false, json: async () => r.body ?? {} } as Response
  }))
  return calls
}

beforeEach(() => { setActivePinia(createPinia()) })

describe('defaultNickname', () => {
  it('takes the local part of the address', () => {
    const store = useFriendsStore()
    expect(store.defaultNickname('jordan@example.com')).toBe('jordan')
  })

  it('falls back when there is no usable local part', () => {
    const store = useFriendsStore()
    expect(store.defaultNickname('')).toBe('A player')
    expect(store.defaultNickname('@example.com')).toBe('A player')
  })
})

describe('mint', () => {
  it('stores the link and exposes an absolute share url', async () => {
    stubFetch([{ body: { token: 'abc', url: '/f/abc', nickname: 'Sam' } }])
    const store = useFriendsStore()
    await store.mint('Sam')
    expect(store.invite?.token).toBe('abc')
    expect(store.shareUrl).toBe(`${location.origin}/f/abc`)
    expect(store.error).toBe('')
  })

  it('surfaces a failure instead of pretending it has a link', async () => {
    stubFetch([{ ok: false, body: { code: 'not_signed_in', message: 'Sign in first.' } }])
    const store = useFriendsStore()
    await store.mint('Sam')
    expect(store.invite).toBeNull()
    expect(store.error).toBe('Sign in first.')
  })
})

describe('load', () => {
  it('names the sender', async () => {
    stubFetch([{ body: { nickname: 'Sam' } }])
    const store = useFriendsStore()
    await store.load('abc')
    expect(store.senderName).toBe('Sam')
    expect(store.status).toBe('ready')
  })

  it('marks a dead link rather than showing an error box', async () => {
    stubFetch([{ ok: false, body: { code: 'no_such_invite', message: 'gone' } }])
    const store = useFriendsStore()
    await store.load('abc')
    expect(store.status).toBe('dead')
  })
})

describe('accept', () => {
  it('reports added and already_friends the same way', async () => {
    stubFetch([{ body: { nickname: 'Sam', status: 'added' } }])
    const store = useFriendsStore()
    await store.accept('abc')
    expect(store.status).toBe('accepted')
    expect(store.senderName).toBe('Sam')
  })

  it('marks your own link', async () => {
    stubFetch([{ ok: false, body: { code: 'self_invite', message: 'own link' } }])
    const store = useFriendsStore()
    await store.accept('abc')
    expect(store.status).toBe('self')
  })

  it('marks a dead link', async () => {
    stubFetch([{ ok: false, body: { code: 'no_such_invite', message: 'gone' } }])
    const store = useFriendsStore()
    await store.accept('abc')
    expect(store.status).toBe('dead')
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npm test`
Expected: FAIL — `./friends` cannot be resolved.

- [ ] **Step 3: Write the implementation**

Create `web/src/stores/friends.ts`:

```ts
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '../api'
import type { AcceptResult, APIError, FriendInvite, PublicInvite } from '../types'

// The landing page's five states. 'loading' is the initial one so the page
// renders nothing decisive before the server has answered.
export type InviteStatus = 'loading' | 'ready' | 'accepted' | 'self' | 'dead'

// Mirrors friends.DefaultNickname in Go. Two implementations of one rule is a
// smell, but the alternative is a round trip before the input can be prefilled,
// and both are covered by tests that assert the same examples.
const FALLBACK_NICKNAME = 'A player'
const MAX_NICKNAME = 40

export const useFriendsStore = defineStore('friends', () => {
  // Sender side.
  const invite = ref<FriendInvite | null>(null)
  const nickname = ref('')
  // Recipient side.
  const senderName = ref('')
  const status = ref<InviteStatus>('loading')

  const loading = ref(false)
  const error = ref('')

  // Relative from the server, absolute for the share sheet -- which is why no
  // BASE_URL setting has to exist server-side.
  const shareUrl = computed(() => (invite.value ? `${location.origin}${invite.value.url}` : ''))

  function fail(e: unknown) {
    error.value = (e as APIError)?.message || 'Something went wrong.'
  }
  function codeOf(e: unknown) {
    return (e as APIError)?.code ?? ''
  }

  function defaultNickname(email: string) {
    const local = (email ?? '').trim().split('@')[0]?.trim() ?? ''
    if (!local) return FALLBACK_NICKNAME
    return [...local].slice(0, MAX_NICKNAME).join('')
  }

  // mint is called when the panel opens, not when Share is pressed. The token
  // is stable and independent of the nickname, so having the URL in hand early
  // is what lets the Share button call navigator.share() synchronously --
  // an awaited fetch first would consume the transient user activation Safari
  // requires, and the share sheet would silently fail to open.
  async function mint(name: string) {
    error.value = ''
    loading.value = true
    try {
      const got = await api<FriendInvite>('/api/friends/invite', {
        method: 'POST',
        body: JSON.stringify({ nickname: name }),
      })
      invite.value = got
      nickname.value = got.nickname
    } catch (e) {
      fail(e)
    } finally {
      loading.value = false
    }
  }

  // saveNickname is deliberately separate from mint and is never awaited by the
  // share handler. The link is already valid; a failed rename means it carries
  // the previous name, which is the right failure for a button whose job was to
  // produce a link.
  async function saveNickname(name: string) {
    if (!invite.value || name.trim() === invite.value.nickname) return
    try {
      const got = await api<FriendInvite>('/api/friends/invite', {
        method: 'POST',
        body: JSON.stringify({ nickname: name }),
      })
      invite.value = got
      nickname.value = got.nickname
    } catch (e) {
      fail(e)
    }
  }

  async function load(token: string) {
    error.value = ''
    status.value = 'loading'
    try {
      const got = await api<PublicInvite>(`/api/friends/invite/${encodeURIComponent(token)}`)
      senderName.value = got.nickname
      status.value = 'ready'
    } catch (e) {
      // Every failure to read an invite is a dead link from the visitor's point
      // of view -- a 404, a 503, a dropped connection -- so they collapse into
      // one state the page renders, rather than an error box over nothing they
      // can act on or retry.
      status.value = 'dead'
      fail(e)
    }
  }

  async function accept(token: string) {
    error.value = ''
    loading.value = true
    try {
      const got = await api<AcceptResult>(`/api/friends/invite/${encodeURIComponent(token)}/accept`, {
        method: 'POST',
      })
      // added and already_friends render identically. Telling the visitor which
      // one it was would report on a friendship they may not remember making.
      senderName.value = got.nickname
      status.value = 'accepted'
    } catch (e) {
      switch (codeOf(e)) {
        case 'self_invite':
          status.value = 'self'
          break
        case 'no_such_invite':
          status.value = 'dead'
          break
        default:
          fail(e)
      }
    } finally {
      loading.value = false
    }
  }

  return {
    invite, nickname, senderName, status, loading, error,
    shareUrl, defaultNickname, mint, saveNickname, load, accept,
  }
})
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web && npm test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/types.ts web/src/stores/friends.ts web/src/stores/friends.test.ts
git commit -m "feat: add a friends store for minting and accepting invites"
```

---

### Task 9: The Send friend request button

**Files:**
- Create: `web/src/components/SendFriendRequest.vue`
- Modify: `web/src/components/ResultsView.vue`
- Modify: `web/src/style.css`

**Interfaces:**
- Consumes: `useFriendsStore` from Task 8; the existing `useAccountStore` and `SignIn.vue`.
- Produces: `<SendFriendRequest />`, mounted by `ResultsView`.

- [ ] **Step 1: Write the component**

Create `web/src/components/SendFriendRequest.vue`:

```vue
<script setup lang="ts">
import { ref, watch } from 'vue'
import { useAccountStore } from '../stores/account'
import { useFriendsStore } from '../stores/friends'
import SignIn from './SignIn.vue'

const account = useAccountStore()
const friends = useFriendsStore()
const open = ref(false)
const name = ref('')
const shared = ref(false)

// Opening the panel mints the link, so the Share button has a URL in hand
// before it is pressed. That is not an optimisation: navigator.share() needs
// transient user activation, and an awaited fetch inside the click handler
// consumes it in Safari, so the share sheet silently never opens.
async function begin() {
  open.value = true
  if (!account.signedIn) return
  name.value = friends.defaultNickname(account.email)
  if (!friends.invite) await friends.mint(name.value)
}

// A player who signs in from inside this panel continues into it, rather than
// being dropped back where they started.
watch(() => account.signedIn, async (isIn) => {
  if (isIn && open.value) {
    name.value = friends.defaultNickname(account.email)
    if (!friends.invite) await friends.mint(name.value)
  }
})

// share() calls navigator.share FIRST and synchronously, then saves the name.
// The token is stable and does not depend on the nickname, so the link is
// correct either way; a save that fails only means it carries the previous name.
function share() {
  const url = friends.shareUrl
  if (!url) return
  void friends.saveNickname(name.value)
  if (navigator.share) {
    void navigator.share({ url, text: `${name.value} wants to be your friend on Trivial` })
  } else {
    void navigator.clipboard.writeText(url)
  }
  shared.value = true
}
</script>

<template>
  <!-- Nothing at all when this server has no sign-in: a friendship needs two
       accounts, and there are none without a mailer. -->
  <section v-if="account.available" class="friends">
    <button v-if="!open" class="primary primary--small" type="button" @click="begin">
      Send friend request
    </button>

    <template v-else>
      <template v-if="!account.signedIn">
        <p class="friends__lede">
          Friends attach to an email rather than to this browser, so they follow
          you to any device. Sign in and your link is ready.
        </p>
        <SignIn />
      </template>

      <template v-else>
        <p class="friends__lede">
          Pick the name your friend will see, then send them the link.
        </p>
        <label class="field">
          <span>Your name</span>
          <input v-model="name" type="text" maxlength="40" placeholder="Sam">
        </label>
        <button
          class="primary primary--small" type="button"
          :disabled="friends.loading || !friends.invite || !name.trim()"
          @click="share">
          {{ shared ? 'Link sent!' : 'Share friend link' }}
        </button>
        <p v-if="friends.invite" class="fine friends__url">{{ friends.shareUrl }}</p>
      </template>
    </template>

    <p v-if="friends.error" class="form-error">{{ friends.error }}</p>
  </section>
</template>
```

- [ ] **Step 2: Mount it on the results screen**

In `web/src/components/ResultsView.vue`, add the import beside the existing ones:

```ts
import SendFriendRequest from './SendFriendRequest.vue'
```

and place the component directly after the existing share button in the template:

```html
    <button class="primary" @click="share">{{copied?'Shared!':'Share result'}}</button>
    <SendFriendRequest />
```

- [ ] **Step 3: Add the styles**

Append to `web/src/style.css`:

```css
.friends { margin: 1.25rem 0; display: flex; flex-direction: column; gap: .6rem; align-items: center; }
.friends__lede { max-width: 34ch; margin: 0; text-align: center; opacity: .85; }
/* The URL is shown so a player can see what they are about to send, and can
   select it by hand where the share sheet is unavailable. It wraps rather than
   overflowing, because a token makes it just long enough to break a phone. */
.friends__url { word-break: break-all; max-width: 34ch; text-align: center; }
```

- [ ] **Step 4: Verify it builds and the suite is green**

```bash
cd web && npm run build && npm test
```

Expected: `vue-tsc` reports no type errors and the build succeeds; Vitest passes.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/SendFriendRequest.vue web/src/components/ResultsView.vue web/src/style.css
git commit -m "feat: offer a friend link from the results screen"
```

---

### Task 10: The invite landing page

**Files:**
- Create: `web/src/views/FriendInviteView.vue`
- Modify: `web/src/main.ts`
- Modify: `web/src/style.css`

**Interfaces:**
- Consumes: `useFriendsStore` from Task 8; `useAccountStore` and `SignIn.vue`.
- Produces: the `/f/:token` route.

- [ ] **Step 1: Write the view**

Create `web/src/views/FriendInviteView.vue`:

```vue
<script setup lang="ts">
import { onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useAccountStore } from '../stores/account'
import { useFriendsStore } from '../stores/friends'
import SignIn from '../components/SignIn.vue'

const route = useRoute()
const account = useAccountStore()
const friends = useFriendsStore()
const token = String(route.params.token ?? '')

// Both requests go out together: neither answer depends on the other, and the
// page cannot render a decisive state until it has both.
onMounted(async () => {
  await Promise.all([friends.load(token), account.probe()])
})

// Case B ends here. accounts.VerifyCode upserts the user, so "they already had
// an account" and "an account is created for them" are the same call -- this
// page just waits for the flag to flip and then offers Accept.
watch(() => account.signedIn, (isIn) => { if (isIn) friends.error = '' })
</script>

<template>
  <main class="invite">
    <template v-if="friends.status === 'loading'">
      <p class="invite__lede">Checking that link…</p>
    </template>

    <template v-else-if="friends.status === 'dead'">
      <p class="eyebrow">Friend request</p>
      <h1>This link doesn’t work any more.</h1>
      <p class="invite__lede">Ask for a new one, or just come and play.</p>
      <RouterLink class="primary" to="/">Play today’s puzzle</RouterLink>
    </template>

    <template v-else-if="friends.status === 'accepted'">
      <p class="eyebrow">Friend request</p>
      <h1>You and <span class="invite__name">{{ friends.senderName }}</span> are now friends.</h1>
      <RouterLink class="primary" to="/">Play today’s puzzle</RouterLink>
    </template>

    <template v-else-if="friends.status === 'self'">
      <p class="eyebrow">Friend request</p>
      <h1>This is your own friend link.</h1>
      <p class="invite__lede">Send it to someone else and they can add you.</p>
      <RouterLink class="primary" to="/">Play today’s puzzle</RouterLink>
    </template>

    <template v-else>
      <p class="eyebrow">Friend request</p>
      <h1><span class="invite__name">{{ friends.senderName }}</span> wants to be your friend.</h1>

      <template v-if="account.signedIn">
        <button class="primary" type="button" :disabled="friends.loading" @click="friends.accept(token)">
          {{ friends.loading ? 'Adding…' : 'Accept' }}
        </button>
      </template>
      <template v-else>
        <p class="invite__lede">
          Add your email to accept. It’s how a friendship follows you to any
          device — no password, and you can play without one.
        </p>
        <SignIn />
      </template>
    </template>

    <p v-if="friends.error" class="form-error">{{ friends.error }}</p>
  </main>
</template>
```

- [ ] **Step 2: Register the route**

In `web/src/main.ts`, add the import and the route **above** the `/:rest(.*)` catch-all — that catch-all currently sends everything unrecognised to `GameView`, so a route added below it would never match:

```ts
import FriendInviteView from './views/FriendInviteView.vue'
```

```ts
const router=createRouter({history:createWebHistory(),routes:[
  {path:'/',component:GameView},
  {path:'/admin',component:AdminView},
  {path:'/audit',component:AuditView},
  {path:'/f/:token',component:FriendInviteView},
  // Anything else is the game, which is what the server's SPA fallback serves.
  {path:'/:rest(.*)',component:GameView},
]})
```

- [ ] **Step 3: Add the styles**

Append to `web/src/style.css`:

```css
.invite { max-width: 34rem; margin: 0 auto; padding: 2rem 1rem; text-align: center; display: flex; flex-direction: column; gap: .9rem; align-items: center; }
.invite__lede { margin: 0; max-width: 40ch; opacity: .85; }
.invite__name { white-space: nowrap; }
```

- [ ] **Step 4: Verify it builds and the suite is green**

```bash
cd web && npm run build && npm test
```

Expected: no type errors, build succeeds, Vitest passes.

- [ ] **Step 5: Commit**

```bash
git add web/src/views/FriendInviteView.vue web/src/main.ts web/src/style.css
git commit -m "feat: accept a friend request from an invite link"
```

---

### Task 11: End-to-end verification and documentation

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: everything above.
- Produces: nothing further; this task proves the feature works in the running application.

- [ ] **Step 1: Run the whole suite**

```bash
make lint && make test && (cd web && npm run build && npm test)
```

Expected: all green. Do not proceed past a failure — fix it and re-run.

- [ ] **Step 2: Drive the real flow in a browser**

```bash
make serve
```

Then, with sign-in unconfigured (`RESEND_API_KEY` unset), the code is written to the server log instead of being emailed — that is where you read it in development.

1. Open `http://localhost:8080`, play and finish a run.
2. Press **Send friend request**. Signed out, the sign-in form appears; sign in with any address and read the code from the `make serve` output.
3. The nickname input should be prefilled with the local part of that address. Change it, press **Share friend link**, and copy the URL shown beneath.
4. Open that URL in a **different browser or a private window** (a different browser means a different player cookie, which is the whole point). It should say `<name> wants to be your friend` and offer sign-in.
5. Sign in there with a second address and press **Accept**. Expect "You and `<name>` are now friends."
6. Press the browser Back button and Accept again: it must still say the same thing, not error.

- [ ] **Step 3: Verify the rows landed correctly**

```bash
docker compose exec -T db psql -U trivial -d trivial -c \
  "SELECT user_low, user_high FROM friendships; SELECT token, user_id, nickname FROM friend_invites;"
```

Expected: exactly one `friendships` row with `user_low < user_high`, and one `friend_invites` row per sender.

- [ ] **Step 4: Document the feature**

Add to `README.md`, after the "CLI commands" section:

```markdown
## Friend links

A signed-in player can press **Send friend request** on the results screen,
choose the name their friend will see, and share the link it produces. Opening
that link and pressing **Accept** makes the two accounts friends — signing in
first, and creating an account, if the recipient has neither.

The link is reusable and does not expire: pressing the button again renames the
sender but returns the same URL, so a link already sent to somebody keeps
working. That also makes it a bearer credential — anyone holding it can become
that player's friend — which is why it lives in a column that a future rotate or
revoke feature can overwrite in place.

Nothing reads the resulting `friendships` rows yet. There is no friend list and
no friend count; the rows accumulate for a later feature.
```

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: describe friend links and what they deliberately omit"
```

---

## Self-Review

**Spec coverage.** Every section of `2026-09-02-friend-requests-design.md` maps to a task: §3 data model → Task 2; §4 `internal/friends` → Tasks 3–5 (`DefaultNickname`, `UpsertInvite`/`InviteByToken`, `Accept`, plus the `cleanNickname` repair called out in §4 → Task 1); §5 HTTP surface → Tasks 6–7, including the 503 gate, the 404, the 409, and the no-email assertion; §6 sender flow → Task 9, with the `navigator.share` activation ordering preserved; §7 recipient flow → Tasks 8 and 10, with all five landing states; §8 testing → the test steps throughout, plus Task 11's end-to-end run; §9 out-of-scope → nothing in any task reads `friendships`.

**Type consistency.** `friends.Invite`, `friends.Outcome`, `OutcomeAdded`, `OutcomeAlreadyFriends`, `ErrNoInvite`, `ErrSelfInvite`, `ErrEmptyNickname` are defined in Task 3 and used under those exact names in Tasks 4–7. `UpsertInvite`'s parameter order `(ctx, q, userID, nickname, token, now)` is identical in Task 4's definition and Task 5's tests and Task 6's caller. The store's action names in Task 8 — `defaultNickname`, `mint`, `saveNickname`, `load`, `accept`, `shareUrl` — are the ones Tasks 9 and 10 call. `maxNicknameRunes` is defined once in each package that needs it (40 in both), matching the SQL `CHECK`.

**Known duplication, accepted.** The default-nickname rule exists twice: `friends.DefaultNickname` in Go and `defaultNickname` in the store. The alternative is a round trip before the input can be prefilled. Both are tested against the same examples, and Task 8's implementation comment says so.
