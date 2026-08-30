#!/usr/bin/env bash
# Tell Jon on Telegram when the WEEKLY quota is nearly gone. agent-supervisor#327.
#
# WHY THIS IS SEPARATE FROM quota.sh. The session gate and the weekly cap are
# different instruments answering different questions, and conflating them is
# the mistake this file exists to prevent:
#
#   SESSION (5h)  quota.sh's floor. Resets on its own every 5 hours, so
#                 stopping near it costs almost nothing. This is the gate that
#                 halts dispatch.
#   WEEKLY        Does not reset until the stamped date. Running out is not an
#                 outage -- Jon switches to a second account -- but he has to
#                 KNOW, and only he can make the switch.
#
# Jon's own recorded parameters, all three hard:
#   quota_weekly=spend_it_all       the weekly is to be USED, not preserved
#   quota_session=wind_down_at_end  the session floor is the real stop
#   quota_exhausted=switch_account  exhaustion is a swap, not an outage
#
# So this script NEVER stops anything. It has exactly one job: page a human
# before the wall, because the recovery action is his and needs lead time.
#
# NO MODEL IN THIS PATH. It reads a number and sends one message.
#
# ONE PAGE PER THRESHOLD PER WEEK. `heartbeat.sh` paged three times in seven
# hours for one unchanging cause and that trained the reader to skim -- the
# exact failure a page exists to prevent. Each threshold fires once, and the
# stamp is keyed to the weekly RESET timestamp so a new week re-arms them all
# automatically without anyone remembering to clear anything.
#
# agent-supervisor#341: this script shipped (PR #328) with no liveness
# instrumentation at all -- no heartbeat on any code path, and no
# watchdog.sh check. A hung process, a permanently broken meter, and a
# launchd job that silently stopped firing all produced the identical
# observable state as "everything's fine, below threshold": no file, only a
# log line in weekly-watch.log that nothing scans automatically. The one
# alarm that tells Jon the weekly quota is nearly gone could die silently
# with it. quota-watch.sh already solved this the same day this file was
# written (#276): a `checked:`/`state:` heartbeat written EVERY run,
# success or failure, that watchdog.sh alarms on for staleness -- a live
# pid or a freshly-loaded launchd job with a stale heartbeat reads as
# unhealthy, because the process table (or launchd's own "loaded" status)
# is not what the check reads. This file now does the same, via a trap so
# an unexpected crash mid-script still leaves a heartbeat, not silence.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
STAMP="$STATE/.weekly-watch.paged"
# Thresholds in PERCENT USED. 90 is "start thinking about the swap", 97 is
# "do it now". Two rather than one because a single page at 97 leaves no time
# to react if Jon is asleep or away from the machine.
THRESHOLDS="${WEEKLY_WATCH_THRESHOLDS:-90 97}"

log() { printf '%s weekly-watch: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"; }

# --- #341: heartbeat, written on every exit path, never only on success ----
# A separate stamp from $STAMP above: $STAMP is the per-threshold PAGE dedup
# (what has already fired this week); $HEARTBEAT is pure liveness (did this
# invocation run at all, regardless of outcome). Conflating them was the
# original bug -- $STAMP is only ever written inside the successful-page
# branch, so a failed read or an unhung-but-below-threshold run left nothing
# behind for anything to check staleness against.
HEARTBEAT="$STATE/.weekly-watch.state"
hb_state="unknown"   # default: only overwritten below on a confirmed clean read
write_heartbeat() {
  mkdir -p "$STATE" 2>/dev/null
  local tmp="$HEARTBEAT.$$"
  { printf 'checked: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"; printf 'state: %s\n' "$hb_state"; } >"$tmp" 2>/dev/null \
    && mv -f "$tmp" "$HEARTBEAT" 2>/dev/null
}
# Trap, not a call at the bottom of the script: the whole point is that a
# crash, an unhandled exception in the embedded python, or `set -e`-style
# early exit still leaves a fresh `checked:` line. A heartbeat that only
# fires on the happy path is not a heartbeat, it is a success counter.
trap write_heartbeat EXIT

# codexbar is the authoritative meter: it reads the real Anthropic usage
# endpoint and reports BOTH windows with their reset stamps. ccusage cannot --
# it does not know the plan cap. Jon: "you should know your own tools."
# THREE SAMPLES, same discipline as quota.sh. codexbar is marginal -- it
# measures ~12.5s against a 15s default timeout and fetch-fails intermittently.
# The very first launchd run of this script hit exactly that and reported the
# meter unreadable, while the identical call seconds later returned 94%. A
# single sample would delay the 97% page by a full poll interval, and a run of
# failures could miss the threshold entirely -- which is the one thing this
# script exists to prevent.
#
# Unlike quota.sh this is OPTIMISTIC on purpose: quota.sh gates SPENDING, so a
# bad read there must fail closed. This only sends a message, so the dangerous
# failure is silence, not a false alarm. First clean read wins.
used=""; resets=""; pace=""
for attempt in 1 2 3; do
  raw=$(timeout 60 codexbar usage --provider claude --json 2>/dev/null) || true
  [ -n "$raw" ] || { log "sample ${attempt}/3 -- codexbar returned nothing"; sleep 2; continue; }
  read -r used resets pace <<<"$(printf '%s' "$raw" | python3 -c '
import json,sys
try:
    d=json.load(sys.stdin)[0]
except Exception:
    print("ERR ERR ERR"); raise SystemExit
u=(d.get("usage") or {}).get("secondary") or {}
p=(d.get("pace") or {}).get("secondary") or {}
print(u.get("usedPercent","ERR"), u.get("resetsAt","unknown"), (p.get("summary") or "-").replace(" ","_"))
' 2>/dev/null)"
  case "$used" in ''|*[!0-9]*) log "sample ${attempt}/3 -- weekly usedPercent unreadable ('$used')"; used=""; sleep 2; continue ;; esac
  break
done

case "$used" in
  ''|*[!0-9]*)
    log "all 3 samples failed -- cannot read the weekly meter. UNKNOWN is never treated as fine; retrying next pass."
    # hb_state stays "unknown" -- the trap above writes it on the way out.
    exit 2 ;;
esac

log "weekly ${used}% used, resets ${resets}"
# A confirmed clean read, whether or not any threshold fires below. This is
# the fact the heartbeat exists to certify: the meter was read this tick.
hb_state="ok"

# The stamp records which thresholds have already fired FOR THIS WEEK, keyed on
# the reset timestamp. A new week changes the key, which re-arms every
# threshold with no manual clearing -- the failure mode heartbeat.sh had, where
# a stale stamp suppressed a page whose cause had changed.
fired=""
[ -f "$STAMP" ] && fired="$(cat "$STAMP" 2>/dev/null)"
case "$fired" in "$resets"*) : ;; *) fired="$resets" ;; esac

for t in $THRESHOLDS; do
  [ "$used" -ge "$t" ] || continue
  case "$fired" in *":$t:"*) continue ;; esac

  if [ "$t" -ge 97 ]; then
    subject="Weekly quota nearly gone - ${used}%"
    body="Weekly quota is ${used}% used and resets ${resets}. This is the point to switch to your friend's account.

Nothing is broken and nothing has stopped. The 5-hour session window keeps resetting on its own; it is the WEEKLY cap that runs out, and the recovery is yours to make (quota_exhausted=switch_account).

Pace: ${pace//_/ }"
  else
    subject="Weekly quota at ${used}%"
    body="Weekly quota is ${used}% used, resets ${resets}. Not urgent - flagging early so the account switch is not a surprise. You will get one more message at 97%.

Pace: ${pace//_/ }"
  fi

  if AGENT_NOTIFY_CALLER=supervisor "$HERE/notify.sh" "$subject" "$body" >/dev/null 2>&1; then
    log "paged Jon at the ${t}% threshold"
    fired="${fired}:${t}:"
    printf '%s' "$fired" > "$STAMP"
  else
    # Do NOT record it as fired. A page that did not send must be retried on
    # the next pass -- recording it would silently swallow the one alert whose
    # whole purpose is to reach a human.
    log "FAILED to page Jon at ${t}% -- notify.sh returned nonzero; will retry next pass"
  fi
done

exit 0
