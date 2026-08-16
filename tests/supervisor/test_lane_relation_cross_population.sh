#!/bin/bash
# agent-supervisor#292: author exclusion could not distinguish a tmux lane
# from a claude-print lane, because `core.lane_relation`'s ONLY identity
# check is the lane id's string shape (`<session>:<index>`), which a
# claude-print (or pi-rpc) lane never has -- its id IS its task id, since
# there is no window to index. Measured 2026-08-16: three fresh tmux review
# candidates for PR #288 (authored by claude-print lane
# `as284-finish-migration-b`) were ALL refused, not because any of them was
# provably the author, but because none could be proven NOT to be.
#
# This exercises `cli.py lane-relation` directly against a real ledger, both
# directions #292 itself requires: a tmux lane against a claude-print author,
# and a claude-print lane against a tmux author. Both must resolve
# `different` when the ledger's own `pane_id` registry proves they are, and
# `unknown` when it has nothing to say (still fail-closed -- the widening
# does not loosen what counts as established).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI="$HERE/../../scripts/supervisor/cli.py"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "cli.py lane-relation -- cross-population identity (agent-supervisor#292)"

D=$(mktemp -d)
STATE="$D/state"

# Seeds a `lanes` row directly through `Ledger.register_lane` -- the same
# write both `dispatch.sh`'s tmux flow (via `record-dispatch`) and
# `dispatch-claude-print.sh` (via `cli.py register`) ultimately produce, just
# without spawning a real tmux pane or a real `claude -p` process for a test
# that is only about the identity comparison downstream of that write.
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

relate() {  # relate <lane> <other> -> the raw JSON
  python3 "$CLI" --state-dir "$STATE" lane-relation --lane "$1" --other "$2"
}

# --- direction 1: a tmux candidate against a claude-print author (the PR
#     #288 shape itself) ----------------------------------------------------
seed_lane "t:3" "%12" claude send-keys
seed_lane "as284-finish-migration-b" "claude-print:as284-finish-migration-b" claude claude-print

OUT1=$(relate "t:3" "as284-finish-migration-b")
want_contains "a tmux candidate against a claude-print author resolves different" '"relation":"different"' "$OUT1"

# --- direction 2: a claude-print candidate against a tmux author ----------
seed_lane "as9-other-lane" "claude-print:as9-other-lane" claude claude-print
seed_lane "t:5" "%40" claude send-keys

OUT2=$(relate "as9-other-lane" "t:5")
want_contains "a claude-print candidate against a tmux author resolves different" '"relation":"different"' "$OUT2"

# --- both must be REFUSED (same/unknown) when the ledger cannot prove they
#     differ: a claude-print lane compared against itself ------------------
OUT3=$(relate "as284-finish-migration-b" "as284-finish-migration-b")
want_contains "a claude-print lane against itself is same, not different" '"relation":"same"' "$OUT3"

# --- and unknown stays unknown when the ledger has never heard of either
#     side, rather than defaulting to admit -------------------------------
OUT4=$(relate "as284-finish-migration-b" "never-registered-lane")
want_contains "an unregistered lane on either side still refuses (unknown)" '"relation":"unknown"' "$OUT4"
want_contains "and still names the side it COULD identify" '"lane_population":"claude-print"' "$OUT4"

echo
echo "cli.py lane-relation cross-population: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
