#!/usr/bin/env bash
# Drive the Director on a cadence. agent-supervisor#321.
#
# WHY THIS EXISTS. The watchdog's own prompt has said for weeks that the
# Director "runs in tmux director:1, self-driving on a 15-min loop". Checked
# on 2026-08-17 against reality: there is NO crontab entry, NO launchd job,
# and NO self-scheduled wakeup anywhere in its pane. It has never been
# self-driving. Every action it took in an eighteen-hour period was triggered
# by an external nudge -- the hourly watchdog tick, or heartbeat.sh's stall
# detector -- so the shape was: nudge, do exactly one thing, stop, silence.
# That is why "nothing is moving" was the recurring question.
#
# The description was intent, and it was read as fact for eighteen hours. It
# is the same failure this estate keeps hitting: a claim nobody measured.
#
# DIVISION OF LABOUR WITH heartbeat.sh -- read this before changing either.
# Two things poking one pane is a real hazard (a stranded prompt once sat in
# the input box of the lane that AUTHORED the PR it was told to merge), so
# the two have different jobs and different trigger conditions:
#
#   director-loop.sh  NORMAL CADENCE. Fires every LOOP_INTERVAL. Sends a tick
#                     ONLY when the pane is idle. This is what should keep the
#                     estate moving, and if it works the heartbeat never fires.
#   heartbeat.sh      BACKSTOP. Fires only on a real stall -- no ledger write
#                     for 1800s AND nothing in flight -- at most once an hour.
#
# The cadence (900s) is deliberately shorter than the stall threshold (1800s):
# in a healthy estate the loop always gets there first, and a heartbeat nudge
# means the LOOP failed, which is itself a signal worth having.
#
# NEVER SENDS TO A BUSY PANE. Text sent mid-turn sits in the input box until
# the turn ends -- that is the stranded-prompt defect, and a loop firing every
# 15 minutes would manufacture it at scale.
#
# launchd IS the loop. `--once` does one pass and exits, so a wedged tmux
# cannot leave a long-lived process that pgrep still calls alive.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
TARGET="${DIRECTOR_LOOP_TARGET:-director:@3}"
QUOTA_GATE="${QUOTA_GATE:-$HERE/quota.sh}"
# The interactive codexbar call measures ~12.5s and the gate's own default
# per-sample timeout is 15s, so samples trip over it intermittently and a
# SLOW instrument gets reported as a BROKEN one -- which halted the estate for
# two hours on 2026-08-17. Give it room; a real answer beats a fast UNKNOWN.
export QUOTA_GUARD_TIMEOUT_SECONDS="${QUOTA_GUARD_TIMEOUT_SECONDS:-45}"
export QUOTA_USAGE_TIMEOUT_SECONDS="${QUOTA_USAGE_TIMEOUT_SECONDS:-45}"

log() { printf '%s director-loop: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"; }

# --- quota gate. Fail closed on anything that is not a definite SAFE.
"$QUOTA_GATE" check >/dev/null 2>&1
qrc=$?
case "$qrc" in
  0) ;;
  1) log "quota WIND DOWN -- not ticking the Director"; exit 0 ;;
  *) log "quota UNKNOWN (rc=$qrc) -- never treated as safe, not ticking"; exit 0 ;;
esac

if ! tmux has-session -t "${TARGET%%:*}" 2>/dev/null; then
  log "target session ${TARGET%%:*} does not exist -- a human should look"
  exit 3
fi

# Busy check is the LAST NON-BLANK LINE only, never a scrollback sweep. A
# whole-pane grep matches the phrase `esc to interrupt` wherever it appears --
# including inside a previous nudge's own text, which is exactly how
# heartbeat.sh silently disabled itself for four readings on 2026-08-17.
pane=$(tmux capture-pane -p -t "=$TARGET" 2>/dev/null | grep -v '^[[:space:]]*$')
if [ -z "$pane" ]; then
  log "could not capture $TARGET -- not ticking"
  exit 3
fi
if tail -1 <<<"$pane" | grep -q 'esc to interrupt'; then
  log "Director is working -- not interrupting"
  exit 0
fi

# A non-empty input box means a previous send is sitting unsubmitted. Do NOT
# add to it: retyping over stranded text is how a half-message gets sent.
box=$(tail -1 <<<"$pane")
case "$box" in
  *'❯'*)
    if [ "$(printf '%s' "$box" | sed 's/.*❯ *//' | tr -d '[:space:]')" != "" ]; then
      log "input box is NOT empty -- refusing to type over a stranded prompt; a human should look"
      exit 4
    fi
    ;;
esac

TICK='Director tick. Keep this turn SHORT -- act, do not plan. A turn that runs long with no tool calls is the failure mode that killed the previous session.

1. QUOTA GATE FIRST, with the timeouts the instrument actually needs:
   QUOTA_GUARD_TIMEOUT_SECONDS=45 QUOTA_USAGE_TIMEOUT_SECONDS=45 bash scripts/supervisor/quota.sh check
   0 proceed, 1 wind down and go quiet, 2/3 UNKNOWN -- never treat UNKNOWN as safe.

2. Read the standing brief rather than re-deriving it: ~/.local/state/agent-dotfiles-supervisor/PHASES.md and NOTEBOOK-jon-directives.md.

3. Do ONE thing, then STOP and report one line. One PR per turn. Do not fan out -- the WEEKLY quota is the binding constraint, not the session window.

Priority order, unless GitHub state says otherwise -- re-derive from live state, do not trust this list blindly:
  #320 (non-blocking dispatch -- fixes the 900s defect that has been destroying lanes all day)
  #316 (lane classifier -- without it every idle lane reads unknown and capacity silently hits ZERO)
  #317, #308, then 251, 205.

Standing traps, all measured, all cost real time today:
  - A merged fix is NOT a deployed fix. Check live/ (#310).
  - A dispatch timeout may be hidden SUCCESS -- check the ledger and GitHub, never the exit code (#278).
  - gh pr review --approve CANNOT work here; GitHub rejects it because every lane authenticates as jonhill90. Post evidence as a comment and merge.
  - Run checks synchronously. A lane that backgrounds its work gets recorded complete having delivered nothing.

If there is genuinely nothing worth doing, say so in one line and stop. Concluding "nothing" is a valid tick.'

tmux send-keys -t "=$TARGET" C-u 2>/dev/null
sleep 1
tmux send-keys -t "=$TARGET" -l "$TICK" 2>/dev/null
sleep 2
tmux send-keys -t "=$TARGET" Enter 2>/dev/null
sleep 6

# Verify it ARRIVED, not merely that it was sent. "Delivered" is a claim about
# someone else's state; if you did not ask them, do not say it.
if tmux capture-pane -p -t "=$TARGET" 2>/dev/null | grep -q 'esc to interrupt'; then
  log "ticked $TARGET -- pane is now working"
  exit 0
fi
log "ticked $TARGET but the pane did NOT start working -- a human should look"
exit 1
