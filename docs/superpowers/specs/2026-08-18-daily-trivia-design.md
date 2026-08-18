# Daily Trivia — Design

**Date:** 2026-08-18
**Status:** Approved for planning
**Project:** `trivial`

## 1. Overview

A daily trivia game in the Wordle mold. Every day, all players face the same
nine questions: three topics, and within each topic an easy, a medium, and a
hard question. Players have 135 seconds total to answer as many as they can,
moving freely between questions. Each question is first offered as free text;
a wrong answer (or an explicit request) converts it to a six-option multiple
choice. When the run ends, the player gets a shareable result grid and a
challenge link that invites a friend to attempt the same puzzle.

Play is anonymous, identified by a cookie. Accounts come last.

### Goals

- A run takes under three minutes and creates time pressure.
- Everyone plays an identical puzzle, so scores are directly comparable.
- Sharing is one tap and produces text that pastes cleanly into any messenger.
- The question library supports imported, hand-written, and AI-generated
  content without schema changes.
- A question is never re-served within six months of its last use.

### Non-goals

- Accounts, login, and cross-device sync (deferred to the final phase).
- An admin authoring UI. Content arrives by import or SQL for now.
- Rendered image share cards. Text plus a link only.
- Anti-cheat beyond rate limiting. See §10.

## 2. Core mechanics

### The daily puzzle

Three topics are chosen per day from a larger pool. Each topic contributes one
easy, one medium, and one hard question, for nine total. The set is identical
for every player and fixed for the calendar day.

### The clock

A run has a single 135-second budget covering all nine questions. The clock
starts when the player begins and **keeps running whether or not the page is
open**. It is therefore authoritative on the server: the run stores
`started_at` and `expires_at`, and the browser only renders a countdown derived
from them.

`time_limit_seconds` is stored per day on the puzzle record so the value can be
retuned from live data without a deploy. 135 seconds across nine questions is
aggressive by design; expect the timeout symbol to be common early on.

### Answering

Each question runs through at most two attempts:

1. **Free text.** The player types an answer. Correct scores a star.
2. **Multiple choice.** Triggered by a wrong free-text answer, or by the
   player pressing "show me the choices" to skip step 1. Six options — the
   correct answer plus five distractors. Correct scores a green circle.

A wrong multiple-choice pick is final for that question. Skipping directly to
multiple choice forfeits the star: the best available outcome becomes a green
circle. Answers are graded on the server; correct answers and distractors are
never sent to the browser ahead of the moment they are needed.

### Outcomes

| Symbol | Outcome | Meaning |
|--------|---------|---------|
| ⭐ | `star` | Correct on the free-text attempt |
| 🟢 | `circle` | Correct on multiple choice |
| 🔴 | `miss` | Attempted, both paths exhausted, wrong |
| ⏰ | `expired` | Never answered before time ran out |

### The day boundary

The puzzle date is computed in **America/Denver**, configurable via
`PUZZLE_TIMEZONE`. The zone observes daylight saving, so rollover is local
midnight rather than a fixed UTC offset; all date arithmetic goes through a
loaded `time.Location`, never a hardcoded offset.

A single global date is required for challenge links to compare like with like.

## 3. Architecture

A single Go binary serves both the JSON API and the built Vue application from
an embedded filesystem, backed by PostgreSQL.

One origin was chosen deliberately. It removes CORS, makes the anonymous player
cookie first-party, and lets challenge links be served as real HTML with
OpenGraph tags so they unfurl in messaging apps.

```
┌─────────────────────────────────────┐
│ Go binary                           │
│  ├── /api/*     JSON API            │
│  ├── /c/{token} SSR challenge page  │─── OG meta tags
│  └── /*         embedded Vue SPA    │
└──────────────┬──────────────────────┘
               │
        ┌──────┴──────┐
        │ PostgreSQL  │
        └─────────────┘
```

**Backend:** Go, `chi` or stdlib routing, `pgx` for Postgres, `goose` for
migrations, `slog` for structured logging.
**Frontend:** Vue 3, TypeScript, Vite, Pinia, Vue Router.

## 4. Data model

### Content

Shaped so bulk import is a straightforward upsert.

**`topics`** — `id`, `slug` (unique), `name`, `active`, `created_at`

**`questions`** — `id`, `topic_id`, `difficulty` (`easy`/`medium`/`hard`),
`prompt`, `canonical_answer`, `status` (`draft`/`active`/`retired`), `source`,
`external_id`, `created_at`, `updated_at`

A partial unique index on `(source, external_id)` where `external_id` is not
null makes re-running an importer idempotent.

**`question_aliases`** — `id`, `question_id`, `alias`, `normalized`. Accepted
free-text variants, one row each. `normalized` is written by the application
using the same function that normalizes player input, and is uniquely indexed
per question.

**`question_distractors`** — `id`, `question_id`, `text`. Wrong options for the
multiple-choice stage.

**Eligibility.** A question may only appear in a puzzle when it is `active` and
has a canonical answer, at least one alias, and at least five distractors.
Eligibility is enforced in the selection query and asserted by a test, so
half-formed imported content can sit in `draft` indefinitely without risk of
reaching a player.

### Puzzles

**`daily_puzzles`** — `puzzle_date` (PK), `time_limit_seconds` (default 135),
`created_at`

**`daily_puzzle_questions`** — `puzzle_date`, `topic_id`, `topic_position`
(0–2), `difficulty`, `question_id`. Primary key
`(puzzle_date, topic_id, difficulty)`.

`topic_position` fixes row order so the shared grid reads consistently for
everyone.

### Play

**`players`** — `id` (uuid), `created_at`, `last_seen_at`, `user_id` (nullable,
unused until the accounts phase)

**`runs`** — `id` (uuid), `player_id`, `puzzle_date`, `started_at`,
`expires_at`, `completed_at` (nullable), `option_seed` (bigint),
`referred_by_run_id` (nullable). Unique on `(player_id, puzzle_date)`.

`option_seed` drives a deterministic shuffle of multiple-choice options, so
reloading mid-run does not reorder the options underneath the player.

**`run_answers`** — `run_id`, `question_id`, `stage` (`free_text` /
`multiple_choice`), `free_text_submission` (nullable), `chosen_option`
(nullable text), `outcome` (nullable), `first_touched_at`, `resolved_at`
(nullable). Primary key `(run_id, question_id)`.

The row is created the moment a question is first touched — by a free-text
submission or by revealing options — and updated in place thereafter. `stage`
records where the question currently sits, `outcome` stays null until the
question is resolved, and both attempts are preserved: a player who guessed
"Ceasar" and then picked the right option keeps that guess in
`free_text_submission` for later alias curation.

The expiry sweep writes rows for untouched questions with `stage` null and
`outcome` set to `expired`; a question touched but never resolved is swept the
same way, keeping whatever attempt data it already had.

**`share_tokens`** — `token` (PK, short url-safe string), `run_id`,
`view_count`, `created_at`

`referred_by_run_id` plus `view_count` are the minimum needed to answer "is the
share loop actually working".

### Cooldown

Reuse is computed directly from `daily_puzzle_questions`, indexed on
`(question_id, puzzle_date)`. No separate usage table; the puzzle history is
already the usage history.

## 5. API

All endpoints are under `/api`. The player cookie (`HttpOnly`, `Secure`,
`SameSite=Lax`, long expiry) is created on first contact.

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/runs` | Start or resume today's run. Idempotent on (player, date). |
| `GET` | `/runs/current` | Resume state for today. |
| `POST` | `/runs/{id}/questions/{qid}/reveal-options` | Forfeit free text; return 6 options. |
| `POST` | `/runs/{id}/questions/{qid}/answer` | Submit and grade an answer. |
| `POST` | `/runs/{id}/finish` | Complete the run; return the summary. |
| `POST` | `/runs/{id}/share` | Create (or return) a share token. |
| `GET` | `/challenges/{token}` | Challenge summary for the SPA. Increments `view_count` once per token per player. |
| `GET` | `/stats` | The player's streak and history. |
| `GET` | `/healthz` | Liveness. |

Plus `GET /c/{token}`, served outside `/api` as HTML with OpenGraph tags.

### Start / resume payload

Returns the puzzle date, `time_limit_seconds`, `expires_at`, the **server's
current time**, the nine questions (id, topic, topic_position, difficulty,
prompt), and every answer recorded so far.

It does **not** include correct answers or distractors. The client computes a
clock offset from the server time and never trusts the device clock.

### Revealing options

Marks the free-text attempt forfeited and returns the correct answer mixed with
five distractors, ordered by a shuffle seeded from `(option_seed, question_id)`.
Repeat calls return the identical ordering.

### Answering

Server-side validation, in order: the run belongs to the caller; the run has
not expired (with a small network grace, ~2s); the question belongs to today's
puzzle; the question is unanswered; the submitted mode is legal for the
question's current state. The response carries the outcome and the canonical
answer.

### Expiry

Any request touching a run whose `expires_at` has passed triggers a sweep: the
run is marked complete and every unanswered question is recorded as `expired`.
This makes expiry idempotent and independent of whether the browser was open.

## 6. Grading

Answer matching is a pure function with no database access, so it can be
exhaustively table-tested.

Normalization: lowercase; strip diacritics; strip punctuation; collapse
whitespace; drop leading articles (`the`, `a`, `an`). The input is compared
against the question's normalized aliases, first exactly, then with a
Levenshtein tolerance that scales with length (distance ≤1 up to 8 characters,
≤2 beyond).

Every near-miss — inside a slightly wider distance than the accept threshold —
is logged with the question id and the submitted text. That log is the raw
material for curating aliases, and the fastest route to fixing the single
complaint this design will reliably generate: "I typed the right answer."

## 7. Puzzle generation

`GenerateFor(date)` is deterministic and idempotent. It seeds an RNG from the
date, so regenerating a day produces the same puzzle.

1. Shuffle the active topics with the seeded RNG.
2. Walk the shuffled list, and for each candidate topic try to select one
   eligible question at each of the three difficulties, excluding any question
   used within the **question cooldown** (default 180 days). A topic that can
   fill all three difficulties is accepted; a topic that cannot is skipped.
3. Stop at three accepted topics and insert the puzzle and its nine rows in one
   transaction.

There is no topic cooldown — topics are chosen at random each day and may
repeat freely. This makes the question cooldown a **hard constraint**: it is
never relaxed. Since topic choice is free, an exhausted pool is absorbed by
skipping to a different topic rather than by re-serving a recent question.

Generation fails, loudly and without writing a partial puzzle, only when fewer
than three topics in the entire library can field an uncooled question at all
three difficulties. Because days are generated well ahead of time (§ CLI), that
failure surfaces as a cron alert weeks before any player could be affected.
Each skipped topic is logged with the difficulty that starved it, which is the
signal that the library needs content in a specific place.

### What the cooldown demands of the library

The arithmetic is direct: nine distinct questions a day with no repeat inside
180 days means **1,620 eligible questions** — 540 at each difficulty — to run
indefinitely. Random topic selection adds variance on top of that, so a
comfortable library carries headroom above the floor and spreads it evenly
across topics, since a topic starved at one difficulty is simply never chosen.

That is the steady-state target, not the launch requirement.
`QUESTION_COOLDOWN_DAYS` is configuration: development and early production run
it short, and lengthen it toward 180 as the library fills out.

Exposed as a CLI so days can be pre-generated and hand-edited:

```
trivial puzzles generate --from 2026-08-19 --days 30
trivial puzzles show 2026-08-19
```

A lazy fallback generates the current day under a Postgres advisory lock if it
is somehow missing, so a lapsed cron never takes the game down.

## 8. Frontend

Vue 3 with TypeScript. Pinia holds run state; a single store owns the answer
map, the timer, and the resume logic.

**Routes:** `/` (today's run and results), `/c/:token` (challenge landing).

**Key components:** `TimerBar`, `TopicRow`, `QuestionCard` (free-text and
multiple-choice states), `ResultsGrid`, `ShareSheet`.

**Board.** Three topic rows by three difficulty columns, all nine reachable at
any time. Answered cells show their symbol immediately.

**Timer.** Derived from `expires_at` and the clock offset measured at run
start. On reaching zero the client stops accepting input and calls finish; the
server is the arbiter regardless.

**Resume.** Every load hits the resume endpoint. A run that expired while the
tab was closed lands directly on the results screen with the correct symbols
already filled in.

**Share text**, built client-side from the run summary:

```
Trivial 2026-08-18
⭐🟢⏰
🟢🔴⏰
⭐⭐🔴
5/9 in 2:15
https://trivial.example/c/a7Kd93
```

Copied to the clipboard, and offered through the Web Share API where available.

**Challenge landing.** `/c/{token}` is server-rendered HTML carrying OG and
Twitter meta so the link unfurls with the sharer's grid, then boots the SPA
into a screen showing their result and a start button. Starting from a
challenge link records `referred_by_run_id`.

## 9. Testing

**Go unit tests.** The normalizer and grader get the heaviest table-driven
coverage in the project — accents, punctuation, articles, edit distance at both
sides of each boundary, and adversarial near-misses. The puzzle selector is
tested with a fake clock and a fixed seed, covering determinism, cooldown
enforcement, and the exhaustion-and-relax path.

**Go integration tests.** Against a real Postgres, covering idempotent run
start, resume, option-order stability across repeated reveals, rejection of
double answers, mode-order enforcement, and expiry sweep behaviour when the
deadline passes between requests.

**Frontend.** Vitest over the store: timer arithmetic against a mocked clock
offset, resume rehydration, share-text construction for every symbol
combination.

**End to end.** One Playwright happy path — start, answer across both modes,
let the clock expire, reach results, generate a share link — and one covering
reload mid-run.

## 10. Accepted risks

**Cookie-clearing replays the same puzzle.** A player who clears cookies gets a
fresh run at a puzzle they have already seen. Mitigation is limited to rate
limiting by IP. Wordle has the same hole; policing it costs more than it
returns, and any real fix requires the accounts that are deliberately last in
the plan. Revisit only if shared scores stop looking plausible.

**Content exhaustion.** The 180-day question cooldown is never relaxed, so a
library too thin to satisfy it stops producing puzzles rather than repeating
questions. Pre-generating a month ahead converts this from an outage into an
alert with weeks of lead time, but it does mean content supply is a hard
operational dependency, not a soft one.

**135 seconds may be too short.** Nine questions with a two-stage answer flow
inside 135 seconds is unproven. The value is per-day configuration precisely so
it can be tuned once real completion rates exist.

## 11. Implementation phases

| # | Phase | Delivers |
|---|-------|----------|
| 0 | Scaffold | Go module, Vue + Vite app, docker-compose Postgres, migration tooling, Makefile, CI |
| 1 | Schema and seed content | Migrations, eligibility constraints, hand-written seed set (~6 topics × 3 difficulties × 3 questions) with a short dev cooldown |
| 2 | Puzzle generator | Cooldown selection, determinism, CLI, fully tested — no HTTP |
| 3 | Grading engine | Normalizer and alias matching, pure and heavily tested — no HTTP |
| 4 | Run API | Cookie identity, start/resume, reveal options, answer, expiry sweep, finish |
| 5 | Play UI | Timer, 3×3 board, free-text to multiple-choice flow, resume on reload |
| 6 | Results, share text, stats | Symbol grid, share builder, clipboard and Web Share, streak endpoint |
| 7 | Challenge links | Token creation, server-rendered landing with meta tags, referral attribution |
| 8 | Ship | Embedded SPA single binary, configuration, logging, rate limiting, deploy |
| 9 | Accounts | Magic-link auth, claiming anonymous player history, cross-device streaks |

Phases 2 and 3 precede any HTTP deliberately. They hold the only non-trivial
logic in the system and are far easier to get right in isolation, with the
database and the browser both out of the picture.

## 12. Decisions made without explicit direction

Recorded so they are easy to overturn:

- **Streaks and stats land in phase 6**, not with accounts. Runs are already
  server-side and keyed by player, so a stats endpoint costs little, and the
  streak is the retention hook that makes daily play stick.
- **Cheating is not defended against** beyond rate limiting. See §10.
- **One free-text attempt, then one multiple-choice attempt.** Neither stage
  allows retries.
