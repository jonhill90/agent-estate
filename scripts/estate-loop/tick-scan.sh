#!/bin/bash
# Mechanical half of an ESTATE TICK -- detection and gated action only, no
# judgement. Extracted 2026-08-22 (mechanize skill) after three consecutive
# ticks reasoned through the identical board-scan + pane-capture sequence
# and reached the identical no-op conclusion each time: that is the smell
# ("any answer a model re-derives identically every time it runs"), not
# evidence the reasoning was ever needed.
#
# What's mechanical and stays here: is a PR CI-green + actually reviewed
# (merge it -- the gate, not the judgement, already lives in merge-pr.sh /
# ci_gate.py / verdict-independence.sh); is a pane busy or idle (a fixed
# rule over `esc to interrupt`). Both are functions of visible state with
# closed, already-known failure modes.
#
# What's deliberately NOT here, and must stay judgement in the calling
# tick: deciding what brief an idle-with-nothing-queued pane gets
# (sequencing/priority), diagnosing why a PR is red or a rebase is stuck
# (context a rule can't enumerate), and delivering a brief (this script
# only REPORTS idle; the C-u/type/Enter send is a judgement act because
# sending the same already-completed brief again is exactly the "stop the
# loop" concatenation-class mistake this estate has already paid for once).
#
# Usage: tick-scan.sh   -- prints one line per finding, machine-parseable:
#   MERGED <repo>#<n> rc=<rc>
#   PANE <window> busy
#   PANE <window> idle brief=<path-or-none>
# Exit 0 always (a scan finding nothing to do is success, not failure).
set -uo pipefail
B=/Users/jon/.local/state/estate-loop

# agent-supervisor#521: this file is private Director state (not tracked in
# any repo -- confirmed, `git rev-parse --show-toplevel` here fails), so it
# is fixed directly rather than through the agent-supervisor PR the rest of
# #521's fix ships in. Same defense-in-depth reasoning as that PR's
# dim-strip.sh: a plain `capture-pane -p` here (below) cannot tell a Claude
# Code prompt-suggestion (painted dim into an idle box) apart from real pane
# text. Part 1 of #521 (CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false at
# launch, agent-supervisor/scripts/supervisor/harness/claude.sh) is the
# primary fix and covers this file's own busy/session-limit grep already
# (neither string is ever painted dim); this sources the same shared
# stripping rule anyway, for a future addition to this file's classification
# that DOES need to read the box, and for a harness release where the
# launch-time suppression stops working.
AGENT_SUPERVISOR_REPO="${AGENT_SUPERVISOR_REPO:-/Users/jon/source/repos/Personal/agent-supervisor}"
# agent-supervisor#682: FAIL LOUDLY, not quietly, when this doesn't resolve
# to a real checkout. The inventory's ranked failure mode for this file was
# `exit 0` on a missing path reading as "no work" -- a scan that finds
# nothing because $AGENT_SUPERVISOR_REPO is wrong looks identical to a scan
# that found nothing. Checked once, up front, before the merge/status loops
# below can be misread as "board clear".
if [ ! -d "$AGENT_SUPERVISOR_REPO" ]; then
  echo "FATAL: AGENT_SUPERVISOR_REPO does not resolve to a real checkout: $AGENT_SUPERVISOR_REPO" >&2
  exit 1
fi
DIM_STRIP="$AGENT_SUPERVISOR_REPO/scripts/supervisor/dim-strip.sh"
[ -r "$DIM_STRIP" ] && . "$DIM_STRIP"

# --- mechanical: merge anything CI-green + actually reviewed ---------------
# `--match-head-commit` pins the merge to the exact SHA this scan just
# evaluated (added 2026-08-22, verify-the-instrument pass): without it there
# is a TOCTOU window between the `gh pr list` snapshot above and the
# `gh pr merge` call below where a new, unreviewed commit could land on the
# branch and get merged under cover of an approval that was never given for
# it -- the same stale-SHA class merge-pr.sh's own ci_gate.py already
# guards against for agent-supervisor; this closes the same hole on the
# plain-`gh` path used for repos with no lane ledger.
# agent-supervisor is handled SEPARATELY (fixed 2026-08-22, found while
# investigating why check.log had never once shown an automated merge
# there despite real comment-verdict reviews existing, e.g. #505):
# verdicts in this repo arrive as a `Verdict:`/`Review-Lane:` PR COMMENT,
# never a native GitHub review, so `reviewDecision` sits empty/null
# forever no matter how thoroughly a PR was actually reviewed -- the
# pre-filter below silently excluded every single agent-supervisor PR from
# ever reaching the merge attempt. `merge-pr.sh` already knows how to read
# a comment verdict (via verdict.py) and already gates on CI+independence
# correctly; the fix is to stop pre-filtering this repo on a signal it
# doesn't use and let merge-pr.sh's own gates decide instead.
gh pr list --repo jonhill90/agent-supervisor --state open --json number,headRefOid \
  --jq '.[] | "\(.number) \(.headRefOid)"' 2>/dev/null |
while read -r n sha; do
  [ -n "$n" ] && [ -n "$sha" ] || continue
  out=$(bash "$AGENT_SUPERVISOR_REPO/scripts/supervisor/merge-pr.sh" jonhill90/agent-supervisor "$n" --squash --delete-branch --match-head-commit "$sha" 2>&1)
  rc=$?
  # merge-pr.sh refuses (rc=1) for every not-yet-mergeable PR, which is
  # most of them most ticks -- that's normal gate behavior, not a finding.
  # Only log it when it actually did something.
  [ "$rc" -eq 0 ] && echo "MERGED jonhill90/agent-supervisor#$n rc=$rc sha=$sha out=$out"
done

# `--match-head-commit` pins the merge to the exact SHA this scan just
# evaluated (added 2026-08-22, verify-the-instrument pass): without it there
# is a TOCTOU window between the `gh pr list` snapshot above and the
# `gh pr merge` call below where a new, unreviewed commit could land on the
# branch and get merged under cover of an approval that was never given for
# it. agent-tui/skills/agent-dotfiles have no lane ledger and no
# comment-verdict path, so a real GitHub review (reviewDecision==APPROVED)
# is the only signal available for them.
for repo in jonhill90/agent-tui jonhill90/skills jonhill90/agent-dotfiles; do
  gh pr list --repo "$repo" --state open --json number,reviewDecision,statusCheckRollup,headRefOid \
    --jq '.[] | select(.reviewDecision=="APPROVED" and ((.statusCheckRollup|length)>0) and ([.statusCheckRollup[].conclusion]|all(.=="SUCCESS"))) | "\(.number) \(.headRefOid)"' 2>/dev/null |
  while read -r n sha; do
    [ -n "$n" ] && [ -n "$sha" ] || continue
    out=$(gh pr merge "$n" --repo "$repo" --squash --delete-branch --match-head-commit "$sha" 2>&1)
    echo "MERGED $repo#$n rc=$? sha=$sha out=$out"
  done
done

# --- mechanical: classify each build pane busy/idle -------------------------
# Also checks for a Claude Code SESSION LIMIT message (added 2026-08-22):
# on 2026-08-22 all 4 build panes hit this simultaneously and sat idle for
# ~2h40m before anyone noticed, because the ONLY quota signal this loop
# watched was codexbar's primary-window usedPercent -- a different, and
# apparently unrelated, constraint from Claude Code's own per-session cap.
# A pane hitting this looks EXACTLY like "idle, nothing queued" to the
# busy/idle check above; this line is what makes it distinguishable so a
# tick can say "session-limited" instead of quietly deciding there's
# nothing to do.
#
# Window IDs are discovered fresh every run (fixed 2026-08-23) rather than
# hardcoded, per invariant 5 (address by window_id, never index) -- but a
# hardcoded window_id goes stale exactly the same way an index does the
# moment the estate is restored/rebuilt and windows get new IDs. Caught
# live: @38/@39/@51/@52 used to be BUILD-1..4; a restore had since made
# them build-2..build-5 (shifted one), added a 6th lane (build-6/@76,
# estate:6) this scan had never seen at all, and the hardcoded map was
# silently misattributing one lane's pane content to a different lane's
# label. Discover by name pattern (build-N) in the `estate` session
# instead, so a rebuild can't desync the map again.
while read -r w who; do
  [ -n "$w" ] || continue
  idx=${who##build-}
  if command -v strip_dim_sgr >/dev/null 2>&1; then
    pane=$(tmux capture-pane -pe -t "$w" 2>/dev/null | strip_dim_sgr) || { echo "PANE $who gone"; continue; }
  else
    pane=$(tmux capture-pane -p -t "$w" 2>/dev/null) || { echo "PANE $who gone"; continue; }
  fi
  if grep -q 'esc to interrupt' <<<"$pane"; then
    echo "PANE $who busy"
  elif grep -qi "hit your session limit" <<<"$pane"; then
    # busy-check above MUST run first: a session-limit banner from an
    # earlier hit can still be visible on-screen (stale scrollback) even
    # after the pane resumed and is actively mid-turn again -- confirmed
    # live 2026-08-22, this exact ordering bug produced a false positive
    # on a pane that was 4m45s into real, current work.
    resets=$(grep -io 'resets [^)]*' <<<"$pane" | tail -1)
    echo "PANE $who SESSION-LIMITED ($resets)"
  else
    # Brief naming has not been consistent in practice (agent${N}.md vs.
    # agent-b${N}.md for build-6) -- check both rather than assume one.
    brief="$B/agent${idx}.md"
    [ -f "$brief" ] || brief="$B/agent-b${idx}.md"
    [ -f "$brief" ] && echo "PANE $who idle brief=$brief" || echo "PANE $who idle brief=none"
  fi
done < <(tmux list-windows -t estate -F '#{window_id} #{window_name}' 2>/dev/null | awk '$2 ~ /^build-/')
