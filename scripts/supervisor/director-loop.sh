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

SESSION="${TARGET%%:*}"
if ! tmux has-session -t "$SESSION" 2>/dev/null; then
  log "target session $SESSION does not exist -- a human should look"
  exit 3
fi

# RESOLVE THE WINDOW, do not trust the id in the default.
#
# tmux window ids (@N) are unique within a server but are NOT stable across a
# server restart. This target defaulted to `director:@3`; the estate's tmux was
# restarted on 2026-08-18 and the Director's window came back as @35. The loop
# then failed to capture @3 on every single tick and logged "not ticking" --
# for nearly two hours, silently, while looking perfectly healthy in
# `launchctl list`. The same restart broke quota-watch (@1) and restore.sh the
# same way. A hardcoded @id is a defect, not a configuration.
#
# So: if the configured window is not there, fall back to the session's windows
# -- but only when there is exactly ONE, because guessing which of several
# windows is the Director is precisely the "invent an identity rather than
# refuse" failure (skills#179) that has cost this estate real work.
if ! tmux capture-pane -p -t "=$TARGET" >/dev/null 2>&1; then
  wins=$(tmux list-windows -t "$SESSION" -F '#{window_id}' 2>/dev/null)
  count=$(printf '%s\n' "$wins" | grep -c .)
  if [ "$count" -eq 1 ]; then
    NEW="$SESSION:$(printf '%s' "$wins" | tr -d '[:space:]')"
    log "configured target $TARGET is gone (tmux renumbered across a restart); resolved to $NEW"
    TARGET="$NEW"
  else
    log "configured target $TARGET is gone and $SESSION has $count windows -- refusing to guess which is the Director; a human should look"
    exit 3
  fi
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

3. Keep the TURN short -- act, report one line, stop. That is about turn length, NOT about how much work may be in flight.

FILL THE FREE LANES. If lanes are idle and there is independent work, dispatch to all of them in the same turn. Reviews of different PRs touch different files and do not block each other; there is no coded concurrency cap and none is wanted. An idle lane is waste (lane_default=keep_working_not_idle).

The old instruction here said \"one PR per turn, do not fan out -- the WEEKLY quota is the binding constraint\". That was written when weekly was at 94% and it is now the throttle rather than the protection: the premise expired when the window reset and the sentence outlived it. Judge from the CURRENT reading, never from a remembered one.

Serialise only where there is a REAL dependency. Right now #301, #300 and #205 genuinely queue behind #235, because until lane identity resolves to a stable id the author guard excludes every lane and is right to.

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
