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
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
STAMP="$STATE/.closed-report.since"
WINDOW_MINUTES="${CLOSED_REPORT_WINDOW_MINUTES:-30}"
REPOS="${CLOSED_REPORT_REPOS:-jonhill90/agent-supervisor jonhill90/agent-dotfiles jonhill90/agent-tui jonhill90/skills jonhill90/agent-evals jonhill90/Hill90 jonhill90/hill90-app}"
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
  # --limit 40 is well above any plausible 30-minute close rate; a window that
  # genuinely closed 40 issues is worth truncating loudly rather than paging
  # silently through the API (see #319, digest.sh's rate-limit burn).
  raw=$(gh issue list --repo "$repo" --state closed --limit 40 \
          --json number,title,closedAt 2>/dev/null)
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
  got=$(printf '%s' "$raw" | SINCE="$since_iso" REPO="$short" python3 -c '
import json, os, sys
since = os.environ["SINCE"]; repo = os.environ["REPO"]
rows = json.load(sys.stdin)
out = [r for r in rows if (r.get("closedAt") or "") > since]
out.sort(key=lambda r: r["closedAt"])
for r in out:
    t = r["title"]
    if len(t) > 90:
        t = t[:87] + "..."
    print("%s#%s  %s" % (repo, r["number"], t))
' 2>/dev/null)
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
