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
go run ./cmd/trivial puzzles generate [--from YYYY-MM-DD] [--days N]
go run ./cmd/trivial puzzles show YYYY-MM-DD
go run ./cmd/trivial serve
```

For local play, `make serve` builds the Vue application, migrates the database,
and starts the game at `http://localhost:8080`.

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

For local work, generate in short batches (e.g. `--days 5`) instead of
trying to fill a long run at once. To actually sustain a full year at the
production 180-day cooldown, `seed/questions.json` needs enough breadth
that 9 distinct questions a day never repeat within 180 days -- on the
order of 1,600 eligible questions, roughly 540 per difficulty.
