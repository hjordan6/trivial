# trivial

A daily trivia game in the Wordle mold: three topics a day, easy/medium/hard
per topic, free-text answers that fall back to multiple choice. This repo
currently holds the headless core — schema, question library, deterministic
puzzle generator, grader, an admin CLI, and an HTTP server with an embedded Vue
front end. See `docs/superpowers/specs/2026-08-18-daily-trivia-design.md` for the
full design.

## Prerequisites

- Go 1.26+
- Docker (for Postgres via `docker compose`)
- Node 22+ (for the `web/` scaffold)

## Getting started

```
cp .env.example .env
make db-up
make migrate
make seed
make test
```

## CLI commands

```
go run ./cmd/trivial migrate up|down
go run ./cmd/trivial seed apply [--file seed/questions.json]
go run ./cmd/trivial seed replace --file question_dump.json
go run ./cmd/trivial puzzles generate [--from YYYY-MM-DD] [--days N]
go run ./cmd/trivial puzzles show YYYY-MM-DD
go run ./cmd/trivial serve
```

For local play, `make serve` builds the Vue application, migrates the database,
starts the game at `http://localhost:8080`, and enables a local-only reset
button so the same daily puzzle can be replayed during development. The reset
endpoint is disabled unless `DEVELOPMENT_MODE=true`.

## Starter library limits

The seeded content (`seed/questions.json`) has 54 questions: 6 topics x 3
difficulties x 3 questions each, or 18 questions per difficulty. Each day of
generation draws 9 new questions (3 topics x 3 difficulties) under a
no-repeat cooldown, so with only 18 questions per difficulty the library
runs dry after around six days of generation -- this is a property of the
library's size, not of the cooldown setting.

At the production cooldown of 180 days, once the library is exhausted it
stays exhausted for the rest of that window. At the shorter dev value of 7
days, generation recovers as questions age back out of cooldown, but only
periodically -- expect a run of good days broken up by a run of failing
ones, not a full recovery. The 7-day dev cooldown shortens the outage; it
does not remove the limit.

Hitting `InsufficientContentError` while generating against the starter
library is therefore expected, not a bug: it's the no-repeat guarantee
refusing to re-serve a question that's still on cooldown, and the generator
deliberately writes nothing for a day it can't fully fill rather than
producing a partial one.

## Topic weights

Each topic has a positive integer `weight` in `seed/questions.json`. Weights
are relative: on each topic draw, a topic weighted `3` is three times as likely
to be selected as one weighted `1`. Selection is without replacement, so a
topic still appears at most once per board. All starter topics default to `1`.

After changing weights, run `make seed`. The new weights affect puzzles
generated afterward; already-generated puzzles remain unchanged. Weights are
also editable in the admin panel, which writes them directly and needs no
reseed.

## Admin panel

Set `ADMIN_PASSWORD` and the panel appears at `/admin`. Leave it unset and the
page and every `/api/admin` route answer 404 -- there is no default password,
because a default admin password is worse than none.

```sh
ADMIN_PASSWORD=letmein make serve
```

The panel does eight things:

**Pin a date's categories.** Each upcoming date has three slots, and each slot
is either a topic you chose or `Automatic`. Pinning is per slot, so you can fix
one category and let the other two be drawn as usual. Questions are always
picked automatically -- the panel never chooses a question. "Reset to
automatic" clears a date's pins and rebuilds it.

Only future dates can change. Today and the past are refused, and so is any
date someone has already played: that board is history.

Two things are worth knowing. Saving a pin re-picks all nine questions for the
date, including the slots you left automatic, because one date-seeded RNG feeds
both the topic order and every question pick. And if a pinned topic has no
eligible question at some difficulty, the save fails naming the topic and the
difficulty rather than quietly substituting another topic -- which is what
happens to an automatically chosen topic in the same position.

**Generate the days that are missing**, equivalent to `trivial puzzles
generate` but skipping dates that already exist and reporting per-date failures
instead of stopping at the first one. Pins are honoured whenever a date is
generated, including from the CLI, so pinning an ungenerated date and
generating later works.

**Edit topic weights and the active flag**, which previously required editing
`seed/questions.json` and re-running `make seed`. At least three topics must
stay active or no board can be generated, so the last three cannot be switched
off.

**Browse the question library.** The list pages through every question with its
topic, difficulty band and 1-10 rating. Prompts are clipped to one line until
you open a row, which reveals the full text and the answer choices. Answers and
their accepted spellings stay redacted until clicked, so the library can be
read over someone's shoulder without spoiling a board; while an answer is
hidden it also sits in alphabetical order among the distractors, so its
position gives nothing away. Filters cover topic, difficulty and a substring
search over prompts and answers.

Each row also reports how many times the question has been used and when it was
last drawn. That is the answer to "why can nothing generate": a topic fails
because its questions are inside the cooldown window, not because the topic is
missing.

**Write a question by hand.** A form for one question: topic, difficulty on
the 1-10 scale, prompt, answer, other accepted spellings, and the wrong
options. It goes through exactly the same validation a pasted seed file does,
so a question that would never reach a board -- fewer than three distinct
distractors, or a distractor that also grades as correct -- is refused here
too. Save it as a Draft to keep it out of selection until it is ready.

**Edit a question from the list.** Opening a row and clicking "Edit question"
turns the detail panel into the same form. Wording, answer, accepted
spellings, wrong options and status can always change.

Topic and difficulty band cannot change while the question is on a generated
board. `daily_puzzle_questions` records the topic and difficulty alongside the
question id, so moving a scheduled question would leave a board describing
content its own question no longer has; the edit is refused naming the dates.
Rewording is always allowed, and so is a rating that stays inside its band --
2 to 4 is still easy. To move a scheduled question, rebuild those dates first.

Retiring a question stops it being *selected* in future generation; it does
not pull it off a board that already exists. Rebuild the date for that.

**Export the library as CSV.** Tick the columns you want -- id, external_id,
topic, difficulty band and rating, prompt, answer, aliases, distractors,
status, use count, last used -- and the link downloads the whole bank. The
export deliberately ignores the listing's filters and paging: it is the "give
me everything" button, and a partial file that looks complete is worse than no
file. Aliases and distractors hold several values in one cell, joined with
` | ` rather than a comma so they stay readable when the file is eyeballed
instead of parsed.

**Add questions by pasting JSON**, in any shape the seed loader already accepts
(see below). A paste is validated before anything is written and applied in one
transaction, so it lands completely or not at all -- a file with a bad question
at the end does not leave the good ones behind. The parser's own message comes
back verbatim, naming the topic and the question index. Imported questions
arrive `active` and are eligible for the next board generated. Re-pasting the
same `external_id` updates that question rather than creating a duplicate,
which makes fixing a typo a paste-again operation.

## Accounts

Play is anonymous by default: a player is a browser, identified by a signed
cookie. Adding an email is optional and never required to play. It exists so a
person's history survives two things a cookie does not — a second device, and a
browser that clears its cookies.

**Signing in attaches; it never migrates.** The current browser's `players` row
is pointed at a `users` row. Nothing is merged, moved, or deleted, so a user
accumulates one player row per browser and signing in on the second device is
the same operation as the first. Stats then span every browser that user has
signed in on. Where two of them completed the same date, the run started first
is the one that counts.

Signing out clears the session cookie only. The attachment stays, deliberately:
clearing it would stop that browser's past runs counting toward the account, and
would let the next person to sign in on a shared computer inherit them. For the
same reason, signing in on a browser already claimed by a different account is
refused rather than silently re-pointed.

**A six-digit code, not a magic link.** A link tapped in a mail app opens in that
app's in-process browser, so the session lands somewhere the player is not. A
code typed into the tab they are already sitting in cannot do that, and
`autocomplete="one-time-code"` lets the phone offer it straight from the
notification. Codes are single-use, short-lived, stored as a keyed HMAC rather
than a bare digest, and superseded by the next request for the same address.

Six digits is only about 20 bits, so the defence is the attempt budget, not the
code: five guesses per code, one live code per address at a time, and a ceiling
on failures per address per hour so the budget cannot be refreshed by simply
asking for another code. The lever for more margin is code length, not more
limits.

### Configuration

`APP_SECRET` is required to serve. It keys every signed cookie, has no default,
and changing it signs everyone out:

```sh
openssl rand -base64 48
```

`RESEND_API_KEY` switches on real email. Without it, `DEVELOPMENT_MODE=true`
writes the code to the server log instead, so the whole flow is exercisable
locally with no provider and no outbound mail:

```sh
make serve   # then read the code out of the log and type it in
```

Leaving `RESEND_API_KEY` unset in production makes sign-in report itself
unavailable and hides the prompt, rather than half-working. `MAIL_FROM` is
required whenever the key is set, and its domain must be verified in Resend --
`onboarding@resend.dev` works only for mail to your own account address. Behind
a reverse proxy, set `TRUST_PROXY_IP=true`, or every request shares one rate
limit bucket.

An address that is typed by mistake never becomes an account: a `users` row is
only created once someone proves they can read the address, so a typo lives in
`login_tokens` until the retention sweep and then disappears.

## Importing questions

The seed command also accepts a flat JSON array. Difficulty is stored on a
1–10 scale but players see only the derived band: 1–4 easy, 5–7 medium, and
8–10 hard. Each item uses this shape:

```json
{
  "question": "Which Serbian center is a multiple-time NBA MVP?",
  "category": "Sports",
  "difficulty": 5,
  "answer": "Nikola Jokić",
  "acceptedAnswers": ["Nikola Jokic", "Jokic", "Jokić"],
  "multipleChoiceOptions": [
    "Nikola Jokić",
    "Luka Dončić",
    "Giannis Antetokounmpo",
    "Joel Embiid"
  ]
}
```

Put one or more objects in a JSON array, then import them with:

```bash
go run ./cmd/trivial seed apply --file path/to/questions.json
```

The importer groups questions by category, derives stable external IDs from
their prompts, removes accepted answers from the distractor set, and requires
at least three distinct wrong options.

For local work, generate in short batches (e.g. `--days 5`) instead of
trying to fill a long run at once. To actually sustain a full year at the
production 180-day cooldown, `seed/questions.json` needs enough breadth
that 9 distinct questions a day never repeat within 180 days -- on the
order of 1,600 eligible questions, roughly 540 per difficulty.
