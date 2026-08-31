-- +goose Up

-- Move the fallback time limit from 135 seconds to 240 (four minutes) so the
-- column default agrees with TIME_LIMIT_SECONDS. Nothing in the application
-- relies on this default -- the generator always writes the value explicitly
-- from config -- but a stale 135 here is a trap for hand-written inserts and
-- for tests that omit the column.
--
-- Existing rows are left alone on purpose: a daily_puzzles row is the record
-- of the budget a puzzle was actually played under, so rewriting past dates
-- would falsify runs that already happened against the old clock.
ALTER TABLE daily_puzzles ALTER COLUMN time_limit_seconds SET DEFAULT 240;

-- +goose Down
ALTER TABLE daily_puzzles ALTER COLUMN time_limit_seconds SET DEFAULT 135;
