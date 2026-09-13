#!/usr/bin/env bash
#
# Create two demo accounts that are friends with each other, and give each of
# them a finished run on today's board.
#
# This exists so the friends leaderboard can be looked at without two people and
# two phones: sign in as either address, and the other one is sitting there with
# a result to compare against.
#
# The accounts are demo data on a QA box whose database is a copy of production,
# so they are deliberately easy to tell apart from real players: both use the
# reserved .test TLD, which can never be a deliverable address.
#
# Re-running is safe and is the intended way to reset: the accounts and the
# friendship are upserted, only today's runs are replaced, and both accounts get
# their sign-in rate-limit budget back -- which is the usual reason to re-run it
# on a day that is already seeded.
#
# Usage:
#   scripts/seed-demo-friends.sh            seed against the QA database
#   QA_CONTAINER=... scripts/seed-demo-friends.sh
#
# It refuses to touch production -- see the guard below.

set -euo pipefail

readonly QA_CONTAINER="${QA_CONTAINER:-trivial-qa-db-1}"
readonly DB_USER="${DB_USER:-trivial}"
readonly DB_NAME="${DB_NAME:-trivial}"
readonly TZ_NAME="${PUZZLE_TIMEZONE:-America/Denver}"
# How many past boards the demo accounts have played. Comfortably over the
# three-day floor the all-time rankings apply.
readonly DEMO_DAYS="${DEMO_DAYS:-6}"

readonly ONE_EMAIL="janedoe33@trivial.test"
readonly ONE_NAME="janedoe33"
readonly TWO_EMAIL="marcusq@trivial.test"
readonly TWO_NAME="marcusq"

if [[ -t 1 ]]; then
  readonly C_STEP=$'\033[1;36m' C_OK=$'\033[1;32m' C_ERR=$'\033[1;31m' C_OFF=$'\033[0m'
else
  readonly C_STEP='' C_OK='' C_ERR='' C_OFF=''
fi
step() { printf '%s==>%s %s\n' "$C_STEP" "$C_OFF" "$*"; }
ok()   { printf '%s  ok%s %s\n' "$C_OK" "$C_OFF" "$*"; }
die()  { printf '%serr %s %s\n' "$C_ERR" "$C_OFF" "$*" >&2; exit 1; }

# Demo accounts have no business in the production database, and this script
# writes runs, so the refusal is worth more than the convenience of an override.
[[ "$QA_CONTAINER" != "trivial-db-1" ]] \
  || die "QA_CONTAINER is trivial-db-1, which is production. Refusing."

docker inspect -f '{{.State.Running}}' "$QA_CONTAINER" 2>/dev/null | grep -qx true \
  || die "container '$QA_CONTAINER' is not running."

psql() { docker exec -i "$QA_CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 "$@"; }

# The puzzle date is the calendar date in the game's timezone, not the
# container's UTC date -- between midnight and 06:00 MDT those disagree, and
# seeding the wrong day produces two accounts whose results never show up.
DATE="$(psql -At -c "SELECT (now() AT TIME ZONE '$TZ_NAME')::date")"
step "seeding demo friends for $DATE"

psql -At -c "SELECT 1 FROM daily_puzzles WHERE puzzle_date = '$DATE'" | grep -qx 1 \
  || die "no puzzle generated for $DATE. Run 'go run ./cmd/trivial puzzles generate' first."

# One transaction: a half-seeded pair -- two accounts but no friendship, or a
# run with no answers -- is worse than none, because it looks like a working
# fixture right up until the page is opened.
psql <<SQL
BEGIN;

-- The accounts. ON CONFLICT makes re-running a reset rather than an error, and
-- keeps the ids stable so the friendship below survives it.
INSERT INTO users (email) VALUES ('$ONE_EMAIL'), ('$TWO_EMAIL')
    ON CONFLICT (email) DO NOTHING;

-- Give both accounts their sign-in budget back.
--
-- The per-address limiter is a count over login_tokens inside a window -- three
-- per fifteen minutes, ten per day -- and comparing these two accounts means
-- signing in and out repeatedly, which spends it fast. Over the limit, the
-- server answers 202 "sent" and drops the request silently, deliberately: a 429
-- keyed on an address would tell a caller that somebody has been requesting
-- codes for it. Correct for real players, and indistinguishable from a broken
-- sign-in screen when it is your own demo account.
--
-- These rows are spent codes for two throwaway accounts, so deleting them costs
-- nothing and makes the fixture usable again immediately.
DELETE FROM login_tokens WHERE email IN ('$ONE_EMAIL', '$TWO_EMAIL');

-- The display name the leaderboard shows. It comes from the invite a person
-- last minted, so seeding one here is what makes these two read as "janedoe33"
-- rather than falling back to the address's local part.
INSERT INTO friend_invites (token, user_id, nickname)
SELECT 'demo-' || u.id || '-' || substr(md5(random()::text), 1, 16), u.id,
       CASE u.email WHEN '$ONE_EMAIL' THEN '$ONE_NAME' ELSE '$TWO_NAME' END
  FROM users u WHERE u.email IN ('$ONE_EMAIL', '$TWO_EMAIL')
    ON CONFLICT (user_id) DO UPDATE SET nickname = EXCLUDED.nickname;

-- friendships stores one row per pair in canonical order; least/greatest is
-- what satisfies the CHECK (user_low < user_high) whichever id came out larger.
INSERT INTO friendships (user_low, user_high)
SELECT least(a.id, b.id), greatest(a.id, b.id)
  FROM users a, users b
 WHERE a.email = '$ONE_EMAIL' AND b.email = '$TWO_EMAIL'
    ON CONFLICT DO NOTHING;

-- One browser each. Demo players are marked by their created_at being the
-- account's, which is only used to find them again on a re-run.
INSERT INTO players (user_id)
SELECT u.id FROM users u
 WHERE u.email IN ('$ONE_EMAIL', '$TWO_EMAIL')
   AND NOT EXISTS (SELECT 1 FROM players p WHERE p.user_id = u.id);

-- The one browser each demo result belongs to.
--
-- A demo account accumulates players: every sign-in on a new browser attaches
-- another one, and looking at the leaderboard is a sign-in. The oldest is the
-- one this script created, so that is the one that carries the result.
CREATE TEMP VIEW demo_player AS
SELECT DISTINCT ON (u.id) u.id AS user_id, u.email, p.id AS player_id,
       -- Percentage of questions this account types from memory. The rest of
       -- the distribution is built off it, so one number separates the two.
       -- Chosen by measuring the result, not by guessing: md5 over 54 draws is
       -- lumpy enough that a narrower gap left the two accounts effectively
       -- tied, which demonstrates nothing on a page about ranking. These give
       -- roughly 40 and 27 average points a day.
       CASE u.email WHEN '$ONE_EMAIL' THEN 55 ELSE 20 END AS skill
  FROM users u JOIN players p ON p.user_id = u.id
 WHERE u.email IN ('$ONE_EMAIL', '$TWO_EMAIL')
 ORDER BY u.id, p.created_at, p.id;

-- The days they have played: the most recent generated boards up to today.
-- More than MinimumDays in internal/httpapi, or neither account clears the
-- floor and the all-time board has nothing to rank.
CREATE TEMP VIEW demo_dates AS
SELECT puzzle_date FROM daily_puzzles
 WHERE puzzle_date <= '$DATE'
 ORDER BY puzzle_date DESC
 LIMIT $DEMO_DAYS;

-- Today's runs are replaced outright, so re-running re-rolls the scores rather
-- than colliding with UNIQUE (player_id, puzzle_date).
-- Every demo day is replaced outright, so re-running re-rolls the record
-- rather than colliding with UNIQUE (player_id, puzzle_date).
DELETE FROM runs r
 USING players p, users u
 WHERE r.player_id = p.id AND p.user_id = u.id
   AND u.email IN ('$ONE_EMAIL', '$TWO_EMAIL')
   AND r.puzzle_date IN (SELECT puzzle_date FROM demo_dates);

INSERT INTO runs (player_id, puzzle_date, started_at, expires_at, completed_at, option_seed)
SELECT dp.player_id, d.puzzle_date,
       d.puzzle_date + time '19:40', d.puzzle_date + time '19:44', d.puzzle_date + time '19:43', 1
  FROM demo_player dp CROSS JOIN demo_dates d;

-- The outcomes.
--
-- Derived from a hash of (account, date, question) rather than listed by hand,
-- because the all-time board needs several days of varied play and a fixed
-- nine-outcome pattern repeated across them would give both accounts the same
-- average every day -- which is exactly the thing the board is supposed to
-- distinguish.
--
-- The hash is stable, so re-running reproduces the same record instead of
-- shuffling everyone's rank. `skill` is the only difference between the two
-- accounts: janedoe33 types more answers and misses fewer, so she should
-- finish ahead of marcusq on average without either being perfect.
INSERT INTO run_answers (run_id, question_id, stage, free_text_submission, chosen_option, outcome, first_touched_at, resolved_at)
SELECT r.id, dpq.question_id,
       CASE WHEN o.outcome = 'star' THEN 'free_text' ELSE 'multiple_choice' END::answer_stage,
       CASE WHEN o.outcome = 'star' THEN q.canonical_answer END,
       CASE WHEN o.outcome = 'circle' THEN q.canonical_answer END,
       o.outcome::answer_outcome,
       r.started_at, r.started_at + interval '30 seconds'
  FROM demo_player dp
  JOIN runs r ON r.player_id = dp.player_id
  JOIN demo_dates d ON d.puzzle_date = r.puzzle_date
  JOIN daily_puzzle_questions dpq ON dpq.puzzle_date = d.puzzle_date
  JOIN questions q ON q.id = dpq.question_id
  CROSS JOIN LATERAL (
      SELECT CASE
               WHEN roll.v < dp.skill                THEN 'star'
               WHEN roll.v < dp.skill + 30           THEN 'circle'
               WHEN roll.v < 95                      THEN 'miss'
               ELSE 'expired'
             END AS outcome
        FROM (SELECT ('x' || substr(md5(dp.email || d.puzzle_date::text || dpq.question_id::text), 1, 8))::bit(32)::bigint % 100 AS v) roll
  ) o;

COMMIT;
SQL

step "verifying"
psql -c "
WITH demo_player AS (
    SELECT DISTINCT ON (u.id) u.id AS user_id, u.email, p.id AS player_id
      FROM users u JOIN players p ON p.user_id = u.id
     WHERE u.email IN ('$ONE_EMAIL', '$TWO_EMAIL')
     ORDER BY u.id, p.created_at, p.id
)
SELECT dp.email, fi.nickname,
       count(DISTINCT r.puzzle_date) AS days,
       count(*) FILTER (WHERE ra.outcome IN ('star','circle')) AS correct,
       count(*) AS answered,
       (SELECT count(*) FROM runs r2
          JOIN players p2 ON p2.id = r2.player_id
         WHERE p2.user_id = dp.user_id AND r2.puzzle_date = '$DATE') AS runs_today
  FROM demo_player dp
  JOIN friend_invites fi ON fi.user_id = dp.user_id
  JOIN runs r ON r.player_id = dp.player_id
  JOIN run_answers ra ON ra.run_id = r.id
 GROUP BY dp.email, dp.user_id, fi.nickname
 ORDER BY correct DESC;"

ok "seeded $ONE_EMAIL and $TWO_EMAIL as friends, both finished for $DATE"
