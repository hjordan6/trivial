#!/usr/bin/env bash
#
# Replace the QA database with a fresh copy of production.
#
# Both databases are Postgres containers on this box: production is
# trivial-db-1 (host port 5442, the checkout at /home/jordan/trivial) and QA is
# trivial-qa-db-1 (host port 5444, the staging worktree at
# /home/jordan/trivial-qa). The copy goes through a custom-format pg_dump piped
# between the two containers, so nothing depends on a psql client being
# installed on the host -- there isn't one.
#
# This DROPS the QA database. Production is only ever read: the dump runs
# against it and nothing else does, and the guard below refuses to run at all if
# the target container is the production one, so a mistyped override cannot turn
# this into a restore over live data.
#
# Usage:
#   scripts/refresh-qa.sh          copy production into QA, asking first
#   scripts/refresh-qa.sh --yes    skip the confirmation (for scripts and cron)
#
# Overrides, if the containers are ever renamed:
#   PROD_CONTAINER=trivial-db-1 QA_CONTAINER=trivial-qa-db-1 scripts/refresh-qa.sh

set -euo pipefail

readonly PROD_CONTAINER="${PROD_CONTAINER:-trivial-db-1}"
readonly QA_CONTAINER="${QA_CONTAINER:-trivial-qa-db-1}"
readonly DB_USER="${DB_USER:-trivial}"
readonly DB_NAME="${DB_NAME:-trivial}"

if [[ -t 1 ]]; then
  readonly C_STEP=$'\033[1;36m' C_OK=$'\033[1;32m' C_ERR=$'\033[1;31m' C_OFF=$'\033[0m'
else
  readonly C_STEP='' C_OK='' C_ERR='' C_OFF=''
fi
step() { printf '%s==>%s %s\n' "$C_STEP" "$C_OFF" "$*"; }
ok()   { printf '%s  ok%s %s\n' "$C_OK" "$C_OFF" "$*"; }
die()  { printf '%serr %s %s\n' "$C_ERR" "$C_OFF" "$*" >&2; exit 1; }

ASSUME_YES=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --yes|-y) ASSUME_YES=true ;;
    -h|--help) sed -n '2,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^#\{1,2\} \{0,1\}//'; exit 0 ;;
    *) die "unknown option: $1 (try --help)" ;;
  esac
  shift
done

# The one refusal that matters. Everything below drops and recreates
# $DB_NAME on $QA_CONTAINER, so the target must never be production, however
# the variables were set.
[[ "$QA_CONTAINER" != "$PROD_CONTAINER" ]] \
  || die "QA_CONTAINER and PROD_CONTAINER are both '$QA_CONTAINER'. Refusing to restore over the source."
[[ "$QA_CONTAINER" != "trivial-db-1" ]] \
  || die "QA_CONTAINER is trivial-db-1, which is production. Refusing."

for c in "$PROD_CONTAINER" "$QA_CONTAINER"; do
  docker inspect -f '{{.State.Running}}' "$c" 2>/dev/null | grep -qx true \
    || die "container '$c' is not running. Start it with 'docker compose up -d db'."
done

if [[ "$ASSUME_YES" != true ]]; then
  printf 'This will DROP the database "%s" on %s and replace it with a copy of %s.\n' \
    "$DB_NAME" "$QA_CONTAINER" "$PROD_CONTAINER"
  read -r -p 'Type "yes" to continue: ' reply
  [[ "$reply" == "yes" ]] || die "aborted"
fi

# The dump lands in a private temp file rather than being streamed straight
# through, so a failure part-way leaves QA's old database untouched instead of
# half-replaced. It holds real player email addresses, hence the 600 and the
# trap.
DUMP="$(mktemp -t trivial-prod-dump.XXXXXX)"
chmod 600 "$DUMP"
trap 'rm -f "$DUMP"' EXIT

step "dumping $DB_NAME from $PROD_CONTAINER"
docker exec "$PROD_CONTAINER" pg_dump -U "$DB_USER" -d "$DB_NAME" -Fc --no-owner --no-acl > "$DUMP"
ok "dumped $(du -h "$DUMP" | cut -f1)"

# Sessions have to go before the DROP: Postgres refuses to drop a database that
# anything is connected to, and the QA server holds a pool open. It reconnects
# on its own once the new database exists, so there is no need to stop it.
step "closing open connections to $DB_NAME on $QA_CONTAINER"
docker exec "$QA_CONTAINER" psql -U "$DB_USER" -d postgres -v ON_ERROR_STOP=1 -q -c \
  "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$DB_NAME' AND pid <> pg_backend_pid();" >/dev/null

step "recreating $DB_NAME on $QA_CONTAINER"
docker exec "$QA_CONTAINER" psql -U "$DB_USER" -d postgres -v ON_ERROR_STOP=1 -q \
  -c "DROP DATABASE IF EXISTS $DB_NAME;" -c "CREATE DATABASE $DB_NAME OWNER $DB_USER;"

step "restoring into $QA_CONTAINER"
# --no-owner keeps the restore working whatever role the dump was taken as.
# pg_restore reports non-fatal notices on stderr and still exits 0; -e would
# make a single harmless notice abort a good restore, so the exit code is what
# is trusted here.
docker exec -i "$QA_CONTAINER" pg_restore -U "$DB_USER" -d "$DB_NAME" --no-owner --no-acl < "$DUMP"
ok "restored"

step "verifying"
counts="$(docker exec "$QA_CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -At -F' ' -c \
  "SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM questions), (SELECT count(*) FROM friendships);")"
read -r users questions friendships <<<"$counts"
ok "QA now holds $users users, $questions questions, $friendships friendships"

printf '\nQA data is a copy of production as of %s.\n' "$(date '+%Y-%m-%d %H:%M:%S')"
