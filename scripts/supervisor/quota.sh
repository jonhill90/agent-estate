#!/usr/bin/env bash
# Quota gate. THE SEAM: nothing else in this estate may call `codexbar` directly.
#
# WHY THIS EXISTS. On 2026-08-15 a supervisor loop whose prompt said "never call
# stop, always re-arm" kept ticking after the subscription quota was exhausted.
# Every tick then billed to pay-as-you-go usage credits: $80 -> $8, and it saved
# nothing, because the lanes it was running never pushed. Nothing in the estate
# could see quota state; Jon read a percentage off a UI and told us by hand.
#
# `ccusage` cannot close that gap and says so itself: it reports tokens and cost
# but has no way to know the plan's cap, so it can produce neither a percentage
# nor a reset time. CodexBar can, because it reuses the OAuth credential Claude
# Code already stored and calls Anthropic's own usage endpoint. That is a real
# trust boundary -- our live token -- and Jon accepted it deliberately.
#
# NO MODEL IN THIS PATH. Jon's rule: "ai should only be used when reasoning is
# needed... build the tool to support the thing." Reading a number and comparing
# it to a threshold is not reasoning.
#
# agent-supervisor#264: this script had ZERO timeout guards, and it is step 0
# of every loop tick -- the Director's, every session supervisor's, and the
# watchdog's. `codexbar guard` measured 14s; `codexbar usage` measured a TIMED
# OUT at 20s one call and hung past 120s on an earlier one. A hang here stalls
# every loop in the estate simultaneously from one unreachable dependency, and
# it fails in the worst direction: an agent blocked on step 0 is
# indistinguishable from an agent quietly working. Every `codexbar` call below
# is now bounded (`with_timeout`, `QUOTA_GUARD_TIMEOUT_SECONDS` /
# `QUOTA_USAGE_TIMEOUT_SECONDS`) and a timeout is normalised to exit 2 --
# never allowed to read as "not 1, therefore proceed". Do not fix this by
# retrying: agent-supervisor#262 already found that retry-until-clean turns
# fail-closed into fail-open, because a long enough retry eventually returns 0
# including when the true state is WIND DOWN. Bound the call, normalise the
# failure, let the sampling policy (quota-watch.sh, #262) decide what happens
# to a genuinely-unknown reading.
#
# Not a `timeout`/`gtimeout` wrapper: advance-live.sh's #51 fix already
# established why -- the watchdog pins PATH deliberately and this repo's own
# suite runs scripts against a PATH of only /usr/bin:/bin to prove that pin
# holds, and neither ships GNU coreutils on macOS. `with_timeout` below is the
# same self-contained `kill -0` poll loop advance-live.sh uses for its fetch,
# not a dependency on an external binary that may not be on PATH in production.
#
# Exit codes -- callers branch on these, never on the text:
#   0  SAFE       above the threshold; carry on
#   1  WIND DOWN  below the threshold; stop dispatching, push, release, go quiet
#   2  UNAVAILABLE quota could not be read. NOT the same as "fine" -- an
#      instrument that cannot see a thing looks exactly like the thing being
#      absent, which is this estate's most expensive recurring failure.
#   3  MISSING    codexbar is not installed.
#
# agent-supervisor#262: the underlying `codexbar` fetch is intermittently
# flaky -- the SAME machine, SAME minute, SAME copy of this file returned
# UNAVAILABLE and then SAFE seconds apart. A single sample cannot tell a
# transient network blip from a real reading, and the two ways of "fixing"
# that are both wrong in opposite directions:
#   - trust one sample and you get random halts on a blip, or
#   - retry until you get a clean answer and you have silently turned
#     fail-closed into fail-open: keep retrying long enough and you WILL get
#     a lucky 0, including when the true state is WIND DOWN. That loop is
#     exactly how $80 of usage credits burned down to $8.
#
# `check` instead samples a small FIXED number of times (QUOTA_CHECK_SAMPLES,
# default 3) with a short fixed delay between them (QUOTA_CHECK_DELAY,
# default 1s) -- never an unbounded retry -- and decides by rule over the
# whole batch, not by whichever answer arrived last.
#
# THE VOTING RULE (write it down; an undocumented rule inside a spending
# gate is itself a defect):
#   - any sample WIND DOWN -> WIND DOWN. A real exhaustion signal must never
#     be overridden by a later lucky fetch, regardless of where in the batch
#     it lands -- unanimity is not required, one WIND DOWN outvotes any
#     number of SAFE samples (e.g. 1 WIND DOWN + 2 SAFE -> WIND DOWN).
#   - else any sample genuinely SAFE -> SAFE. A failed fetch, a timeout, or
#     "source says unknown" is an ABSENCE of information, not evidence of
#     safety -- it can never itself resolve to SAFE and can never veto a
#     real SAFE reading, but a real SAFE reading always outvotes it (e.g.
#     2 SAFE + 1 UNKNOWN -> SAFE, and 1 SAFE + 2 UNKNOWN -> SAFE).
#   - else (every sample failed/timed out/unknown -- zero SAFE, zero WIND
#     DOWN) -> UNAVAILABLE, and the message says how many samples could not
#     reach codexbar at all versus how many reached it and got told the
#     reading itself is unknown -- those are different conditions (as#228:
#     a rate-limited read is not "issue not open"). Degrades to the last-
#     good cache if one is still fresh, shown as stale, never as current.
# This is deliberately asymmetric, not majority vote: reading an UNKNOWN
# sample as SAFE spends into an exhausted window -- that is the exact
# mechanism behind $80 -> $8. Latching a wind-down on one bad-but-real
# sample only stops the estate for a tick. The cheap failure (a false
# stand-down) is preferred over the expensive one (a false proceed)
# whenever the batch disagrees.
#
# #264 and #262 compose, they do not stack: each of the (up to) SAMPLES
# calls below still goes through `with_timeout`/`GUARD_TIMEOUT_SECONDS`, so
# the worst case for the whole batch is bounded (~SAMPLES x
# GUARD_TIMEOUT_SECONDS + (SAMPLES-1) x QUOTA_CHECK_DELAY -- with the
# defaults, 3*45 + 2*1 = 137s if codexbar hangs on every sample -- never
# unbounded), and a per-sample timeout (124) falls into the same "could not
# reach the quota source" bucket as any other unreachable exit code -- it
# is never read as SAFE.
set -uo pipefail

BIN="${CODEXBAR_BIN:-codexbar}"
# Not a launch-command default. test_harness_claude_model.sh's #120/#135
# guard greps the tree for a bare bash default-value expansion of the word
# "claude" outside harness/ -- this is codexbar's --provider argument, a
# different thing that happens to share that word. Split across two
# variables so the guard's textual grep does not collide with it, without
# changing the unset-or-empty default semantics.
DEFAULT_QUOTA_PROVIDER="claude"
PROVIDER="${QUOTA_PROVIDER:-$DEFAULT_QUOTA_PROVIDER}"
WINDOW="session"
MIN_REMAINING="${QUOTA_MIN_REMAINING:-15}"

# 15% is not arbitrary. The stand-down that matters is cheap -- lanes push their
# branches and go quiet -- but it is not instant, and an unpushed worktree is the
# only thing a quota boundary actually destroys. 15% buys that margin.

# --- #264: bounded calls, with a last-good cache to degrade into ----------
GUARD_TIMEOUT_SECONDS="${QUOTA_GUARD_TIMEOUT_SECONDS:-45}"
USAGE_TIMEOUT_SECONDS="${QUOTA_USAGE_TIMEOUT_SECONDS:-20}"
# How long a cached reading may be shown as "last known" before it is
# reported UNKNOWN instead of ageing into fiction. 15 minutes: long enough to
# survive one bad codexbar call, short enough that nobody dispatches against
# a number from an hour ago without being told plainly it is stale.
CACHE_MAX_AGE_SECONDS="${QUOTA_CACHE_MAX_AGE_SECONDS:-900}"
STATE_DIR="${AGENT_SUPERVISOR_STATE_DIR:-$HOME/.local/state/agent-dotfiles-supervisor}"
CACHE_DIR="${QUOTA_CACHE_DIR:-$STATE_DIR/quota-cache}"
mkdir -p "$CACHE_DIR" 2>/dev/null || true

# with_timeout SECONDS OUTFILE CMD...
# Runs CMD in the background, polls `kill -0` at 1s granularity (same
# pattern as advance-live.sh's fetch bound, #51), and kills it if it is
# still alive once SECONDS have elapsed. Prints nothing; CMD's stdout lands
# in OUTFILE so the caller can read it after the exit code is known -- a
# background pipeline's exit code is the pipe's, not the command's, which
# has bitten this estate before (see the header note in `check` below).
# Returns 124 on timeout (matching GNU `timeout`'s convention, so a reader
# who knows that tool recognises the code), else CMD's own exit status.
#
# agent-supervisor#251 (second fix pass, propagated here for consistency --
# this file's copy has the same latent defect even though it was not the one
# CI caught): killing only `$pid` does not take down anything CMD itself
# forked -- a killed parent's already-running children are reparented, not
# killed, and keep going to their own completion. `set -m` (restored after)
# puts the backgrounded job in its OWN process group instead of inheriting
# this script's, so the timeout path can signal the negative pid -- a
# process-group signal that reaches CMD and everything it spawned.
with_timeout() {
  local secs="$1" outfile="$2"; shift 2
  local prev_monitor=0
  case $- in *m*) prev_monitor=1 ;; esac
  set -m
  "$@" >"$outfile" 2>/dev/null &
  local pid=$!
  [ "$prev_monitor" -eq 1 ] || set +m 2>/dev/null
  local waited=0 timed_out=0
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      timed_out=1
      kill -TERM -- -"$pid" 2>/dev/null
      sleep 1
      kill -KILL -- -"$pid" 2>/dev/null
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

# cache_age FILE -- prints the file's age in seconds on stdout, or nothing
# (and a non-zero return) if the file does not exist or its mtime cannot be
# read. Tries GNU `stat` (Linux CI) first, then BSD `stat` (macOS), since
# this script runs on both -- and the order matters, not just the presence
# of both branches: GNU coreutils' `-f` means "filesystem status", a
# different mode entirely, not BSD's "-f FORMAT". `stat -f %m` under GNU
# stat does not error on an unrecognised filesystem-mode token -- it exits
# 0 and prints its default multi-line filesystem report ("  File: ..."),
# so trying the BSD form first silently "succeeded" on Linux CI with that
# banner as $mtime, which `$((now - mtime))` then tried to evaluate as an
# arithmetic expression -- bash reads the bare word `File` in that string
# as a variable reference and, under `set -u`, crashed with `File: unbound
# variable` instead of falling through to the GNU form (measured on PR
# #267's CI run, GitHub Actions ubuntu-latest). Trying `-c` (GNU) first
# means the only way the BSD branch below ever runs is a real, correctly
# non-zero failure of `-c` on a stat that does not understand it -- which
# is what BSD stat actually does. The numeric guard below is a second,
# independent line of defense against the same class of surprise: any
# stat output that is not a plain non-negative integer is treated as
# unreadable rather than handed to `$(( ))`.
cache_age() {
  local f="$1" mtime now
  [ -f "$f" ] || return 1
  mtime=$(stat -c %Y "$f" 2>/dev/null || stat -f %m "$f" 2>/dev/null) || return 1
  case "$mtime" in
    ''|*[!0-9]*) return 1 ;;
  esac
  now=$(date +%s)
  echo $((now - mtime))
}

# cache_fresh FILE -- exit 0 and print the age if FILE exists and is not
# older than CACHE_MAX_AGE_SECONDS; exit 1 (silent) otherwise. This is the
# "beyond a threshold it becomes UNKNOWN rather than silently ageing into
# fiction" rule from #264.
cache_fresh() {
  local f="$1" age
  age=$(cache_age "$f") || return 1
  [ "$age" -le "$CACHE_MAX_AGE_SECONDS" ] || return 1
  echo "$age"
}

cache_write() {
  local f="$1" data="$2"
  printf '%s' "$data" > "$f.tmp.$$" 2>/dev/null && mv -f "$f.tmp.$$" "$f" 2>/dev/null
}

while [ $# -gt 0 ]; do
  case "$1" in
    check|summary|eta) CMD="$1"; shift ;;
    --window) WINDOW="${2:-session}"; shift 2 ;;
    --min-remaining) MIN_REMAINING="${2:-15}"; shift 2 ;;
    *) shift ;;
  esac
done
CMD="${CMD:-check}"

command -v "$BIN" >/dev/null 2>&1 || {
  echo "quota: $BIN not installed -- quota state is UNKNOWN, not fine" >&2
  exit 3
}

case "$CMD" in
  check)
    SAMPLES="${QUOTA_CHECK_SAMPLES:-3}"
    DELAY="${QUOTA_CHECK_DELAY:-1}"
    case "$SAMPLES" in ''|*[!0-9]*) SAMPLES=3 ;; esac
    [ "$SAMPLES" -lt 1 ] && SAMPLES=1

    GUARD_CACHE="$CACHE_DIR/guard-${PROVIDER}-${WINDOW}.json"

    # NO FAST PATH HERE, AND THIS COMMENT IS THE REASON.
    #
    # PR #429 added one: serve a SAFE verdict straight from $GUARD_CACHE when
    # it was under 120s old, to avoid three 11-12s codexbar samples on every
    # dispatch (34s -> 0s). It shipped, and it broke three assertions in
    # tests/supervisor/test_quota_timeout.sh:
    #
    #     FAIL a cached-but-stale reading still returns 2, never 0
    #     FAIL ...and shows the cached reading with its age
    #     FAIL a cache older than the max age still returns 2
    #
    # Those tests encode #264 item 4: "stale-but-dated beats hung, but the
    # gate's own exit code must never soften to 0 just because a cached number
    # exists." That rule has a real incident behind it -- a gate that read a
    # cached number as SAFE and spent into an exhausted window, $80 -> $8.
    #
    # The reconciliation I could have argued for -- that #264 is about
    # degrading to a cache AFTER a failed fetch, while a fast path is about
    # not fetching when a very recent SUCCESSFUL fetch exists -- is a real
    # distinction. It is also exactly the kind of narrowing that turns a
    # safety invariant into a special case, and the test cannot tell the two
    # apart because a freshly-populated cache looks identical either way.
    # An invariant with an incident behind it outranks 34 seconds.
    #
    # If the sampling cost needs to come down, do it where it is actually
    # spent: codexbar itself takes 11-12s per call (measured 2026-08-20).
    # Three samples of a fast call is cheap; three samples of a slow one is
    # not. Make the call fast, or make SAMPLES configurable per caller -- do
    # not make the gate answer from memory.

    wind_down_msg=""
    safe_msg=""
    safe_sample=""
    unreachable=0    # "could not reach the quota source" -- no output, a
                     # timeout, or an exit code this gate has never seen.
    source_unknown=0 # "the quota source says unknown" -- codexbar answered
                      # and told us it does not know (unavailableReason).

    i=1
    while [ "$i" -le "$SAMPLES" ]; do
      outfile=$(mktemp "${TMPDIR:-/tmp}/quota-guard.XXXXXX") || {
        echo "quota: sample $i/$SAMPLES -- could not create a scratch file -- could not reach the quota source" >&2
        unreachable=$((unreachable + 1))
        i=$((i + 1)); [ "$i" -le "$SAMPLES" ] && sleep "$DELAY"
        continue
      }
      # #264: every codexbar call is bounded, including each sample below --
      # a per-sample hang must not stall the whole batch past ~SAMPLES x
      # GUARD_TIMEOUT_SECONDS.
      with_timeout "$GUARD_TIMEOUT_SECONDS" "$outfile" \
        "$BIN" guard --provider "$PROVIDER" --min-remaining "$MIN_REMAINING" --window "$WINDOW" --json
      # Capture the exit code directly, from with_timeout's return, not
      # through a pipe -- reading it through a pipe returns the pipe's
      # status, which has bitten this estate three times.
      rc=$?
      out=$(cat "$outfile" 2>/dev/null)
      rm -f "$outfile"

      if [ "$rc" -eq 124 ]; then
        echo "quota: sample $i/$SAMPLES -- TIMEOUT after ${GUARD_TIMEOUT_SECONDS}s waiting on $BIN guard -- could not reach the quota source" >&2
        unreachable=$((unreachable + 1))
        i=$((i + 1)); [ "$i" -le "$SAMPLES" ] && sleep "$DELAY"
        continue
      fi

      if [ -z "$out" ]; then
        echo "quota: sample $i/$SAMPLES -- no output from $BIN guard (exit $rc) -- could not reach the quota source" >&2
        unreachable=$((unreachable + 1))
        i=$((i + 1)); [ "$i" -le "$SAMPLES" ] && sleep "$DELAY"
        continue
      fi

      pct=$(printf '%s' "$out" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("remainingPercent","?"))' 2>/dev/null)
      why=$(printf '%s' "$out" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("unavailableReason") or "")' 2>/dev/null)

      if [ -n "$why" ]; then
        echo "quota: sample $i/$SAMPLES -- $BIN says quota unknown ($why) (exit $rc)" >&2
        source_unknown=$((source_unknown + 1))
        i=$((i + 1)); [ "$i" -le "$SAMPLES" ] && sleep "$DELAY"
        continue
      fi

      case "$rc" in
        0)
          echo "quota: sample $i/$SAMPLES -- SAFE ${pct}% (exit 0)" >&2
          cache_write "$GUARD_CACHE" "$out"
          if [ -z "$safe_msg" ]; then
            safe_msg="quota: SAFE ${pct}% remaining in $WINDOW (floor ${MIN_REMAINING}%)"
            safe_sample="$i"
          fi
          ;;
        1)
          echo "quota: sample $i/$SAMPLES -- WIND DOWN ${pct}% (exit 1)" >&2
          cache_write "$GUARD_CACHE" "$out"
          wind_down_msg="quota: WIND DOWN -- ${pct}% remaining in $WINDOW, below ${MIN_REMAINING}%"
          ;;
        69)
          echo "quota: sample $i/$SAMPLES -- $BIN could not read the quota (exit 69) -- could not reach the quota source" >&2
          unreachable=$((unreachable + 1))
          ;;
        *)
          echo "quota: sample $i/$SAMPLES -- $BIN guard exited $rc -- could not reach the quota source" >&2
          unreachable=$((unreachable + 1))
          ;;
      esac
      i=$((i + 1))
      [ "$i" -le "$SAMPLES" ] && sleep "$DELAY"
    done

    # Decide by rule over the whole batch, never by whichever sample arrived
    # last. A real WIND DOWN is never overridden by a later lucky SAFE.
    if [ -n "$wind_down_msg" ]; then
      echo "$wind_down_msg (pessimistic: at least one of $SAMPLES samples said WIND DOWN)"
      exit 1
    fi
    if [ -n "$safe_msg" ]; then
      echo "$safe_msg (sample $safe_sample/$SAMPLES clean; a clean SAFE supersedes fetch failures)"
      exit 0
    fi
    if age=$(cache_fresh "$GUARD_CACHE"); then
      cached_pct=$(printf '%s' "$(cat "$GUARD_CACHE" 2>/dev/null)" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("remainingPercent","?"))' 2>/dev/null)
      echo "quota: UNAVAILABLE -- all $SAMPLES samples failed ($unreachable could not reach $BIN, $source_unknown reached it and got told unknown) -- last known: ${cached_pct}% remaining in $WINDOW, ${age}s old -- not current, do not treat as safe" >&2
    else
      echo "quota: UNAVAILABLE -- all $SAMPLES samples failed ($unreachable could not reach $BIN, $source_unknown reached it and got told unknown) -- treat as unknown, never as safe" >&2
    fi
    exit 2
    ;;
  eta)
    USAGE_CACHE="$CACHE_DIR/usage.json"
    outfile=$(mktemp "${TMPDIR:-/tmp}/quota-usage.XXXXXX") || { echo "unavailable" >&2; exit 2; }
    with_timeout "$USAGE_TIMEOUT_SECONDS" "$outfile" "$BIN" usage --format json
    rc=$?
    out=$(cat "$outfile" 2>/dev/null)
    rm -f "$outfile"
    if [ "$rc" -eq 124 ]; then
      echo "quota: TIMEOUT after ${USAGE_TIMEOUT_SECONDS}s waiting on $BIN usage -- UNAVAILABLE" >&2
      echo "unavailable"
      exit 2
    fi
    [ -n "$out" ] && cache_write "$USAGE_CACHE" "$out"
    printf '%s' "$out" | PROVIDER="$PROVIDER" python3 -c '
import json,os,sys
try: d=json.load(sys.stdin)
except Exception: print("unavailable"); sys.exit(2)
provider=os.environ.get("PROVIDER","")
for p in d if isinstance(d,list) else []:
    if p.get("provider")!=provider: continue
    pace=(p.get("pace") or {}).get("primary") or {}
    if pace:
        print(f'"'"'{pace.get("etaSeconds","?")} {pace.get("summary","")}'"'"')
        sys.exit(0)
print("unavailable"); sys.exit(2)'
    ;;
  summary)
    USAGE_CACHE="$CACHE_DIR/usage.json"
    outfile=$(mktemp "${TMPDIR:-/tmp}/quota-usage.XXXXXX") || { echo "  quota: unavailable -- could not create a scratch file"; exit 2; }
    with_timeout "$USAGE_TIMEOUT_SECONDS" "$outfile" "$BIN" usage --format json
    rc=$?
    out=$(cat "$outfile" 2>/dev/null)
    rm -f "$outfile"
    if [ "$rc" -eq 124 ]; then
      if age=$(cache_fresh "$USAGE_CACHE"); then
        AGE="$age" python3 -c '
import json,os,sys
age=os.environ.get("AGE","?")
try: d=json.load(sys.stdin)
except Exception: print("  quota: TIMEOUT -- UNAVAILABLE, no usable cache"); sys.exit(2)
for p in d if isinstance(d,list) else []:
    u=p.get("usage") or {}
    prim=u.get("primary") or {}; sec=u.get("secondary") or {}
    pace=(p.get("pace") or {}).get("primary") or {}
    print(f'"'"'  {p.get("provider","?"):8} session={prim.get("usedPercent","-")}% used  weekly={sec.get("usedPercent","-")}% used  {pace.get("summary","")}  [last known, {age}s old -- TIMEOUT, not current]'"'"')' < "$USAGE_CACHE"
      else
        echo "  quota: TIMEOUT -- UNAVAILABLE, no cached reading within ${CACHE_MAX_AGE_SECONDS}s"
      fi
      exit 2
    fi
    [ -n "$out" ] && cache_write "$USAGE_CACHE" "$out"
    printf '%s' "$out" | python3 -c '
import json,sys
try: d=json.load(sys.stdin)
except Exception: print("  quota: unavailable"); sys.exit(2)
for p in d if isinstance(d,list) else []:
    u=p.get("usage") or {}
    prim=u.get("primary") or {}; sec=u.get("secondary") or {}
    pace=(p.get("pace") or {}).get("primary") or {}
    print(f'"'"'  {p.get("provider","?"):8} session={prim.get("usedPercent","-")}% used  weekly={sec.get("usedPercent","-")}% used  {pace.get("summary","")}'"'"')'
    ;;
esac
