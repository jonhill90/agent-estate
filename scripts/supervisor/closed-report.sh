#!/usr/bin/env bash
# Tell Jon on Telegram which issues CLOSED in the last window. Every 30 minutes.
#
# WHY. Jon, 2026-08-18: "Every 30 minutes you send me the issues closed on
# Telegram. Not excuses."
#
# The second sentence is the specification, not decoration. Every reporting
# surface this estate has built so far drifted into narrating effort -- what was
# attempted, what was blocked, why a thing did not happen. This one reports
# exactly one fact: which issues went from open to closed since the last run,
# across every repo. Nothing else is in scope. A window in which nothing closed
# says so in one line and stops.
#
# NO MODEL IN THIS PATH. It reads GitHub and sends a message.
#
# HONEST ZERO vs BLIND ZERO. "Nothing closed" and "I could not tell" are
# different messages and this never conflates them (see CONTRIBUTING.md, "verify
# the instrument before you believe the verdict"). A repo whose query fails is
# named as unreadable in the message; it is never silently counted as zero,
# because a silent zero is exactly how an estate looks healthy while broken.
#
# STATE, and why it is a timestamp rather than a set of issue numbers: the
# window is [last successful send, now]. Recording the send time means a failed
# send does not lose the issues -- the next run's window still covers them, so
# a Telegram outage delays the report instead of eating it.
#
# #251/#205: every `gh` call here is bounded (`with_timeout`, quota.sh's
# pattern -- background the call, poll `kill -0`, TERM then KILL past the
# deadline, no external `timeout`/`gtimeout` binary required). Each repo's
# call is bounded on its own, not one timeout around the whole loop, so one
# stuck repo times out and the rest still report.
#
# #364: `gh issue list`'s default order is issue-number-descending, not
# closedAt-descending. The original version fetched `--state closed --limit
# 40` and filtered by `closedAt > since` in code, which silently dropped an
# old, low-numbered issue that closed inside the window once a repo had 40+
# higher-numbered closed issues -- no UNREADABLE marker, nothing; it just
# wasn't in the output. The fix lets GitHub's search do the narrowing
# server-side (`--search "closed:>=<since>"`) instead of relying on a
# fixed-count `--limit` over an unrelated sort order. `--limit` is still
# passed, but only as a loud backstop for a window that genuinely closed an
# implausible number of issues (see FETCH_LIMIT below), not as the mechanism
# that decides which issues are in scope.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
STAMP="$STATE/.closed-report.since"
WINDOW_MINUTES="${CLOSED_REPORT_WINDOW_MINUTES:-30}"
REPOS="${CLOSED_REPORT_REPOS:-jonhill90/agent-supervisor jonhill90/agent-dotfiles jonhill90/agent-tui jonhill90/skills jonhill90/agent-evals jonhill90/Hill90 jonhill90/hill90-app}"
GH_TIMEOUT_SECONDS="${CLOSED_REPORT_GH_TIMEOUT_SECONDS:-20}"
# A safety backstop, not the narrowing mechanism (see #364 note above). The
# `closed:>=since` search qualifier does the actual filtering server-side;
# this just bounds the response size for a window that closed an implausible
# number of issues, and is reported loudly (not truncated silently) if hit.
FETCH_LIMIT="${CLOSED_REPORT_FETCH_LIMIT:-200}"
# Send even when nothing closed? Default NO. Jon asked for the issues, and a
# recurring "0 closed" every 30 minutes is precisely the kind of message that
# trains a reader to stop opening them -- the failure heartbeat.sh already
# caused once by paging three times in seven hours for one unchanging cause.
SEND_EMPTY="${CLOSED_REPORT_SEND_EMPTY:-0}"
DRY="${CLOSED_REPORT_DRY:-0}"

while [ $# -gt 0 ]; do
  case "$1" in
    --dry)          DRY=1; shift ;;
    --send-empty)   SEND_EMPTY=1; shift ;;
    --window)       WINDOW_MINUTES="${2:-30}"; shift 2 ;;
    --since)        SINCE_OVERRIDE="${2:-}"; shift 2 ;;
    *) shift ;;
  esac
done

log() { printf '%s closed-report: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"; }

# with_timeout SECONDS OUTFILE CMD... -- quota.sh's pattern (#264), copied
# rather than sourced so this script has no import-time dependency on
# quota.sh. Backgrounds CMD, polls `kill -0` at 1s granularity, TERM then
# KILL once SECONDS have elapsed. CMD's stdout lands in OUTFILE so the exit
# code read afterward is CMD's own, never a pipe's. Returns 124 on timeout.
with_timeout() {
  local secs="$1" outfile="$2"; shift 2
  "$@" >"$outfile" 2>/dev/null &
  local pid=$! waited=0 timed_out=0
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      timed_out=1
      kill -TERM "$pid" 2>/dev/null
      sleep 1
      kill -KILL "$pid" 2>/dev/null
      break
    fi
    sleep 1
    waited=$((waited + 1))
  done
  wait "$pid" 2>/dev/null
  local rc=$?
  [ "$timed_out" -eq 1 ] && return 124
  return "$rc"
}

mkdir -p "$STATE" 2>/dev/null

now_epoch=$(date -u +%s)
if [ -n "${SINCE_OVERRIDE:-}" ]; then
  since_iso="$SINCE_OVERRIDE"
elif [ -f "$STAMP" ] && since_iso="$(cat "$STAMP" 2>/dev/null)" && [ -n "$since_iso" ]; then
  :
else
  # First ever run: use the nominal window rather than reporting the entire
  # history of the estate, which would be a 500-line message nobody reads.
  since_iso="$(date -u -r $((now_epoch - WINDOW_MINUTES * 60)) '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null \
            || date -u -d "@$((now_epoch - WINDOW_MINUTES * 60))" '+%Y-%m-%dT%H:%M:%SZ')"
fi
now_iso="$(date -u -r "$now_epoch" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null \
        || date -u -d "@$now_epoch" '+%Y-%m-%dT%H:%M:%SZ')"

log "window ${since_iso} .. ${now_iso}"

lines=""; total=0; unreadable=""
for repo in $REPOS; do
  short="${repo#*/}"
  gh_out=$(mktemp "${TMPDIR:-/tmp}/closed-report-gh.XXXXXX") || {
    log "$repo UNREADABLE -- could not create a scratch file; NOT counted as zero"
    unreadable="${unreadable}${short} "
    continue
  }
  # #364: the `closed:>=since` search qualifier does the narrowing -- GitHub
  # returns only issues that closed inside the window, not an arbitrary
  # top-N-by-issue-number slice that a fixed `--limit` would produce.
  # `--limit` here only bounds an implausibly large result (see FETCH_LIMIT
  # above).
  with_timeout "$GH_TIMEOUT_SECONDS" "$gh_out" \
    gh issue list --repo "$repo" --state closed --search "closed:>=${since_iso}" \
      --limit "$FETCH_LIMIT" --json number,title,closedAt
  gh_rc=$?
  raw=$(cat "$gh_out" 2>/dev/null)
  rm -f "$gh_out"
  if [ "$gh_rc" -eq 124 ]; then
    log "$repo UNREADABLE -- TIMEOUT after ${GH_TIMEOUT_SECONDS}s waiting on gh issue list; NOT counted as zero"
    unreadable="${unreadable}${short} "
    continue
  fi
  if [ -z "$raw" ]; then
    log "$repo UNREADABLE -- gh returned nothing; NOT counted as zero"
    unreadable="${unreadable}${short} "
    continue
  fi
  # NOTE: rc is captured from the python process itself, never after a pipe.
  # ANY nonzero -- a JSON failure, a SyntaxError in this very snippet, an
  # interpreter that is not there -- is UNREADABLE, never zero. The first draft
  # of this script special-cased one exit code and a SyntaxError fell through
  # the gap and was reported as "every repo read cleanly", which is the precise
  # blind-zero failure the header above warns about. Caught on its own dry run.
  got=$(printf '%s' "$raw" | SINCE="$since_iso" REPO="$short" LIMIT="$FETCH_LIMIT" python3 -c '
import json, os, sys
since = os.environ["SINCE"]; repo = os.environ["REPO"]; limit = int(os.environ["LIMIT"])
rows = json.load(sys.stdin)
# Defense in depth: the search qualifier already restricts to closedAt >=
# since, but re-check in code rather than trust the query string blindly.
out = [r for r in rows if (r.get("closedAt") or "") > since]
out.sort(key=lambda r: r["closedAt"])
if len(rows) >= limit:
    sys.stderr.write("closed-report: %s hit FETCH_LIMIT=%d -- window may be truncated\n" % (repo, limit))
for r in out:
    t = r["title"]
    if len(t) > 90:
        t = t[:87] + "..."
    print("%s#%s  %s" % (repo, r["number"], t))
')
  rc=$?
  if [ "$rc" -ne 0 ]; then
    log "$repo UNREADABLE -- the filter exited $rc; NOT counted as zero"
    unreadable="${unreadable}${short} "
    continue
  fi
  if [ -n "$got" ]; then
    n=$(printf '%s\n' "$got" | grep -c .)
    total=$((total + n))
    lines="${lines}${got}
"
  fi
done

if [ "$total" -eq 0 ] && [ -z "$unreadable" ] && [ "$SEND_EMPTY" != "1" ]; then
  log "nothing closed in the window and every repo read cleanly -- not sending"
  printf '%s' "$now_iso" > "$STAMP"
  exit 0
fi

if [ "$total" -eq 1 ]; then subject="1 issue closed"; else subject="${total} issues closed"; fi

body="Closed since ${since_iso}:

${lines}"
[ "$total" -eq 0 ] && body="Nothing closed since ${since_iso}."
if [ -n "$unreadable" ]; then
  # Named explicitly. An unreadable repo is not a zero, and the whole point of
  # saying so is that the count above is a FLOOR, not a total.
  body="${body}
Could not read: ${unreadable}-- the count above is a floor, not a total."
  subject="${subject} (+unread repos)"
fi

if [ "$DRY" = "1" ]; then
  printf '=== %s ===\n%s\n' "$subject" "$body"
  exit 0
fi

if AGENT_NOTIFY_CALLER=supervisor "$HERE/notify.sh" "$subject" "$body" >/dev/null 2>&1; then
  log "sent -- ${total} closed"
  # Advance the window ONLY on a successful send, so a failed send delays the
  # report rather than losing the issues it was going to name.
  printf '%s' "$now_iso" > "$STAMP"
  exit 0
fi
log "FAILED to send -- window NOT advanced; the next run still covers these issues"
exit 1
