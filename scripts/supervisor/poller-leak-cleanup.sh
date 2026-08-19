#!/bin/bash
# agent-supervisor#382: a safe, one-time cleanup for inbox-poll.sh processes
# that have leaked outside the estate's single-owner lock.
#
# WHAT LEAKS, MEASURED: not a double-dispatch of the production poller --
# both respawn paths (advance-live.sh's maybe_restart_poller and watchdog's
# poller-recover.sh backstop) end up executing THIS SAME inbox-poll.sh, which
# already refuses a second concurrent start via its own mkdir-based lock
# (see inbox-poll.sh's "SINGLE-INSTANCE IS ENFORCED HERE" header and
# acquire_lock). What actually accumulates, measured directly against the
# live estate while investigating #382, is REAL background processes started
# by test suites (test_advance_live.sh, test_digest.sh, and others) that
# spawn a long-lived inbox-poll.sh stand-in outside tmux/launchd control and
# whose cleanup trap either never ran (the suite itself was SIGKILLed by an
# outer timeout before its trap fired) or used a bare, unverified `pkill -f`
# that silently failed to match. watchdog.sh's own POLLER-DUPLICATE alarm
# (poller_process_rows, poller-lib.sh) already measures this population
# correctly and fails closed (#194: no fixture marker, no exclusion) -- this
# script acts on exactly what that alarm sees, never a separate guess.
#
# THE RULE THIS SCRIPT FOLLOWS: detect and report before it mutates. Default
# mode only lists what it found -- pid, start time, fixture-marker status,
# full command line -- and changes nothing. Only `--reap` acts, and even
# then only through reap_pid_verified (reap-verified.sh, #104): SIGTERM,
# wait, escalate to SIGKILL, confirm death, and REFUSE outright if a pid's
# own command line cannot be read or does not match what this script itself
# just observed it running -- the same "treat UNKNOWN as alive, never guess"
# posture the quota gate uses elsewhere in this estate. A pid it cannot
# positively confirm dead is reported as still alive, never assumed clean.
#
# THE ONE THING THIS SCRIPT MUST NEVER TOUCH: the live estate's actual
# production poller(s) -- the pid(s) currently holding inbox-poll.sh's own
# lock (STATE/.inbox-poll*.lock/pid). Those are excluded before anything
# else runs, by pid, not by path or name (a leaked test fixture can share a
# plausible-looking path; a lock holder cannot be faked without holding the
# lock).
#
# Usage:
#   poller-leak-cleanup.sh              # report only, changes nothing
#   poller-leak-cleanup.sh --reap       # report, then verified-reap every
#                                        # candidate that survives the
#                                        # lock-holder exclusion
#   poller-leak-cleanup.sh --reap --grace 10   # override the SIGTERM/SIGKILL
#                                        # grace period (default 5s each)
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./poller-lib.sh
. "$HERE/poller-lib.sh"
# shellcheck source=./reap-verified.sh
. "$HERE/reap-verified.sh"

STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"

REAP=0
GRACE=5
while [ $# -gt 0 ]; do
  case "$1" in
    --reap) REAP=1; shift ;;
    --grace) GRACE="${2:?--grace requires a value}"; shift 2 ;;
    -h|--help)
      sed -n '1,44p' "$0" | grep '^#' | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "poller-leak-cleanup.sh: unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

# --- who owns the lock right now: excluded by pid, never by path/name ------
#
# Checked against $STATE (SUPERVISOR_STATE, overridable) AND, unconditionally,
# the real default state directory -- never only the former. A caller that
# overrides SUPERVISOR_STATE for an isolated test run must not thereby make
# this script blind to the ACTUAL live estate's lock: the enumeration below
# (poller_process_rows) is machine-wide by construction, so an override that
# narrowed protection the same way would recreate exactly the failure mode
# the brief warns against -- "a reaper that kills a poller it mistakenly
# thinks is dead is worse than the leak." De-duplicated so a caller that
# never overrides SUPERVISOR_STATE does not scan the same directory twice.
DEFAULT_STATE="$HOME/.local/state/agent-dotfiles-supervisor"
state_dirs="$STATE"
[ "$STATE" != "$DEFAULT_STATE" ] && state_dirs="$state_dirs
$DEFAULT_STATE"

protected_pids=""
for state_dir in $state_dirs; do
for lockdir in "$state_dir"/.inbox-poll*.lock; do
  [ -d "$lockdir" ] || continue
  holder=$(cat "$lockdir/pid" 2>/dev/null)
  [ -n "$holder" ] || {
    echo "poller-leak-cleanup: $lockdir has no readable pid file -- cannot confirm its holder, excluding no pid on its behalf" >&2
    continue
  }
  if kill -0 "$holder" 2>/dev/null; then
    protected_pids="$protected_pids $holder"
    echo "poller-leak-cleanup: pid $holder holds $lockdir -- the live production poller, never a cleanup target"
  else
    echo "poller-leak-cleanup: $lockdir names pid $holder but it is not alive -- a stale lock, not a protection (inbox-poll.sh's own acquire_lock reclaims it, not this script)"
  fi
done
done

is_protected() {
  local pid="$1" p
  for p in $protected_pids; do
    [ "$p" = "$pid" ] && return 0
  done
  return 1
}

# --- enumerate: the exact same population watchdog.sh's alarm measures ----
rows=$(poller_process_rows)
rows_rc=$?
if [ "$rows_rc" -ne 0 ]; then
  echo "poller-leak-cleanup: could not measure live inbox-poll.sh processes (pgrep/ps unavailable, rc=$rows_rc) -- treating this as UNKNOWN, not as zero" >&2
  exit 2
fi

candidates=""
total=0
while IFS=$'\t' read -r pid start fixture cmd; do
  [ -n "$pid" ] || continue
  total=$((total + 1))
  if is_protected "$pid"; then
    continue
  fi
  marker_note="unmarked"
  [ "$fixture" = "1" ] && marker_note="marked test fixture"
  echo "poller-leak-cleanup: CANDIDATE pid $pid started ${start:-unknown} ($marker_note) -- $cmd"
  candidates="${candidates}${pid}	${cmd}
"
done <<<"$rows"

candidate_count=$(grep -c . <<<"$candidates" 2>/dev/null || true)
[ -z "$candidates" ] && candidate_count=0

if [ "$candidate_count" -eq 0 ]; then
  echo "poller-leak-cleanup: $total live inbox-poll.sh process(es) measured, 0 candidates after excluding the live lock holder(s) -- nothing to do"
  exit 0
fi

echo "poller-leak-cleanup: $candidate_count candidate(s) of $total live inbox-poll.sh process(es) measured"

if [ "$REAP" -ne 1 ]; then
  echo "poller-leak-cleanup: report only (pass --reap to actually terminate the candidates above); nothing was signaled"
  exit 0
fi

worst=0
while IFS=$'\t' read -r pid cmd; do
  [ -n "$pid" ] || continue
  # agent-supervisor#387: candidacy above was decided by a substring match on
  # this same $cmd text (poller_process_rows/POLLER_SERVICE_RE). Re-deriving
  # the "sandbox" from that same text and checking it against that same text
  # again (reap_pid_verified's own check, below) shares that one blind spot --
  # a process that merely carries "inbox-poll.sh" as an argument (`sleep 300
  # scripts/supervisor/inbox-poll.sh`, a shellcheck/vim/grep invocation) would
  # trivially satisfy both. poller_verify_executing answers from an
  # INDEPENDENT field -- `ps -o comm=`, what the kernel actually exec'd, plus
  # positional argv for the shebang-interpreter case -- never "does the
  # string appear anywhere on the line". Only a pid confirmed this way is
  # even offered to reap_pid_verified, whose own substring check then runs as
  # a second, belt-and-suspenders gate, not the only one.
  if ! poller_verify_executing "$pid"; then
    echo "reap: REFUSING to signal $pid -- not confirmed to be actually running inbox-poll.sh (ps -o comm= and its positional script argument disagree with the candidate match); command was '$cmd'" >&2
    worst=1
    continue
  fi
  sandbox=$(poller_script_token "$cmd")
  if [ -z "$sandbox" ]; then
    echo "reap: REFUSING to signal $pid -- could not re-derive its inbox-poll.sh path from '$cmd'" >&2
    worst=1
    continue
  fi
  reap_pid_verified "$pid" "$sandbox" "$GRACE"
  rc=$?
  [ "$rc" -gt "$worst" ] && worst=$rc
done <<<"$candidates"

exit "$worst"
