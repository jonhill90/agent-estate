#!/bin/bash
# agent-estate#863: `director_reviewer_relation()` (verdict-independence.sh)
# answers `unknown` for EVERY off-pane (claude-print/pi-rpc) reviewer of a
# director-authored PR, not as an edge case -- its `LANE_ID_RE` shape check
# only recognizes a tmux-pane lane id (`<session>:<index>`), and a
# claude-print/pi-rpc reviewer lane IS its own task id, which never matches
# that shape. `independence_verdict` refuses on `unknown` exactly as hard as
# on a real self-review, so this silently refused every claude-print/pi-rpc
# review of a director-authored PR.
#
# Surfaced live by agent-tui#187: reviewer `at187-rerev187` (claude-print,
# pane_id a fresh UUID) vs `estate:1` (the Director's own window) on a
# director-authored PR. `agent-tui` is decommissioned, so this reproduces
# that exact row shape directly against `director_reviewer_relation()`
# rather than replaying it live.
#
# Fix: when the shape check answers `unknown`, resolve the reviewer's own
# row via `lane_or_task_row` (the same fallback `_lane_own_pane_id` already
# uses in this file). If a row resolves and the reviewer's own
# `_lane_identity_status` (already computed earlier in this function, for
# the `contradicted` short-circuit) is not `contradicted`, answer
# `different` -- the Director's own window structurally never has a `lanes`
# row (`register-lane-self.sh` refuses to ever register it), so ANY
# resolvable, non-contradicted row for the reviewer is proof by itself that
# the reviewer isn't the Director, regardless of transport. No row at all
# stays `unknown`, unchanged.
#
# This test drives `director_reviewer_relation()` directly (sourcing
# verdict-independence.sh the same way merge-pr.sh does), not through
# merge-pr.sh's full gate -- the defect and the fix are both entirely inside
# this one function.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "verdict-independence.sh director_reviewer_relation() (agent-estate#863)"

command -v jq >/dev/null 2>&1 || { echo "  SKIP no jq"; exit 0; }

D=$(mktemp -d)
STATE="$D/state"
mkdir -p "$STATE"
trap 'rm -rf "$D"' EXIT

# The same variables merge-pr.sh sets before sourcing this file.
HERE="$SUP"
LEDGER_PYTHON="python3"
LEDGER_CLI="$SUP/cli.py"
VERDICT_PYTHON="python3"
VERDICT_SOURCE="github"
VERDICT_BIN=""
# shellcheck source=../../scripts/supervisor/verdict-independence.sh
. "$SUP/verdict-independence.sh"

register_claude_print() {  # register_claude_print <lane> <pane-id-uuid>
  python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
Ledger(sys.argv[2]).register_lane(
    lane=sys.argv[3], pane_id=sys.argv[4], nonce="nonce-" + sys.argv[3], harness="claude",
    repo="/tmp/repo", server_id="srv", session_id="sess", command="claude -p",
    transport="claude-print",
)
' "$SUP" "$STATE" "$1" "$2"
}

# ============================================================================
# Case 1 (the fix, and agent-tui#187's exact row shape): a claude-print
# reviewer lane, registered with a live-resolvable, non-contradicted row
# (pane_id a fresh UUID, transport=claude-print, id not matching LANE_ID_RE)
# -> `different`.
# ============================================================================
register_claude_print "at187-rerev187" "3dfea7f3-0000-4000-8000-000000000001"
out=$(director_reviewer_relation "at187-rerev187")
got=$(jq -r '.overall' <<<"$out")
[ "$got" = "different" ] && ok "#863: claude-print reviewer with a resolvable, non-contradicted row is different" \
  || bad "#863: claude-print reviewer with a resolvable row is different" "got '$got' -- $out"
matched=$(jq -r '.matched_lane' <<<"$out")
[ "$matched" = "at187-rerev187" ] && ok "#863: ...and names the matched lane" \
  || bad "#863: matched_lane names the reviewer" "$out"

# ============================================================================
# Case 2: a reviewer id that resolves to NO row at all (typo, hand-typed
# Review-Lane:, never registered) -> stays `unknown`, unchanged.
# ============================================================================
out=$(director_reviewer_relation "never-registered-task-id-999")
got=$(jq -r '.overall' <<<"$out")
[ "$got" = "unknown" ] && ok "#863: an unresolvable reviewer id stays unknown" \
  || bad "#863: unresolvable reviewer id stays unknown" "got '$got' -- $out"
matched=$(jq -r '.matched_lane' <<<"$out")
[ "$matched" = "null" ] && ok "#863: ...and names no matched lane" \
  || bad "#863: unknown case names no matched lane" "$out"

# ============================================================================
# Case 3: a tmux reviewer lane whose index literally IS LANES_SUPERVISOR_WINDOW
# (genuine self-review, the shape this function exists to catch) -> stays
# `same`, completely unaffected -- the shape check answers before the new
# branch is ever reached.
# ============================================================================
out=$(LANES_SUPERVISOR_WINDOW=1 director_reviewer_relation "estate:1")
got=$(jq -r '.overall' <<<"$out")
[ "$got" = "same" ] && ok "#863: a tmux reviewer at the supervisor's own window index is still same" \
  || bad "#863: supervisor-window reviewer still same" "got '$got' -- $out"

# ============================================================================
# Case 4: a tmux reviewer lane at a genuinely different index -> stays
# `different`, via the pre-existing shape check, unaffected by this change.
# ============================================================================
out=$(LANES_SUPERVISOR_WINDOW=1 director_reviewer_relation "estate:2")
got=$(jq -r '.overall' <<<"$out")
[ "$got" = "different" ] && ok "#863: a tmux reviewer at a different window index is still different" \
  || bad "#863: different-window tmux reviewer still different" "got '$got' -- $out"

# ============================================================================
# Case 5: registration CONTRADICTED by the live tmux server it names -> stays
# refused (`contradicted`), confirming the new branch never runs (the
# short-circuit at the top of the function returns before `overall` is even
# computed). `lane_identity.py` only ever answers `contradicted` for a
# `send-keys` (tmux-pane) transport whose recorded pane is genuinely absent
# from a REAL, reachable socket's `list-panes` listing (`PANE_TRANSPORTS` in
# lane_identity.py -- an off-pane claude-print/pi-rpc row can only ever be
# `unverifiable`, never `contradicted`, by construction). Reproduced with a
# real socket FILE (so lane_identity.py's own `Path(socket_path).exists()`
# check passes) and a stub `VERDICT_TMUX_BIN` standing in for `tmux -S
# <socket> list-panes -a -F ...`, returning a listing that does not include
# the registered pane -- the exact "row names a pane the live server does
# not have" shape agent-supervisor#520 guards.
# ============================================================================
FAKE_SOCKET="$D/fake-tmux-socket"
: > "$FAKE_SOCKET"
FAKE_TMUX_BIN="$D/fake-tmux"
cat > "$FAKE_TMUX_BIN" <<'FAKE'
#!/bin/bash
# lane_identity.py invokes: tmux -S <socket> list-panes -a -F <fmt>
# Reports a live server whose only pane belongs to a DIFFERENT lane --
# the registered pane_id never appears, so the row's own pane reads as gone.
echo -e "%other\tsomeone-else:9\tfake-server-incarnation"
FAKE
chmod +x "$FAKE_TMUX_BIN"
python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
Ledger(sys.argv[2]).register_lane(
    lane=sys.argv[3], pane_id="%contradicted", nonce="nonce-contradicted", harness="claude",
    repo="/tmp/repo", server_id=sys.argv[4] + ":1000000000", session_id="sess", command="claude",
    transport="send-keys",
)
' "$SUP" "$STATE" "t:5" "$FAKE_SOCKET"
out=$(VERDICT_TMUX_BIN="$FAKE_TMUX_BIN" director_reviewer_relation "t:5")
got=$(jq -r '.overall' <<<"$out")
if [ "$got" = "contradicted" ]; then
  ok "#863: a contradicted reviewer registration stays refused, new branch never runs"
elif [ "$got" = "unverifiable" ]; then
  bad "#863: contradicted reviewer registration -- test setup produced unverifiable, not contradicted (fake tmux stub did not force disagreement)" "$out"
else
  bad "#863: contradicted reviewer registration stays refused" "got '$got' -- $out"
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
