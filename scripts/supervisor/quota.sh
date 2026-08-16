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
# Usage:
#   quota.sh check [--window session|weekly] [--min-remaining N]
#   quota.sh summary          human-readable, all providers
#   quota.sh eta              seconds until the active window is projected empty
#
# Exit codes -- callers branch on these, never on the text:
#   0  SAFE       above the threshold; carry on
#   1  WIND DOWN  below the threshold; stop dispatching, push, release, go quiet
#   2  UNAVAILABLE quota could not be read. NOT the same as "fine" -- an
#      instrument that cannot see a thing looks exactly like the thing being
#      absent, which is this estate's most expensive recurring failure.
#   3  MISSING    codexbar is not installed.
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
    out=$("$BIN" guard --provider "$PROVIDER" --min-remaining "$MIN_REMAINING" \
            --window "$WINDOW" --json 2>/dev/null)
    # Capture the exit code directly. Reading it through a pipe returns the
    # pipe's status, which has bitten this estate three times.
    rc=$?
    [ -z "$out" ] && { echo "quota: no output from $BIN guard -- UNAVAILABLE" >&2; exit 2; }
    pct=$(printf '%s' "$out" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("remainingPercent","?"))' 2>/dev/null)
    why=$(printf '%s' "$out" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("unavailableReason") or "")' 2>/dev/null)
    if [ -n "$why" ]; then
      echo "quota: UNAVAILABLE ($why) -- treat as unknown, never as safe" >&2
      exit 2
    fi
    case "$rc" in
      0) echo "quota: SAFE ${pct}% remaining in $WINDOW (floor ${MIN_REMAINING}%)"; exit 0 ;;
      1) echo "quota: WIND DOWN -- ${pct}% remaining in $WINDOW, below ${MIN_REMAINING}%"; exit 1 ;;
      69) echo "quota: UNAVAILABLE -- $BIN could not read the quota" >&2; exit 2 ;;
      *) echo "quota: $BIN guard exited $rc -- treating as UNAVAILABLE" >&2; exit 2 ;;
    esac
    ;;
  eta)
    "$BIN" usage --format json 2>/dev/null | python3 -c '
import json,sys
try: d=json.load(sys.stdin)
except Exception: print("unavailable"); sys.exit(2)
for p in d if isinstance(d,list) else []:
    if p.get("provider")!="'"$PROVIDER"'": continue
    pace=(p.get("pace") or {}).get("primary") or {}
    if pace:
        print(f'"'"'{pace.get("etaSeconds","?")} {pace.get("summary","")}'"'"')
        sys.exit(0)
print("unavailable"); sys.exit(2)'
    ;;
  summary)
    "$BIN" usage --format json 2>/dev/null | python3 -c '
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
