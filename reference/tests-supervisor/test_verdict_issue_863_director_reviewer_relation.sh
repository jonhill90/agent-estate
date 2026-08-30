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
#
# agent-estate#866 (review of #863, PR #866): #863's fix above justified
# itself with "the Director's own window structurally never has a `lanes`
# row" -- that is a CALL-SITE convention (`register-lane-self.sh`'s own
# refusal), not something `Ledger.register_lane` itself enforces. Case 6
# below reproduces the exact row the review named: a single `register_lane`
# call (the same primitive a hand-typed `cli.py register` or a future
# repair/backfill script would hit) inserting a row for the Director's own
# LIVE pane under an off-pane-shaped lane id (`rogue-reviewer`). Before the
# fix, "a row resolved" alone was treated as proof the reviewer wasn't the
# Director; the fix requires the resolved row's own pane_id to also NOT be
# the pane a live tmux query finds at `LANES_SUPERVISOR_WINDOW`.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "verdict-independence.sh director_reviewer_relation() (agent-estate#863, agent-estate#866)"

command -v jq >/dev/null 2>&1 || { echo "  SKIP no jq"; exit 0; }

D=$(mktemp -d)
STATE="$D/state"
mkdir -p "$STATE"
trap 'rm -rf "$D"' EXIT

# agent-estate#866: `_director_live_pane_id` (verdict-independence.sh) calls
# `${VERDICT_TMUX_BIN:-tmux}` with no explicit binary set for cases below
# that never touch it -- point the default at an isolated, empty
# TMUX_TMPDIR so a bare `tmux` call on a host that happens to be running a
# real session cannot reach it and produce a non-deterministic result. The
# directory itself must EXIST (empty, no `tmux-<uid>` socket dir inside it):
# tmux 3.5, given a TMUX_TMPDIR whose parent directory does not exist at
# all, was measured falling back to the real default socket (`/tmp/tmux-
# <uid>/default`) instead of failing -- silently defeating this isolation
# and reconnecting to whatever real session the host happens to be running.
# Never destructive (`list-panes` only), so this is belt-and-braces rather
# than an invariant-4 requirement.
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$D/isolated-empty-tmux-tmpdir"
mkdir -p "$TMUX_TMPDIR"

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

# agent-estate#866 (re-review, second pass): the Director's own live pane
# must be genuinely RESOLVABLE (not just "tmux reachable") for a row to
# legitimately answer `different` -- see Case 1b below for the
# tmux-unreachable case, which now answers `unknown` instead. Defined here,
# ahead of Case 1, because Case 1 itself now needs a reachable stub to test
# what it always meant to test ("a resolvable row that is NOT the Director's
# pane reads different"), rather than incidentally relying on tmux being
# unreachable in the test sandbox -- that reliance was exercising the exact
# gap #866's second review found, not the fix.
FAKE_WINDOW_TMUX_BIN="$D/fake-tmux-window-query"
cat > "$FAKE_WINDOW_TMUX_BIN" <<'FAKE'
#!/bin/bash
# _director_live_pane_id invokes: tmux list-panes -a -F "#{window_index}\t#{pane_id}"
# Reports window 1 (the default LANES_SUPERVISOR_WINDOW) as pane
# %director-real -- the Director's own genuinely live pane.
printf '1\t%%director-real\n'
printf '2\t%%worker-real\n'
FAKE
chmod +x "$FAKE_WINDOW_TMUX_BIN"

# ============================================================================
# Case 1 (the fix, and agent-tui#187's exact row shape): a claude-print
# reviewer lane, registered with a live-resolvable, non-contradicted row
# (pane_id a fresh UUID, transport=claude-print, id not matching LANE_ID_RE),
# with the Director's live pane genuinely resolvable and NOT this reviewer's
# pane -> `different`.
# ============================================================================
register_claude_print "at187-rerev187" "3dfea7f3-0000-4000-8000-000000000001"
out=$(VERDICT_TMUX_BIN="$FAKE_WINDOW_TMUX_BIN" LANES_SUPERVISOR_WINDOW=1 director_reviewer_relation "at187-rerev187")
got=$(jq -r '.overall' <<<"$out")
[ "$got" = "different" ] && ok "#863: claude-print reviewer with a resolvable, non-contradicted row is different" \
  || bad "#863: claude-print reviewer with a resolvable row is different" "got '$got' -- $out"
matched=$(jq -r '.matched_lane' <<<"$out")
[ "$matched" = "at187-rerev187" ] && ok "#863: ...and names the matched lane" \
  || bad "#863: matched_lane names the reviewer" "$out"

# ============================================================================
# Case 1b (agent-estate#866, second review -- the fix this brief adds): the
# SAME resolvable, non-contradicted reviewer row as Case 1, but now the
# Director's live pane cannot be determined AT ALL (no tmux server reachable
# -- the default `VERDICT_TMUX_BIN=tmux` against this test's isolated,
# empty `TMUX_TMPDIR`, set above). Before this fix: falls through to
# `different` (a false ALLOW -- a pre-planted or hand-forged row carrying
# the Director's own pane_id would launder straight through during exactly
# this kind of tmux-outage window). After this fix: `unknown`, refused the
# same way `_lane_identity_status`'s own unverifiable case already is.
# ============================================================================
register_claude_print "tmux-down-reviewer" "3dfea7f3-0000-4000-8000-000000000002"
out=$(director_reviewer_relation "tmux-down-reviewer")
got=$(jq -r '.overall' <<<"$out")
[ "$got" = "unknown" ] && ok "#866: a resolvable reviewer row with the Director's live pane unresolvable is unknown, not different" \
  || bad "#866: tmux-unreachable reviewer row must be unknown, never different" "got '$got' -- $out"
matched=$(jq -r '.matched_lane' <<<"$out")
[ "$matched" = "null" ] && ok "#866: ...and names no matched lane, matching every other unknown case" \
  || bad "#866: unknown case (tmux unreachable) must name no matched lane" "$out"

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

# ============================================================================
# Case 6 (agent-estate#866 -- the live laundering path the review named): a
# row registered for the DIRECTOR'S OWN LIVE PANE, under an off-pane-shaped
# lane id (`rogue-reviewer`, which -- like `at187-rerev187` in Case 1 --
# does not match `LANE_ID_RE`, so the shape check answers `unknown` exactly
# the same way). Built via the same primitive `register_claude_print`
# (Case 1) already uses -- a bare `Ledger.register_lane` call, standing in
# for the hand-typed `cli.py register` the review named as the actual
# attack -- with `pane_id` set to whatever a live tmux query would report
# for `LANES_SUPERVISOR_WINDOW`. `transport=claude-print` keeps
# `_lane_identity_status` at `unverifiable` (never `contradicted`): an
# off-pane transport's pane_id is never checked against a live server by
# `lane_identity.py` (`PANE_TRANSPORTS` there is `send-keys`-only), which is
# exactly how a fabricated pane_id survives that check unnoticed --
# confirming the pre-existing `contradicted` short-circuit does NOT catch
# this shape, so this case is actually exercising the new `_director_live_
# pane_id` comparison and not the old guard.
#
# Before the fix: "a row resolved" alone -> `different` (LAUNDERED).
# After the fix: the row's own pane_id IS the Director's live pane -> `same`
# (REFUSED), which `independence_verdict` treats as "NOT independent -- the
# Director's own window."
#
# Reuses `FAKE_WINDOW_TMUX_BIN`, defined once above ahead of Case 1.
# ============================================================================
register_claude_print "rogue-reviewer" "%director-real"
out=$(VERDICT_TMUX_BIN="$FAKE_WINDOW_TMUX_BIN" LANES_SUPERVISOR_WINDOW=1 director_reviewer_relation "rogue-reviewer")
got=$(jq -r '.overall' <<<"$out")
[ "$got" = "same" ] && ok "#866: a row registered for the Director's own live pane refuses (same), not different" \
  || bad "#866: rogue row naming the Director's live pane must refuse" "got '$got' -- $out"
matched=$(jq -r '.matched_lane' <<<"$out")
[ "$matched" = "rogue-reviewer" ] && ok "#866: ...and names the offending lane" \
  || bad "#866: refusal names the matched lane" "$out"
detail=$(jq -r '.detail' <<<"$out")
[[ "$detail" == *"Director"* ]] && ok "#866: ...with a detail naming the Director's window" \
  || bad "#866: refusal detail names the Director's window" "$out"

# ============================================================================
# Case 7 (the other mutation direction, same fixture): a genuine claude-print
# reviewer whose pane_id is NOT the Director's live pane must still resolve
# `different` -- proving the new comparison is exact (pane_id equality), not
# a blanket refusal of every off-pane row once a live tmux query succeeds. If
# this ever answers anything but `different`, the fix has widened refusal
# past the one row #866 named and re-broken #863's own off-pane reviewers.
# ============================================================================
register_claude_print "genuine-claude-print-reviewer" "%worker-real"
out=$(VERDICT_TMUX_BIN="$FAKE_WINDOW_TMUX_BIN" LANES_SUPERVISOR_WINDOW=1 director_reviewer_relation "genuine-claude-print-reviewer")
got=$(jq -r '.overall' <<<"$out")
[ "$got" = "different" ] && ok "#866: a genuine off-pane reviewer whose pane is NOT the Director's still resolves different" \
  || bad "#866: genuine off-pane reviewer must still resolve different" "got '$got' -- $out"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
