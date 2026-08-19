# Remaining Phases — Design

**Date:** 2026-08-19
**Status:** Approved for planning
**Project:** `trivial`
**Covers:** spec phases 4–9 (the run API, play UI, results and sharing, challenge
links, deployment, and accounts)
**Builds on:** `docs/superpowers/specs/2026-08-18-daily-trivia-design.md`, which
remains the authority for the game's rules and for phases 0–3.

## 1. Scope

Phases 0–3 are built and merged: schema, content library, the deterministic
puzzle generator, the answer grader, and an administration CLI. What exists is a
game that can be generated but not played — there is no HTTP server and no user
interface.

This document specifies the rest: how a player takes a run, how the clock is
enforced, how results are shared, how the application is deployed, and how
optional accounts work.

Where this document and the original design disagree, this one wins — it is
written with the foundation in hand rather than in front of it.

### What already exists, and what this design may not change

| Package | Provides | Constraint on this design |
|---------|----------|---------------------------|
| `internal/clock` | `Date`, `PuzzleDateAt`, `Clock`/`Real`/`Fake` | The puzzle date is derived here, in `America/Denver`, never from the browser |
| `internal/config` | Env loading with validation | New settings extend `Config`; they do not introduce a second mechanism |
| `internal/db` | Pool, embedded goose migrations, `DBTX` | New tables arrive as a second migration; the first is never edited |
| `internal/content` | Question library, eligibility | Read-only from the API's perspective |
| `internal/puzzle` | `Puzzle`, `Entry`, `Get`, `Generator` | `Generator.DB` is a `pgx.Tx` by design; the lazy fallback in §9 must respect that |
| `internal/grading` | `Normalize`, `Grade` | Grading is settled. The API calls it; it does not reimplement or tune it |

`grading.Grade` returns a `NearMiss` flag that currently has no consumer. Phase 4
is where it gets logged.

## 2. New data model

One migration, `00002_play.sql`. Phase 9 adds a third; it is described in §10 and
is not part of this migration.

**`players`** — `id` (uuid, PK), `created_at`, `last_seen_at`, `user_id` (bigint,
nullable, FK added in phase 9).

A player is a browser, not a person. The same human on a phone and a laptop is
two players until they sign in.

**`runs`** — `id` (uuid, PK), `player_id`, `puzzle_date`, `started_at`,
`expires_at`, `completed_at` (nullable), `option_seed` (bigint),
`referred_by_run_id` (uuid, nullable, self-FK), `created_at`. Unique on
`(player_id, puzzle_date)`.

`expires_at` is computed once at start as `started_at + time_limit_seconds` from
the day's `daily_puzzles` row. It is never recomputed, so retuning the limit
cannot extend a run already in flight.

`option_seed` is drawn at start and drives multiple-choice ordering, so a reload
does not reshuffle the options under the player.

**`run_answers`** — `run_id`, `question_id`, `stage`
(`free_text`/`multiple_choice`), `free_text_submission` (nullable),
`chosen_option` (nullable text), `outcome` (nullable:
`star`/`circle`/`miss`/`expired`), `first_touched_at`, `resolved_at` (nullable).
Primary key `(run_id, question_id)`.

The row is created the moment a question is first touched — by a free-text
submission or by revealing options — and updated in place. Both attempts are
preserved: a player who guessed "Ceasar" and then picked the right option keeps
that guess for later alias curation.

**`share_tokens`** — `token` (text PK, url-safe), `run_id` (unique),
`nickname` (text, nullable), `view_count` (int), `created_at`.

The nickname lives here rather than on `players` deliberately. It costs nothing
until someone chooses to share, and the same person can be named on one day's
share and anonymous on another.

**Indexes.** `runs (player_id, puzzle_date)` is the unique constraint and serves
resume. `runs (player_id, completed_at)` serves the streak query. `share_tokens
(run_id)` is unique so one run yields one stable URL.

## 3. Identity

A player is identified by an opaque uuid in an `HttpOnly`, `Secure`,
`SameSite=Lax`, one-year cookie. There is no login until phase 9.

**Creation is deliberate, not ambient.** A crawler fetching the home page must
not create a player row. Endpoints fall into two groups:

- **Identity-required** (`POST /api/runs`, everything under a run, and
  `GET /api/challenges/{token}`): a missing cookie creates a player and sets it.
- **Identity-optional** (`GET /api/runs/current`, `GET /api/stats`): a missing
  cookie returns the empty state and sets nothing.
- **Identity-free** (`GET /c/{token}`, `GET /healthz`, static assets): never reads
  or writes identity. See §7 for why the challenge page in particular must not.

So the first write is what mints a player, and the start screen is free to load
for anyone.

`last_seen_at` is updated at most once per day per player, not per request.

## 4. The run lifecycle

### Start

The player lands on a start screen showing the date, the three topic names, the
rules, and the time limit. The topics are safe to reveal and make the run feel
prepared for; nothing else about the puzzle is sent.

**No run exists and no clock runs until the player taps Start.** This is the
central decision of phase 4. The 135-second clock continues while the tab is
closed, and challenge links arrive by text message, so starting on page load
would mean that opening a link and putting the phone down silently costs the
player their day. Gating on an explicit tap also stops a player reading all nine
questions before the clock moves.

`POST /api/runs` is idempotent on `(player, puzzle_date)`. It creates the run,
stamps `started_at` and `expires_at`, draws `option_seed`, and returns the run
state. Called again it returns the existing run untouched, whether in flight or
finished.

### Resume

`GET /api/runs/current` returns today's run state, or an empty state if the
player has not started. The payload carries:

- the puzzle date, `time_limit_seconds`, `expires_at`, and **the server's current
  time**;
- the nine questions — id, topic name, topic position, difficulty, prompt;
- every answer recorded so far, including which stage each question sits at;
- whether the run is complete.

It does **not** carry correct answers or distractors. Options arrive only from
`reveal-options`, and canonical answers only once a question is resolved.

The client computes its clock offset from the server time in this response and
renders the countdown from `expires_at`. It never trusts the device clock.

### Expiry

Any request touching a run whose `expires_at` has passed triggers a sweep, in a
transaction: every unresolved question is written `outcome = 'expired'` and the
run is marked complete. The sweep is idempotent and does not care whether the
browser was open, so a run abandoned mid-answer and reopened a week later lands
on a correct, finished results screen.

### Finish

`POST /api/runs/{id}/finish` completes the run and sweeps any unresolved
questions to `expired`. It is idempotent. A player who has answered all nine
reaches results immediately rather than waiting out the clock; a player who wants
to stop early may also call it, which is the "give up" path.

## 5. Answering

Two stages per question, at most one attempt each.

`POST /api/runs/{id}/questions/{qid}/reveal-options` marks the free-text attempt
forfeited and returns six options — the correct answer plus five distractors —
ordered by a shuffle seeded from `(option_seed, question_id)`. Repeat calls
return the identical ordering. This one endpoint serves both the automatic
transition after a wrong guess and the "show me the choices" button.

`POST /api/runs/{id}/questions/{qid}/answer` grades server-side. Validation runs
in this order, and each failure is distinct in the response:

1. the run belongs to the calling player;
2. the run has not expired (with a two-second grace for network latency);
3. the question belongs to today's puzzle;
4. the question is not already resolved;
5. the submitted stage is legal given the question's current stage.

Free text is graded by `grading.Grade` against the question's stored normalized
aliases. Multiple choice compares the chosen option to the canonical answer. The
response carries the outcome and, once resolved, the canonical answer.

**Outcomes.** `star` — correct on free text. `circle` — correct on multiple
choice. `miss` — attempted and wrong at both available stages. `expired` — never
resolved before time ran out. Requesting options forfeits the star, so the best
remaining outcome becomes `circle`.

**Near misses are logged.** Every rejected free-text answer whose `Result.NearMiss`
is set is written to the application log with the question id, the submitted
text, and the matched alias. This is the feedback loop that turns "I typed the
right answer!" complaints into alias rows, and it is the reason `NearMiss` exists.

## 6. Results, sharing, and stats

### Results

The results screen shows the 3×3 grid, each question's canonical answer, the
player's own submission where they made one, and elapsed time. Correct answers
are only ever sent for a completed run.

### Share text

Built client-side from the run summary:

```
Trivial 2026-08-19
⭐🟢⏰
🟢🔴⏰
⭐⭐🔴
5/9 in 2:15
https://trivial.example/c/a7Kd93
```

Rows are topics in `topic_position` order; columns are easy, medium, hard. The
score is stars plus circles out of nine. Copied to the clipboard, and offered
through the Web Share API where the browser supports it.

`POST /api/runs/{id}/share` creates the token, accepting an optional nickname. It
is idempotent per run: a second call returns the same token, updating the
nickname if a different one is supplied. One run, one stable URL.

### Stats

`GET /api/stats` returns the player's history: days played, the distribution of
scores 0–9, current streak, and longest streak.

A **streak** is consecutive puzzle dates with a completed run, evaluated against
today's date in `America/Denver`. Today counts as unbroken if it has not yet been
played — a streak is only broken by a date that has passed without a completed
run.

Stats land in phase 6 rather than with accounts. Runs are already server-side and
keyed by player, so the endpoint is cheap, and the streak is the mechanic that
brings people back the next day. It is also the thing an account later makes
durable, which is a better argument for accounts than an empty profile page.

## 7. Challenge links

`GET /c/{token}` is served by Go outside `/api`, as real HTML carrying OpenGraph
and Twitter meta so the link unfurls in iMessage, WhatsApp, Slack, and Discord.
The meta description contains the sharer's grid and score; the page then boots
the SPA into a challenge screen.

The friend sees the grid, the score, the nickname if one was given, and the date.
They never see the questions or the answers — the payload for an unstarted
visitor contains neither.

**The SSR route must not mint players or count views.** Every unfurl in iMessage,
Slack, and WhatsApp fetches `/c/{token}` with a crawler, and counting those would
both fill `players` with junk rows and inflate the metric with robots. So
`GET /c/{token}` renders meta tags and the SPA shell without touching identity.

The count happens one layer in: the booted SPA calls `GET /api/challenges/{token}`,
which is identity-required — it mints a player if there is no cookie, and
increments `view_count` at most once per token per player. A human who opens the
link is about to need a player row anyway. Starting a run from a challenge records
`referred_by_run_id` on the new run. Those two
numbers together are the whole measurement of whether the share loop works, which
is worth having from the first day rather than retrofitting.

A challenge link for a date other than today shows the sharer's result and offers
today's puzzle instead. Old links stay meaningful rather than 404ing.

## 8. Frontend

Vue 3, TypeScript, Vite, Pinia, Vue Router. One store owns the run: the answer
map, the timer, and resume.

**Routes.** `/` (start screen, board, or results, depending on run state),
`/c/:token` (challenge landing).

**Components.** `StartScreen`, `TimerBar`, `TopicRow`, `QuestionCard` (free-text
and multiple-choice states), `ResultsGrid`, `ShareSheet`, `StatsPanel`.

**Board.** Three topic rows by three difficulty columns, all nine reachable at
any time. Answered cells show their symbol immediately.

**Timer.** A single component driven by `expires_at` plus the offset measured at
start. At zero it locks input and calls finish. It also recomputes the offset when
the tab regains focus, because a phone that slept for ten minutes will otherwise
render a stale countdown for a frame.

**Resume.** Every load calls `GET /api/runs/current` and renders whatever state
comes back — start screen, mid-run board with answers restored, or results. A run
that expired while the tab was closed lands directly on results with the correct
symbols already filled.

## 9. Deployment

One Go binary serving the API, the SSR challenge page, and the Vue application
from `embed.FS`. One origin: no CORS, first-party cookies, and real meta tags on
the challenge route.

**Target: Fly.io** with Fly Postgres. It fits a single-binary Docker deployment,
has managed Postgres, and is inexpensive at this scale. Nothing in the design
depends on it — Railway, Render, or a VPS with Docker Compose would need only a
different deploy config and secret store.

**Migrations run on boot**, before the server accepts traffic, using the advisory
lock `internal/db` already takes. That makes a multi-instance rollout safe.

**Puzzle generation runs as a scheduled job**, generating 30 days ahead daily so
that content exhaustion surfaces as an alert weeks before it could reach a player.
The original design also called for a lazy fallback that generates today's puzzle
on demand if it is somehow missing; it belongs here, and it must open its own
transaction, because `puzzle.Generator.DB` is typed `pgx.Tx` precisely to prevent
a pool being passed.

**Operational surface.** Structured logs via `slog`; `/healthz` for liveness;
per-IP rate limiting on the answer, reveal, and share endpoints; and counters for
runs started, runs completed, shares created, and challenge referrals.

## 10. Accounts

Optional, last, and additive. Anonymous play is never removed.

**`users`** — `id`, `email` (citext, unique), `created_at`.
**`login_tokens`** — `token_hash`, `email`, `expires_at`, `consumed_at`.

Magic link only: enter an email, receive a link, click it. No passwords to store,
hash, reset, or leak — appropriate for an account whose only job is carrying a
streak between devices. Tokens are single-use, short-lived, and stored hashed.

Email goes out through a `Mailer` interface with a transactional-provider
implementation and a log-only implementation for development, so the whole flow
is exercisable locally without sending mail.

**Signing in attaches, it does not migrate.** `players.user_id` is set on the
current player row. A user accumulates player rows — one per browser — rather
than having rows merged and deleted. Nothing is destroyed by signing in, and
signing in on a second device is the same operation as the first.

Stats then aggregate over every player belonging to the user. Where two of a
user's players both completed the same date — played anonymously on a phone and a
laptop before signing in — the run with the earliest `started_at` wins. It is
deterministic, and it is the run they actually played first.

## 11. Error handling

API errors return a JSON body with a stable machine-readable `code` and a human
message. The codes the client branches on:

| Code | Meaning | Client behaviour |
|------|---------|------------------|
| `run_expired` | The clock ran out | Navigate to results |
| `already_answered` | Question already resolved | Refresh run state |
| `invalid_stage` | Multiple choice before revealing options, or free text after | Refresh run state |
| `no_puzzle` | No puzzle generated for today | Apologise, offer stats |
| `not_your_run` | The run belongs to another player | Discard local state, reload |
| `rate_limited` | Too many requests | Back off, show a message |

`no_puzzle` should be unreachable given 30-day pre-generation plus the lazy
fallback, but it is a real state and the client must not white-screen on it.

## 12. Testing

**Go unit.** Timer arithmetic and the streak calculation against `clock.Fake`,
including the DST-transition days. US daylight saving ends on Sunday 1 November
2026, so a streak spanning 31 October to 2 November crosses a 25-hour local day —
exactly where a naive "subtract 86400 seconds" implementation breaks. Share-text construction for every
symbol combination.

**Go integration**, against real Postgres: idempotent start; resume with answers
restored; option order stable across repeated reveals; double-answer rejected;
stage order enforced; expiry swept correctly when the deadline passes between two
requests; finish idempotent; share token stable per run; one player cannot touch
another's run.

**Frontend.** Vitest over the store — timer maths against a mocked offset, resume
rehydration, share-text output.

**End to end**, Playwright: start, answer across both stages, let the clock
expire, reach results, generate a share link, open it in a second browser context
and confirm the referral is recorded. Plus one reload-mid-run case.

The suite must fail if the server ever sends a correct answer or a distractor
before the client is entitled to it. That is one assertion over the resume payload
and it guards the only cheat that would be trivial to exploit.

## 13. Accepted risks

**Cookie clearing replays the day.** A player who clears cookies gets a fresh run
at a puzzle they have seen. Mitigation is per-IP rate limiting only. Wordle has
the same hole; closing it needs the accounts that are deliberately last, and
policing it costs more than it returns.

**Content exhaustion is the binding constraint on launch.** The 54-question
starter library sustains roughly six days at any cooldown. Nothing in these three
plans changes that. Growing the library is independent work and can proceed in
parallel; the app is not launchable to real players without it.

**Clock skew and lie-ability.** The server owns the clock, so a client with a
wrong clock is merely confused, not advantaged. A client that never calls finish
is swept on its next request.

## 14. Plan split

| Plan | Phases | Delivers |
|------|--------|----------|
| **2. Playable game** | 4–6 | `00002_play.sql`, identity, run lifecycle, answering, the play UI, results, share text, stats |
| **3. Challenge links and ship** | 7–8 | Share tokens, the SSR challenge page, referral attribution, embedded SPA, deployment, scheduled generation, rate limiting |
| **4. Accounts** | 9 | `users`, `login_tokens`, magic-link flow, `Mailer`, attaching players to users, stats aggregation |

Plan 2 is much the largest and is the one that turns the repository into a game.
Plans 3 and 4 are each roughly a third of its size.

## 15. Decisions made without explicit direction

Recorded so they are cheap to overturn.

- **A challenge link for a past date** shows the sharer's result and offers today's
  puzzle, rather than letting the friend play the old one. Letting them play it
  would break the "everyone plays the same puzzle today" premise that makes scores
  comparable.
- **Finish is allowed before the clock expires** even with questions unanswered,
  making it a "give up" button. The alternative — forcing a player to sit out the
  timer — punishes them for nothing.
- **`view_count` counts one view per token per player**, not raw hits, so a link
  pasted into a group chat where twenty people open it reads as twenty.
- **Users accumulate player rows rather than merging them.** Merging means
  destructive writes on sign-in and a much harder rollback if the flow has a bug.
- **Where a user has two runs for one date, the earliest `started_at` wins.**
- **The start screen shows topic names.** It makes the run feel prepared for and
  leaks nothing — the topics are already visible in any shared grid.
