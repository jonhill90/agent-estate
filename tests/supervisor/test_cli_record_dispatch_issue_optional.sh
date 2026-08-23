#!/bin/bash
# agent-supervisor#538. `cli.py record-dispatch --issue` used to be required
# unconditionally, even when `--pr` was given -- so nothing could record a
# retroactive, PR-scoped dispatch for work that was never claimed against a
# GitHub issue at all (the estate loop's brief-file dispatches). This is the
# narrow, no-tmux-needed test for the argument-matrix change itself; the live
# end-to-end path (a real lane registering itself, then the merge gate
# resolving it) is test_register_pr_dispatch_self.sh and
# test_merge_pr_estate_lane_identity.sh.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI="$HERE/../../scripts/supervisor/cli.py"
REPO="acme/repo"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "cli.py record-dispatch -- --issue optional only when --pr is given (#538)"

command -v jq >/dev/null 2>&1 || { echo "  SKIP no jq"; exit 0; }

D=$(mktemp -d); STATE="$D/state"; WORK="$D/work"; mkdir -p "$STATE" "$WORK"

record() {  # record <lane> <task> [extra record-dispatch args...]
  local lane="$1" task="$2"; shift 2
  python3 "$CLI" --state-dir "$STATE" record-dispatch \
    --lane "$lane" --task "$task" --summary seed --pane-id "%1" --pane-path "$WORK" \
    --command node --server-id srv --session-id sess --github "$REPO" --harness codex \
    "$@" 2>&1
}

# --- refuses with neither --issue nor --pr ----------------------------------
OUT=$(record estate:1 t1); RC=$?
[ "$RC" -ne 0 ] && ok "refuses with neither --issue nor --pr" \
  || bad "refuses with neither --issue nor --pr" "rc=$RC: $OUT"
grep -qi "requires --issue, --pr, or both" <<<"$OUT" \
  && ok "...and names what is missing" || bad "names what is missing" "$OUT"

# --- still works with only --issue (dispatch.sh's own, unaffected shape) ---
OUT=$(record estate:2 t2 --issue 100); RC=$?
[ "$RC" -eq 0 ] && ok "--issue alone (no --pr) still succeeds -- dispatch.sh's own shape is unaffected" \
  || bad "--issue alone still succeeds" "rc=$RC: $OUT"
KNOWN=$(python3 "$CLI" --state-dir "$STATE" contributor-issue-lanes --issue 100 --repo "$REPO" 2>&1 | jq -r '.known')
[ "$KNOWN" = "true" ] && ok "...and resolves through contributor-issue-lanes, same as before" \
  || bad "resolves through contributor-issue-lanes" "$KNOWN"

# --- both given: dispatch.sh --pr's existing shape, also unaffected --------
OUT=$(record estate:3 t3 --issue 101 --pr 900); RC=$?
[ "$RC" -eq 0 ] && ok "--issue and --pr together still succeeds (dispatch.sh --pr's shape)" \
  || bad "--issue and --pr together succeeds" "rc=$RC: $OUT"
KNOWN=$(python3 "$CLI" --state-dir "$STATE" contributor-pr-lanes --pr 900 --repo "$REPO" 2>&1 | jq -r '.known')
[ "$KNOWN" = "true" ] && ok "...and resolves through contributor-pr-lanes, keyed by the PR" \
  || bad "resolves through contributor-pr-lanes" "$KNOWN"

# --- #538: --pr alone, NO --issue at all -> the new, estate-loop shape -----
OUT=$(record estate:4 t4 --pr 901); RC=$?
[ "$RC" -eq 0 ] && ok "#538: --pr alone with no --issue now succeeds" \
  || bad "#538: --pr alone succeeds" "rc=$RC: $OUT"
RESULT=$(python3 "$CLI" --state-dir "$STATE" contributor-pr-lanes --pr 901 --repo "$REPO" 2>&1)
KNOWN=$(jq -r '.known' <<<"$RESULT")
LANE=$(jq -r '.contributors[0].lane // ""' <<<"$RESULT")
[ "$KNOWN" = "true" ] && [ "$LANE" = "estate:4" ] \
  && ok "#538: ...and contributor-pr-lanes resolves the lane with no issue anywhere in the record" \
  || bad "#538: contributor-pr-lanes resolves the issue-less PR record" "$RESULT"

# --- the record carries no fabricated issue evidence ------------------------
EVIDENCE=$(sqlite3 "$STATE/ledger.sqlite3" "SELECT evidence_json FROM source_tasks WHERE id = 't4'")
grep -q "issues: none" <<<"$EVIDENCE" \
  && ok "#538: evidence honestly records no issue, rather than inventing one" \
  || bad "#538: evidence says 'issues: none'" "$EVIDENCE"
grep -q "recorded retroactively by lane estate:4" <<<"$EVIDENCE" \
  && ok "#538: ...and does not claim dispatch.sh did something it did not" \
  || bad "#538: evidence does not falsely claim dispatch.sh" "$EVIDENCE"
grep -qv "claimed by dispatch.sh" <<<"$EVIDENCE" \
  && ok "#538: ...specifically, the 'claimed by dispatch.sh' phrase is absent" \
  || bad "#538: 'claimed by dispatch.sh' phrase absent" "$EVIDENCE"

rm -rf "$D"
echo "  -> $pass ok, $fail failed"
[ "$fail" -eq 0 ]
