#!/bin/bash
# agent-supervisor#605: daemon-authored PRs cannot pass the independence
# gate, because `daemon`/`d-<task>` (supervisord's own author-lane shapes)
# and `session:window` (a tmux lane) cannot be compared by
# `resolve_lane_relation` -- neither has a `pane_id` the other's does, so
# every daemon-vs-tmux pairing answered `unknown`, and `independence_verdict`
# refuses `unknown` exactly as hard as a real self-review (`verdict-
# independence.sh:737`).
#
# The director's decision (issue #605, second comment): teach the gate that
# these two namespaces are DISJOINT BY CONSTRUCTION -- a tmux lane resolves
# to a registered pane, `daemon` never has one -- so a cross-namespace
# author/reviewer pair is independent, narrowly, without loosening anything
# else. Three hard constraints from that decision, each pinned by its own
# case below:
#   1. no new self-assertion path -- the daemon-shape check must confirm the
#      value came from supervisord's own ledger write (`server_id =
#      'supervisord'`, the literal value only `EnsureLane` in
#      daemon/internal/ledger/ledger.go ever writes), never pattern-match
#      the string "daemon" wherever it appears in a trailer.
#   2. same-namespace stays as it is -- `daemon` vs `daemon`, `d-X` vs `d-X`
#      must not read as independent.
#   3. fail-closed default preserved -- a daemon-shaped id that does not
#      verify against a real ledger row still resolves `unknown`.
#
# This drives `contributor_lane_relation()` directly (the function
# `independence_verdict` reads `$rel.overall` from at :737) against a real
# scratch ledger -- no `gh` stub, no merge-pr.sh -- because the defect and
# the fix are entirely inside that one comparison; a full merge-pr.sh
# end-to-end fixture would only be re-testing #332/#292's own plumbing.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
LEDGER_CLI="$SUP/cli.py"
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 -- $2"; fail=$((fail+1)); }

command -v jq >/dev/null 2>&1 || { echo "verdict-independence.sh (#605 daemon namespace)"; echo "  SKIP no jq"; exit 0; }

echo "verdict-independence.sh (#605 daemon namespace)"

D=$(mktemp -d)
STATE="$D/state"
mkdir -p "$STATE"
PYTHON_BIN="python3"

# Callers must set these before sourcing verdict-independence.sh (its own
# header comment).
HERE_FOR_SOURCE="$SUP"
LEDGER_PYTHON="$PYTHON_BIN"
VERDICT_PYTHON="$PYTHON_BIN"
VERDICT_SOURCE="github"
HERE="$SUP"
# shellcheck source=../../scripts/supervisor/verdict-independence.sh
source "$SUP/verdict-independence.sh"
HERE="$HERE_FOR_SOURCE"

# Registers a genuine tmux lane -- the shape `register_lane` accepts, with a
# real non-empty `pane_id`, exactly what a live pane produces.
register_tmux_lane() {  # register_tmux_lane <lane> <pane-id>
  "$PYTHON_BIN" -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
Ledger(sys.argv[2]).register_lane(
    lane=sys.argv[3], pane_id=sys.argv[4], nonce="nonce-" + sys.argv[3], harness="claude",
    repo="/tmp/repo", server_id="srv", session_id="sess", command="claude", transport="send-keys",
)
' "$SUP" "$STATE" "$1" "$2"
}

# Registers a lane the way `daemon/internal/ledger/ledger.go`'s `EnsureLane`
# actually does -- NOT through `register_lane`/`_register_lane_tx`, which
# requires every field (including `pane_id`) non-empty and would refuse this
# shape outright. `EnsureLane`'s own INSERT is `pane_id=''`,
# `server_id='supervisord'`, `transport='claude-print'` -- reproduced
# byte-for-byte (comment quotes the exact statement) so the fixture is the
# real shape, not an invented approximation of it.
register_daemon_lane() {  # register_daemon_lane <lane>
  "$PYTHON_BIN" -c '
import sys, time
sys.path.insert(0, sys.argv[1])
from core import Ledger
ledger = Ledger(sys.argv[2])
with ledger._connect() as conn:
    conn.execute(
        "INSERT INTO lanes (lane, pane_id, nonce, harness, repo, server_id, session_id, command, "
        "harness_session_id, harness_project_dir, transport, updated_at) "
        "VALUES (?, \"\", \"\", ?, ?, \"supervisord\", \"\", ?, \"\", \"\", \"claude-print\", ?)",
        (sys.argv[3], "claude", "acme/repo", "claude -p", int(time.time())),
    )
' "$SUP" "$STATE" "$1"
}

author_json_for() {  # author_json_for <lane> <task>
  jq -nc --arg lane "$1" --arg task "$2" \
    '{known:true, external:false, contributors:[{lane:$lane, task:$task}], claimed_lane:null, detail:""}'
}

# ============================================================================
# The real #605 shape: a daemon-authored PR, reviewed by a genuine tmux lane.
# Before the fix this reads `unknown` (refuse); after, `different` (permit).
# ============================================================================
register_daemon_lane "daemon"
register_tmux_lane "estate:5" "%51"

rel=$(contributor_lane_relation "$(author_json_for daemon d-604)" "estate:5" | jq -r '.overall')
if [ "$rel" = "different" ]; then
  ok "daemon author, real tmux reviewer -> different (independent)"
else
  bad "daemon author, real tmux reviewer -> different" "got overall=$rel"
fi

# batch mode's `d-<task>` shape, not just the bare default -- both are
# supervisord's own, per batch.go:103-105.
register_daemon_lane "d-605"
rel=$(contributor_lane_relation "$(author_json_for d-605 d-605)" "estate:5" | jq -r '.overall')
if [ "$rel" = "different" ]; then
  ok "d-<task> author, real tmux reviewer -> different (independent)"
else
  bad "d-<task> author, real tmux reviewer -> different" "got overall=$rel"
fi

# ============================================================================
# Constraint 2: same-namespace stays as it is.
# ============================================================================
rel=$(contributor_lane_relation "$(author_json_for daemon d-604)" "daemon" | jq -r '.overall')
if [ "$rel" != "different" ]; then
  ok "constraint 2: daemon vs daemon does not read as independent (overall=$rel)"
else
  bad "constraint 2: daemon vs daemon does not read as independent" "got overall=different"
fi

rel=$(contributor_lane_relation "$(author_json_for d-605 d-605)" "d-605" | jq -r '.overall')
if [ "$rel" != "different" ]; then
  ok "constraint 2: d-X vs d-X does not read as independent (overall=$rel)"
else
  bad "constraint 2: d-X vs d-X does not read as independent" "got overall=different"
fi

# ============================================================================
# Constraint 1: no new self-assertion path. A REAL tmux self-review, where
# the reviewer hand-types "Review-Lane: daemon" hoping the cross-namespace
# rule launders it as independent, must NOT get "different" -- "daemon" the
# STRING really is a verified supervisord lane (registered above), but the
# CONTRIBUTOR here is a genuine tmux lane, not daemon-shaped, and the real
# self-review must still be caught.
# ============================================================================
register_tmux_lane "estate:9" "%59"
rel=$(contributor_lane_relation "$(author_json_for estate:9 as605-selfreview)" "daemon" | jq -r '.overall')
if [ "$rel" != "different" ]; then
  ok "constraint 1: a tmux self-review claiming Review-Lane: daemon is not laundered (overall=$rel)"
else
  bad "constraint 1: tmux self-review claiming Review-Lane: daemon" "got overall=different -- SELF-REVIEW LAUNDERED"
fi

# ============================================================================
# Constraint 3: fail-closed default preserved. A daemon-SHAPED contributor
# lane that was never actually registered by supervisord (no ledger row at
# all) must still resolve unknown, not "different" by shape alone.
# ============================================================================
rel=$(contributor_lane_relation "$(author_json_for d-999-never-registered d-999)" "estate:5" | jq -r '.overall')
if [ "$rel" = "unknown" ]; then
  ok "constraint 3: unregistered daemon-shaped lane still fails closed to unknown"
else
  bad "constraint 3: unregistered daemon-shaped lane still fails closed to unknown" "got overall=$rel"
fi

# The real (sourced) definition, saved so each mutation below can be
# reverted exactly -- `unset -f` after overriding a sourced function leaves
# it UNDEFINED, not restored, which would make every case after the first
# mutation block a false "still refuses" (the function simply not found,
# `contributor_lane_relation`'s subshell dies, `jq -r` reads empty as
# "unknown" -- looking identical to a correct refusal for the wrong reason).
ORIG_DAEMON_CROSS_NAMESPACE_RELATION="$(declare -f _daemon_cross_namespace_relation)"
restore_daemon_cross_namespace_relation() { eval "$ORIG_DAEMON_CROSS_NAMESPACE_RELATION"; }

# ============================================================================
# MUTATION CHECK -- permissive direction: make the new comparison always say
# "different" (delete both guards) -- every case above that must refuse
# should now wrongly read independent. Assert the mutation actually applied
# (a smoke case) before trusting the rest, and report how many refusing
# cases flip.
# ============================================================================
_daemon_cross_namespace_relation() { printf 'different\n'; }

smoke=$(contributor_lane_relation "$(author_json_for daemon d-604)" "daemon" | jq -r '.overall')
if [ "$smoke" = "different" ]; then
  ok "permissive mutation applied (smoke: daemon vs daemon now reads different)"
else
  bad "permissive mutation applied" "smoke case did not flip -- mutation did not reach the real call site"
fi

permissive_breaks=0
[ "$(contributor_lane_relation "$(author_json_for daemon d-604)" "daemon" | jq -r '.overall')" = "different" ] && permissive_breaks=$((permissive_breaks + 1))
[ "$(contributor_lane_relation "$(author_json_for d-605 d-605)" "d-605" | jq -r '.overall')" = "different" ] && permissive_breaks=$((permissive_breaks + 1))
[ "$(contributor_lane_relation "$(author_json_for estate:9 as605-selfreview)" "daemon" | jq -r '.overall')" = "different" ] && permissive_breaks=$((permissive_breaks + 1))
[ "$(contributor_lane_relation "$(author_json_for d-999-never-registered d-999)" "estate:5" | jq -r '.overall')" = "different" ] && permissive_breaks=$((permissive_breaks + 1))
echo "  permissive mutation: $permissive_breaks/4 refusing assertions now wrongly read independent"
if [ "$permissive_breaks" -eq 4 ]; then
  ok "permissive mutation breaks all 4 refusing assertions (test suite is real evidence)"
else
  bad "permissive mutation breaks all 4 refusing assertions" "only $permissive_breaks/4 broke -- some refusing case is not actually pinned"
fi
restore_daemon_cross_namespace_relation

# ============================================================================
# MUTATION CHECK -- paranoid direction: make the new comparison never fire
# (always empty, i.e. fall through unconditionally) -- the one case that
# MUST become independent (daemon author, real tmux reviewer) should now
# wrongly refuse.
# ============================================================================
_daemon_cross_namespace_relation() { printf '\n'; }

paranoid_breaks=0
[ "$(contributor_lane_relation "$(author_json_for daemon d-604)" "estate:5" | jq -r '.overall')" != "different" ] && paranoid_breaks=$((paranoid_breaks + 1))
[ "$(contributor_lane_relation "$(author_json_for d-605 d-605)" "estate:5" | jq -r '.overall')" != "different" ] && paranoid_breaks=$((paranoid_breaks + 1))
echo "  paranoid mutation: $paranoid_breaks/2 independent assertions now wrongly refuse"
if [ "$paranoid_breaks" -eq 2 ]; then
  ok "paranoid mutation breaks both independence assertions (test suite is real evidence)"
else
  bad "paranoid mutation breaks both independence assertions" "only $paranoid_breaks/2 broke"
fi
restore_daemon_cross_namespace_relation

# Re-run the primary case unmutated, to prove the mutation guards above were
# not left in a state that accidentally stayed applied (the unset above is
# the real assertion; this is belt-and-suspenders against a shell quirk).
rel=$(contributor_lane_relation "$(author_json_for daemon d-604)" "estate:5" | jq -r '.overall')
if [ "$rel" = "different" ]; then
  ok "unmutated behaviour restored after both mutation checks"
else
  bad "unmutated behaviour restored after both mutation checks" "got overall=$rel"
fi

rm -rf "$D"
echo "verdict-independence.sh (#605 daemon namespace): $pass ok, $fail failed"
[ "$fail" -eq 0 ]
