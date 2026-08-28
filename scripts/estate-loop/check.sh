#!/bin/bash
# Estate build loop. No supervisor, no ledger, no lease. Five panes, one board.
#
# LOOP CONTRACT (agent-dotfiles docs/loop-engineering.md), because the last
# loop had none and never terminated:
#   Objective    : SPEC-shell.md built AND merged, PLUS whatever the Director
#                  dispatches once it clears (content-pane wiring,
#                  feat/supervisord, skills#230) -- widened 2026-08-22 when
#                  the original narrow objective (spec items only) was found
#                  to strand its own follow-on work the same way "never
#                  merges" stranded the original 8 PRs. One board, not one
#                  fixed list, is the actual objective now.
#   Trigger      : launchd, every 10 minutes
#   Verification : `go build && go test && go vet` in CI on each PR
#   Stop 1 (goal): spec items done AND zero open PRs across all four
#                  tracked repos (quiescence, not a fixed item count)
#                  -> writes DONE, unloads itself
#   Stop 2 (safety): 300 ticks (~50h), quota >=97%, or host pressure
#   Terminal states: done | halted-quota | halted-ticks
#
# Fixed 2026-08-22, same tick as the widening above:
#   - goal check counted PR TITLES matching "S[0-9]+", not code -- undercounted
#     forever (S1+S2 share one PR title, S4 never appears in any title even
#     though it shipped). Now checks package/wiring presence on origin/main
#     directly.
#   - loop dispatched and re-prompted but never MERGED -- see the merge step
#     below, gated on CI green + an actual GitHub review (merge-pr.sh's own
#     ci_gate.py + verdict-independence.sh for agent-estate, plain
#     reviewDecision==APPROVED for repos with no lane ledger).
#   - re-prompting a pane with unsubmitted text already in its input box
#     concatenated onto it instead of replacing it -- now clears (C-u) first.
set -uo pipefail
export PATH="/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:$PATH"
B=/Users/jon/.local/state/estate-loop
LOG=$B/check.log
STATE=$B/state
ts() { date -u +%Y-%m-%dT%H:%M:%SZ; }
say() { echo "$(ts) $*" >> "$LOG"; }

# agent-supervisor#682: single resolution point for "where is the
# agent-supervisor checkout", so a repo rename (agent-supervisor ->
# agent-estate) can't leave this hardcoded path silently wrong. This is the
# same estate-wide $AGENT_SUPERVISOR_REPO variable tick-scan.sh (this
# directory) and supervisor_view.py's _default_supervisord_binary already
# resolve from -- reused here rather than adding a second convention.
# #686 covers the separate GH-repo-NAME literal via session-defaults.sh;
# this is the filesystem-checkout-PATH half of the same rename, which #686
# does not touch.
#
# agent-estate#729: the rename actually happened (checkout moved to
# .../Personal/agent-estate, #728), so this hardcoded default went stale.
# Prefer whichever of the two names exists on disk, rather than swapping one
# hardcoded literal for another -- that would just rebuild the same trap
# under a fresh label the next time this repo is renamed.
_AGENT_SUPERVISOR_REPO_DEFAULT=/Users/jon/source/repos/Personal/agent-estate
[ -d "$_AGENT_SUPERVISOR_REPO_DEFAULT" ] || _AGENT_SUPERVISOR_REPO_DEFAULT=/Users/jon/source/repos/Personal/agent-supervisor
AGENT_SUPERVISOR_REPO="${AGENT_SUPERVISOR_REPO:-$_AGENT_SUPERVISOR_REPO_DEFAULT}"
# FAIL LOUDLY, not quietly: the inventory's ranked failure mode for this
# script was `exit 0` on a missing path reading as "no work" -- so after a
# rename it goes quiet instead of breaking. A missing checkout is fatal here,
# checked once up front, before anything below can mistake "can't find it"
# for "found nothing to do".
if [ ! -d "$AGENT_SUPERVISOR_REPO" ]; then
  say "FATAL: AGENT_SUPERVISOR_REPO does not resolve to a real checkout: $AGENT_SUPERVISOR_REPO"
  echo "FATAL: AGENT_SUPERVISOR_REPO does not resolve to a real checkout: $AGENT_SUPERVISOR_REPO" >&2
  exit 1
fi
# NOTIFY-ON-HALT. The loop must never stop silently.
#
# On 2026-08-22 it halted at 14:31Z on an invented $120 budget cap and Jon
# found out hours later, from a human, after the build had been dead all
# morning. A loop that stops without telling anyone is indistinguishable from
# a loop that is working.
NOTIFY=/Users/jon/.claude/skills/notify/scripts/notify.py
NOTIFY_ENV=/Users/jon/.local/state/agent-dotfiles-supervisor/notify.env
page() {
  [ -f "$NOTIFY_ENV" ] || { say "NOTIFY SKIPPED: no $NOTIFY_ENV"; return; }
  [ -f "$NOTIFY" ] || { say "NOTIFY SKIPPED: no $NOTIFY"; return; }
  ( set -a; . "$NOTIFY_ENV"; set +a
    python3 "$NOTIFY" --channel telegram --send --force --message "$1" >>"$LOG" 2>&1 ) \
    && say "NOTIFY sent" || say "NOTIFY FAILED (exit $?)"
}
halt() {
  say "TERMINAL: $1"
  echo "$1" > "$STATE/terminal"
  open=$(gh pr list --repo jonhill90/agent-tui --state open --json number --jq 'length' 2>/dev/null || echo '?')
  page "estate loop STOPPED. reason: $1. ticks=$TICKS, open PRs=$open. restart: launchctl kickstart -k gui/$(id -u)/com.jonhill.estate-build"
  launchctl bootout gui/$(id -u)/com.jonhill.estate-build 2>/dev/null
  exit 0
}
mkdir -p "$STATE"

[ -f "$STATE/terminal" ] && exit 0

# --- Stop 2a: tick cap -------------------------------------------------------
TICKS=$(( $(cat "$STATE/ticks" 2>/dev/null || echo 0) + 1 ))
echo "$TICKS" > "$STATE/ticks"
[ "$TICKS" -gt 300 ] && halt "halted-ticks ($TICKS)"

# --- Stop 2b: host pressure (skip this tick, not terminal) -------------------
load=$(uptime | sed 's/.*averages: //' | awk '{print $1}' | tr -d ,)
cores=$(sysctl -n hw.ncpu)
if awk -v l="$load" -v c="$cores" 'BEGIN{exit !(l/c >= 3.0)}'; then
  say "SKIP tick=$TICKS load/core high ($load/$cores)"; exit 0
fi

# --- Stop 2c: QUOTA, not dollars ----------------------------------------------
# The dollar cap is GONE. It was mine, unasked for, set at $120 -- about a
# sixth of a normal day -- and it halted the build at $125 on the cheapest day
# of the week.
#
# The real constraint is the weekly quota, which EXPIRES UNUSED. Under-using it
# is a loss, not a saving. So the only spend stop is Jon's own standing rule:
# stop at 97%, because past that the remaining credits fund the wake-up poll
# and nothing else.
#
# A quota reading that cannot be taken is NOT treated as safe -- but it is also
# not treated as terminal, because a slow codexbar means the MACHINE is loaded,
# which the pressure gate above already handles. Skip the tick, try again.
pct=$(timeout 45 codexbar usage --provider claude --json 2>/dev/null | python3 -c "
import json,sys
try:
    d=json.load(sys.stdin); u=(d[0] if isinstance(d,list) else d)['usage']
    print(int(u['secondary']['usedPercent']))
except Exception: print(-1)
" 2>/dev/null || echo -1)
if [ "$pct" = "-1" ]; then
  say "SKIP tick=$TICKS quota unreadable -- not treated as safe, retrying next tick"; exit 0
fi
if [ "$pct" -ge 97 ]; then halt "halted-quota (weekly ${pct}%)"; fi

# --- Stop 1: goal ------------------------------------------------------------
# FIXED 2026-08-22: this used to count MERGED PRs whose title matched
# "S[0-9]+" and compare that PR-count against the 12 spec headers. Two
# things made that always wrong: #73 lands S1+S2 under one title, #74 lands
# S3 under a title that never mentions S4 even though S4 ("wire the three
# existing screens" -- tasks/usage/lanes -> routeToPane) shipped inside it
# incrementally. A title-text metric can't see either case, so the count
# topped out at 10 or 11 of 12 forever and the goal check could never fire
# -- the loop re-prompted builders with nothing to build, tick after tick.
# Check the CODE instead of PR titles (build-loop.md's own rule: "check the
# code, not your memory"): every SPEC-shell.md item's backing package
# either exists on origin/main or it doesn't.
cd /Users/jon/source/repos/Personal/agent-tui || exit 0
git fetch origin --quiet 2>/dev/null
remaining=$(git show origin/main:docs/SPEC-shell.md 2>/dev/null | grep -cE '^## S[0-9]+ —' || echo 0)
# One boolean per S-item, checked against the actual package tree (S1/S2
# both live in internal/nav and so share one check; S4 is routeToPane's
# wiring, not a package of its own).
has_pkg() { git show "origin/main:internal/$1" >/dev/null 2>&1; }
shell_model=$(git show origin/main:internal/shell/model.go 2>/dev/null)
s4_wired=0
grep -q '"tasks": *PaneBoard' <<<"$shell_model" && grep -q '"usage": *PaneCost' <<<"$shell_model" \
  && grep -q '"lanes": *PaneLanes' <<<"$shell_model" && s4_wired=1
merged=0
has_pkg nav          && merged=$((merged+2))  # S1, S2
has_pkg shell         && merged=$((merged+1))  # S3
[ "$s4_wired" = 1 ]   && merged=$((merged+1))  # S4
has_pkg stub          && merged=$((merged+1))  # S5
has_pkg agents        && merged=$((merged+1))  # S6
has_pkg chat          && merged=$((merged+1))  # S7
has_pkg skills        && merged=$((merged+1))  # S8
has_pkg mcpservers    && merged=$((merged+1))  # S9
has_pkg connectors    && merged=$((merged+1))  # S10
has_pkg admin         && merged=$((merged+1))  # S11
has_pkg session       && merged=$((merged+1))  # S12
spec_done=0
[ "$remaining" -gt 0 ] && [ "$merged" -ge "$remaining" ] && spec_done=1
# DELIBERATELY not an immediate halt on spec_done alone (2026-08-22): the
# loop's contract was written when SPEC-shell.md was the entire objective.
# It no longer is -- the Director dispatches follow-on work once the spec
# clears (content-pane wiring, the feat/supervisord PR, skills#230) that
# still needs this same tick's merge step and re-prompt loop, or it hits
# the exact "opened, never merged" gap this file was just fixed for, one
# level up. The real stop condition is quiescence: spec done AND every
# tracked repo's board is clear AND nobody is mid-brief.
open_all=0
for repo in jonhill90/agent-tui jonhill90/agent-estate jonhill90/skills jonhill90/agent-dotfiles; do
  n=$(gh pr list --repo "$repo" --state open --json number --jq 'length' 2>/dev/null || echo 0)
  open_all=$((open_all + n))
done
say "tick=$TICKS quota=${pct}% spec_items=$remaining spec_items_built=$merged spec_done=$spec_done open_prs_all_repos=$open_all"
if [ "$spec_done" = 1 ] && [ "$open_all" -eq 0 ]; then halt "done (spec complete, board clear)"; fi

# --- Work: merge what is green and reviewed ---------------------------------
# The loop's other known gap (fixed 2026-08-22): it dispatched and
# re-prompted but NEVER MERGED -- 8 PRs landed overnight, zero merged by the
# loop itself. Nothing here skips review: `merge-pr.sh` (agent-estate)
# chains ci_gate.py + verdict-independence.sh and refuses closed on any
# unresolved verdict; agent-tui has no lane ledger, so the bar there is
# GitHub's own `reviewDecision == APPROVED` (a real review event happened)
# plus every check green. Neither path merges on CI alone.
for repo in jonhill90/agent-tui jonhill90/agent-estate; do
  gh pr list --repo "$repo" --state open --json number,reviewDecision,statusCheckRollup \
    --jq '.[] | select(.reviewDecision=="APPROVED" and ((.statusCheckRollup|length)>0) and ([.statusCheckRollup[].conclusion]|all(.=="SUCCESS"))) | .number' 2>/dev/null |
  while read -r n; do
    [ -n "$n" ] || continue
    if [ "$repo" = "jonhill90/agent-estate" ]; then
      out=$(bash "$AGENT_SUPERVISOR_REPO/scripts/supervisor/merge-pr.sh" "$repo" "$n" --squash --delete-branch 2>&1)
      rc=$?
    else
      out=$(gh pr merge "$n" --repo "$repo" --squash --delete-branch 2>&1)
      rc=$?
    fi
    say "merge $repo#$n rc=$rc: $out"
  done
done

# --- Work: re-prompt any idle agent -----------------------------------------
# FIXED 2026-08-23: this used to hardcode window @IDs (@58/@38/@39/@51/@52).
# Window IDs are NOT stable across a tmux server restart -- the server died
# at ~06:54 and the estate session was rebuilt with new IDs, so every send
# here targeted a window that no longer existed. `tmux send-keys` to a dead
# target fails silently (nonzero exit, no visible error in this loop's own
# output), so the symptom was indistinguishable from "lane already handled":
# the loop logged a re-prompt and the lane just stayed idle forever, tick
# after tick. Address by session:index instead -- index is what actually
# stayed stable across the rebuild (verified live via `tmux list-windows -a`
# immediately after), and re-verify with that command rather than trusting
# any hardcoded id, including this fix, the next time the server restarts.
for pair in "1:DIRECTOR" "2:BUILD-1" "3:BUILD-2" "4:BUILD-3" "5:BUILD-4"; do
  w=${pair%%:*}; who=${pair##*:}
  pane=$(tmux capture-pane -p -t "=estate:$w" 2>/dev/null) || { say "$who pane GONE"; continue; }
  if grep -q 'esc to interrupt' <<<"$pane"; then say "$who busy"; continue; fi
  # The DIRECTOR gets a different prompt: it owns the estate and decides the
  # work. The builds get told to take the next spec item -- UNLESS the
  # Director has already dropped a specific brief for them (agent1.md ..
  # agent4.md, the b1.md pattern), in which case that brief wins: the
  # generic "next unbuilt SPEC item" message is a fallback for when nobody
  # judged yet, not a standing instruction once spec items run out (they
  # did -- see the goal-count fix above; re-sending it after that point is
  # exactly the wasted-tick churn build-2/3/4 kept reporting).
  idx=${who##BUILD-}
  brief="$B/agent${idx}.md"
  if [ "$who" = "DIRECTOR" ]; then
    msg="Read $B/owner.md. You are the DIRECTOR. Check the board (open PRs across the four repos), check your build agents at estate:2-5, and drive the next thing. Merge what is green and reviewed. Do not ask Jon anything you can answer from the corpus, the docs, a council or a sanity-check."
  elif [ -f "$brief" ]; then
    msg="Read $brief and do exactly what it says."
  else
    msg="Read $B/build-loop.md and do exactly what it says. You are $who. Take the next unbuilt item in docs/SPEC-shell.md that no open PR already covers."
  fi
  # C-u FIRST: clears anything already sitting unsubmitted in the box before
  # typing. Without this, a prior unsubmitted line (observed live 2026-08-22
  # in three of four build panes, from a manual "stop the loop" that never
  # got an Enter) gets this tick's message concatenated onto it instead of
  # replaced -- the #178 failure class. Then send text, pause, THEN Enter --
  # a single send-keys with Enter appended has dropped its first character
  # before (the launch that produced "command not found: laude"). Verified
  # delivery beats a one-liner.
  tmux send-keys -t "=estate:$w" C-u
  tmux send-keys -t "=estate:$w" "$msg"
  sleep 2
  tmux send-keys -t "=estate:$w" Enter
  say "$who idle -> re-prompted"
done

open=$(gh pr list --repo jonhill90/agent-tui --state open --json number,title --jq '[.[]|"#\(.number) \(.title[0:50])"]|join(" | ")' 2>/dev/null)
say "open PRs: ${open:-none}"
