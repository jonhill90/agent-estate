#!/bin/bash
# agent-estate#827 fix2: pins `lanes.sh`'s own classification of exactly
# the pane shape `_vacate_pane_before_reap` (reconcile_lane_completions.py)
# produces, before and after this fix pass -- the piece the REQUEST_CHANGES
# review on #827 found neither existing test covered.
#
# Both rows below keep the window NAME a just-finished review task left
# behind (`_vacate_pane_before_reap` never renames a window -- that is
# `lane-done.sh`'s job, on a different path, and untouched by this fix).
# What differs is only what `respawn-pane` was told to run there:
#
#   w-bare-vacate   the FIRST version of this fix (`respawn-pane -k -c
#                   <dir>` with NO command) -- a bare shell. lanes.sh reads
#                   the command (`^($SHELLS)$`, lanes.sh:536), sees the
#                   window name still matches TASK_NAME_RE (lanes.sh:589),
#                   and reports `stale`, not `free` -- `dispatch-lane-
#                   select.sh` only ever offers `state=="free"`
#                   (`lanes.sh --free`), so a `stale` lane silently drops
#                   out of the dispatch pool until a human notices. This is
#                   the exact defect the #827 review caught.
#
#   w-fixed-vacate  this fix pass: `respawn-pane -k -c <dir> <launch_cmd>`,
#                   the SAME harness the lane was already running
#                   (`harness-launch-cmd.sh`, resolved from the ledger's
#                   own recorded harness). Once that fresh harness paints
#                   its own ready chrome, lanes.sh reads `free` again --
#                   dispatchable, exactly like any other idle lane.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANES="$HERE/../../scripts/supervisor/lanes.sh"
pass=0; fail=0

D=$(mktemp -d); mkdir -p "$D/bin"
cp "$HERE/stubs/tmux-lanes" "$D/bin/tmux"
cp "$HERE/stubs/ps-lanes" "$D/bin/ps"
# columns: index|name|command|status-line|seconds-since-output|in-mode
cat > "$D/fixture" <<'FIX'
5|ae827-vacate|zsh|❯ |1|0
6|ae827-vacate|claude.exe|❯ ready|1|0
FIX

out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$LANES" test-session 2>&1)

# Both rows share the identical window name `ae827-vacate` on purpose (see
# this file's own header) -- `want`'s helper keys off the name, which would
# no longer disambiguate the two rows, so assert directly against index.
if grep -qE '^5 +ae827-vacate +[^ ]+ +stale$' <<<"$out"; then
  echo "  ok   a bare-shell vacated pane (the pre-fix2 defect) is stale, not free"; pass=$((pass+1));
else
  echo "  FAIL bare-shell vacated pane is not 'stale' in:"; sed 's/^/       /' <<<"$out"; fail=$((fail+1));
fi
if grep -qE '^6 +ae827-vacate +[^ ]+ +free$' <<<"$out"; then
  echo "  ok   a harness-relaunched vacated pane (this fix) is free, dispatchable again"; pass=$((pass+1));
else
  echo "  FAIL harness-relaunched vacated pane is not 'free' in:"; sed 's/^/       /' <<<"$out"; fail=$((fail+1));
fi

# The consumer that actually matters: `dispatch-lane-select.sh` only ever
# reads `lanes.sh --free`. The bare-shell row (window 5) must never appear
# there; the relaunched row (window 6) must.
free_out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/fixture" bash "$LANES" --free test-session 2>&1)
if grep -qE '^test-session:5\b' <<<"$free_out"; then
  echo "  FAIL a stale bare-shell lane must never appear in --free:"; sed 's/^/       /' <<<"$free_out"; fail=$((fail+1));
else
  echo "  ok   a stale bare-shell lane never appears in --free (the #827 defect stays closed)"; pass=$((pass+1));
fi
if grep -qE '^test-session:6\b' <<<"$free_out"; then
  echo "  ok   the harness-relaunched lane appears in --free -- dispatchable again"; pass=$((pass+1));
else
  echo "  FAIL the harness-relaunched lane is missing from --free:"; sed 's/^/       /' <<<"$free_out"; fail=$((fail+1));
fi

rm -rf "$D"
echo "test_lanes_vacated_review_pane: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
