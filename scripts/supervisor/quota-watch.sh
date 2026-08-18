#!/usr/bin/env bash
# The other half of the quota gate: watch for the window COMING BACK, and
# watch for it going AWAY. agent-supervisor#260.
#
# WHY. quota.sh detects the wind-down and the tick that reads it goes quiet --
# but that stand-down was still triggered by a human until this script sent it
# itself. And the resume half had a bug of its own: it fired on ANY observed
# SAFE that differed from the previous *raw* reading, including SAFE that
# followed a transient UNKNOWN (a codexbar timeout, not a real wind-down).
# Measured 2026-08-16: three "QUOTA IS BACK" resumes went out; only the first
# followed a genuine wind-down. The other two landed on a running estate with
# work in flight and had to be re-verified against GitHub by hand -- a false
# signal is worse than no signal, because it trains the reader to skim.
#
# THE FIX, both directions gated on a CONFIRMED state, never a raw one:
#   - `confirmed` only ever holds SAFE or WINDDOWN, persisted across restarts.
#     A transient UNKNOWN reading (codexbar timeout, missing binary, an
#     exit code this script has never seen) is logged but never written to
#     `confirmed` -- so it can neither fake a wind-down nor swallow a real one
#     sitting on the other side of the blip.
#   - The wind-down fires on the genuine SAFE -> WINDDOWN edge: `confirmed`
#     was SAFE (or unset, i.e. first reading), the fresh read is WINDDOWN.
#   - The resume fires on the genuine WINDDOWN -> SAFE edge: `confirmed` was
#     WINDDOWN, the fresh read is SAFE. Never on "not WINDDOWN", always on
#     "was WINDDOWN" -- the mirror of the standing rule that a gate keys on
#     "is 1", never "is not 0".
#   - Each direction sends exactly once per edge; sitting in the same
#     confirmed state on later ticks (WINDDOWN, WINDDOWN, WINDDOWN, ...) sends
#     nothing more, the same as it always did for the resume side.
#
# A PAUSED AGENT CANNOT POLL FOR ITS OWN RECOVERY. That was the original design
# error. The supervisor was told to "re-arm a cheap poll", but the thing that
# has stood down is the least reliable thing to trust with restarting itself --
# it is idle, it may hold a stranded prompt, and its loop may not re-arm at
# all. The watcher must live OUTSIDE the thing it restarts -- and, symmetrically,
# outside the thing it winds down: the estate cannot be trusted to notice its
# own quota exhaustion any more reliably than it can notice its own recovery.
# Same principle as watchdog.sh, which runs from a LaunchAgent so it survives
# the loop dying.
#
# NO MODEL IN THIS PATH. Polling a number and sending one message when it
# crosses a threshold is not reasoning. Jon's rule: build the tool.
#
# Nothing in this file calls `codexbar` directly. `quota.sh` is the seam;
# QUOTA_GATE below only overrides which quota.sh-shaped binary is asked, never
# reaches around it.
#
# agent-supervisor#276: for three hours this process's own liveness was
# reported as "healthy" purely because `pgrep` found its pid, while it sat
# hung inside `quota.sh check` -- the exact instrument error this repository
# keeps filing against itself (as#228/#230/#259): a process EXISTING is not a
# process WORKING. $STAMP is now a HEARTBEAT, not a bare state cache: it
# carries `checked:`/`state:` lines, the same shape `inbox-poll.status`
# already uses for #163's identical fix, so watchdog.sh's existing generic
# `--mode heartbeat` check reads it with no new parsing code. It is written
# AFTER `quota.sh check` returns (never before), so a hang inside that call
# stops the stamp advancing instead of certifying intent as completion. The
# stamp's `state:` field is the RAW reading (SAFE/WINDDOWN/UNKNOWN, so a hang
# is visible as UNKNOWN rather than silently missing) -- edge detection below
# still keys on a separate `confirmed:` field that only ever holds SAFE or
# WINDDOWN, for exactly the reason the previous section gives.
#
# Usage:
#   quota-watch.sh [--interval SECONDS] [--target SESSION:WINDOW] [--once]
#   nohup bash scripts/supervisor/quota-watch.sh >>~/.local/state/agent-dotfiles-supervisor/quota-watch.log 2>&1 &
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
INTERVAL="${QUOTA_WATCH_INTERVAL:-300}"
TARGET="${QUOTA_WATCH_TARGET:-agent-supervisor:@1}"
# Overridable so tests can point this at a stub, same convention as
# dispatch.sh's QUOTA_GATE -- never call codexbar directly from anywhere,
# everything goes through a quota.sh-shaped binary.
QUOTA_GATE="${QUOTA_GATE:-$HERE/quota.sh}"
ONCE=0
STAMP="$STATE/.quota-watch.state"

# agent-supervisor#305: the reset-boundary blindness fix. Measured 2026-08-17:
# alive, on schedule, two consecutive UNKNOWN readings straddling a session
# reset, and nothing distinguished that from a watcher correctly holding a
# real wind-down -- so nothing alarmed and the estate stayed down until Jon
# noticed by hand. Two independent readings this file previously threw away:
#   - UNKNOWN is transient by design (see write-up above `confirmed`) -- but
#     a RUN of them is not a blip, it is the watcher losing vision, and a
#     watcher that cannot see must say so louder than a logfile line.
#   - the poll interval (300s) is right for a steady state, but a watcher
#     that has lost vision should look again sooner, not wait a full cycle
#     to find out if it can see yet.
# Start at 2 (the issue's own number, and what was actually measured) so a
# single blip -- the correct quiet case -- never pages; two isolated blips
# never adjoin because a definite SAFE/WINDDOWN reading between them resets
# the streak below.
UNKNOWN_ALARM_AFTER="${QUOTA_WATCH_UNKNOWN_ALARM_AFTER:-2}"
UNKNOWN_INTERVAL="${QUOTA_WATCH_UNKNOWN_INTERVAL:-45}"
# Overridable so tests can point this at a recording stub instead of the
# real notify.sh, same convention as QUOTA_GATE above -- this is a PAGE
# (#273: "escalate on the unrecoverable condition only, and rate-limit it --
# one message per incident, not one per tick"), not a log line nobody reads.
NOTIFY_SCRIPT="${QUOTA_WATCH_NOTIFY_SCRIPT:-$HERE/notify.sh}"

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

# send_alarm N -- page a human that this watcher has been blind for N
# consecutive ticks. Exit 0 only if a channel actually accepted it (notify.sh's
# own contract) -- the caller uses that to decide whether the "already paged
# this incident" flag may be set, so a send that merely LOGGED but never
# reached anyone does not get treated as "a human has been told" (#91's rule,
# reused here).
send_alarm() {
  local n="$1"
  local subject="quota-watch BLIND (#305)"
  local body="quota-watch.sh has read UNKNOWN $n consecutive times (target $TARGET, confirmed state stuck at ${confirmed:-<none>}). This is not a wind-down -- it is the watcher unable to see the meter at all, which looks identical to a correct quiet wind-down from outside. Check by hand: bash scripts/supervisor/quota.sh check"
  AGENT_NOTIFY_CALLER=supervisor bash "$NOTIFY_SCRIPT" "$subject" "$body"
}

# The wind-down instruction is lifted verbatim from loop-tick.md's "Exit 1 is
# covered, not blocked" section -- that text is the estate's one written
# definition of what a wind-down means, and re-inventing a second version here
# would make two artifacts that can drift. Both directions read from this file.
WINDDOWN_MSG='QUOTA IS LOW -- quota.sh check returns WIND DOWN. This is an automatic stand-down from quota-watch.sh, not a human.

Stop dispatching, tell every in-flight lane to push and release, then go quiet WITHOUT scheduling a wakeup of your own. This is a legitimate stop, not a failure -- quota-watch.sh is the wakeup. The instruction "never call stop, always re-arm" is RETIRED; re-arming into an exhausted window is what burned $80 of usage credits down to $8. Do not reinstate it, and do not poll for your own recovery -- a paused agent cannot reliably watch for its own resume.

quota-watch.sh will send exactly one resume message once quota.sh check returns SAFE again.'

RESUME_MSG='QUOTA IS BACK -- the session window has reset and quota.sh check returns SAFE. This is an automatic wake-up from quota-watch.sh, not a human. Resume work now.

Read ~/.local/state/agent-dotfiles-supervisor/QUOTA-HANDOFF.md for what was in flight when the estate stood down, and re-derive priority from current GitHub state -- do not trust a priority list baked into this message, it goes stale the moment anything merges.

YOUR LOOP TICK MUST START WITH: bash scripts/supervisor/quota.sh check -- exit 0 proceed, exit 1 wind down and go quiet, exit 2 or 3 quota is UNKNOWN so say so and do not treat it as safe. The instruction "never call stop, always re-arm" is RETIRED; re-arming into an exhausted window is what burned $80 of usage credits down to $8. Do not reinstate it.'

# write_heartbeat RAW CONFIRMED UNKNOWN_STREAK ALARM_SENT -- overwrites
# $STAMP with `checked:`/`state:`/`confirmed:`/`unknown_streak:`/
# `blind_alarm_sent:` lines, tmp+rename so a reader never sees a
# half-written file. Called ONLY after this iteration's work is done (see
# the file header) -- never at the top of the loop. `state:` is the raw
# per-tick reading, for watchdog.sh's staleness check; `confirmed:` is the
# edge-detection value below, restored from this same file across restarts.
# `unknown_streak:`/`blind_alarm_sent:` are #305's reset-boundary-blindness
# counters, restored the same way so a restart mid-blind-spell does not
# reset the count to zero and quietly buy back two more free UNKNOWNs.
write_heartbeat() {
  local tmp="$STAMP.$$"
  { printf 'checked: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"; printf 'state: %s\n' "$1"; printf 'confirmed: %s\n' "$2"; printf 'unknown_streak: %s\n' "$3"; printf 'blind_alarm_sent: %s\n' "$4"; } >"$tmp" 2>/dev/null \
    && mv -f "$tmp" "$STAMP" 2>/dev/null
}

log "watching every ${INTERVAL}s, target $TARGET"

# `confirmed` is the CONFIRMED state used for edge detection: it is only ever
# SAFE or WINDDOWN, never UNKNOWN, and persists across restarts. A transient
# UNKNOWN reading is logged (below) but never written here -- see the file
# header for why that is the whole fix.
confirmed=""
[ -f "$STAMP" ] && confirmed="$(sed -n 's/^confirmed: *//p' "$STAMP" 2>/dev/null | head -1)"
case "$confirmed" in
  SAFE|WINDDOWN) : ;;
  *) confirmed="" ;;
esac

# #305: how many consecutive UNKNOWN readings this watcher has seen, and
# whether a human has already been paged for the CURRENT blind spell.
# Restored across restarts for the same reason `confirmed` is -- a restart
# mid-blind-spell must not silently reset the count.
unknown_streak=0
[ -f "$STAMP" ] && unknown_streak="$(sed -n 's/^unknown_streak: *//p' "$STAMP" 2>/dev/null | head -1)"
case "$unknown_streak" in ''|*[!0-9]*) unknown_streak=0 ;; esac
alarm_sent=0
[ -f "$STAMP" ] && alarm_sent="$(sed -n 's/^blind_alarm_sent: *//p' "$STAMP" 2>/dev/null | head -1)"
[ "$alarm_sent" = "1" ] || alarm_sent=0

last_raw=""   # last-seen raw reading, SAFE/WINDDOWN/UNKNOWN, only for log dedup

while :; do
  bash "$QUOTA_GATE" check >/dev/null 2>&1
  rc=$?   # captured directly -- never through a pipe
  # Enumerate what is accepted; refuse everything else. Key on "is 1", never
  # "is not 0" -- that is how an UNKNOWN silently becomes SAFE. 2 and 3 are
  # the gate's own UNAVAILABLE/MISSING codes; 127 (quota.sh missing from a
  # deployment tree, agent-supervisor#260) and anything else fall into the
  # same UNKNOWN bucket by virtue of not matching 0 or 1 below.
  case "$rc" in
    0) raw=SAFE ;;
    1) raw=WINDDOWN ;;
    *) raw=UNKNOWN ;;
  esac

  if [ "$raw" != "$last_raw" ]; then
    log "reading $raw (rc=$rc), confirmed state is ${confirmed:-<none>}"
  fi

  case "$raw" in
    WINDDOWN)
      if [ "$confirmed" != "WINDDOWN" ]; then
        log "transition ${confirmed:-<start>} -> WINDDOWN; sending ONE wind-down to $TARGET"
        if send_message "$WINDDOWN_MSG"; then
          log "wind-down delivered"
        else
          log "wind-down did NOT take -- pane did not confirm after send; a human should look"
        fi
      fi
      confirmed=WINDDOWN
      # A genuine WINDDOWN is a DEFINITE reading -- the watcher can see, it
      # just does not like what it sees. That is not blindness; only a run
      # of UNKNOWNs is (#305). Clear the streak on this edge too, same as SAFE.
      if [ "$unknown_streak" -gt 0 ]; then
        log "vision restored after $unknown_streak consecutive UNKNOWN reading(s)"
      fi
      unknown_streak=0
      alarm_sent=0
      ;;
    SAFE)
      if [ "$confirmed" = "WINDDOWN" ]; then
        log "transition WINDDOWN -> SAFE; sending ONE resume to $TARGET"
        if send_message "$RESUME_MSG"; then
          log "resume delivered and the pane is working"
        else
          log "resume did NOT take -- pane is not working after send; a human should look"
        fi
      fi
      confirmed=SAFE
      if [ "$unknown_streak" -gt 0 ]; then
        log "vision restored after $unknown_streak consecutive UNKNOWN reading(s)"
      fi
      unknown_streak=0
      alarm_sent=0
      ;;
    UNKNOWN)
      # #305: a run of UNKNOWNs is the watcher losing vision, not a policy
      # decision -- it must not silently look identical to a correctly-held
      # wind-down. `confirmed` is left exactly as it was above; this is
      # purely "does a human need to be told we cannot see".
      unknown_streak=$((unknown_streak + 1))
      if [ "$unknown_streak" -ge "$UNKNOWN_ALARM_AFTER" ] && [ "$alarm_sent" != "1" ]; then
        log "BLIND: $unknown_streak consecutive UNKNOWN readings, confirmed state stuck at ${confirmed:-<none>} -- paging"
        if send_alarm "$unknown_streak"; then
          alarm_sent=1
          log "blind alarm sent"
        else
          log "blind alarm did NOT send -- still blind, will retry next tick"
        fi
      fi
      ;;
  esac

  write_heartbeat "$raw" "$confirmed" "$unknown_streak" "$alarm_sent"
  last_raw="$raw"
  [ "$ONCE" = "1" ] && break
  # #305: a watcher that has lost vision retries sooner than steady-state --
  # 300s is right when it can see, wrong when it cannot.
  sleep_for="$INTERVAL"
  [ "$raw" = "UNKNOWN" ] && sleep_for="$UNKNOWN_INTERVAL"
  sleep "$sleep_for"
done
