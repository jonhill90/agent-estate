#!/usr/bin/env bash
# Detect a STALLED estate and nudge it once. agent-supervisor#315.
#
# WHY A TIMER AND NOT AN EVENT. Jon, 2026-08-17: "a signal should be determine
# why we have to rely on cron and cant just use system". The answer is a real
# distinction, not a shrug:
#
#   An event tells you something HAPPENED.
#   Only a clock tells you NOTHING happened.
#
# Every event-driven mechanism available -- hooks (Stop, SessionStart,
# TaskCompleted), the cross-session messaging socket, a ledger write -- fires
# when work occurs. None of them fire when work STOPS occurring, because a
# non-event emits nothing. Absence detection is irreducibly a timer, and every
# outage tonight was an absence: a hung turn making no tool calls, a dispatch
# refused with no lane, a classifier that stopped recognising idle panes. All
# of them looked exactly like a quiet, healthy estate.
#
# So this is NOT a workaround for missing event support. It is the one job a
# timer is genuinely required for. What SHOULD be event-driven -- "this lane
# finished", "this PR merged" -- is tracked separately in #316; polling those
# is the part worth removing.
#
# launchd IS the loop. `--once` does one pass and exits, so a wedged tmux or a
# hung sqlite cannot leave a long-lived process that `pgrep` still calls alive
# -- the exact failure quota-watch hit for three hours on 2026-08-16.
#
# NOTHING HERE CALLS A MODEL. It reads the ledger, tmux and a clock, then sends
# at most one message. Jon's rule: build the tool.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
LEDGER="$STATE/ledger.sqlite3"
STAMP="$STATE/.heartbeat.state"
TARGET="${HEARTBEAT_TARGET:-director:@3}"
# How long the estate may show no ledger write before it counts as stalled.
STALE_AFTER="${HEARTBEAT_STALE_AFTER:-1800}"
# Never nudge more often than this, however long the stall runs. A stalled
# estate nudged every pass is a message storm, which is the failure mode
# rule 10 exists for -- four orchestration messages cost more than the lanes
# they were quieting.
NUDGE_COOLDOWN="${HEARTBEAT_NUDGE_COOLDOWN:-3600}"
QUOTA_GATE="${QUOTA_GATE:-$HERE/quota.sh}"
# Overridable so tests can point this at a recording stub instead of the
# real notify.sh, same convention as quota-watch.sh's QUOTA_WATCH_NOTIFY_SCRIPT
# (agent-supervisor#273).
NOTIFY_SCRIPT="${HEARTBEAT_NOTIFY_SCRIPT:-$HERE/notify.sh}"
ONCE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --once)     ONCE=1; shift ;;
    --target)   TARGET="${2:-}"; shift 2 ;;
    --stale-after) STALE_AFTER="${2:-1800}"; shift 2 ;;
    *) shift ;;
  esac
done

log() { printf '%s heartbeat: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"; }

# send_takeover_alarm SUBJECT BODY -- page a human when this script's own
# nudge attempt hits a state it cannot recover from itself (agent-supervisor
# #273: a correct "a human should look" refusal used to go only to a
# logfile, which produced the same outward silence as no check at all).
# Reuses notify.sh, the estate's one human-notification path, rather than
# inventing a second one. Every call site below sits inside the STALLED
# branch, which this script only enters once per NUDGE_COOLDOWN (see below)
# -- that is what keeps this to one message per incident, not one per tick.
send_takeover_alarm() {
  local subject="$1" body="$2"
  AGENT_NOTIFY_CALLER=supervisor bash "$NOTIFY_SCRIPT" "$subject" "$body"
}

now=$(date +%s)

# --- 0. the quota gate. A stalled estate that is stalled BECAUSE it wound
# down is correct, not broken, and must not be nudged back into spending.
"$QUOTA_GATE" check >/dev/null 2>&1
qrc=$?
case "$qrc" in
  0) ;;
  1) log "quota WIND DOWN -- a stood-down estate is not a stalled one, nothing to do"; exit 0 ;;
  *) log "quota UNKNOWN (rc=$qrc) -- refusing to nudge on an unreadable meter"; exit 0 ;;
esac

# --- 1. read the estate. Every number here is measured, never inferred.
if [ ! -f "$LEDGER" ]; then
  log "NO LEDGER at $LEDGER -- cannot tell stalled from idle, a human must look"
  exit 3
fi

last_write=$(sqlite3 "$LEDGER" "select coalesce(max(updated_at),0) from lanes;" 2>/dev/null)
[ -n "$last_write" ] || last_write=0
since=$(( now - last_write ))

working=0
free=0
while read -r w; do
  [ -n "$w" ] || continue
  # LAST NON-BLANK LINE ONLY -- never a scrollback sweep. This is lanes.sh's
  # #65 discipline and skipping it broke this file within one hour of writing
  # it: the nudge text below QUOTES the string `esc to interrupt`, so the
  # moment the first nudge landed in the Director's pane, a whole-pane grep
  # matched its own message and every later pass reported "1 pane-working".
  # The heartbeat fired once and then permanently disabled itself, staying
  # silent through a 9,223-second stall. A busy marker is a statement about
  # the CURRENT footer, never about text a pane happens to be displaying.
  p=$(tmux capture-pane -p -t "=$w" 2>/dev/null) || continue
  if grep -v '^[[:space:]]*$' <<<"$p" | tail -1 | grep -q 'esc to interrupt'; then
    working=$(( working + 1 ))
  fi
done < <(tmux list-windows -a -F '#{session_name}:#{window_id}' 2>/dev/null | grep -v Hill90)

free=$(bash "$HERE/lanes.sh" --free 2>/dev/null | grep -c ':' )
[ -n "$free" ] || free=0

# A claude-print lane HAS NO PANE, so `esc to interrupt` can never see it and
# a pane-only probe reports a busy claude-print estate as 0 working. Caught by
# this script's own first run: #308 was genuinely mid-flight and invisible.
# Ask the ledger instead -- `delivery_pending`/`delivered`/`accepted`/`running`
# are the in-flight statuses in tasks' own CHECK constraint. `created` is
# EXCLUDED deliberately: the `ledger-claim:` rows sit at `created` and are
# claim bookkeeping, not work, so counting them would make every stalled
# estate look busy forever.
# BOUNDED BY RECENCY, and that bound is load-bearing. The unbounded count read
# 18 on this script's second run, because a task whose lane died stays at
# `delivery_pending` forever -- `as307-fix307` had sat there since 03:11. An
# in-flight count that includes abandoned rows never drops to zero, so the
# stall branch below could never fire and this whole file would be decoration.
# A task not touched inside the same window that defines staleness is not
# in-flight, it is abandoned, and abandoned work is exactly what a stall IS.
inflight=$(sqlite3 "$LEDGER" \
  "select count(*) from tasks
    where status in ('delivery_pending','delivered','accepted','running')
      and id not like 'ledger-claim:%'
      and updated_at > $(( now - STALE_AFTER ));" 2>/dev/null)
case "$inflight" in ''|*[!0-9]*) inflight=0 ;; esac

log "last ledger write ${since}s ago; ${working} pane-working, ${inflight} in-flight tasks, ${free} free"

# --- 2. decide. Stalled means BOTH: nothing has been written for STALE_AFTER,
# and nothing is working. A long single turn with no ledger write is not a
# stall -- that is a lane doing its job, and #278 means a dispatch can block
# for the whole task without touching the ledger again.
if [ "$working" -gt 0 ] || [ "$inflight" -gt 0 ]; then
  printf '%s' "$now" > "$STAMP.ok"
  log "OK -- ${working} pane-working, ${inflight} in-flight"
  exit 0
fi

if [ "$since" -lt "$STALE_AFTER" ]; then
  log "OK -- last write is inside the ${STALE_AFTER}s window"
  exit 0
fi

# Nothing working AND nothing written. That is a stall.
last_nudge=0
[ -f "$STAMP" ] && last_nudge=$(cat "$STAMP" 2>/dev/null)
case "$last_nudge" in ''|*[!0-9]*) last_nudge=0 ;; esac
if [ $(( now - last_nudge )) -lt "$NUDGE_COOLDOWN" ]; then
  log "STALLED ${since}s, but last nudge was $(( now - last_nudge ))s ago -- inside cooldown, staying quiet"
  exit 1
fi

MSG="HEARTBEAT: the estate looks STALLED -- no ledger write for ${since}s and no lane showing \`esc to interrupt\`. ${free} lane(s) read free. This is heartbeat.sh, not a human, and it fires at most once per hour.

Do not assume this message is right. VERIFY before acting -- three things tonight looked stalled and were not, and one looked healthy and was not:
- \`bash scripts/supervisor/quota.sh check\` first. Exit 1 means wind down and go quiet; that is a legitimate stop, not a stall.
- \`bash scripts/supervisor/lanes.sh\` -- a lane reading \`unknown\` is NOT free, and a footer change made every idle lane read unknown earlier tonight (#314). Estate capacity was zero while two lanes sat idle.
- \`bash scripts/supervisor/claim.sh stale\` -- a stale claim silently blocks its issue from ever being dispatched. There were 10.
- A dispatch that reported a 900s timeout may have SUCCEEDED (#278). Check the ledger and GitHub, not the exit code.
- A merged fix is NOT a deployed fix. Check the file in ~/.local/state/agent-dotfiles-supervisor/live/ (#310).

If work genuinely stopped, dispatch ONE thing and stop. Do not fan out: the WEEKLY quota is the binding constraint, not the session window."

NUDGE_SESSION="${TARGET%%:*}"
if ! tmux has-session -t "$NUDGE_SESSION" 2>/dev/null; then
  log "STALLED ${since}s -- target session $NUDGE_SESSION does not exist; a human should look"
  if send_takeover_alarm "heartbeat: estate STALLED, no target session (#273)" \
    "No ledger write for ${since}s, nothing pane-working, and the target session '$NUDGE_SESSION' does not exist at all -- there is nowhere to nudge. Check the estate by hand: tmux ls"; then
    log "escalation sent"
  else
    log "escalation did NOT send -- see notify.log"
  fi
  exit 1
fi

# RESOLVE THE WINDOW, do not trust the @id in TARGET. tmux window ids are
# unique within a server but are NOT stable across a server restart
# (agent-supervisor#346) -- director-loop.sh hit this exact defect with this
# exact default (director:@3), measured as nearly two hours of silent
# non-ticking while `launchctl list` reported it healthy.
#
# Fall back to the session's own window only when there is EXACTLY ONE.
# Guessing which of several windows is the Director is the "invent an
# identity on recovery" failure skills#179 exists to prevent, and a wrong
# guess here types this nudge into an unrelated pane.
if ! tmux capture-pane -p -t "=$TARGET" >/dev/null 2>&1; then
  wins=$(tmux list-windows -t "$NUDGE_SESSION" -F '#{window_id}' 2>/dev/null)
  count=$(grep -c . <<<"$wins")
  if [ "$count" -eq 1 ]; then
    resolved="$NUDGE_SESSION:$(tr -d '[:space:]' <<<"$wins")"
    log "configured target $TARGET is gone (tmux renumbered across a restart); resolved to $resolved"
    TARGET="$resolved"
  else
    log "STALLED ${since}s -- configured target $TARGET is gone and $NUDGE_SESSION has $count windows -- refusing to guess which is the Director; a human should look"
    if send_takeover_alarm "heartbeat: estate STALLED, ambiguous Director window (#273)" \
      "No ledger write for ${since}s, nothing pane-working, and the configured target $TARGET is gone -- $NUDGE_SESSION now has $count windows, so which one is the Director cannot be guessed safely. Check the estate by hand: tmux list-windows -t $NUDGE_SESSION"; then
      log "escalation sent"
    else
      log "escalation did NOT send -- see notify.log"
    fi
    exit 1
  fi
fi

# C-u then retype then Enter, ALWAYS -- Enter alone does not submit text a
# previous send-keys left in the box.
tmux send-keys -t "=$TARGET" C-u 2>/dev/null
sleep 1
tmux send-keys -t "=$TARGET" -l "$MSG" 2>/dev/null
sleep 2
tmux send-keys -t "=$TARGET" Enter 2>/dev/null
sleep 6
if tmux capture-pane -p -t "=$TARGET" 2>/dev/null | grep -q 'esc to interrupt'; then
  log "STALLED ${since}s -- nudged $TARGET, pane is now working"
else
  log "STALLED ${since}s -- nudged $TARGET but the pane did NOT start working; a human should look"
  if send_takeover_alarm "heartbeat: nudge did NOT take (#273)" \
    "Nudged $TARGET after ${since}s with no ledger write, but the pane never confirmed (no 'esc to interrupt' after send). The pane may be stranded or unresponsive -- check it by hand: tmux attach -t ${TARGET%%:*}"; then
    log "escalation sent"
  else
    log "escalation did NOT send either -- still unverified, see notify.log"
  fi
fi
printf '%s' "$now" > "$STAMP"

exit 1
