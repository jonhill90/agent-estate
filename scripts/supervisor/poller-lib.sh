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
