# Play reminders — design

Date: 2026-10-07

## Goal

Bring lapsed players back with an occasional email that nudges them to play
the daily puzzle, without mailing regulars and without hurting the reputation
of a sending domain that is still new.

Success: a player who stops playing gets at most three reminders per lapse,
each one showing that day's topics; anyone can stop them with one click; a
player who plays daily never gets one.

## Decisions

- **Audience:** every signed-in user (a row in `users`), opted in by default,
  with a one-click unsubscribe in every message. Existing users included.
- **Cadence:** a backoff per lapse. First reminder after 3 days without
  playing, second 7 days after that, third and last 14 days after that. Then
  silence until the user plays again; playing resets the cycle.
- **Content:** today's three topics are the hook.

## Architecture

A new CLI command, `trivial reminders send [--dry-run]`, run once a day from
cron in the morning (`PUZZLE_TIMEZONE`). It is not a goroutine inside `serve`,
so a restart or deploy can never cause a double send.

One run:

1. Resolves today's puzzle through the existing on-demand path (the same one
   the server uses when the `puzzles generate` cron has lapsed), so the topics
   are always available.
2. Selects the users who are due (below).
3. For each, builds the message, sends it through the existing `mail.Sender`,
   and records the send. The record is written per user, immediately after a
   successful send, so a run that dies halfway resumes cleanly.

Sending is throttled to 2 messages per second. A `mail.ErrRejected` for one
address is logged and skipped; any other send error is logged and the run
continues with the next user, exiting non-zero at the end if anything failed.
`--dry-run` prints who would get which step and sends and records nothing.

If `RESEND_API_KEY` is unset, the command uses the existing `mail.Logger`
sender, exactly as sign-in does in development.

New code lives in `internal/reminders` (selection, recording, message
building — pure SQL against a `db.DBTX`, clock passed in, like
`internal/accounts`), with the CLI wiring in `internal/cli` and the unsubscribe
handlers in `internal/httpapi`.

## Who is due

For each user:

- `last_played` = the latest `runs.puzzle_date` across every player row whose
  `user_id` is that user. A user who has never played uses the date of
  `users.created_at` (in `PUZZLE_TIMEZONE`).
- `lapse_sends` = rows in `reminder_sends` for the user with `sent_at` after
  `last_played`. Sends before the latest play belong to an earlier lapse and
  do not count.
- `anchor` = the date of the latest of `lapse_sends`, or `last_played` if
  there are none.

The user is due step `n = len(lapse_sends) + 1` today when all hold:

- `users.reminders_unsubscribed_at IS NULL`
- `last_played < today` (they have not played today)
- `n <= len(REMINDER_DAYS)`
- `today - anchor >= REMINDER_DAYS[n-1]`
- no `reminder_sends` row for the user already has `sent_at` today — which,
  with the rule above, makes a second run on the same day a no-op.

`REMINDER_DAYS` defaults to `3,7,14`.

## Schema — `00009_reminders.sql`

```sql
ALTER TABLE users
    ADD COLUMN reminders_unsubscribed_at timestamptz,
    ADD COLUMN unsubscribe_token text;
-- Backfill every existing user with a random token, then make it
-- NOT NULL UNIQUE with a default so new users get one on insert.

CREATE TABLE reminder_sends (
    id       bigserial PRIMARY KEY,
    user_id  bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    step     smallint NOT NULL,
    sent_at  timestamptz NOT NULL
);
CREATE INDEX reminder_sends_user_sent_idx ON reminder_sends (user_id, sent_at);
```

The unsubscribe token is `replace(gen_random_uuid()::text, '-', '')` — 122
random bits, built in, no extension — and is permanent per user. It is deliberately not an HMAC of `APP_SECRET`: rotating
that secret would break every unsubscribe link already delivered, and an
opt-out link must keep working.

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `APP_BASE_URL` | none | Required by `reminders send`; the command refuses to run without it. Absolute origin used for the play button and unsubscribe links. Cron has no `location.origin` to borrow. |
| `REMINDER_DAYS` | `3,7,14` | Comma-separated gaps; its length is the per-lapse cap. |
| `MAIL_POSTAL_ADDRESS` | none | Required by `reminders send`. Printed in the footer; CAN-SPAM requires a valid postal address (a PO box or registered mailbox is fine) in commercial email. |

`RESEND_API_KEY` and `MAIL_FROM` are reused unchanged. All three new variables
go in `.env.example` with comments in the existing style.

## The message

Built in `internal/reminders` following `httpapi/logincodemail.go`: a text
part always, plus an inline-styled, table-based HTML part in the site palette,
rendered with `html/template`.

- **Subject:** `Today's Trivial: <Topic>, <Topic> & <Topic>`
- **Opening line by step:**
  1. "Haven't seen you in a few days."
  2. "It's been a week."
  3. "Last nudge from us — we'll stop after this one."
- Today's three topics with their emoji.
- One button/link: **Play today's puzzle** → `APP_BASE_URL/`.
- Footer: "You're getting this because you signed in to Trivial with this
  address.", the **Unsubscribe** link, and `MAIL_POSTAL_ADDRESS`.
- Headers:
  - `List-Unsubscribe: <APP_BASE_URL/unsubscribe/<token>>`
  - `List-Unsubscribe-Post: List-Unsubscribe=One-Click`

`mail.Message` gains `Headers map[string]string`; `resend.go` passes it through
as the API's `headers` field, and `mail.Logger` ignores it. The sign-in email
is unchanged.

## Unsubscribe

Routes on the existing server:

- `GET /unsubscribe/{token}` — a small server-rendered page with an
  **Unsubscribe** button (a form POST). GET never changes state, because mail
  security scanners prefetch every link in a message.
- `POST /unsubscribe/{token}` — sets `reminders_unsubscribed_at = now()` if
  unset and renders "You won't get reminders anymore." with a **Resubscribe**
  button. This is also the RFC 8058 one-click target: a mail client POSTs
  `List-Unsubscribe=One-Click` here and gets a 200.
- `POST /unsubscribe/{token}/resubscribe` — clears the column and confirms.

An unknown token renders the same neutral pages and the same status as a known
one, so the endpoint does not reveal which tokens exist. No sign-in is
required, and the handlers read no cookies: the token is the credential, and
one-click POSTs from mail providers arrive without any.

## Testing

- **Selection** (test database, rolled-back transactions): due at day 3, 7
  after that, 14 after that, never a fourth; playing resets to step 1; playing
  today suppresses; unsubscribed users excluded; never-played users anchored on
  sign-up; plays on a second device count; a second run on the same day selects
  nobody.
- **Message:** subject from topics, step-specific copy, both parts contain the
  play and unsubscribe URLs and the postal address, headers present.
- **HTTP:** GET does not unsubscribe; POST does; resubscribe clears; one-click
  POST without cookies succeeds; unknown token is indistinguishable.
- **Resend:** headers serialized into the request body.
- **CLI:** `--dry-run` sends and records nothing; missing `APP_BASE_URL` or
  `MAIL_POSTAL_ADDRESS` is a clear error.

## Operations

- Add a daily cron entry on the production box, after the puzzle generation
  entry, e.g. `0 9 * * * cd <deploy dir> && ./trivial reminders send`.
- The first run reaches every existing user who has been away three or more
  days, throttled to 2/s.
- README gains a short "Play reminders" section covering the command, cron
  line, settings and unsubscribe behaviour.

## Out of scope

An in-app reminder preference, a daily opt-in "today's puzzle" email,
stats-based content.
