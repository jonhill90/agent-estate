#!/bin/bash
# healthtick.sh <deadline_epoch> <interval_seconds>
#
# Event-driven estate watcher. Polls estate/PR state and exits as soon as the
# state STRING changes, so the caller wakes on a real transition, not a timer.
#
# Reconstructed 2026-09-07 after a Claude update removed
# ~/.claude/jobs/8182f39f/tmp/, which held the only copy. THIS is the durable
# master; the old path is now a symlink to it. Keep it in the repo tree, never
# in job scratch. agent-estate#1248 tracks that this never re-arms itself.
set -u
DEADLINE=${1:?deadline_epoch required}
INTERVAL=${2:-180}
# Repos whose open PRs count toward run state. agent-dotfiles was invisible
# here until 2026-09-07 -- #348 sat open and unreviewable with the watcher
# reporting IDLE, the same blind spot the old flow-watch had (Fable finding 3).
REPOS="jonhill90/agent-estate jonhill90/agent-dotfiles jonhill90/skills"
BIN=/tmp/estate-bin
# Fail loud if the binary vanished the way the job scratch did. A missing BIN
# silently read active=0 and turned every wake into a false IDLE/STALLED.
if [ ! -x "$BIN" ]; then
  printf 'HEALTHTICK-ERROR: %s missing or not executable -- refusing to report state\n' "$BIN" >&2
  exit 2
fi
# PRs held by standing order -- never counted as a stall.
PARKED="1014 1015 1223 1224"

state() {
  local active prs open line n info head mergeable ci verdicts label
  active=$( { "$BIN" inflight 2>&1 || true; } | grep -cE '^[[:space:]]*(dispatch|run)-' )
  prs=""
  for REPO in $REPOS; do
    for n in $(gh pr list --repo "$REPO" --state open --json number --jq '.[].number' 2>/dev/null | sort -n); do
      prs="$prs ${REPO}#${n}"
    done
  done
  open=""; line=""
  for ref in $prs; do
    REPO=${ref%%#*}; n=${ref##*#}
    short=$(printf '%s' "$REPO" | sed 's|jonhill90/agent-estate|est|;s|jonhill90/agent-dotfiles|dot|;s|jonhill90/skills|skl|')
    open="${open}${short}${n},"
    info=$(gh pr view "$n" --repo "$REPO" --json headRefOid,mergeable 2>/dev/null)
    head=$(printf '%s' "$info" | sed -n 's/.*"headRefOid":"\([0-9a-f]*\)".*/\1/p')
    mergeable=$(printf '%s' "$info" | sed -n 's/.*"mergeable":"\([A-Z]*\)".*/\1/p')
    ci=$(gh pr checks "$n" --repo "$REPO" 2>/dev/null | awk '{print $2}' | sort -u | paste -sd/ -)
    verdicts=$(gh pr view "$n" --repo "$REPO" --json comments \
      --jq "[.comments[]|select(.body|test(\"Reviewed-SHA: ${head}\"))|select(.body|test(\"Verdict: APPROVE\"))]|length" 2>/dev/null)
    [ -z "$verdicts" ] && verdicts=0
    line="${line} ${short}${n}=${mergeable},ci=${ci},verdicts@head=${verdicts}"
  done
  # PARKED holds PRs deferred by standing order; they are not a stall.
  # 1014/1015 (held), 1223 (held on provenance), 1224/K6.0 (deferred).
  label="IDLE-no-eligible-work"
  if [ "$active" = "0" ]; then
    for ref in $prs; do
      n=${ref##*#}; REPO=${ref%%#*}
      case " $PARKED " in *" $n "*) continue ;; esac
      short=$(printf '%s' "$REPO" | sed 's|jonhill90/agent-estate|est|;s|jonhill90/agent-dotfiles|dot|;s|jonhill90/skills|skl|')
      if printf '%s' "$line" | grep -q "${short}${n}=MERGEABLE,ci=pass"; then
        label="STALLED"
      fi
    done
  fi
  printf '%s | active=%s open=%s%s' "$label" "$active" "$open" "$line"
}

BASE=$(state)
printf 'BASELINE: %s\n' "$BASE"
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  sleep "$INTERVAL"
  NOW=$(state)
  if [ "$NOW" != "$BASE" ]; then
    printf 'WAKE: status change\n  was: %s\n  now: %s\n' "$BASE" "$NOW"
    exit 0
  fi
done
printf 'WAKE: deadline reached\n  final: %s\n' "$BASE"
