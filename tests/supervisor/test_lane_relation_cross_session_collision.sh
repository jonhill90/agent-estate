#!/bin/bash
# agent-supervisor#235. `core.lane_relation`'s string-shape check answers
# `same` not only for two ids that differ solely by a session RENAME (its
# original, correct #108 case) but also for two ids from TWO DIFFERENT,
# SIMULTANEOUSLY-LIVE sessions that merely happen to share a window index --
# `int(left.group("index")) == int(right.group("index"))` does not check the
# session names match at all. Measured verbatim on 2026-08-18, PR #205 (six
# contributor tasks across three sessions):
#
#   dispatch: skipping agent-supervisor:3 -- it cannot be told apart from
#   contributor lane agent-tui:3 (task as199-fix205b, contributed to PR #205
#   under review)
#   dispatch: skipping agent-supervisor:4 -- it cannot be told apart from
#   contributor lane agent-dotfiles:4 (task live199-ad199-bash32,
#   contributed to PR #205 under review)
#
# `agent-supervisor:3` and `agent-tui:3` are two DIFFERENT physical windows
# on two live sessions that never renamed into each other -- the shape check
# cannot tell that apart from #108's rename case, so it answers `same` and
# the guard refuses every candidate, for every session, that happens to sit
# at an occupied index. Minting more lanes does not help: a new index is
# just as likely to collide with some other session's contributor.
#
# The fix is the same one #235's renumber case already uses: reconcile
# through the ledger's own `pane_id` registry via `--lane-pane-id`, which
# proves two SESSIONS' windows apart exactly as it proves two INDICES of the
# same session apart. This file must fail against a `cli.py` with no
# `--lane-pane-id` support, and pass once it has one.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI="$HERE/../../scripts/supervisor/cli.py"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "cli.py lane-relation -- cross-session index collision must not admit a self-review, nor block a real reviewer (agent-supervisor#235)"

D=$(mktemp -d)
STATE="$D/state"

seed_lane() {  # seed_lane <lane> <pane-id> <harness> <transport>
  python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
ledger = Ledger(sys.argv[2])
ledger.register_lane(
    lane=sys.argv[3], pane_id=sys.argv[4], nonce="nonce-" + sys.argv[3],
    harness=sys.argv[5], repo="/tmp/repo", server_id="srv", session_id="sess",
    command="claude", transport=sys.argv[6],
)
' "$HERE/../../scripts/supervisor" "$STATE" "$1" "$2" "$3" "$4"
}

relate() {  # relate <lane> <other> [lane-pane-id] -> the raw JSON
  if [ -n "${3:-}" ]; then
    python3 "$CLI" --state-dir "$STATE" lane-relation --lane "$1" --other "$2" --lane-pane-id "$3"
  else
    python3 "$CLI" --state-dir "$STATE" lane-relation --lane "$1" --other "$2"
  fi
}

# The contributor: recorded as "agent-tui:3", real tmux pane %50 -- exactly
# PR #205's shape.
seed_lane "agent-tui:3" "%50" claude send-keys

# A genuinely free candidate window, index 3 in a DIFFERENT, unrelated live
# session, real tmux pane %61.
seed_lane "agent-supervisor:3" "%61" claude send-keys

# Without a live pane id, the shape check alone decides on index equality
# and gets it wrong: both index "3" -> "same" -- refuses a lane that never
# touched this PR.
OUT_BROKEN=$(relate "agent-supervisor:3" "agent-tui:3")
want_contains "shape-only comparison (no live pane id) is the known-broken case: 'same'" \
  '"relation":"same"' "$OUT_BROKEN"

# With the candidate's live pane id supplied (%61, what dispatch.sh measures
# straight off the tmux target it already resolved), the same comparison
# must resolve 'different' -- these are two distinct windows and the real
# reviewer must be admitted.
OUT_FIXED=$(relate "agent-supervisor:3" "agent-tui:3" "%61")
want_contains "with the live pane id, two distinct sessions at the same index resolve 'different'" \
  '"relation":"different"' "$OUT_FIXED"

# The negative this must not break: a candidate that IS the contributor
# (same physical pane, e.g. reached via a stale ledger string) still
# resolves 'same' and is still excluded.
OUT_SELF=$(relate "agent-tui:3" "agent-tui:3" "%50")
want_contains "the genuine contributor, identified live, still resolves 'same' and is excluded" \
  '"relation":"same"' "$OUT_SELF"

# The other negative: identity that cannot be resolved must still refuse,
# never default to admit. A live pane id proves nothing about a contributor
# the ledger has never heard of.
OUT_UNKNOWN=$(relate "agent-supervisor:3" "never-registered:9" "%61")
want_contains "an unresolvable contributor still refuses (unknown), not different" \
  '"relation":"unknown"' "$OUT_UNKNOWN"

echo
echo "cli.py lane-relation cross-session collision: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
