#!/bin/bash
# agent-supervisor#235. `core.lane_relation` decides `same`/`different`
# straight from the `<session>:<index>` STRING -- and the index is tmux's own
# window index, which `renumber-windows on` (Jon's standing tmux setting)
# rewrites the instant a lower window closes. Measured 2026-08-16 in the
# `skills` session: window `%15` answered to index 3 before a close, and to
# index 4 after -- so a lane id minted before the renumber and one minted
# after can name the SAME physical pane while `lane_relation` still calls
# them `different` on index alone. That is a same-review shape wearing a
# different-review answer: the exact case `dispatch.sh`'s author-exclusion
# guard exists to catch (#235's own words -- "this can produce a
# self-review").
#
# `cli.py lane-relation --lane-pane-id` is the fix: when the CALLER can name
# the candidate's freshly-measured, live pane id (dispatch.sh gets this for
# free from the same tmux target it already resolved), the comparison is
# made against the ledger's registry (`pane_id`, already stable across a
# renumber -- #292's own widening) INSTEAD OF the index-shape check, not only
# when the shape check answers `unknown`. This test seeds a ledger exactly
# the way #292's cross-population test does, then reproduces the renumber:
# a contributor row recorded under the OLD index, and a candidate string
# that is the NEW index for the very same pane. Without `--lane-pane-id` the
# old code call answers `different` (wrong -- it is the same pane, a
# self-review). This test must fail against dispatch.sh/cli.py before the
# fix and pass after it.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI="$HERE/../../scripts/supervisor/cli.py"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "cli.py lane-relation -- window renumber must not admit a self-review (agent-supervisor#235)"

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

# The contributor: task lane recorded as "skills:5", real tmux pane %77, from
# BEFORE the renumber.
seed_lane "skills:5" "%77" claude send-keys

# The renumber: window %77 is now index 4, not 5 -- so a candidate offered to
# dispatch.sh reads "skills:4". Nothing has re-registered THAT string yet
# (the ledger's row for "skills:4", if any, is stale or absent) -- exactly
# the gap #235 measured.
#
# Without live reconciliation, the string-shape check alone decides:
# index 4 != index 5 -> "different" -- WRONG, it is the same pane.
OUT_BROKEN=$(relate "skills:4" "skills:5")
want_contains "shape-only comparison (no live pane id) is the known-broken case: 'different'" \
  '"relation":"different"' "$OUT_BROKEN"

# With the candidate's LIVE pane id supplied (%77 -- what dispatch.sh would
# measure straight off the tmux target it already resolved), the SAME
# comparison must now resolve "same" and the guard must refuse.
OUT_FIXED=$(relate "skills:4" "skills:5" "%77")
want_contains "with the live pane id, the same physical pane resolves 'same', not 'different'" \
  '"relation":"same"' "$OUT_FIXED"

# A genuinely different pane, offered with its own live id, must still
# resolve 'different' -- the fix widens what is provably safe, it must not
# start refusing everything.
seed_lane "skills:6" "%99" claude send-keys
OUT_DIFFERENT=$(relate "skills:6" "skills:5" "%99")
want_contains "a genuinely different live pane still resolves 'different'" \
  '"relation":"different"' "$OUT_DIFFERENT"

echo
echo "cli.py lane-relation renumber: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
