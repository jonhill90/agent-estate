#!/bin/bash
# agent-supervisor#382: the inbox-poll.sh process-detection logic that
# watchdog.sh's duplicate-poller alarm (agent-supervisor#18/#147/#194) relied
# on, pulled out so a second, independent tool -- poller-leak-cleanup.sh --
# can enumerate the exact same population instead of hand-rolling its own
# `pgrep -f inbox-poll.sh` and silently drifting from the alarm's definition
# of "suspicious". Two detectors that disagree about what a duplicate poller
# even is are worse than one: a cleanup tool that sees a narrower set than
# the alarm leaves processes behind that keep paging; a cleanup tool that
# sees a wider set risks reaping something the alarm never flagged. Sourced
# by both watchdog.sh and poller-leak-cleanup.sh; not meant to be run
# directly.
#
# THIS FILE HAS NO OPINION ON WHAT TO DO WITH A ROW. watchdog.sh's own
# check_poller_process_count still decides "does a fixture marker silence
# the alarm" (#194's fail-closed answer: no marker, no exclusion, alert
# fires). poller-leak-cleanup.sh makes an independent, harsher call for its
# own purpose: a marker proves "this was a genuine test fixture", never "it
# is still someone's business to leave running" -- so it treats every row
# poller_process_rows returns as a reap candidate, marked or not, only
# skipping the one thing neither the alarm nor the cleanup tool may ever
# touch: the live production poller holding STATE's own lock (see
# poller-leak-cleanup.sh's `poller_lock_holder_pids`).
POLLER_SERVICE_RE="${LANES_SERVICE_RE:-(^|/)inbox-poll\.sh( |$)}"

poller_script_token() { # poller_script_token <cmd> -- prints the exact
  # inbox-poll.sh path token from <cmd> (a wrapper like "/bin/bash <path>
  # --flag" still has the script path as its own token, not necessarily the
  # last one), or nothing if none of <cmd>'s whitespace-separated tokens
  # names an inbox-poll.sh path at all.
  local cmd="$1" tok
  for tok in $cmd; do
    case "$tok" in
      */inbox-poll.sh|inbox-poll.sh)
        printf '%s' "$tok"
        return 0
        ;;
    esac
  done
}

poller_fixture_marker() { # poller_fixture_marker <cmd> -- prints the marker
  # path a genuine watchdog test fixture at this script path would carry, or
  # nothing if <cmd> names no inbox-poll.sh path (see poller_script_token).
  local cmd="$1" tok dir
  tok=$(poller_script_token "$cmd")
  [ -n "$tok" ] || return 0
  case "$tok" in
    */inbox-poll.sh) dir="${tok%/inbox-poll.sh}" ;;
    *)                dir="." ;;
  esac
  printf '%s/.watchdog-test-fixture' "$dir"
}

poller_is_verified_fixture() { # poller_is_verified_fixture <cmd>
  local marker
  marker=$(poller_fixture_marker "$1")
  [ -n "$marker" ] && [ -f "$marker" ]
}

# poller_verify_executing <pid> -- agent-supervisor#387: an independent
# confirmation that <pid> is ACTUALLY RUNNING inbox-poll.sh as its executed
# program, not merely carrying the string somewhere on its command line.
# poller_process_rows/POLLER_SERVICE_RE and poller_script_token both work by
# scanning the SAME `ps -o command=` text for a token that looks like
# inbox-poll.sh -- a candidate list built that way and a "confirmation" built
# the same way share one blind spot: `sleep 300 scripts/supervisor/inbox-poll.sh`
# or `vim scripts/supervisor/inbox-poll.sh` both carry a token matching
# */inbox-poll.sh without ever executing it. This function answers a
# different question, from a different field: `ps -o comm=` names what the
# kernel actually exec'd, never argv. That resolves to a shell interpreter
# for a script run via a shebang (e.g. /bin/bash), so it is not enough on its
# own -- when comm names an interpreter, the check requires inbox-poll.sh to
# be that interpreter's own script argument (the first non-flag positional
# token), never "anywhere in the line". A pid whose comm is `sleep` or `vim`
# fails outright: those are never interpreters, so no positional check even
# runs.
poller_verify_executing() {
  local pid="$1" comm args tok saw_positional
  comm=$(ps -o comm= -p "$pid" 2>/dev/null)
  [ -n "$comm" ] || return 1
  case "$comm" in
    */inbox-poll.sh|inbox-poll.sh) return 0 ;;
  esac
  case "$comm" in
    */bash|bash|*/sh|sh|*/dash|dash|*/zsh|zsh|*/ksh|ksh) ;;
    *) return 1 ;;
  esac
  args=$(ps -o args= -p "$pid" 2>/dev/null)
  [ -n "$args" ] || return 1
  saw_positional=0
  for tok in $args; do
    if [ "$saw_positional" -eq 0 ]; then
      saw_positional=1  # skip argv[0], the interpreter invocation itself
      continue
    fi
    case "$tok" in
      -*) continue ;;  # an interpreter flag (e.g. -x, -c), not the script
      */inbox-poll.sh|inbox-poll.sh) return 0 ;;
      *) return 1 ;;   # first positional token is some other script/arg
    esac
  done
  return 1
}

# poller_process_rows -- one row per LIVE process whose command line matches
# POLLER_SERVICE_RE, after excluding a poller's own same-pgid child (the one
# legitimate child every poller forks -- see watchdog.sh's #147 comment for
# why parentage is checked BEFORE anything path- or marker-based). Columns,
# tab-separated: pid, start, fixture (1 if poller_is_verified_fixture, else
# 0), cmd. Every surviving row is included regardless of fixture status --
# callers decide what that column means for their own purpose.
poller_process_rows() {
  command -v pgrep >/dev/null 2>&1 || return 2
  command -v ps >/dev/null 2>&1 || return 2
  local pid cmd start ppid pgid records fixture skip parent_pid parent_ppid parent_pgid parent_start parent_cmd
  while IFS= read -r pid; do
    [ -n "$pid" ] || continue
    cmd=$(ps -o command= -p "$pid" 2>/dev/null) || continue
    [[ "$cmd" =~ $POLLER_SERVICE_RE ]] || continue
    ppid=$(ps -o ppid= -p "$pid" 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//') || ppid=""
    pgid=$(ps -o pgid= -p "$pid" 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//') || pgid=""
    start=$(ps -o lstart= -p "$pid" 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    [ -n "$start" ] || start=$(ps -o start= -p "$pid" 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    records="${records:-}${pid}	${ppid:-unknown}	${pgid:-unknown}	${start:-unknown}	${cmd}
"
  done < <(pgrep -f inbox-poll.sh 2>/dev/null || true)
  while IFS=$'\t' read -r pid ppid pgid start cmd; do
    [ -n "$pid" ] || continue
    skip=0
    while IFS=$'\t' read -r parent_pid parent_ppid parent_pgid parent_start parent_cmd; do
      [ -n "$parent_pid" ] || continue
      if [ "$ppid" = "$parent_pid" ] && [ "$pgid" != "unknown" ] && [ "$pgid" = "$parent_pgid" ]; then
        skip=1
        break
      fi
    done <<<"${records:-}"
    [ "$skip" -eq 1 ] && continue
    fixture=0
    poller_is_verified_fixture "$cmd" && fixture=1
    printf '%s\t%s\t%s\t%s\n' "$pid" "${start:-unknown}" "$fixture" "$cmd"
  done <<<"${records:-}"
  return 0
}
