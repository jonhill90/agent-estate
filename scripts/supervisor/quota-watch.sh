#!/usr/bin/env bash
# The other half of the quota gate: watch for the window COMING BACK.
#
# WHY. quota.sh detects the wind-down and the estate goes quiet -- that half
# works and was proven on 2026-08-16. But nothing watched for the recovery, so
# when the session window reset the estate stayed idle until a human noticed.
# Jon: "quota is back any reason there was not watchdog or something to trigger
# off that. i thought we were watching for it."
#
# A PAUSED AGENT CANNOT POLL FOR ITS OWN RECOVERY. That was the design error.
# The supervisor was told to "re-arm a cheap poll", but the thing that has stood
# down is the least reliable thing to trust with restarting itself -- it is
# idle, it may hold a stranded prompt, and its loop may not re-arm at all. The
# watcher must live OUTSIDE the thing it restarts. Same principle as
# watchdog.sh, which runs from a LaunchAgent so it survives the loop dying.
#
# NO MODEL IN THIS PATH. Polling a number and sending one message when it
# crosses a threshold is not reasoning. Jon's rule: build the tool.
#
# It sends EXACTLY ONE message on the 1->0 transition, never on every poll --
# a stand-down (or a start-up) is one message, sent once, to one place.
#
# Usage:
#   quota-watch.sh [--interval SECONDS] [--target SESSION:WINDOW] [--once]
#   nohup bash scripts/supervisor/quota-watch.sh >>~/.local/state/agent-dotfiles-supervisor/quota-watch.log 2>&1 &
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
INTERVAL="${QUOTA_WATCH_INTERVAL:-300}"
TARGET="${QUOTA_WATCH_TARGET:-agent-supervisor:@1}"
ONCE=0
STAMP="$STATE/.quota-watch.state"

while [ $# -gt 0 ]; do
  case "$1" in
    --interval) INTERVAL="${2:-300}"; shift 2 ;;
    --target)   TARGET="${2:-}"; shift 2 ;;
    --once)     ONCE=1; shift ;;
    *) shift ;;
  esac
done

log() { printf '%s quota-watch: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"; }

# Send is C-u then retype then Enter, ALWAYS. Enter alone does not submit text a
# previous send-keys left in the input box -- that defect stranded seven panes a
# tick for a full day before it was understood.
send_resume() {
  local msg="$1"
  tmux send-keys -t "=$TARGET" C-u 2>/dev/null || return 1
  sleep 1
  tmux send-keys -t "=$TARGET" -l "$msg" 2>/dev/null || return 1
  sleep 2
  tmux send-keys -t "=$TARGET" Enter 2>/dev/null || return 1
  sleep 6
  # Verify it ARRIVED, not merely that it was sent. "Delivered" is a claim about
  # someone else's state; if you did not ask them, do not say it.
  tmux capture-pane -p -t "=$TARGET" 2>/dev/null | grep -q 'esc to interrupt'
}

RESUME_MSG='QUOTA IS BACK -- the session window has reset and quota.sh check returns SAFE. This is an automatic wake-up from quota-watch.sh, not a human. Resume work now.

Priority order, do not reorder it: (1) the three skills PRs awaiting review -- skills#193 determine-intent, #194 devils-advocate, #196 determine-signals; (2) skills174-mine-transcripts, which is the longest-outstanding thing Jon has asked for and has still produced no output; (3) the remainder of the docs and grounding sweep; (4) Phase 4b app shell work in agent-tui.

Read ~/.local/state/agent-dotfiles-supervisor/QUOTA-HANDOFF.md for what was in flight when the estate stood down.

YOUR LOOP TICK MUST START WITH: bash scripts/supervisor/quota.sh check -- exit 0 proceed, exit 1 wind down and go quiet, exit 2 or 3 quota is UNKNOWN so say so and do not treat it as safe. The instruction "never call stop, always re-arm" is RETIRED; re-arming into an exhausted window is what burned $80 of usage credits down to $8. Do not reinstate it.

Routing note: codex is at 100% weekly with zero credits and copilot at 97.1%. Both are exhausted for the week. Claude is the only harness with real capacity.'

log "watching every ${INTERVAL}s, target $TARGET"
prev=""
[ -f "$STAMP" ] && prev="$(cat "$STAMP" 2>/dev/null)"

while :; do
  bash "$HERE/quota.sh" check >/dev/null 2>&1
  rc=$?   # captured directly -- never through a pipe
  case "$rc" in
    0) state=SAFE ;;
    1) state=WINDDOWN ;;
    *) state=UNKNOWN ;;
  esac

  if [ "$state" = "SAFE" ] && [ "$prev" != "SAFE" ] && [ -n "$prev" ]; then
    log "transition $prev -> SAFE; sending ONE resume to $TARGET"
    if send_resume "$RESUME_MSG"; then
      log "resume delivered and the pane is working"
    else
      log "resume did NOT take -- pane is not working after send; a human should look"
    fi
  elif [ "$state" != "$prev" ]; then
    log "state $prev -> $state (no resume sent)"
  fi

  printf '%s' "$state" > "$STAMP"
  prev="$state"
  [ "$ONCE" = "1" ] && break
  sleep "$INTERVAL"
done
