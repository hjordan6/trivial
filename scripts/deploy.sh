#!/usr/bin/env bash
#
# Rebuild the Vue bundle and the Go binary, swap them in, and restart the
# server. This box runs trivial as a plain detached process -- no systemd unit,
# no container, no reverse proxy -- so "deploy" is: build, stop, swap, start,
# prove it answers.
#
# The binary being replaced is kept as bin/trivial.prev and put back
# automatically if the new one fails its health check, so a bad build costs a
# few seconds of downtime instead of a hand repair at the shell.
#
# Configuration comes from .env, which is deliberately not in git: it holds
# APP_SECRET, ADMIN_PASSWORD and RESEND_API_KEY. This script reads it rather
# than carrying any value of its own, so what gets deployed is always what that
# file says.
#
# Usage:
#   scripts/deploy.sh                build, test, restart, verify
#   scripts/deploy.sh --skip-tests   skip the suites; build and restart only
#   scripts/deploy.sh --rollback     put bin/trivial.prev back and restart
#   scripts/deploy.sh --status       report what is running; change nothing
#   scripts/deploy.sh --stop         stop the server and leave it stopped

set -euo pipefail

readonly ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

readonly BIN="bin/trivial"
readonly PREV="bin/trivial.prev"
readonly NEXT="bin/trivial.next"
readonly PIDFILE="bin/trivial.pid"
readonly LOG="server.log"

# Long enough to cover a cold start that has to open the pool and run
# migrations, short enough that a crash-looping binary is reported rather than
# waited on.
readonly HEALTH_TRIES=40
readonly HEALTH_DELAY=0.25

RUN_TESTS=true
MODE=deploy

# --- output -----------------------------------------------------------------
# Colour only when stdout is a terminal, so piping to a file or a log stays
# readable.
if [[ -t 1 ]]; then
  readonly C_STEP=$'\033[1;36m' C_OK=$'\033[1;32m' C_WARN=$'\033[1;33m' C_ERR=$'\033[1;31m' C_OFF=$'\033[0m'
else
  readonly C_STEP='' C_OK='' C_WARN='' C_ERR='' C_OFF=''
fi

step() { printf '%s==>%s %s\n' "$C_STEP" "$C_OFF" "$*"; }
ok()   { printf '%s  ok%s %s\n' "$C_OK" "$C_OFF" "$*"; }
warn() { printf '%swarn%s %s\n' "$C_WARN" "$C_OFF" "$*" >&2; }
die()  { printf '%serr %s %s\n' "$C_ERR" "$C_OFF" "$*" >&2; exit 1; }

usage() { sed -n '2,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^#\{1,2\} \{0,1\}//'; }

# --- arguments --------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-tests) RUN_TESTS=false ;;
    --rollback)   MODE=rollback ;;
    --status)     MODE=status ;;
    --stop)       MODE=stop ;;
    -h|--help)    usage; exit 0 ;;
    *)            die "unknown option: $1 (try --help)" ;;
  esac
  shift
done

# --- configuration ----------------------------------------------------------

# load_env sources .env the same way `make serve` and the shell do. It is the
# project's own file, so sourcing it is no more trust than running make; the
# alternative -- parsing it by hand -- would break on MAIL_FROM, whose angle
# brackets are quoted precisely because they are shell syntax.
load_env() {
  [[ -f .env ]] || die ".env not found. Copy .env.example to .env and fill it in."
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a

  : "${HTTP_ADDRESS:=:8080}"
  [[ -n "${APP_SECRET:-}" ]] || die "APP_SECRET is unset in .env; the server refuses to start without it."
  [[ -n "${DATABASE_URL:-}" ]] || die "DATABASE_URL is unset in .env."

  # HTTP_ADDRESS goes straight to http.Server{Addr} (cmd/trivial/main.go), which
  # takes exactly one host:port. A comma-separated list looks reasonable and is
  # not: it fails at listen with "too many colons in address", after the log has
  # already claimed the server is listening. Catch it here, where the message
  # can say what to do about it.
  if [[ "$HTTP_ADDRESS" == *,* ]]; then
    die "HTTP_ADDRESS is a list ($HTTP_ADDRESS). The server binds one address only.
     Use :$(printf '%s' "${HTTP_ADDRESS##*:}") to serve loopback, the tailnet and the LAN at once,
     or a single host:port to restrict it."
  fi
}

# health_url turns a bind address into something curl can fetch. A wildcard bind
# (":8092", "0.0.0.0:8092", "[::]:8092") is reachable on loopback, which is the
# one interface this script can rely on being up.
health_url() {
  local addr="$HTTP_ADDRESS" host port
  port="${addr##*:}"
  host="${addr%:*}"
  case "$host" in
    ''|'0.0.0.0'|'[::]'|'*') host=127.0.0.1 ;;
  esac
  printf 'http://%s:%s/healthz' "$host" "$port"
}

# --- process control --------------------------------------------------------

# find_server_pids prints every process that is this checkout's server: an
# executable living under this directory, running with the `serve` argument.
#
# Matching the executable rather than the command line is the whole point. A
# shell whose command line merely *mentions* "./bin/trivial serve" -- this
# script, or the wrapper that launched the server by hand -- matches a naive
# `pgrep -f` and is emphatically not the server. Killing that instead leaves the
# real one running and the port taken.
find_server_pids() {
  local pid exe args
  for pid in $(pgrep -x trivial 2>/dev/null || true); do
    exe="$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)"
    # The binary may since have been renamed to .prev underneath a running
    # process, so match the directory rather than the exact path.
    [[ "$exe" == "$ROOT"/* ]] || continue
    args="$(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null || true)"
    [[ " $args " == *" serve "* ]] || continue
    printf '%s\n' "$pid"
  done
  return 0
}

# server_pid is the one server to act on, preferring the pidfile and falling
# back to a scan for a server started by hand or by an older script.
server_pid() {
  local pid
  if [[ -f "$PIDFILE" ]]; then
    pid="$(cat "$PIDFILE" 2>/dev/null || true)"
    # Trust the pidfile only if that pid is still a server; pids get recycled,
    # and a stale file must never aim a kill at an unrelated process.
    if [[ -n "$pid" ]] && find_server_pids | grep -qx "$pid"; then
      printf '%s' "$pid"
      return 0
    fi
  fi
  pid="$(find_server_pids | head -1)"
  [[ -n "$pid" ]] && printf '%s' "$pid"
  # Always succeed: an empty answer is a normal one, and a non-zero return here
  # would abort every caller under `set -e`.
  return 0
}

# stop_server asks politely, then insists. The server installs no signal
# handler, so SIGTERM ends it immediately and there is no graceful drain to wait
# for -- the loop is for the kernel releasing the port, not for the process
# finishing work.
stop_server() {
  local pid; pid="$(server_pid)"
  if [[ -z "$pid" ]]; then
    ok "nothing running"
    rm -f "$PIDFILE"
    return 0
  fi

  step "stopping server (pid $pid)"
  kill "$pid" 2>/dev/null || true
  for _ in $(seq 20); do
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.25
  done
  if kill -0 "$pid" 2>/dev/null; then
    warn "pid $pid ignored SIGTERM; sending SIGKILL"
    kill -9 "$pid" 2>/dev/null || true
    sleep 0.5
  fi
  rm -f "$PIDFILE"

  # A second server from an earlier hand-start would hold the port and make the
  # new one fail to bind, so clear out any stragglers too.
  local stray
  for stray in $(find_server_pids); do
    warn "another server was running (pid $stray); stopping it too"
    kill "$stray" 2>/dev/null || true
  done
  ok "stopped"
}

# start_server detaches the binary from this shell. setsid plus a redirected
# stdin is what keeps it alive after the script exits -- without both, it dies
# with its parent or blocks on a terminal read.
start_server() {
  step "starting $BIN on $HTTP_ADDRESS"
  setsid nohup "./$BIN" serve >>"$LOG" 2>&1 </dev/null &
  disown 2>/dev/null || true

  # Discover the pid rather than trusting $!, which is setsid's -- setsid forks
  # before exec, so the shell's idea of the child is a process that has already
  # exited by the time the server is listening.
  local pid=''
  for _ in $(seq 20); do
    pid="$(find_server_pids | head -1)"
    [[ -n "$pid" ]] && break
    sleep 0.25
  done
  if [[ -n "$pid" ]]; then
    printf '%s' "$pid" > "$PIDFILE"
  else
    rm -f "$PIDFILE"
  fi
}

# wait_healthy polls /healthz, which answers 204 and touches no database, so it
# reports "this process is serving HTTP" and nothing more ambitious.
wait_healthy() {
  local url; url="$(health_url)"
  local pid; pid="$(server_pid)"
  for _ in $(seq "$HEALTH_TRIES"); do
    # A process that has already exited will never become healthy, so stop
    # waiting and let the caller show the log.
    if [[ -n "$pid" ]] && ! kill -0 "$pid" 2>/dev/null; then
      return 1
    fi
    if curl -fsS -o /dev/null --max-time 2 "$url" 2>/dev/null; then
      ok "healthy at $url"
      return 0
    fi
    sleep "$HEALTH_DELAY"
  done
  return 1
}

# tail_log is what to show when something failed: the server's own last words.
tail_log() {
  if [[ -f "$LOG" ]]; then
    printf '\n--- last 15 lines of %s ---\n' "$LOG" >&2
    tail -15 "$LOG" >&2
    printf -- '---\n' >&2
  fi
}

# --- modes ------------------------------------------------------------------

do_status() {
  local pid; pid="$(server_pid)"
  if [[ -n "$pid" ]]; then
    printf 'running   pid %s, up %s\n' "$pid" "$(ps -o etime= -p "$pid" | tr -d ' ')"
    printf 'command   %s\n' "$(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null || echo unknown)"
    # DEVELOPMENT_MODE is worth surfacing: it swaps real email for the log
    # sender and opens the run-reset endpoint, and it is set per-process, so the
    # only honest answer comes from the process rather than from .env.
    printf 'dev mode  %s\n' "$(tr '\0' '\n' < "/proc/$pid/environ" 2>/dev/null | sed -n 's/^DEVELOPMENT_MODE=//p' || true)"
  else
    printf 'running   no\n'
  fi
  printf 'address   %s\n' "$HTTP_ADDRESS"
  printf 'binary    %s\n' "$([[ -f $BIN ]] && date -r "$BIN" '+%Y-%m-%d %H:%M:%S' || echo 'not built')"
  printf 'rollback  %s\n' "$([[ -f $PREV ]] && date -r "$PREV" '+%Y-%m-%d %H:%M:%S' || echo 'none')"
  if [[ -n "$pid" ]]; then
    curl -fsS -o /dev/null --max-time 2 "$(health_url)" 2>/dev/null \
      && printf 'health    ok (%s)\n' "$(health_url)" \
      || printf 'health    FAILING (%s)\n' "$(health_url)"
  fi
}

do_rollback() {
  [[ -f "$PREV" ]] || die "no $PREV to roll back to."
  step "rolling back to $PREV ($(date -r "$PREV" '+%Y-%m-%d %H:%M:%S'))"
  stop_server
  # The swap is a three-way exchange so that a rollback is itself reversible:
  # what you just rolled back from becomes the new .prev.
  if [[ -f "$BIN" ]]; then
    local scratch="$NEXT"
    mv "$BIN" "$scratch"
    mv "$PREV" "$BIN"
    mv "$scratch" "$PREV"
  else
    mv "$PREV" "$BIN"
  fi
  start_server
  if wait_healthy; then
    ok "rolled back"
  else
    tail_log
    die "the rolled-back binary is not healthy either. Fix by hand."
  fi
}

run_tests() {
  step "checking formatting and vetting"
  local unformatted; unformatted="$(gofmt -l . 2>/dev/null || true)"
  [[ -z "$unformatted" ]] || die "gofmt needed:"$'\n'"$unformatted"
  go vet ./... || die "go vet failed"
  ok "gofmt and vet clean"

  step "running Go tests"
  # The suite needs the throwaway Postgres from docker compose. TEST_DATABASE_URL
  # comes from .env, which on this box points at the remapped port in
  # docker-compose.override.yml rather than the Makefile's default.
  if ! docker compose up -d db testdb >/dev/null 2>&1; then
    die "could not start the docker databases (needed for the tests). Use --skip-tests to bypass."
  fi
  docker compose exec -T testdb sh -c 'until pg_isready -U trivial -q; do sleep 0.5; done'
  go test ./... -count=1 || die "Go tests failed"
  ok "Go tests passed"

  step "running web tests"
  ( cd web && npm test --silent ) || die "web tests failed"
  ok "web tests passed"
}

do_deploy() {
  command -v go >/dev/null || die "go is not installed"
  command -v npm >/dev/null || die "npm is not installed"

  if [[ "$RUN_TESTS" == true ]]; then
    run_tests
  else
    warn "skipping tests (--skip-tests)"
  fi

  # The Vue bundle is compiled into the Go binary through web/dist and
  # //go:embed, so the order here is load-bearing: building Go first would embed
  # the previous bundle and quietly ship a stale front end.
  step "building the web bundle"
  ( cd web && npm run build ) || die "web build failed"
  ok "web bundle built"

  step "building the server binary"
  mkdir -p bin
  rm -f "$NEXT"
  go build -o "$NEXT" ./cmd/trivial || die "go build failed"
  ok "built $NEXT ($(du -h "$NEXT" | cut -f1))"

  # Everything above is reversible and touches nothing that is serving. From
  # here on the running server is affected, so this is the last safe moment to
  # have failed.
  stop_server

  if [[ -f "$BIN" ]]; then
    mv -f "$BIN" "$PREV"
  fi
  mv -f "$NEXT" "$BIN"

  # No separate migrate step: serve() runs db.Migrate on startup
  # (cmd/trivial/main.go), so the schema is brought up by the same binary that
  # is about to depend on it.
  start_server

  if wait_healthy; then
    ok "deployed $(git rev-parse --short HEAD 2>/dev/null || echo 'unknown revision')"
    printf '\n'
    do_status
    return 0
  fi

  warn "the new binary did not become healthy; rolling back"
  tail_log
  if [[ -f "$PREV" ]]; then
    stop_server
    mv -f "$BIN" "$NEXT"
    mv -f "$PREV" "$BIN"
    mv -f "$NEXT" "$PREV"
    start_server
    if wait_healthy; then
      die "deploy failed and was rolled back. The previous binary is serving again."
    fi
    tail_log
    die "deploy failed and the rollback did not come up either. Fix by hand."
  fi
  die "deploy failed and there is no previous binary to roll back to."
}

# --- main -------------------------------------------------------------------
load_env

case "$MODE" in
  status)   do_status ;;
  stop)     stop_server ;;
  rollback) do_rollback ;;
  deploy)   do_deploy ;;
esac
