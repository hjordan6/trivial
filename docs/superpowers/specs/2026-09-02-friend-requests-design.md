# Friend Requests — Design

**Date:** 2026-09-02
**Status:** Approved for planning
**Project:** `trivial`
**Covers:** sending a friend link, accepting one, and the `friendships` rows that result
**Builds on:** `docs/superpowers/specs/2026-08-19-remaining-phases-design.md` §10 (accounts), which remains the authority for how a player becomes a user.

## 1. Scope

A signed-in player can press **Send friend request**, name themselves, and share
a link. Whoever opens that link and accepts becomes their friend — signing in
first if they need to, creating an account if they have never had one.

That is the whole feature. Nothing reads `friendships` yet: there is no friend
list, no friend count, no leaderboard, and no notification. The rows accumulate
so that a later design has something to read. Anything that displays a
friendship is explicitly out of scope here.

### What already exists, and what this design may not change

| Package | Provides | Constraint on this design |
|---------|----------|---------------------------|
| `internal/accounts` | `NormalizeEmail`, `RequestCode`, `VerifyCode`, `Attach`, `ForPlayer` | Sign-in is settled. Accepting an invite calls into it; it does not grow an invite-shaped parameter |
| `internal/httpapi` | `requireViewer`, `signedInUser`, `accountsEnabled`, `randomToken`, `cleanNickname` | Friend routes reuse these. No second notion of "who is calling" |
| `internal/db` | Pool, embedded goose migrations, `DBTX` | New tables arrive as `00008_friends.sql`; earlier migrations are never edited |
| `web/src/stores/account.ts` | The email-and-code state machine | The invite page mounts the existing `SignIn` component rather than reimplementing the flow |

The identity model is the load-bearing constraint. A **player** is a browser; a
**user** is a person; signing in attaches the player row to a user. A friendship
is between two *users*, never two players — otherwise it evaporates the moment
someone plays on a second device.

## 2. Decisions taken, and what they cost

**The sender shares the link themselves.** Pressing the button hands a URL to
the OS share sheet or the clipboard, exactly as `ResultsView.share()` already
does. The application never learns the recipient's address, which is precisely
why the recipient types their own in case B. Rejected: mailing the invite for
them, which would open an outbound-mail path anyone can aim at any address — the
one thing `RequestCode`'s four rate windows exist to bound.

**One reusable link per person.** The link is stable, never expires, and works
any number of times. This was chosen over a single-use token with an expiry, and
the cost is real and should be stated plainly: the link is a **bearer
credential**. Anyone who comes to hold it — forwarded into a group chat, pasted
into a public channel, scraped — can become the sender's friend. Two things in
this design exist to soften that:

- The token lives in its own row, not in a signed cookie value, so it can be
  rotated with one `UPDATE`. A revoke or reroll feature needs no migration.
- No response on any friend route ever contains an email address, so holding the
  link never yields the sender's address (§5).

**A landing page with an Accept tap**, rather than befriending on page load. A
bare `GET` that writes to the database is fired by things that are not people:
iMessage and Slack link previews, mail-scanning proxies, browser prefetch. With
a link that never expires, one preview bot silently minting friendships is a bug
that would surface much later and be very hard to attribute.

**A nickname, prefilled from the email.** The landing page names the sender by a
nickname they choose, defaulted to the local part of their address
(`jordan@example.com` → `jordan`). Never the address itself.

## 3. Data model — `00008_friends.sql`

```sql
CREATE TABLE friend_invites (
    token      text PRIMARY KEY,
    user_id    bigint NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    nickname   text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT friend_invites_nickname_length CHECK (length(nickname) BETWEEN 1 AND 40)
);

CREATE TABLE friendships (
    user_low   bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_high  bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_low, user_high),
    CONSTRAINT friendships_ordered CHECK (user_low < user_high)
);

CREATE INDEX friendships_high_idx ON friendships (user_high);
```

**`UNIQUE (user_id)` is what makes the link reusable and stable.** Minting is an
upsert that returns the token already on the row and updates the nickname in
place — the same `ON CONFLICT … DO UPDATE SET nickname … RETURNING token` shape
`share_tokens` uses in `Server.share`. Pressing the button a second time yields
the same URL, so a link already sent to somebody never goes stale.

**`nickname` is `NOT NULL` here**, unlike `share_tokens.nickname`. A share can
legitimately be anonymous; an invite cannot, because the nickname is the only
thing the landing page has to say who is asking.

**One row per friendship, not two.** `CHECK (user_low < user_high)` forces
canonical order, and the primary key then buys three properties outright: no
duplicates, no direction-dependent duplicates, and no way to record a
half-friendship where A is B's friend but not the reverse. Accepting is

```sql
INSERT INTO friendships (user_low, user_high, created_at)
VALUES (least($1, $2), greatest($1, $2), $3)
ON CONFLICT DO NOTHING
```

which makes a double-tap and an already-friends re-click the same no-op. The
strict `<` also makes self-friendship unrepresentable, though §5 rejects it a
layer earlier so the player sees a sentence rather than a 500.

**`friendships_high_idx`** exists because the primary key orders on `user_low`
and therefore serves only half the lookups. Nothing in this design reads it; the
first thing that lists a person's friends will need it, and adding it now costs
nothing on an empty table.

**`ON DELETE CASCADE` throughout**, deliberately unlike `players.user_id`, which
is `SET NULL`. That column is `SET NULL` because runs belong to the player and
must outlive the account. An invite or a friendship has no meaning once either
user is gone, so cascading is the correct reading rather than a convenience.

## 4. `internal/friends`

A new package shaped like `internal/accounts`: every function takes a
`db.DBTX`, none reads the environment, none calls `time.Now`. That is what lets
the whole package be tested inside a transaction that is rolled back.

```go
// Invite is a sender's reusable link.
type Invite struct {
    Token    string
    UserID   int64
    Nickname string
}

// Outcome says what accepting an invite actually did.
type Outcome string

const (
    OutcomeAdded         Outcome = "added"
    OutcomeAlreadyFriends Outcome = "already_friends"
)

var (
    ErrNoInvite      = errors.New("no such invite")
    ErrSelfInvite    = errors.New("cannot befriend yourself")
    ErrEmptyNickname = errors.New("nickname is required")
)

func UpsertInvite(ctx context.Context, q db.DBTX, userID int64, nickname, token string, now time.Time) (Invite, error)
func InviteByToken(ctx context.Context, q db.DBTX, token string) (Invite, error)
func Accept(ctx context.Context, q db.DBTX, token string, userID int64, now time.Time) (Invite, Outcome, error)
func DefaultNickname(email string) string
```

`UpsertInvite` takes the token as a parameter rather than drawing one, for the
same reason `accounts.RequestCode` returns its code instead of sending it: the
package stays pure SQL and the test can pin the value. The caller passes
`randomToken()`. On a conflict the passed token is discarded and the stored one
returned, which is the behaviour that makes an existing link stable.

`Accept` returns the `Invite` alongside the outcome so the handler can name the
sender in its response without a second query. It returns `ErrSelfInvite` when
the invite belongs to the accepting user.

`DefaultNickname` is the `jordan@example.com` → `jordan` rule. It lives in Go
rather than only in the frontend so the server can fall back to it when a client
sends a blank nickname, and so the rule is unit-tested in one place. Where the
local part is empty or unusable it returns `"A player"`.

`UpsertInvite` returns `ErrEmptyNickname` rather than letting the `NOT NULL` and
the length `CHECK` reject it. The handler always supplies a fallback so the
error should be unreachable from HTTP; it exists so a future direct caller gets
a sentence instead of a constraint violation.

**One sharp edge to handle, not inherit.** `cleanNickname` truncates with
`s[:40]`, which slices *bytes*. On a multi-byte nickname that splits a rune and
produces invalid UTF-8, which Postgres refuses to store in a `text` column —
today that surfaces as a 500 on the share route, and this design would give it a
second, more reachable home on a `NOT NULL` column. The friends work fixes
`cleanNickname` to truncate on rune boundaries, with a test covering an emoji or
accented nickname at the boundary. This is a targeted repair to code this
feature depends on, not unrelated refactoring: the share route gets the fix for
free.

## 5. HTTP surface

Three routes, registered in `Server.Handler` beside the auth block.

| Route | Auth | Answers |
|-------|------|---------|
| `POST /api/friends/invite` | signed-in | `{token, url, nickname}` |
| `GET /api/friends/invite/{token}` | public | `{nickname}` |
| `POST /api/friends/invite/{token}/accept` | signed-in | `{nickname, status}` |

All three answer `503 accounts_unavailable` when `accountsEnabled()` is false,
matching the auth routes rather than the admin surface's 404 — friends are a
public feature the frontend must be able to ask about in order to hide the
button, not an operator tool that benefits from looking absent.

**`POST /api/friends/invite`** resolves the caller with `signedInUser` and fails
`401 not_signed_in` for anyone else. The nickname is cleaned by the existing
`cleanNickname` — trimmed, truncated to 40 — and falls back to
`friends.DefaultNickname(user.Email)` when that yields nothing. It returns
`url` as `"/f/" + token`, mirroring `share`'s `"/c/" + token`; the frontend makes
it absolute from `location.origin`, so no new `BASE_URL` setting is needed.

**`GET /api/friends/invite/{token}`** is the only public route, and it exists so
the landing page can say who is asking before the visitor has signed in. It
returns the nickname and nothing else. It must never return the email, the user
id, or a count — this is a bearer token that will end up in group chats, and the
repo has been careful not to let endpoints become email oracles. An unknown
token is `404 no_such_invite`, phrased for the frontend as "this link doesn't
work any more".

**`POST /api/friends/invite/{token}/accept`** requires a signed-in caller,
`404`s an unknown token, and answers `409 self_invite` for the sender's own
link. Otherwise it inserts and reports `added` or `already_friends`. It is not
wrapped in a transaction: the insert is a single idempotent statement, so there
is nothing to make atomic with anything else.

## 6. Sender flow

A **Send friend request** button on the results screen, in `ResultsView`, below
the existing share button. Hidden entirely when `account.available` is false.

**Signed out**, pressing it reveals the existing `SignIn` component with a line
explaining that friends attach to an email rather than a browser. When sign-in
completes the panel continues into the signed-in state below, so the player
lands where they were headed rather than back at the start.

**Signed in**, pressing it opens a panel holding a nickname input prefilled with
the local part of their address, and a **Share friend link** button.

One browser constraint shapes the order of operations. `navigator.share()`
requires transient user activation, and an `await`ed `fetch` before the call can
consume that activation in Safari, so the share sheet silently fails to open.
Because the token is stable and does not depend on the nickname, this is
sidestepped rather than worked around:

1. Opening the panel mints the invite (`POST /api/friends/invite` with the
   prefilled nickname), so the URL is in hand before any button is pressed.
2. Pressing **Share friend link** calls `navigator.share({url})` **first and
   synchronously**, preserving the activation, and saves the edited nickname
   alongside it rather than before it.
3. Where `navigator.share` is absent, the URL goes to the clipboard and the
   button reports "Copied!", the same fallback `ResultsView.share()` already uses.

A nickname save that fails surfaces an inline error. The link is still valid —
it simply carries the previous name — which is the right failure for a button
whose job was to produce a link.

## 7. Recipient flow

A new route `/f/:token` in `web/src/main.ts`, rendering `FriendInviteView.vue`,
backed by a new `web/src/stores/friends.ts`. The route is added **above** the
`/:rest(.*)` catch-all, which currently sends everything unrecognised to
`GameView`; the server's SPA fallback already serves `index.html` for the path.

On mount the view fetches the invite and probes the session in parallel, then
shows one of five states:

| State | What the visitor sees |
|-------|----------------------|
| Signed in, not the sender | "**Sam** wants to be your friend." + **Accept** |
| Signed out | The same lede, then the inline `SignIn` component |
| Their own link | "This is your own friend link." No Accept button |
| Unknown token | "This link doesn't work any more." |
| Accepted | "You and **Sam** are now friends." + a link into today's puzzle |

**Case B needs no new sign-in code.** `accounts.VerifyCode` already upserts the
user, so "they already had an account" and "an account is created for them" are
the same call and differ only in whether the `INSERT` conflicts. The invite page
mounts `SignIn`, waits for `account.signedIn` to flip, and then shows Accept.

The `409 already_signed_in` guard in `createSession` needs no change. A visitor
whose browser already belongs to somebody else is simply offered Accept as that
account, which is correct; the guard only fires if they try to sign in as a
*different* address, and its existing message already tells them to sign out
first.

`already_friends` and `added` render identically. Distinguishing them would tell
the visitor something about a friendship they may not remember making, for no
benefit.

## 8. Testing

Test-driven, per the repo's habit, and with the same split the existing suites use.

**`internal/friends`** — unit tests over rolled-back transactions:

- A second `UpsertInvite` for the same user returns the *first* token and the
  *new* nickname.
- `Accept` writes canonical order regardless of which direction the invite runs.
- `Accept` twice yields `added` then `already_friends`, and one row.
- `Accept` on one's own invite returns `ErrSelfInvite`.
- `InviteByToken` on an unknown token returns `ErrNoInvite`.
- `DefaultNickname` table test, including an address with no usable local part.

**`internal/httpapi/friends_test.go`** — the endpoints:

- All three answer 503 when `accountsEnabled()` is false.
- Mint and accept round-trips between two signed-in browsers.
- **No response body from any friend route contains an `@`.** Asserted directly,
  because §5's promise is the kind that erodes quietly.
- Unknown token is 404 on both the read and the accept.
- Accepting one's own invite is 409.
- Minting while signed out is 401.

**Vitest** — `web/src/stores/friends.test.ts` for the store's state machine and
the email→nickname prefill, alongside the existing `account.test.ts`.

## 9. Out of scope

Named so a later reader does not mistake absence for oversight: no friend list
or count, no display of friendships anywhere in the UI, no unfriending, no
invite rotation or revocation UI (§3 leaves room for it), no expiry, no
notification to the sender that somebody accepted, and no change to stats,
streaks, or the results grid.
