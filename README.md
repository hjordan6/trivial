# trivial

A daily trivia game in the Wordle mold: three topics a day, easy/medium/hard
per topic, free-text answers that fall back to multiple choice. This repo
currently holds the headless core — schema, question library, deterministic
puzzle generator, grader, and an admin CLI — with no HTTP server or UI yet.
See `docs/superpowers/specs/2026-08-18-daily-trivia-design.md` for the full
design.

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
generated afterward; already-generated puzzles remain unchanged.

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
