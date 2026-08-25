#!/bin/bash
# agent-supervisor#631. This is a DIFFERENT case from #235's renumber fix
# (see tests/supervisor/test_lane_relation_renumber.sh) even though both are
# triggered by `renumber-windows on` closing a window. #235's test shows a
# lane STRING that changed underneath a still-live pane (index shifted, same
# window) -- fixed by reconciling the CANDIDATE side through the ledger's
# live `pane_id` registry (`--lane-pane-id`).
#
# This test is about the OTHER side of the same comparison: the CONTRIBUTOR
# side, which is not measured live off tmux -- it is a lane STRING recorded
# on a `tasks` row, sometimes days earlier. `core.py`'s `_register_lane_tx`
# upserts the `lanes` table keyed on that same STRING (`ON CONFLICT(lane) DO
# UPDATE`), so when a window closes and the freed index is later handed by
# `lanes.sh` to a totally different lane, the NEXT dispatch's
# `register_lane("agent-supervisor:4", pane_id="%BBB", ...)` silently
# overwrites the `lanes` row for that string in place. Any TASK recorded
# earlier as `lane="agent-supervisor:4"` -- e.g. a contributor row the merge-
# independence gate reads -- now resolves, via `Ledger.get_lane
# ("agent-supervisor:4")`, to pane "%BBB", the pane of the NEW, unrelated
# occupant, not "%AAA", the pane that actually did the historical work.
# `#235`'s own fix does not touch this: it only ever reconciles the
# CANDIDATE side (a live tmux measurement), never the CONTRIBUTOR side (a
# string re-resolved through a table that can have been overwritten).
#
# The fix (agent-supervisor#631): `tasks.pane_id`, an IMMUTABLE snapshot of
# `lanes.pane_id` taken once at `_assign_tx` time, plus `cli.py lane-relation
# --other-pane-id` to let a caller supply that frozen value directly instead
# of falling back to a live `Ledger.get_lane(<stale string>)` lookup.
#
# This test must FAIL against the pre-fix `cli.py`/`core.py` (no
# `--other-pane-id`, no `tasks.pane_id` column) and PASS after it.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
CLI="$SUP/cli.py"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "cli.py lane-relation -- a reused lane STRING must not corrupt a contributor's frozen identity (agent-supervisor#631)"

D=$(mktemp -d)
STATE="$D/state"

# 1. Seed the historical author's lane and dispatch a real task under it --
# `record_dispatch` does register_lane + reconstruct_task + assign in one
# transaction, exactly what dispatch.sh does live. This is the task whose
# `pane_id` column must freeze "%AAA" at assignment time.
DISPATCH_OUT=$(python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
ledger = Ledger(sys.argv[2])
ledger.record_dispatch(
    lane="agent-supervisor:4", pane_id="%AAA", nonce="nonce-aaa", harness="claude",
    repo="/tmp/repo", server_id="srv", session_id="sess", command="claude",
    task_id="as619-impl619", source_kind="issue",
    source_url="https://github.com/acme/repo/issues/619",
    source_ref="619", summary="issue #619", source_state="OPEN",
    evidence=["claimed by dispatch.sh for lane agent-supervisor:4", "issues: 619"],
)
task = ledger.get_task("as619-impl619")
print(task["pane_id"])
' "$SUP" "$STATE")
want_contains "task as619-impl619's own row froze pane_id %AAA at assignment time" "%AAA" "$DISPATCH_OUT"

# 2. The window closes; renumbering hands "agent-supervisor:4" to a fresh
# dispatch on a completely different pane -- ordinary, correct
# register_lane behaviour, not itself a bug.
python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
ledger = Ledger(sys.argv[2])
ledger.register_lane(
    lane="agent-supervisor:4", pane_id="%BBB", nonce="nonce-bbb",
    harness="claude", repo="/tmp/repo2", server_id="srv", session_id="sess",
    command="claude", transport="send-keys",
)
' "$SUP" "$STATE"

GET_LANE_OUT=$(python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
row = Ledger(sys.argv[2]).get_lane("agent-supervisor:4")
print(row["pane_id"])
' "$SUP" "$STATE")
want_contains "register_lane correctly overwrote lanes['"'"'agent-supervisor:4'"'"'] to %BBB -- this is expected, not the bug" \
  "%BBB" "$GET_LANE_OUT"

# 3. A THIRD, genuinely different candidate -- the TRUE original author,
# still alive, now presenting live pane "%AAA" under whatever string it
# currently has -- shows up as a review candidate for the same PR.
CANDIDATE="agent-supervisor:9"

# OLD path: bare --lane-pane-id, no --other-pane-id. Compares the
# candidate's live pane against the CURRENT (overwritten) lanes row for
# "agent-supervisor:4", which is now %BBB, not %AAA -- so this WRONGLY
# resolves "different", admitting a self-review.
OUT_OLD=$(python3 "$CLI" --state-dir "$STATE" lane-relation \
  --lane "$CANDIDATE" --other "agent-supervisor:4" --lane-pane-id "%AAA")
want_contains "OLD path (no --other-pane-id): wrongly resolves 'different' -- this is the bug" \
  '"relation":"different"' "$OUT_OLD"

# NEW path: pass the task's own frozen pane_id ("%AAA", read back from
# task-lane) as --other-pane-id. This must correctly resolve "same".
TASK_PANE_ID=$(python3 "$CLI" --state-dir "$STATE" task-lane --task "as619-impl619" \
  | sed -n 's/.*"pane_id":"\([^"]*\)".*/\1/p')
want_contains "task-lane exposes the frozen pane_id for as619-impl619" "%AAA" "$TASK_PANE_ID"

OUT_NEW=$(python3 "$CLI" --state-dir "$STATE" lane-relation \
  --lane "$CANDIDATE" --other "agent-supervisor:4" --lane-pane-id "%AAA" \
  --other-pane-id "$TASK_PANE_ID")
want_contains "NEW path (--other-pane-id from the task's frozen snapshot): correctly resolves 'same' -- self-review refused" \
  '"relation":"same"' "$OUT_NEW"

# 4. The plain/no-pane-id branch also honours --other-pane-id (main() must
# wire it in BOTH branches, per the brief). "claude-print:xyz" has a
# non-numeric index half, so the string-shape check (`core.lane_relation`)
# itself answers "unknown" and this call falls through to the ledger-row
# widening WITHOUT ever supplying --lane-pane-id -- the code path #292 built
# for a claude-print/pi-rpc lane. Registered here with pane_id "%AAA", same
# pane as the frozen task snapshot, to show --other-pane-id is genuinely
# consulted in this branch too, not just accepted and ignored.
python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
ledger = Ledger(sys.argv[2])
ledger.register_lane(
    lane="claude-print:xyz", pane_id="%AAA", nonce="nonce-cp",
    harness="claude", repo="/tmp/repo3", server_id="srv", session_id="sess",
    command="claude", transport="claude-print",
)
' "$SUP" "$STATE"
OUT_PLAIN=$(python3 "$CLI" --state-dir "$STATE" lane-relation \
  --lane "claude-print:xyz" --other "agent-supervisor:4" \
  --other-pane-id "$TASK_PANE_ID")
want_contains "plain branch (no --lane-pane-id): --other-pane-id (frozen %AAA) matches claude-print:xyz's own registered %AAA -- 'same'" \
  '"relation":"same"' "$OUT_PLAIN"

echo
echo "cli.py lane-relation reused-index: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
