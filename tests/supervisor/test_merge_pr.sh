#!/bin/bash
# merge-pr.sh must refuse to call `gh pr merge` when EITHER gate refuses --
# `ci_gate.py` (agent-supervisor#13) or the authorship/independence gate
# (agent-supervisor#179, scripts/supervisor/verdict-independence.sh) -- and
# must call it only when BOTH pass. This is the whole point of both issues:
# `merge-pr.sh` is documented as the ONLY path a lane or the supervisor
# should use to merge a PR in this repo, so a gate living here "cannot be
# skipped by habit" the way every dispatch-time guard can be walked around by
# free text typed straight into a pane (#179's own reproduction: a "merge the
# PR" prompt sat in the input box of the lane that AUTHORED PR #168, whose
# verdict was `none`).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MERGE_PR="$HERE/../../scripts/supervisor/merge-pr.sh"
LEDGER_CLI="$HERE/../../scripts/supervisor/cli.py"
REPO="acme/repo"
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

command -v jq >/dev/null 2>&1 || { echo "merge-pr.sh"; echo "  SKIP no jq"; exit 0; }

echo "merge-pr.sh"

D=$(mktemp -d)
BIN="$D/bin"; FIX="$D/fixtures"; STATE="$D/state"
mkdir -p "$BIN" "$FIX" "$STATE"
MARKER="$D/merged"

# A fake `gh` that answers exactly the calls `ci_gate.py`, `author_lane_for`
# and `verdict_for` make -- keyed off the PR number and, for `pr view`
# (which all three call, with three DIFFERENT --json field lists), off which
# fields were asked for. Fixture files, not env vars, because this test needs
# a distinct answer per PR number, not just per scenario.
cat > "$BIN/gh" <<'FAKE'
#!/bin/bash
set -uo pipefail
FIX="${GH_FIX:?}"
if [ "$1 $2" = "pr view" ]; then
  num="$3"
  fields=""; prev=""
  for a in "$@"; do
    [ "$prev" = "--json" ] && fields="$a"
    prev="$a"
  done
  case "$fields" in
    headRefOid)
      f="$FIX/head_$num.json"; [ -f "$f" ] && cat "$f" || echo '{"headRefOid":null}'
      ;;
    *closingIssuesReferences*)
      f="$FIX/author_$num.json"
      [ -f "$f" ] && cat "$f" || echo '{"headRefName":"","closingIssuesReferences":[],"commits":[]}'
      ;;
    *)
      f="$FIX/reviews_$num.json"
      [ -f "$f" ] && cat "$f" || echo '{"reviews":[],"comments":[]}'
      ;;
  esac
  exit 0
fi
if [ "$1 $2" = "pr merge" ]; then
  echo "merged" > "$MARKER"
  exit 0
fi
if [ "$1" = "api" ]; then
  case "$2" in
    */check-runs)
      sha="${2%/check-runs}"; sha="${sha##*/commits/}"
      f="$FIX/checkruns_$sha.json"
      [ -f "$f" ] && cat "$f" || echo '[]'
      ;;
    */status)
      echo '{"statuses": []}'
      ;;
    *) echo "fake gh: unexpected api path: $2" >&2; exit 1 ;;
  esac
  exit 0
fi
echo "fake gh: unexpected command: $*" >&2
exit 1
FAKE
chmod +x "$BIN/gh"
export PATH="$BIN:$PATH"
export GH_FIX="$FIX"
export MARKER
export SUPERVISOR_STATE="$STATE"

seed_author() {  # seed_author <lane> <task-id> <issue>
  python3 "$LEDGER_CLI" --state-dir "$STATE" record-dispatch \
    --lane "$1" --task "$2" --summary "seed" --pane-id %9 --pane-path "$D/repo" \
    --command claude --server-id srv --session-id sess --issue "$3" --github "$REPO" \
    --harness claude >/dev/null
  python3 "$LEDGER_CLI" --state-dir "$STATE" record-completion --task "$2" --note done >/dev/null
}
# agent-supervisor#332: registers a lane directly (no task, no author-lane
# resolution machinery) with its own `pane_id` -- what a stamped `Review-Lane:`
# lane must have for resolve_lane_relation() (verdict-independence.sh) to
# reconcile it against the author's pane id instead of refusing `unknown`.
# Defined here, ahead of every PR block below that stamps a reviewer lane,
# because #332 widened the independence gate to require BOTH sides be
# provably registered -- a reviewer lane the ledger has never heard of can
# no longer be waved through on index-string shape alone (see PRs 42 and 47
# below, previously the "known-broken" case this same widening exists to
# close at #235's OWN call site).
register_tmux_lane() {  # register_tmux_lane <lane> <pane-id>
  python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
Ledger(sys.argv[2]).register_lane(
    lane=sys.argv[3], pane_id=sys.argv[4], nonce="nonce-" + sys.argv[3], harness="claude",
    repo="/tmp/repo", server_id="srv", session_id="sess", command="claude", transport="send-keys",
)
' "$HERE/../../scripts/supervisor" "$STATE" "$1" "$2"
}

green_checkruns() {  # green_checkruns <sha>
  cat > "$FIX/checkruns_$1.json" <<S
[{"name": "test", "head_sha": "$1", "status": "completed", "conclusion": "success"}]
S
}

# ============================================================================
# CI gate, unaffected by authorship (agent-supervisor#13) -- both of these
# refuse at the CI check itself and must never reach the authorship gate at
# all, so they need no author/verdict fixtures.
# ============================================================================

# --- failing check: refuses, never calls `gh pr merge` --------------------
rm -f "$MARKER"
cat > "$FIX/head_42.json" <<'S'
{"headRefOid": "sha-1"}
S
cat > "$FIX/checkruns_sha-1.json" <<'S'
[{"name": "test", "head_sha": "sha-1", "status": "completed", "conclusion": "failure"}]
S
out=$("$MERGE_PR" "$REPO" 42 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "failing check exits 1"; else bad "failing check exits 1" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "failing check never merges"; else bad "failing check never merges" "$out"; fi
echo "$out" | grep -q "sha-1" && ok "refusal names the sha" || bad "refusal names the sha" "$out"

# --- green check for an OLDER sha than head: refuses, never merges --------
rm -f "$MARKER"
cat > "$FIX/head_42.json" <<'S'
{"headRefOid": "sha-4-new"}
S
cat > "$FIX/checkruns_sha-4-new.json" <<'S'
[{"name": "test", "head_sha": "sha-3-old", "status": "completed", "conclusion": "success"}]
S
out=$("$MERGE_PR" "$REPO" 42 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "stale-sha check exits 1"; else bad "stale-sha check exits 1" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "stale-sha check never merges"; else bad "stale-sha check never merges" "$out"; fi

# ============================================================================
# Authorship / independence gate (agent-supervisor#179). Every case below has
# CI green -- the only thing under test is the second gate.
# ============================================================================

# --- CI green + an independent, lane-stamped verdict: merges --------------
rm -f "$MARKER"
cat > "$FIX/head_42.json" <<'S'
{"headRefOid": "sha-2"}
S
green_checkruns sha-2
cat > "$FIX/author_42.json" <<'S'
{"headRefName": "fix/42-thing", "closingIssuesReferences": [{"number": 42}], "commits": []}
S
cat > "$FIX/reviews_42.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:4\nReviewed-SHA: sha-2", "createdAt": "2026-08-15T00:00:00Z"}]}
S
seed_author t:3 as42-author 42
register_tmux_lane t:4 %44
out=$("$MERGE_PR" "$REPO" 42 2>&1)
rc=$?
if [ "$rc" -eq 0 ]; then ok "independent review + green CI exits 0"; else bad "independent review + green CI exits 0" "got rc=$rc: $out"; fi
if [ -f "$MARKER" ]; then ok "independent review + green CI merges"; else bad "independent review + green CI merges" "$out"; fi
echo "$out" | grep -q "independence confirmed" && ok "success names independence, not just CI" || bad "success names independence" "$out"

# --- THE #179 REPRODUCTION: author lane reviews (or "reviews") its own PR --
# reproduced exactly: author lane t:3, verdict comment stamped with the SAME
# lane -- this is the shape a self-merge would take even with #178 fixed and
# every guard bypassed via free text into the author's own pane.
rm -f "$MARKER"
cat > "$FIX/head_43.json" <<'S'
{"headRefOid": "sha-10"}
S
green_checkruns sha-10
cat > "$FIX/author_43.json" <<'S'
{"headRefName": "fix/43-thing", "closingIssuesReferences": [{"number": 43}], "commits": []}
S
cat > "$FIX/reviews_43.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:3\nReviewed-SHA: sha-10", "createdAt": "2026-08-15T00:00:00Z"}]}
S
seed_author t:3 as43-author 43
out=$("$MERGE_PR" "$REPO" 43 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "self-merge (author == reviewer lane) refused"; else bad "self-merge refused" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "self-merge never calls gh pr merge"; else bad "self-merge never merges" "$out"; fi
echo "$out" | grep -q "reviewed its own PR" && ok "self-merge refusal names the reason" || bad "self-merge refusal named" "$out"

# --- unknown authorship refuses, even with an otherwise-independent-looking
# verdict -- agent-supervisor#179: "unknown" must never read as "fine". No
# ledger record exists for issue 44 at all (no seed_author call), and the
# branch name matches no dispatch convention either.
rm -f "$MARKER"
cat > "$FIX/head_44.json" <<'S'
{"headRefOid": "sha-11"}
S
green_checkruns sha-11
cat > "$FIX/author_44.json" <<'S'
{"headRefName": "some-hand-pushed-branch", "closingIssuesReferences": [], "commits": []}
S
cat > "$FIX/reviews_44.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:5\nReviewed-SHA: sha-11", "createdAt": "2026-08-15T00:00:00Z"}]}
S
out=$("$MERGE_PR" "$REPO" 44 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "unknown authorship refused"; else bad "unknown authorship refused" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "unknown authorship never merges"; else bad "unknown authorship never merges" "$out"; fi
echo "$out" | grep -q "unresolved" && ok "unknown-authorship refusal names the reason" || bad "unknown-authorship refusal named" "$out"

# --- agent-supervisor#376: a PR marked external via `cli.py mark-pr-external`
# merges on an otherwise-independent-looking verdict, even though the ledger
# has no author lane for it at all -- the exact PR #375 shape (marked
# external, refused anyway before this fix, because `author_lane_for` never
# consulted `pr-external` and fell through to the same `known:false` as
# genuinely unresolved authorship, PR 44 above). No `seed_author` call: the
# whole point is that no ledger record names a contributor.
# ============================================================================
rm -f "$MARKER"
cat > "$FIX/head_62.json" <<'S'
{"headRefOid": "sha-62"}
S
green_checkruns sha-62
cat > "$FIX/author_62.json" <<'S'
{"headRefName": "some-hand-pushed-branch", "closingIssuesReferences": [], "commits": []}
S
cat > "$FIX/reviews_62.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:9\nReviewed-SHA: sha-62", "createdAt": "2026-08-19T00:00:00Z"}]}
S
register_tmux_lane t:9 %90
python3 "$LEDGER_CLI" --state-dir "$STATE" mark-pr-external --repo "$REPO" --pr 62 --note "human pushed directly" --chain-verified >/dev/null
out=$("$MERGE_PR" "$REPO" 62 2>&1)
rc=$?
if [ "$rc" -eq 0 ]; then ok "#376: a PR marked external merges on an independent verdict"; else bad "#376: externally-marked PR merges" "got rc=$rc: $out"; fi
if [ -f "$MARKER" ]; then ok "#376: ...and actually calls gh pr merge"; else bad "#376: externally-marked PR -- gh pr merge called" "$out"; fi
echo "$out" | grep -q "outside the lane system" && ok "#376: success names the external marking, for auditability" || bad "#376: success names external marking" "$out"

# --- ...and marking a PR external does not weaken the fail-closed default:
# a PR that is NEITHER resolvable NOR marked external still refuses exactly
# as PR 44 above did -- reusing that same fixture shape on a fresh PR number
# with no `mark-pr-external` call at all. ----------------------------------
rm -f "$MARKER"
cat > "$FIX/head_63.json" <<'S'
{"headRefOid": "sha-63"}
S
green_checkruns sha-63
cat > "$FIX/author_63.json" <<'S'
{"headRefName": "some-other-hand-pushed-branch", "closingIssuesReferences": [], "commits": []}
S
cat > "$FIX/reviews_63.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:9\nReviewed-SHA: sha-63", "createdAt": "2026-08-19T00:00:00Z"}]}
S
out=$("$MERGE_PR" "$REPO" 63 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "#376: unresolved + NOT marked external still refuses (fail-closed unweakened)"; else bad "#376: unresolved + not external still refuses" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "#376: ...and never merges"; else bad "#376: unresolved + not external -- never merges" "$out"; fi
echo "$out" | grep -q "unresolved" && ok "#376: unresolved-and-not-external refusal names the reason" || bad "#376: unresolved-and-not-external refusal named" "$out"

# --- agent-supervisor#415: PR #400's exact shape -- no closing-issue
# reference AND a branch name that doesn't match the legacy
# `lane/fix/feat/chore/docs` regex (`feat/prior-attempts`), so both the
# issue-linkage path and the branch-regex fallback miss. The ledger DOES
# hold a real contributor record for it, though: `record-pr-for-task`,
# written the way `lane-done.sh` writes it at completion (not `seed_author`,
# which links by issue). `author_lane_for`'s new fourth resolution path
# (`cli.py pr-task`) must find it and merge on an otherwise-independent
# verdict -- the same PR that used to dead-end at "author lane unresolved".
# ============================================================================
rm -f "$MARKER"
cat > "$FIX/head_64.json" <<'S'
{"headRefOid": "sha-64"}
S
green_checkruns sha-64
cat > "$FIX/author_64.json" <<'S'
{"headRefName": "feat/prior-attempts", "closingIssuesReferences": [], "commits": []}
S
cat > "$FIX/reviews_64.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:9\nReviewed-SHA: sha-64", "createdAt": "2026-08-20T00:00:00Z"}]}
S
python3 "$LEDGER_CLI" --state-dir "$STATE" record-dispatch \
  --lane t:8 --task as400-fixpass400 --summary "seed" --pane-id %98 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 400 --github "$REPO" \
  --harness claude >/dev/null
python3 "$LEDGER_CLI" --state-dir "$STATE" record-completion --task as400-fixpass400 --note done >/dev/null
python3 "$LEDGER_CLI" --state-dir "$STATE" record-pr-for-task --task as400-fixpass400 --repo "$REPO" --pr 64 >/dev/null
register_tmux_lane t:9 %91
out=$("$MERGE_PR" "$REPO" 64 2>&1)
rc=$?
if [ "$rc" -eq 0 ]; then ok "#415: pr-task-only authorship (no issue, non-matching branch) merges"; else bad "#415: pr-task-only authorship merges" "got rc=$rc: $out"; fi
if [ -f "$MARKER" ]; then ok "#415: ...and actually calls gh pr merge"; else bad "#415: pr-task-only authorship -- gh pr merge called" "$out"; fi
echo "$out" | grep -q "independence confirmed" && ok "#415: success names independence" || bad "#415: success names independence" "$out"

# --- ...and the same pr-task-resolved authorship still catches a self-review:
# the reviewer lane IS the recorded contributor lane, so this must refuse
# exactly like the #179 reproduction (PR 43 above) does -- the new
# resolution path surfaces evidence, it does not weaken the independence
# check that consumes it. -----------------------------------------------
rm -f "$MARKER"
cat > "$FIX/head_65.json" <<'S'
{"headRefOid": "sha-65"}
S
green_checkruns sha-65
cat > "$FIX/author_65.json" <<'S'
{"headRefName": "feat/prior-attempts", "closingIssuesReferences": [], "commits": []}
S
cat > "$FIX/reviews_65.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:8\nReviewed-SHA: sha-65", "createdAt": "2026-08-20T00:00:00Z"}]}
S
python3 "$LEDGER_CLI" --state-dir "$STATE" record-pr-for-task --task as400-fixpass400 --repo "$REPO" --pr 65 >/dev/null
out=$("$MERGE_PR" "$REPO" 65 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "#415: pr-task-resolved self-review still refused"; else bad "#415: pr-task-resolved self-review refused" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "#415: ...and never merges"; else bad "#415: pr-task-resolved self-review -- never merges" "$out"; fi
echo "$out" | grep -q "reviewed its own PR" && ok "#415: self-review refusal names the reason" || bad "#415: self-review refusal named" "$out"

# --- a verdict with no Review-Lane trailer does not count as independent --
# agent-supervisor#179's acceptance criterion, reproduced literally: a plain
# GitHub review-state APPROVED, with no `**Verdict:` / `Review-Lane:` comment
# at all. Under this estate's single shared GitHub login that state can never
# be told apart from a self-approval, so it must refuse even though the
# author IS known and the verdict IS "approved".
rm -f "$MARKER"
cat > "$FIX/head_45.json" <<'S'
{"headRefOid": "sha-12"}
S
green_checkruns sha-12
cat > "$FIX/author_45.json" <<'S'
{"headRefName": "fix/45-thing", "closingIssuesReferences": [{"number": 45}], "commits": []}
S
cat > "$FIX/reviews_45.json" <<'S'
{"reviews": [{"state": "APPROVED", "commit": {"oid": "sha-12"}}], "comments": []}
S
seed_author t:3 as45-author 45
out=$("$MERGE_PR" "$REPO" 45 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "verdict with no Review-Lane trailer refused"; else bad "no-Review-Lane verdict refused" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "no-Review-Lane verdict never merges"; else bad "no-Review-Lane verdict never merges" "$out"; fi
echo "$out" | grep -q "not a lane-stamped" && ok "no-Review-Lane refusal names the reason" || bad "no-Review-Lane refusal named" "$out"

# --- agent-supervisor#213: a comment APPROVE posted before a later push ---
# must refuse even though CI is green at the new head -- this is #204/#207's
# measured shape, driven through the REAL `merge-pr.sh`, not a re-
# implementation of `_comment_verdict`'s logic. Before this fix,
# `verdict.py`'s comment path never compared `head_sha` at all, so this PR
# would have merged with `ci_gate.py`'s reason ("all checks green at
# sha-47-new") the only thing printed -- true, and silent about the
# verdict, exactly what the issue measured.
rm -f "$MARKER"
cat > "$FIX/head_47.json" <<'S'
{"headRefOid": "sha-47-new"}
S
green_checkruns sha-47-new
cat > "$FIX/author_47.json" <<'S'
{"headRefName": "fix/47-thing", "closingIssuesReferences": [{"number": 47}], "commits": []}
S
cat > "$FIX/reviews_47.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "codex"}, "body": "**Verdict: APPROVE**\nReview-Lane: t:9", "createdAt": "2026-08-15T22:48:01Z"}], "commits": [{"oid": "sha-47-new", "committedDate": "2026-08-15T22:56:42Z"}]}
S
seed_author t:3 as47-author 47
register_tmux_lane t:9 %99
out=$("$MERGE_PR" "$REPO" 47 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "stale comment verdict refuses even with CI green"; else bad "stale comment verdict refuses even with CI green" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "stale comment verdict never merges"; else bad "stale comment verdict never merges" "$out"; fi
echo "$out" | grep -q "sha-47-new" && ok "refusal names the head sha being merged" || bad "refusal names the head sha being merged" "$out"

# --- ...and a `Reviewed-SHA:` trailer matching the head merges normally ---
# the honest mechanism (#213 proposal 1) working end to end: same PR, same
# author/lane setup, but the reviewer states the SHA their verdict covers
# and it is the current head.
rm -f "$MARKER"
cat > "$FIX/reviews_47.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "codex"}, "body": "**Verdict: APPROVE**\nReview-Lane: t:9\nReviewed-SHA: sha-47-new", "createdAt": "2026-08-15T22:48:01Z"}], "commits": [{"oid": "sha-47-new", "committedDate": "2026-08-15T22:56:42Z"}]}
S
out=$("$MERGE_PR" "$REPO" 47 2>&1)
rc=$?
if [ "$rc" -eq 0 ] && [ -f "$MARKER" ]; then ok "Reviewed-SHA matching head merges"; else bad "Reviewed-SHA matching head merges" "got rc=$rc, merged=$([ -f "$MARKER" ] && echo yes || echo no): $out"; fi

# --- a refusal never prints a bare "refused --" (agent-supervisor#192) ----
# `independence_verdict`'s "not yet reviewed" branch deliberately returns an
# empty detail (kept that way for #184's OTHER caller, digest.sh) -- this PR
# has no author fixture (unresolved authorship) AND no verdict comment at
# all, reproducing the exact "detail empty" shape #192 measured on #169,
# #176 and #191 right after the gate went live. The message must still name
# something -- the raw verdict/author JSON, at minimum -- never an empty dash.
rm -f "$MARKER"
cat > "$FIX/head_46.json" <<'S'
{"headRefOid": "sha-13"}
S
green_checkruns sha-13
cat > "$FIX/author_46.json" <<'S'
{"headRefName": "some-hand-pushed-branch", "closingIssuesReferences": [], "commits": []}
S
cat > "$FIX/reviews_46.json" <<'S'
{"reviews": [], "comments": []}
S
out=$("$MERGE_PR" "$REPO" 46 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "never-reviewed + unresolved authorship refuses"; else bad "never-reviewed + unresolved authorship refuses" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "never-reviewed + unresolved authorship never merges"; else bad "never-reviewed + unresolved authorship never merges" "$out"; fi
if ! echo "$out" | grep -qE '^merge-pr: refused -- *$'; then
  ok "refusal is never a bare dash with nothing after it"
else
  bad "refusal is never a bare dash with nothing after it" "$out"
fi
echo "$out" | grep -q "merge-pr: refused -- .\+" && ok "refusal message names a reason" || bad "refusal message names a reason" "$out"

# ============================================================================
# agent-supervisor#292: author exclusion (and this same independence gate)
# could not tell a claude-print lane apart from a tmux lane -- its id has no
# `<session>:<index>` shape to compare (it IS its task id, no window to
# index), so `lane_relation`'s string-shape check answered `unknown` for
# EVERY pairing that involved one, and `independence_verdict` refuses on
# `unknown` exactly as hard as on a real self-review. Both directions #292
# itself requires, run through the REAL `merge-pr.sh`: a tmux lane's verdict
# on a claude-print-authored PR, and a claude-print lane's verdict on a
# tmux-authored PR. Both must merge -- the ledger's own `pane_id` registry
# proves the two differ, whichever population either is in.
# ============================================================================
reregister_as_claude_print() {  # reregister_as_claude_print <lane>
  python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
ledger = Ledger(sys.argv[2])
lane = sys.argv[3]
row = ledger.get_lane(lane)
ledger.register_lane(
    lane=lane, pane_id="claude-print:" + lane, nonce=row["nonce"], harness=row["harness"],
    repo=row["repo"], server_id=row["server_id"], session_id=row["session_id"], command=row["command"],
    harness_session_id=row["harness_session_id"], harness_project_dir=row["harness_project_dir"],
    transport="claude-print",
)
' "$HERE/../../scripts/supervisor" "$STATE" "$1"
}
# --- direction 1: a tmux lane's verdict on a claude-print-authored PR -----
# the PR #288 shape itself: the author lane has no tmux window at all.
rm -f "$MARKER"
cat > "$FIX/head_48.json" <<'S'
{"headRefOid": "sha-20"}
S
green_checkruns sha-20
cat > "$FIX/author_48.json" <<'S'
{"headRefName": "fix/48-thing", "closingIssuesReferences": [{"number": 48}], "commits": []}
S
cat > "$FIX/reviews_48.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:20\nReviewed-SHA: sha-20", "createdAt": "2026-08-16T00:00:00Z"}]}
S
seed_author ad182-author-b as48-author 48
reregister_as_claude_print ad182-author-b
register_tmux_lane t:20 %20
out=$("$MERGE_PR" "$REPO" 48 2>&1)
rc=$?
if [ "$rc" -eq 0 ]; then ok "a tmux reviewer of a claude-print-authored PR merges"; else bad "a tmux reviewer of a claude-print-authored PR merges" "got rc=$rc: $out"; fi
if [ -f "$MARKER" ]; then ok "...and actually calls gh pr merge"; else bad "...and actually calls gh pr merge" "$out"; fi
echo "$out" | grep -q "independence confirmed" && ok "...independence confirmed, not just CI green" || bad "...independence confirmed" "$out"

# --- direction 2: a claude-print lane's verdict on a tmux-authored PR -----
rm -f "$MARKER"
cat > "$FIX/head_49.json" <<'S'
{"headRefOid": "sha-21"}
S
green_checkruns sha-21
cat > "$FIX/author_49.json" <<'S'
{"headRefName": "fix/49-thing", "closingIssuesReferences": [{"number": 49}], "commits": []}
S
cat > "$FIX/reviews_49.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: ad182-review-186\nReviewed-SHA: sha-21", "createdAt": "2026-08-16T00:00:00Z"}]}
S
seed_author t:22 as49-author 49
register_tmux_lane t:22 %22
python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
Ledger(sys.argv[2]).register_lane(
    lane="ad182-review-186", pane_id="claude-print:ad182-review-186", nonce="nonce-review",
    harness="claude", repo="/tmp/repo", server_id="srv", session_id="sess", command="claude",
    transport="claude-print",
)
' "$HERE/../../scripts/supervisor" "$STATE"
out=$("$MERGE_PR" "$REPO" 49 2>&1)
rc=$?
if [ "$rc" -eq 0 ]; then ok "a claude-print reviewer of a tmux-authored PR merges"; else bad "a claude-print reviewer of a tmux-authored PR merges" "got rc=$rc: $out"; fi
if [ -f "$MARKER" ]; then ok "...and actually calls gh pr merge"; else bad "...and actually calls gh pr merge" "$out"; fi
echo "$out" | grep -q "independence confirmed" && ok "...independence confirmed, not just CI green" || bad "...independence confirmed" "$out"

# --- both populations still refuse when the ledger canNOT prove they differ
#     -- e.g. a claude-print lane "reviewing" itself. Fail-closed is not
#     loosened by the widening above. -------------------------------------
rm -f "$MARKER"
cat > "$FIX/head_50.json" <<'S'
{"headRefOid": "sha-22"}
S
green_checkruns sha-22
cat > "$FIX/author_50.json" <<'S'
{"headRefName": "fix/50-thing", "closingIssuesReferences": [{"number": 50}], "commits": []}
S
cat > "$FIX/reviews_50.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: ad182-author-b\nReviewed-SHA: sha-22", "createdAt": "2026-08-16T00:00:00Z"}]}
S
seed_author ad182-author-b as50-author 50
reregister_as_claude_print ad182-author-b
out=$("$MERGE_PR" "$REPO" 50 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "a claude-print lane reviewing its own PR is still refused"; else bad "a claude-print lane reviewing its own PR is still refused" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "...and never merges"; else bad "...and never merges" "$out"; fi

# ============================================================================
# agent-supervisor#332 (PR #332's own reviewer, blocking finding): the
# MERGE-TIME independence gate -- this file's `verdict-independence.sh`
# `lane_relation()`, called from merge-pr.sh -- compared author/reviewer
# lane ids by `<session>:<index>` SHAPE ALONE, with no pane id at all,
# unlike dispatch.sh's author-exclusion loop (#235). A window renumber
# between the author's dispatch and the reviewer's makes that shape answer
# wrong in BOTH directions -- see CLAUDE.md invariant 9. Both cases below
# are driven through the REAL `merge-pr.sh`, exactly as #235's own
# `test_lane_relation_renumber.sh`/`test_lane_relation_cross_session_
# collision.sh` prove the underlying `cli.py lane-relation --lane-pane-id`
# mechanism, but proving the WIRING at the actual enforcement call site --
# the gap #235 left and this PR closes.
# ============================================================================

# --- case 1: index-string SAME (shape says "same" -- self-review), pane ids
# DIFFER (truth: two unrelated windows that happen to share an index in
# differently-named sessions). Before this fix: `lane_relation("old:60",
# "new:60")` shape-checks `same` (index 60 == 60, session name ignored per
# #108) and refuses a genuinely independent review as a false self-merge
# block. After: the ledger's own pane ids (%9 vs %77) prove them different,
# and the PR merges. -------------------------------------------------------
rm -f "$MARKER"
cat > "$FIX/head_60.json" <<'S'
{"headRefOid": "sha-60"}
S
green_checkruns sha-60
cat > "$FIX/author_60.json" <<'S'
{"headRefName": "fix/60-thing", "closingIssuesReferences": [{"number": 60}], "commits": []}
S
cat > "$FIX/reviews_60.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: new:60\nReviewed-SHA: sha-60", "createdAt": "2026-08-18T00:00:00Z"}]}
S
seed_author old:60 as60-author 60
register_tmux_lane new:60 %77
out=$("$MERGE_PR" "$REPO" 60 2>&1)
rc=$?
if [ "$rc" -eq 0 ]; then ok "#332: matching index, different pane -- a genuinely independent review merges"; else bad "#332: matching index, different pane merges" "got rc=$rc: $out"; fi
if [ -f "$MARKER" ]; then ok "#332: ...and actually calls gh pr merge"; else bad "#332: matching index, different pane -- gh pr merge called" "$out"; fi

# --- case 2 (the converse, and the security-critical direction -- invariant
# 9): index-string DIFFERENT (shape says "different" -- looks independent),
# pane id the SAME (truth: the exact same physical window, renumbered
# between the author's dispatch and the review). Before this fix:
# `lane_relation("t:3", "t:9")` shape-checks `different` (3 != 9) and MERGES
# a self-review. After: the ledger's own pane id (%9 shared by both rows)
# proves them the same lane, and the merge is refused. -------------------
rm -f "$MARKER"
cat > "$FIX/head_61.json" <<'S'
{"headRefOid": "sha-61"}
S
green_checkruns sha-61
cat > "$FIX/author_61.json" <<'S'
{"headRefName": "fix/61-thing", "closingIssuesReferences": [{"number": 61}], "commits": []}
S
cat > "$FIX/reviews_61.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:62\nReviewed-SHA: sha-61", "createdAt": "2026-08-18T00:00:00Z"}]}
S
seed_author t:3 as61-author 61
register_tmux_lane t:62 %9
out=$("$MERGE_PR" "$REPO" 61 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "#332: different index, same pane -- a renumbered self-review is refused"; else bad "#332: different index, same pane refused" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "#332: ...and never calls gh pr merge"; else bad "#332: different index, same pane -- gh pr merge never called" "$out"; fi
echo "$out" | grep -q "reviewed its own PR" && ok "#332: renumbered self-review refusal names the reason" || bad "#332: renumbered self-review refusal named" "$out"

# ============================================================================
# MUTATION CHECK: silencing the authorship gate lets the #179 reproduction
# (case above) through. Confirms the test above is real evidence, not a check
# that cannot fail -- see this repo's own CLAUDE.md on that requirement.
# ============================================================================
# A patched COPY of the whole scripts/supervisor/ directory, not just the one
# file -- merge-pr.sh sources ci_gate.py, cli.py and verdict-independence.sh
# as siblings of its own path, and those must still resolve from wherever the
# mutated copy runs.
MUTDIR="$D/mutated"
cp -R "$HERE/../../scripts/supervisor" "$MUTDIR"
MUTATED="$MUTDIR/merge-pr.sh"
python3 - "$MUTATED" <<'PYEOF'
import sys
path = sys.argv[1]
text = open(path).read()
marker = 'if [ "$(jq -r \'.value\' <<<"$IND")" != "true" ]; then'
assert text.count(marker) == 1, "authorship-gate check not found or not unique -- script shape changed"
text = text.replace(
    marker,
    'if false; then  # MUTATED: authorship/independence gate disabled (agent-supervisor#179)',
    1,
)
open(path, "w").write(text)
PYEOF
chmod +x "$MUTATED"
rm -f "$MARKER"
out=$("$MUTATED" "$REPO" 43 2>&1)
rc=$?
if [ "$rc" -eq 0 ] && [ -f "$MARKER" ]; then
  ok "mutation confirmed: disabling the authorship gate lets the #179 self-merge through (case above would be red)"
else
  bad "mutation confirmed: disabling the authorship gate lets the self-merge through" "got rc=$rc, merged=$([ -f "$MARKER" ] && echo yes || echo no): $out"
fi

# ============================================================================
# agent-supervisor#200: `author_lane_for` used to narrow a PR's authorship to
# the single lane that produced its branch. A FIX-PASS task -- dispatched
# later against the SAME issue to address review findings -- is a second,
# later CONTRIBUTOR to that same PR (dispatch.sh's own `--reviews-pr` guard
# already excludes it from being DISPATCHED that PR's review, since #190),
# but this file's independent MERGE gate never learned that widening: the
# fix-pass lane itself could still approve/merge its own fix, because
# `author_lane_for` only ever named the original author. Reproduced here
# exactly as dispatch.sh's own #190 regression was: issue #70 has TWO
# non-review contributors -- t:3 (the original author) and t:4 (a later
# fix-pass) -- and t:4 reviews the PR its own fix-pass produced.
# ============================================================================
rm -f "$MARKER"
python3 "$LEDGER_CLI" --state-dir "$STATE" record-dispatch \
  --lane t:3 --task as70-author --summary "#70 author" --pane-id %70 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 70 --github "$REPO" --harness claude >/dev/null
python3 "$LEDGER_CLI" --state-dir "$STATE" record-completion --task as70-author --note done >/dev/null
python3 "$LEDGER_CLI" --state-dir "$STATE" record-dispatch \
  --lane t:4 --task as70-fixpass --summary "#70 fix pass addressing review findings" --pane-id %71 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 70 --github "$REPO" --harness claude >/dev/null
python3 "$LEDGER_CLI" --state-dir "$STATE" record-completion --task as70-fixpass --note done >/dev/null
cat > "$FIX/head_70.json" <<'S'
{"headRefOid": "sha-70"}
S
green_checkruns sha-70
cat > "$FIX/author_70.json" <<'S'
{"headRefName": "fix/70-thing", "closingIssuesReferences": [{"number": 70}], "commits": []}
S
cat > "$FIX/reviews_70.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:4\nReviewed-SHA: sha-70", "createdAt": "2026-08-20T00:00:00Z"}]}
S
out=$("$MERGE_PR" "$REPO" 70 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "#200: a fix-pass lane approving its own fix-pass is refused, not just the original author"; else bad "#200: fix-pass self-review refused" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "#200: ...and never merges"; else bad "#200: fix-pass self-review never merges" "$out"; fi
echo "$out" | grep -q "author lane t:4 reviewed its own PR" && ok "#200: refusal names the fix-pass lane (t:4), not the original author (t:3)" || bad "#200: refusal names the fix-pass lane" "$out"

# --- MUTATION: narrowing the contributor set back to a single lane (the
# pre-#200 shape) lets the fix-pass self-review above through. A shadow copy
# of the WHOLE scripts/supervisor directory with `cli.py`'s
# `contributor-issue-lanes`/`contributor-pr-lanes` handlers patched to
# return only the FIRST contributor row -- the literal narrowing #200
# removes -- so t:4 (the fix-pass lane) drops out of the set entirely and
# only t:3 (the original author) remains excluded. -----------------------
MUTDIR2="$D/mutated-contrib"
cp -R "$HERE/../../scripts/supervisor" "$MUTDIR2"
python3 - "$MUTDIR2/cli.py" <<'PYEOF'
import sys
path = sys.argv[1]
text = open(path).read()
marker = '"contributors": [{"lane": row["lane"], "task": row["id"]} for row in rows],'
assert text.count(marker) == 2, "contributor-issue-lanes/contributor-pr-lanes shape changed"
text = text.replace(marker, '"contributors": [{"lane": row["lane"], "task": row["id"]} for row in rows][:1],')
open(path, "w").write(text)
PYEOF
rm -f "$MUTDIR2/__pycache__" 2>/dev/null
MUTATED2="$MUTDIR2/merge-pr.sh"
chmod +x "$MUTATED2"
rm -f "$MARKER"
out=$("$MUTATED2" "$REPO" 70 2>&1)
rc=$?
if [ "$rc" -eq 0 ] && [ -f "$MARKER" ]; then
  ok "mutation confirmed: narrowing the contributor set back to one lane lets the fix-pass self-review through (case above would be red)"
else
  bad "mutation confirmed: narrowing the contributor set lets the fix-pass self-review through" "got rc=$rc, merged=$([ -f "$MARKER" ] && echo yes || echo no): $out"
fi

# ============================================================================
# agent-supervisor#251: `author_lane_for`'s `gh pr view` call (the
# closingIssuesReferences/commits lookup) used to run with NO bound at all --
# the one `gh` call in verdict-independence.sh that `digest.sh`'s own
# `gh_call`/`with_timeout` guard never covered, because merge-pr.sh calls
# `author_lane_for` directly. Reproduced live: `tests/supervisor/
# test_shell_suites.py`'s own harness sent SIGTERM then SIGKILL to this
# suite's whole process group after a 300s timeout and still could not
# confirm the group dead -- a `gh` blocked forever is exactly that shape.
# This is the hang case, not just the error case: a dependency that never
# returns, not one that exits non-zero fast.
# ============================================================================
rm -f "$MARKER"
cat > "$FIX/head_51.json" <<'S'
{"headRefOid": "sha-23"}
S
green_checkruns sha-23
cat > "$FIX/reviews_51.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:9\nReviewed-SHA: sha-23", "createdAt": "2026-08-16T00:00:00Z"}]}
S
register_tmux_lane t:9 %9

HANGBIN="$D/hangbin"; mkdir -p "$HANGBIN"
cat > "$HANGBIN/gh" <<FAKE
#!/bin/bash
set -uo pipefail
if [ "\$1 \$2" = "pr view" ]; then
  fields=""; prev=""
  for a in "\$@"; do
    [ "\$prev" = "--json" ] && fields="\$a"
    prev="\$a"
  done
  case "\$fields" in
    *closingIssuesReferences*)
      sleep 30
      echo '{"headRefName":"","closingIssuesReferences":[],"commits":[]}'
      exit 0
      ;;
  esac
fi
exec "$BIN/gh" "\$@"
FAKE
chmod +x "$HANGBIN/gh"

start=$(date +%s)
out=$(PATH="$HANGBIN:$PATH" AUTHOR_LANE_GH_TIMEOUT_SECONDS=2 GH_FIX="$FIX" MARKER="$MARKER" \
  SUPERVISOR_STATE="$STATE" timeout 60 "$MERGE_PR" "$REPO" 51 2>&1)
rc=$?
elapsed=$(( $(date +%s) - start ))
[ "$elapsed" -lt 30 ] && ok "a hanging author-lane gh pr view does not hang merge-pr.sh (returned in ${elapsed}s)" \
  || bad "hanging author-lane gh bounded" "took ${elapsed}s, rc=$rc: $out"
grep -q "gh pr view timed out after 2s" <<<"$out" \
  && ok "a hanging author-lane gh pr view is named as a timeout, not a plain failure" \
  || bad "hanging author-lane gh named as timeout" "rc=$rc: $out"
[ ! -f "$MARKER" ] && ok "a hung author lookup never merges" || bad "a hung author lookup never merges" "$out"

rm -rf "$D"

echo "  -> $pass ok, $fail failed"
[ "$fail" -eq 0 ]
