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

rm -rf "$D"

echo "  -> $pass ok, $fail failed"
[ "$fail" -eq 0 ]
