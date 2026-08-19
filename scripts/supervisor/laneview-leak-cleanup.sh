#!/bin/bash
# agent-supervisor#356: a safe, report-first cleanup for laneview tui.sh
# processes (scripts/supervisor/laneview/tui.sh and the python3 scratch
# file it mktemp's, see that script's own #356 comment) that have outlived
# whatever spawned them -- a human's popup, the tmux plugin's popup.sh, or
# a test suite's isolated tmux session.
#
# UNLIKE poller-leak-cleanup.sh (#382/#387), there is no single-owner lock
# to check a candidate against: laneview-tui is legitimately spawned by
# more than one human, more than once, concurrently, with no shared state
# file naming who "owns" a given invocation. That means this tool has no
# "protected pid" it can exclude the way poller-leak-cleanup.sh excludes
# the live lock holder -- every laneview-tui process this finds is a
# reap candidate, and `--reap` WILL terminate a session someone is
# actively looking at right now if run while one is open. That is a real,
# named limitation, not an oversight: report mode (the default) is the
# safety net -- read what it found before ever passing --reap.
#
# There is also no watchdog.sh alarm this shares detection logic with
# (poller-lib.sh exists because watchdog.sh's duplicate-poller alarm and
# poller-leak-cleanup.sh both needed the SAME population; no such alarm
# exists for laneview-tui today), so the enumeration below lives directly
# in this file rather than a shared lib with only one real caller.
#
# THE #387 LESSON, APPLIED HERE: a candidate is offered to `--reap` only
# after an INDEPENDENT confirmation of what the kernel actually exec'd
# (`ps -o comm=`), never by re-checking the same command-line substring
# that put it on the candidate list in the first place. A process that
# merely carries "laneview-tui" or "laneview/tui.sh" as a trailing
# argument (e.g. `grep -r laneview-tui.sh .`, `vim scripts/supervisor/
# laneview/tui.sh`) must never be reaped; only a process whose comm is a
# shell interpreter with tui.sh as its own positional script argument, or
# whose comm is a python interpreter with the mktemp'd scratch file as its
# own positional argument, qualifies.
#
# Usage:
#   laneview-leak-cleanup.sh              # report only, changes nothing
#   laneview-leak-cleanup.sh --reap       # report, then verified-reap
#                                          # every candidate found
#   laneview-leak-cleanup.sh --reap --grace 10   # override the
#                                          # SIGTERM/SIGKILL grace (default
#                                          # 5s each)
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./reap-verified.sh
. "$HERE/reap-verified.sh"

# Matches EITHER shape this leak takes: the bash process running tui.sh
# itself, or the python3 process running the scratch file tui.sh mktemp's
# (agent-supervisor#356's own template: "laneview-tui.XXXXXX", optionally
# still carrying the pre-fix literal ".py" suffix on a host that has not
# picked up the fix yet -- this tool must still be able to find and report
# those).
LANEVIEW_SERVICE_RE="${LANEVIEW_SERVICE_RE:-(^|/)laneview/tui\.sh( |$)|(^|/)laneview-tui\.[^ ]*}"

REAP=0
GRACE=5
while [ $# -gt 0 ]; do
  case "$1" in
    --reap) REAP=1; shift ;;
    --grace) GRACE="${2:?--grace requires a value}"; shift 2 ;;
    -h|--help)
      sed -n '1,45p' "$0" | grep '^#' | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "laneview-leak-cleanup.sh: unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

laneview_script_token() { # laneview_script_token <cmd> -- the bash-shape
  # token (a path ending in laneview/tui.sh) from <cmd>, or nothing.
  local cmd="$1" tok
  for tok in $cmd; do
    case "$tok" in
      */laneview/tui.sh|laneview/tui.sh)
        printf '%s' "$tok"
        return 0
        ;;
    esac
  done
}

laneview_py_token() { # laneview_py_token <cmd> -- the python-shape token
  # (a path whose basename starts with laneview-tui.) from <cmd>, or
  # nothing.
  local cmd="$1" tok base
  for tok in $cmd; do
    base="${tok##*/}"
    case "$base" in
      laneview-tui.*)
        printf '%s' "$tok"
        return 0
        ;;
    esac
  done
}

laneview_script_or_py_token() {
  local tok
  tok=$(laneview_script_token "$1"); [ -n "$tok" ] && { printf '%s' "$tok"; return 0; }
  tok=$(laneview_py_token "$1"); [ -n "$tok" ] && { printf '%s' "$tok"; return 0; }
  return 1
}

# laneview_verify_executing <pid> -- agent-supervisor#387's lesson, applied
# to this leak family: confirm from `ps -o comm=` (what the kernel actually
# exec'd) that <pid> is genuinely running tui.sh or its scratch python
# file, never merely carrying either string as an argument.
laneview_verify_executing() {
  local pid="$1" comm args tok saw_positional
  comm=$(ps -o comm= -p "$pid" 2>/dev/null)
  [ -n "$comm" ] || return 1
  case "$comm" in
    */bash|bash|*/sh|sh|*/dash|dash|*/zsh|zsh|*/ksh|ksh)
      args=$(ps -o args= -p "$pid" 2>/dev/null)
      [ -n "$args" ] || return 1
      saw_positional=0
      for tok in $args; do
        if [ "$saw_positional" -eq 0 ]; then
          saw_positional=1  # skip argv[0], the interpreter invocation itself
          continue
        fi
        case "$tok" in
          -*) continue ;;  # an interpreter flag, not the script
          */laneview/tui.sh|laneview/tui.sh) return 0 ;;
          *) return 1 ;;   # first positional token is some other script/arg
        esac
      done
      return 1
      ;;
    *[Pp]ython*)
      args=$(ps -o args= -p "$pid" 2>/dev/null)
      [ -n "$args" ] || return 1
      saw_positional=0
      for tok in $args; do
        if [ "$saw_positional" -eq 0 ]; then
          saw_positional=1
          continue
        fi
        case "$tok" in
          -*) continue ;;
          */laneview-tui.*|laneview-tui.*) return 0 ;;
          *) return 1 ;;
        esac
      done
      return 1
      ;;
    *) return 1 ;;
  esac
}

# --- enumerate: every live process matching either leaked shape ------------
command -v pgrep >/dev/null 2>&1 || { echo "laneview-leak-cleanup: pgrep unavailable -- cannot measure live processes, treating as UNKNOWN, not zero" >&2; exit 2; }
command -v ps >/dev/null 2>&1 || { echo "laneview-leak-cleanup: ps unavailable -- cannot measure live processes, treating as UNKNOWN, not zero" >&2; exit 2; }

candidates=""
total=0
while IFS= read -r pid; do
  [ -n "$pid" ] || continue
  cmd=$(ps -o command= -p "$pid" 2>/dev/null) || continue
  [[ "$cmd" =~ $LANEVIEW_SERVICE_RE ]] || continue
  total=$((total + 1))
  start=$(ps -o lstart= -p "$pid" 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
  [ -n "$start" ] || start=$(ps -o start= -p "$pid" 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
  echo "laneview-leak-cleanup: CANDIDATE pid $pid started ${start:-unknown} -- $cmd"
  candidates="${candidates}${pid}	${cmd}
"
done < <(pgrep -f 'laneview' 2>/dev/null || true)

candidate_count=$(grep -c . <<<"$candidates" 2>/dev/null || true)
[ -z "$candidates" ] && candidate_count=0

if [ "$candidate_count" -eq 0 ]; then
  echo "laneview-leak-cleanup: 0 live laneview-tui process(es) found -- nothing to do"
  exit 0
fi

echo "laneview-leak-cleanup: $candidate_count candidate(s) found"

if [ "$REAP" -ne 1 ]; then
  echo "laneview-leak-cleanup: report only (pass --reap to actually terminate the candidates above); nothing was signaled"
  exit 0
fi

worst=0
while IFS=$'\t' read -r pid cmd; do
  [ -n "$pid" ] || continue
  # Reviewed: reaping the bash-shape candidate can itself take the
  # python-shape one with it -- tui.sh's python3 child inherits the pane's
  # pty as its controlling terminal without its own setsid, so when the
  # bash session leader is SIGTERM'd, the kernel delivers SIGHUP to the
  # rest of that terminal's foreground process group, including python3
  # (observed live reaping agent-supervisor#356's own two orphans: both
  # scratch-python candidates were already gone by the time this loop
  # reached them). Checked here, before laneview_verify_executing, so
  # that side effect is reported as the harmless "already gone" it is,
  # never as a REFUSING that implies this pid looked suspicious.
  if ! kill -0 "$pid" 2>/dev/null; then
    echo "reap: $pid already gone" >&2
    continue
  fi
  if ! laneview_verify_executing "$pid"; then
    echo "reap: REFUSING to signal $pid -- not confirmed to be actually running laneview/tui.sh or its scratch python file (ps -o comm= and its positional script argument disagree with the candidate match); command was '$cmd'" >&2
    worst=1
    continue
  fi
  sandbox=$(laneview_script_or_py_token "$cmd")
  if [ -z "$sandbox" ]; then
    echo "reap: REFUSING to signal $pid -- could not re-derive its script path from '$cmd'" >&2
    worst=1
    continue
  fi
  reap_pid_verified "$pid" "$sandbox" "$GRACE"
  rc=$?
  [ "$rc" -gt "$worst" ] && worst=$rc
done <<<"$candidates"

exit "$worst"
