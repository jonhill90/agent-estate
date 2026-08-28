#!/bin/bash
# Deterministic estate status. One call replaces the four the tick was making
# by hand every 3 minutes, identically, for hours.
#
# WHY THIS IS A TOOL AND NOT AN AGENT STEP: the output is a pure FUNCTION of the
# input -- load, proc count, swap, PR list, pane busy-flags. No judgement is
# involved in gathering it; judgement starts only once the numbers are on the
# table. Jon's own rule: a tool beats an agent every time.
#
# Exit codes carry the verdict so a caller can branch without re-reading:
#   0  nothing to do        (board empty, all agents busy, host fine)
#   1  action available     (a mergeable PR, or an idle agent)
#   3  HOST GUARD TRIPPED   -- do one cleanup and stop
set -uo pipefail
export PATH="/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:$PATH"

load=$(uptime | sed 's/.*averages: //' | awk '{print $1}' | tr -d ,)
cores=$(sysctl -n hw.ncpu)
procs=$(ps -eo comm | grep -c '[c]laude')
swap=$(sysctl -n vm.swapusage | awk '{print $6}')
percore=$(awk -v l="$load" -v c="$cores" 'BEGIN{printf "%.2f", l/c}')
echo "host: load=$load/${cores}c (${percore}/core) procs=$procs swap=$swap"

# Orphaned load generators. Twice on 2026-08-22 a lane's "run it under load"
# step spawned a swarm of `yes` and did not reap it; both times the parent had
# already exited (PPID 1), so nothing was ever going to clean them up, and both
# times they drove load/core past 3.0 and tripped the guard below.
#
# ONLY PPID 1 is killed. A generator whose parent is alive belongs to a test
# that is still running and is not ours to touch -- reaping that would corrupt
# a live measurement, which is the more expensive of the two mistakes.
orphans=$(ps -Ao pid,ppid,comm | awk '$3=="yes" && $2==1 {print $1}')
if [ -n "$orphans" ]; then
    n=$(printf '%s\n' "$orphans" | wc -l | tr -d ' ')
    echo "$orphans" | xargs kill 2>/dev/null
    sleep 1
    left=$(ps -Ao pid,ppid,comm | awk '$3=="yes" && $2==1' | wc -l | tr -d ' ')
    echo "reaped: $n orphaned load generator(s), $left still alive"
fi

guard=0
awk -v p="$percore" 'BEGIN{exit !(p>=3.0)}' && guard=1
[ "$procs" -ge 20 ] && guard=1
if [ "$guard" = "1" ]; then echo "HOST GUARD TRIPPED"; exit 3; fi

action=0
echo "prs:"
for r in agent-tui agent-supervisor skills agent-dotfiles agent-evals; do
  gh pr list --repo "jonhill90/$r" --state open \
     --json number,mergeable,title \
     --jq ".[]|\"  $r #\(.number) [\(.mergeable)] \(.title[0:44])\"" 2>/dev/null
done > /tmp/.estate_prs
if [ -s /tmp/.estate_prs ]; then cat /tmp/.estate_prs; grep -q 'MERGEABLE' /tmp/.estate_prs && action=1; else echo "  (none open)"; fi

echo "agents:"
# Window ids are stable across renumbering; names are not. Keyed on id.
for pair in "@58:director" "@38:build-2" "@39:build-3" "@51:build-4" "@52:build-5"; do
  w=${pair%%:*}; n=${pair##*:}
  pane=$(tmux capture-pane -p -t "=estate:$w" 2>/dev/null) || { echo "  $n GONE"; action=1; continue; }
  # BUSY is more than "esc to interrupt". An agent that finished its turn but
  # left background shells running, or that is parked on a PR, is mid-task --
  # dispatching onto it collides with work in flight. Measured live: build-2
  # showed no interrupt marker while running 2 shells and waiting on #91's CI.
  #
  # This is the agent-supervisor#414 shape inverted: there, a lane was recorded
  # complete having delivered nothing; here, a lane doing real work reads as
  # free. Both come from trusting one screen marker as the whole truth.
  if grep -qE 'esc to interrupt|[0-9]+ shells? (still )?running|. to manage' <<<"$pane"; then
    echo "  $n busy"
  else
    echo "  $n IDLE"; action=1
  fi
done

[ "$action" = "1" ] && { echo "verdict: ACTION AVAILABLE"; exit 1; }
echo "verdict: nothing to do"
exit 0
