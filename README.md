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
```

`.env.example` sets `QUESTION_COOLDOWN_DAYS=7` for local development, because
the 54-question starter library can't sustain the production value of 180
days (the spec's never-relaxed six-month no-repeat rule) for more than a
handful of days at a time.
