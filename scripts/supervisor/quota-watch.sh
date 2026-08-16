#!/usr/bin/env bash
# The other half of the quota gate: watch for the window COMING BACK --
# and, since agent-supervisor#261, watch for the window CLOSING too.
#
# WHY. quota.sh detects the wind-down and dispatch.sh's own gate (#227) already
# refuses to hand out new work once it does -- that half works and was proven
# on 2026-08-16. But the estate was still autonomous in one direction only:
# nothing acted on SAFE -> WINDDOWN itself, so the in-flight lane kept running
# until a human noticed, or until a supervisor tick happened to land on the
# gate at the right moment. Measured: the transition logged at 09:40:13 and
# nothing stood the lane down. That asymmetry is what already cost money once
# -- re-arming into an exhausted window burned $80 of usage credits down to
# $8 -- and it is exactly as dangerous left un-set as it was left un-armed.
#
# A PAUSED AGENT CANNOT POLL FOR ITS OWN RECOVERY, and a RUNNING agent that
# is about to lose its window cannot be trusted to notice in time either --
# it is mid-task, and the thing telling it to stop must not be the thing
# that is about to run out. The watcher must live OUTSIDE the thing it
# starts and stops. Same principle as watchdog.sh, which runs from a
# LaunchAgent so it survives the loop dying.
#
# NO MODEL IN THIS PATH. Polling a number and sending one message when it
# crosses a threshold is not reasoning. Jon's rule: build the tool.
#
# It sends EXACTLY ONE message per transition -- one on WINDDOWN -> SAFE
# (resume), one on SAFE -> WINDDOWN (stand-down) -- never on every poll, and
# never on a sample that could not be read as one or the other. A stand-down
# repeated is a conversation, which is the thing forbidden.
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
# Overridable so tests can point this at a deterministic stub instead of the
# real quota.sh (which calls out to codexbar) -- same pattern dispatch.sh
# uses for its own QUOTA_GATE.
QUOTA_GATE="${QUOTA_GATE:-$HERE/quota.sh}"

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
# tick for a full day before it was understood. Shared by both directions --
# a resume and a stand-down are the same delivery mechanism, one message.
send_message() {
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

# agent-supervisor#261: the other direction. Sent once, on a CONFIRMED
# SAFE -> WINDDOWN transition only (never from UNKNOWN, never repeated).
# It asks for exactly what a human has been doing by hand: finish the step
# in progress, commit, push, say what is done and the next action, stop.
# It does NOT ask the pane to kill anything mid-turn -- interrupting a turn
# is the one thing a quota boundary must not do, because unpushed work is
# the only thing it actually destroys. Further dispatch is already blocked
# independently by dispatch.sh's own quota gate (#227), which every dispatch
# call re-checks for itself -- this message does not need to enforce that.
STANDDOWN_MSG='QUOTA IS WINDING DOWN -- the session window is running out and quota.sh check now returns WIND DOWN. This is an automatic stand-down from quota-watch.sh, not a human, and it is sent exactly once.

Finish the step you are on -- do not stop mid-turn or leave anything half-written. Then, in order: commit, push your branch, and post ONE comment on the issue or PR saying what is done and the exact next action for whoever (or whatever) picks this back up. That comment is the whole stand-down message -- do not turn it into a conversation.

Once pushed and commented, go quiet. Do not schedule your own wakeup for this. dispatch.sh already refuses new work while the window is down (agent-supervisor#227), and quota-watch.sh is watching for the window to come back -- it will send exactly one resume when quota.sh check returns SAFE again. Re-arming yourself into an exhausted window is what burned $80 of usage credits down to $8; do not do that.'

log "watching every ${INTERVAL}s, target $TARGET"
prev=""
[ -f "$STAMP" ] && prev="$(cat "$STAMP" 2>/dev/null)"

while :; do
  bash "$QUOTA_GATE" check >/dev/null 2>&1
  rc=$?   # captured directly -- never through a pipe
  case "$rc" in
    0) state=SAFE ;;
    1) state=WINDDOWN ;;
    *) state=UNKNOWN ;;   # 2, 3, 127, and anything unrecognised -- fail closed
  esac

  if [ "$state" = "SAFE" ] && [ "$prev" != "SAFE" ] && [ -n "$prev" ]; then
    log "transition $prev -> SAFE; sending ONE resume to $TARGET"
    if send_message "$RESUME_MSG"; then
      log "resume delivered and the pane is working"
    else
      log "resume did NOT take -- pane is not working after send; a human should look"
    fi
  elif [ "$state" = "WINDDOWN" ] && [ "$prev" = "SAFE" ]; then
    # Only a CONFIRMED prior SAFE counts as the "before" side of this
    # transition. A sample that came back UNKNOWN never sets prev=SAFE (see
    # the case above), so a WINDDOWN seen after an unreadable sample does not
    # fire here -- "cannot tell" must not become "wind down" any more than it
    # may become "safe". Re-running this exact transition is prevented the
    # same way the resume is: prev is already WINDDOWN by the next poll, so
    # this branch does not match again until another confirmed SAFE reading
    # comes back first.
    log "transition $prev -> WINDDOWN; sending ONE stand-down to $TARGET"
    if send_message "$STANDDOWN_MSG"; then
      log "stand-down delivered; dispatch.sh's quota gate now refuses further dispatch"
    else
      log "stand-down did NOT take -- pane did not confirm receipt; a human should look"
    fi
  elif [ "$state" != "$prev" ]; then
    if [ "$state" = "UNKNOWN" ]; then
      log "state $prev -> UNKNOWN -- cannot tell if the window is safe or down; no message sent"
    else
      log "state $prev -> $state (no message sent)"
    fi
  fi

  printf '%s' "$state" > "$STAMP"
  prev="$state"
  [ "$ONCE" = "1" ] && break
  sleep "$INTERVAL"
done
