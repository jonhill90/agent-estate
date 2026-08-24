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

# agent-supervisor#605: raw SQL, not `Ledger.register_lane`/`record_dispatch`
# -- Python's own `_register_lane_tx` REFUSES an empty `pane_id`/`session_id`
# ("lane registration fields must be non-empty"), so it cannot reproduce
# what the daemon's own writer actually puts in the shared ledger. The
# daemon is a SEPARATE Go binary (`daemon/internal/ledger/ledger.go`) that
# writes to the exact same sqlite file with its own driver and no such
# check -- `EnsureLane` inserts `pane_id=''`, `session_id=''`,
# `server_id='supervisord'`, `transport='claude-print'` directly. Mirroring
# THAT statement (not Python's validated wrapper around it) is the only way
# to reproduce the real row `core.daemon_lane_verified` has to recognize.
# `tasks` similarly mirrors `ledger.go`'s `Create` (`main.go`'s own -task/
# -brief flow), just enough columns for `record-pr-for-task` (`author_lane_
# for`'s Path 3, agent-supervisor#308 item 1) to find it afterward -- the
# same resolution path #605's own decision comment cites for how a daemon
# contributor lane reaches the independence gate at all.
seed_daemon_author() {  # seed_daemon_author <lane> <task-id> <issue> <pr>
  python3 -c '
import sqlite3, sys
db_path, lane, task, issue, repo, pr = sys.argv[1:7]
conn = sqlite3.connect(db_path)
now = 1787600000
conn.execute(
    """INSERT INTO lanes
       (lane, pane_id, nonce, harness, repo, server_id, session_id, command,
        harness_session_id, harness_project_dir, transport, updated_at)
       VALUES (?, "", "", ?, ?, "supervisord", "", ?, "", "", "claude-print", ?)
       ON CONFLICT(lane) DO UPDATE SET harness=excluded.harness, command=excluded.command, updated_at=excluded.updated_at""",
    (lane, "claude", repo, "claude -p", now),
)
conn.execute(
    """INSERT INTO tasks (id, lane, pane_nonce, summary, status, created_at, updated_at)
       VALUES (?, ?, "", "daemon dispatch", "complete", ?, ?)""",
    (task, lane, now, now),
)
conn.commit()
conn.close()
' "$STATE/ledger.sqlite3" "$1" "$2" "$3" "$REPO" "$4"
  python3 "$LEDGER_CLI" --state-dir "$STATE" record-pr-for-task --task "$2" --repo "$REPO" --pr "$4" >/dev/null
}

# The negative control #605 explicitly requires be mutation-tested: a row
# that is daemon-SHAPED and even carries the empty pane_id `EnsureLane`
# writes, but NOT the rest of its exact signature (`server_id`, here an
# impostor value instead of 'supervisord') -- i.e. something that
# superficially resembles a daemon registration but did not actually come
# from supervisord's own `EnsureLane`. This must NOT verify:
# `daemon_lane_verified` checks every field of the ledger row's own
# signature, never the shape or the empty pane_id alone.
seed_fake_daemon_author() {  # seed_fake_daemon_author <lane> <task-id> <issue> <pr>
  python3 -c '
import sqlite3, sys
db_path, lane, task, issue, repo, pr = sys.argv[1:7]
conn = sqlite3.connect(db_path)
now = 1787600000
conn.execute(
    """INSERT INTO lanes
       (lane, pane_id, nonce, harness, repo, server_id, session_id, command,
        harness_session_id, harness_project_dir, transport, updated_at)
       VALUES (?, "", "", ?, ?, "impostor", "", ?, "", "", "claude-print", ?)
       ON CONFLICT(lane) DO UPDATE SET harness=excluded.harness, command=excluded.command, updated_at=excluded.updated_at""",
    (lane, "claude", repo, "claude -p", now),
)
conn.execute(
    """INSERT INTO tasks (id, lane, pane_nonce, summary, status, created_at, updated_at)
       VALUES (?, ?, "", "daemon-shaped, but not supervisords own write", "complete", ?, ?)""",
    (task, lane, now, now),
)
conn.commit()
conn.close()
' "$STATE/ledger.sqlite3" "$1" "$2" "$3" "$REPO" "$4"
  python3 "$LEDGER_CLI" --state-dir "$STATE" record-pr-for-task --task "$2" --repo "$REPO" --pr "$4" >/dev/null
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
# agent-supervisor#486 (THE REPRODUCTION): a REJECTED, independently-reviewed
# PR with a green CI gate must be refused -- reproduced live on PR #485,
# which `merge-pr.sh jonhill90/agent-supervisor 485 --squash` actually
# merged with a recorded verdict of "rejected". `independence_verdict`
# answers "was this reviewed independently", not "was it approved" -- both
# `approved` and `rejected` satisfy its own `IN("approved","rejected")`
# branch, so a rejected-but-independent review used to pass the same
# `value == true` gate as an approved one. Same shape as PR 42's
# independent-approve case above (different author lane, registered
# reviewer lane, green CI) -- the ONLY variable changed is the verdict
# comment itself, REQUEST CHANGES instead of APPROVE.
# ============================================================================
rm -f "$MARKER"
cat > "$FIX/head_485.json" <<'S'
{"headRefOid": "sha-485"}
S
green_checkruns sha-485
cat > "$FIX/author_485.json" <<'S'
{"headRefName": "fix/485-thing", "closingIssuesReferences": [{"number": 485}], "commits": []}
S
cat > "$FIX/reviews_485.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: REQUEST CHANGES**\n\nReview-Lane: t:5\nReviewed-SHA: sha-485", "createdAt": "2026-08-21T19:14:14Z"}]}
S
seed_author t:2 as485-author 485
register_tmux_lane t:5 %85
out=$("$MERGE_PR" "$REPO" 485 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "#486: rejected + independent + green CI is refused"; else bad "#486: rejected + independent + green CI is refused" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "#486: ...and never calls gh pr merge"; else bad "#486: rejected + independent + green CI -- gh pr merge NOT called" "$out"; fi
echo "$out" | grep -q "rejected" && ok "#486: refusal names the rejected verdict" || bad "#486: refusal names rejected verdict" "$out"

# --- the opposite case must still work: an APPROVED, independently-reviewed
# PR with green CI still merges -- same fixture shape as immediately above,
# only the verdict comment differs, proving the new rejected-check does not
# also snag the working approve path. ---------------------------------------
rm -f "$MARKER"
cat > "$FIX/head_486.json" <<'S'
{"headRefOid": "sha-486"}
S
green_checkruns sha-486
cat > "$FIX/author_486.json" <<'S'
{"headRefName": "fix/486-thing", "closingIssuesReferences": [{"number": 486}], "commits": []}
S
cat > "$FIX/reviews_486.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:5\nReviewed-SHA: sha-486", "createdAt": "2026-08-21T19:14:14Z"}]}
S
seed_author t:2 as486-author 486
out=$("$MERGE_PR" "$REPO" 486 2>&1)
rc=$?
if [ "$rc" -eq 0 ]; then ok "#486: approved + independent + green CI still merges"; else bad "#486: approved + independent + green CI still merges" "got rc=$rc: $out"; fi
if [ -f "$MARKER" ]; then ok "#486: ...and actually calls gh pr merge"; else bad "#486: approved + independent + green CI -- gh pr merge called" "$out"; fi
echo "$out" | grep -q "independence confirmed" && ok "#486: success still names independence" || bad "#486: success names independence" "$out"

# ============================================================================
# agent-supervisor#513: the `Author-Lane:` trailer -- agent-dotfiles#308
# carried `Author-Lane: estate:4` in its PR body, and its actual reviewer
# (`estate:5`) genuinely was independent, but `cli.py pr-task` and `git grep
# -rn 'Author-Lane' -- scripts/` both proved nothing in this repo ever read
# that trailer: authorship resolved from a ledger dispatch row ONLY, so any
# PR opened outside `dispatch.sh` had no attributable author regardless of
# what its own body said. THE ASYMMETRY this file's own comments hold: the
# trailer is self-attested, so it can only ever REFUSE a merge (an
# admission costs the liar), never PERMIT one (absence, or a differing
# claim, proves nothing).
# ============================================================================

# --- THE #513 REPRODUCTION: ledger genuinely does not know the author (no
# seed_author call at all, reproducing agent-dotfiles#308's own shape), but
# the PR body admits it via `Author-Lane:`, and the reviewer IS that same
# lane. `contributor_lane_relation()` is never even called here (AUTHOR.known
# is false) -- this must be caught by `claimed_author_conflict()` instead,
# which runs unconditionally. ------------------------------------------
rm -f "$MARKER"
cat > "$FIX/head_90.json" <<'S'
{"headRefOid": "sha-90"}
S
green_checkruns sha-90
cat > "$FIX/author_90.json" <<'S'
{"headRefName": "some-hand-pushed-branch", "closingIssuesReferences": [], "commits": [], "body": "Opened by hand.\n\nAuthor-Lane: estate:4\n"}
S
cat > "$FIX/reviews_90.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: estate:4\nReviewed-SHA: sha-90", "createdAt": "2026-08-23T00:00:00Z"}]}
S
register_tmux_lane estate:4 %900
out=$("$MERGE_PR" "$REPO" 90 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "#513: ledger-blind self-attested Author-Lane == Review-Lane is refused"; else bad "#513: ledger-blind self-attestation refused" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "#513: ...and never merges"; else bad "#513: ledger-blind self-attestation never merges" "$out"; fi
if echo "$out" | grep -q "Author-Lane: estate:4" && echo "$out" | grep -q "reviewed by lane estate:4"; then
  ok "#513: refusal names both the claimed author lane and the reviewer lane"
else
  bad "#513: refusal names both lanes" "$out"
fi

# --- MUST PASS: different lanes, `Reviewed-SHA` at head, CI green -- a
# ledger-KNOWN author, a trailer present and naming that SAME (different-
# from-reviewer) lane, still merges. Proves the trailer's presence does not
# perturb the existing permit path -- a gate that refuses everything is not
# a fix. -------------------------------------------------------------------
rm -f "$MARKER"
cat > "$FIX/head_91.json" <<'S'
{"headRefOid": "sha-91"}
S
green_checkruns sha-91
cat > "$FIX/author_91.json" <<'S'
{"headRefName": "fix/91-thing", "closingIssuesReferences": [{"number": 91}], "commits": [], "body": "Fixes #91.\n\nAuthor-Lane: t:3\n"}
S
cat > "$FIX/reviews_91.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:4\nReviewed-SHA: sha-91", "createdAt": "2026-08-23T00:00:00Z"}]}
S
seed_author t:3 as91-author 91
register_tmux_lane t:4 %910
out=$("$MERGE_PR" "$REPO" 91 2>&1)
rc=$?
if [ "$rc" -eq 0 ]; then ok "#513: different lanes + Author-Lane trailer present and different from reviewer still merges"; else bad "#513: different-lane Author-Lane trailer still merges" "got rc=$rc: $out"; fi
if [ -f "$MARKER" ]; then ok "#513: ...and actually calls gh pr merge"; else bad "#513: different-lane Author-Lane trailer -- gh pr merge called" "$out"; fi

# --- MUST PASS (no trailer at all): already proven by PR 42's own case
# above, re-verified unaffected by this change (no `body` field in that
# fixture at all, so `.body // ""` reads empty and `claimed_lane` is null) --
# not re-run here to avoid asserting the same fixture twice.

# --- the newline-swallow regression, at the integration level: a genuine
# self-review (ledger KNOWS t:9 authored it, and t:9 is also the reviewer --
# ordinary #179-shaped detection, untouched by this fix) whose PR body ALSO
# happens to contain a blank `Author-Lane:` line immediately followed by a
# `Review-Lane:` line -- exactly the shape that swallowed the next line
# under the historical `^\s*Author-Lane:\s*(.*)$` pattern (skills#260,
# agent-tui#113, agent-dotfiles#305's own defect, for its Review-Lane
# sibling). Confirms that noise does not perturb the correct refusal --
# `test_verdict.py`'s own `Issue513AuthorLaneTrailerTests` proves at the
# regex/parse level that the FIXED pattern never manufactures a false claim
# from it in the first place. ---------------------------------------------
rm -f "$MARKER"
cat > "$FIX/head_92.json" <<'S'
{"headRefOid": "sha-92"}
S
green_checkruns sha-92
cat > "$FIX/author_92.json" <<'S'
{"headRefName": "fix/92-thing", "closingIssuesReferences": [{"number": 92}], "commits": [], "body": "Notes.\n\nAuthor-Lane:\nReview-Lane: t:9\n"}
S
cat > "$FIX/reviews_92.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:9\nReviewed-SHA: sha-92", "createdAt": "2026-08-23T00:00:00Z"}]}
S
seed_author t:9 as92-author 92
out=$("$MERGE_PR" "$REPO" 92 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "#513: swallow-shaped body text does not block the ordinary self-review refusal"; else bad "#513: swallow-shaped body -- self-review still refused" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "#513: ...and never merges"; else bad "#513: swallow-shaped body -- never merges" "$out"; fi
echo "$out" | grep -q "reviewed its own PR" && ok "#513: refusal is the ordinary ledger-based self-review reason, not a hallucinated trailer claim" || bad "#513: refusal names the ordinary self-review reason" "$out"

# --- the case that actually PROVES the defense-in-depth value, not just a
# second unreachable-in-practice refusal: the ledger's OWN contributor
# resolution is incomplete -- it knows t:5 wrote SOMETHING toward this PR,
# but has no record of t:6, the lane that is ACTUALLY reviewing its own
# work here. Without claimed_author_conflict(), `contributor_lane_relation`
# would compare reviewer t:6 against the ledger's only known contributor
# (t:5), find them different, and PERMIT -- exactly "every [ledger] gate
# passes" this issue's brief describes. The PR body's own `Author-Lane: t:6`
# admission is the only thing that catches it. --------------------------
rm -f "$MARKER"
cat > "$FIX/head_93.json" <<'S'
{"headRefOid": "sha-93"}
S
green_checkruns sha-93
cat > "$FIX/author_93.json" <<'S'
{"headRefName": "fix/93-thing", "closingIssuesReferences": [{"number": 93}], "commits": [], "body": "Fixes #93.\n\nAuthor-Lane: t:6\n"}
S
cat > "$FIX/reviews_93.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: t:6\nReviewed-SHA: sha-93", "createdAt": "2026-08-23T00:00:00Z"}]}
S
seed_author t:5 as93-author 93
register_tmux_lane t:6 %930
out=$("$MERGE_PR" "$REPO" 93 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "#513: ledger's incomplete contributor set is overridden by the PR's own self-attestation"; else bad "#513: incomplete-ledger self-attestation refused" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "#513: ...and never merges"; else bad "#513: incomplete-ledger self-attestation never merges" "$out"; fi
if echo "$out" | grep -q "Author-Lane: t:6" && echo "$out" | grep -q "reviewed by lane t:6"; then
  ok "#513: refusal names both the claimed author lane and the reviewer lane (incomplete-ledger case)"
else
  bad "#513: refusal names both lanes (incomplete-ledger case)" "$out"
fi

# ============================================================================
# MUTATION CHECK: disabling claimed_author_conflict() lets PR 93 above --
# where the ledger's own contributor set is incomplete and would otherwise
# PERMIT -- through. Proves that test is real evidence, not a check that
# cannot fail: PR 90 above refuses either way (ledger-blind authorship is
# already fail-closed on its own), so PR 93 is the case that actually
# demonstrates this check's value, not just a second unreachable-in-practice
# refusal.
# ============================================================================
MUTDIR3="$D/mutated-claim"
cp -R "$HERE/../../scripts/supervisor" "$MUTDIR3"
python3 - "$MUTDIR3/merge-pr.sh" <<'PYEOF'
import sys
path = sys.argv[1]
text = open(path).read()
marker = 'CLAIM_CONFLICT=$(claimed_author_conflict "$AUTHOR" "$REVIEWER_LANE_ID")'
assert text.count(marker) == 1, "claimed_author_conflict call not found or not unique -- script shape changed"
text = text.replace(marker, 'CLAIM_CONFLICT=\'{"conflict":false}\'  # MUTATED: agent-supervisor#513 check disabled', 1)
open(path, "w").write(text)
PYEOF
MUTATED3="$MUTDIR3/merge-pr.sh"
chmod +x "$MUTATED3"
rm -f "$MARKER"
out=$("$MUTATED3" "$REPO" 93 2>&1)
rc=$?
if [ "$rc" -eq 0 ] && [ -f "$MARKER" ]; then
  ok "mutation confirmed: disabling claimed_author_conflict lets the incomplete-ledger self-review (PR 93) through (case above would be red)"
else
  bad "mutation confirmed: disabling claimed_author_conflict lets the incomplete-ledger self-review through" "got rc=$rc, merged=$([ -f "$MARKER" ] && echo yes || echo no): $out"
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

# ============================================================================
# agent-supervisor#605: daemon-authored PRs. `daemon`/`d-<task>` (written
# exclusively by supervisord's own `EnsureLane`) and `<session>:<index>`
# (a tmux lane) are disjoint namespaces -- see `verdict-independence.sh`'s
# and `core.py`'s own #605 comments. This reproduces #604's exact refusal
# ("author lane daemon and reviewer lane estate:5 are not comparable lane
# ids") end to end through the real `merge-pr.sh`, then confirms the fix
# does NOT degrade into a general permissive parse.
# ============================================================================

# --- a genuine daemon-authored PR, reviewed by a genuine tmux lane: merges -
rm -f "$MARKER"
cat > "$FIX/head_70.json" <<'S'
{"headRefOid": "sha-70"}
S
green_checkruns sha-70
cat > "$FIX/author_70.json" <<'S'
{"headRefName": "d-as70-daemon", "closingIssuesReferences": [{"number": 70}], "commits": []}
S
cat > "$FIX/reviews_70.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: estate:5\nReviewed-SHA: sha-70", "createdAt": "2026-08-24T00:00:00Z"}]}
S
seed_daemon_author daemon as70-daemon 70 70
register_tmux_lane estate:5 %39
out=$("$MERGE_PR" "$REPO" 70 2>&1)
rc=$?
if [ "$rc" -eq 0 ]; then ok "#605: daemon-authored PR + independent tmux review merges"; else bad "#605: daemon-authored PR merges" "got rc=$rc: $out"; fi
if [ -f "$MARKER" ]; then ok "#605: ...and actually calls gh pr merge"; else bad "#605: daemon-authored PR -- gh pr merge called" "$out"; fi
echo "$out" | grep -q "independence confirmed" && ok "#605: success names independence" || bad "#605: success names independence" "$out"

# --- the reverse pairing: a batch d-<task> daemon lane, reviewed by tmux --
rm -f "$MARKER"
cat > "$FIX/head_71.json" <<'S'
{"headRefOid": "sha-71"}
S
green_checkruns sha-71
cat > "$FIX/author_71.json" <<'S'
{"headRefName": "d-as71-daemon", "closingIssuesReferences": [{"number": 71}], "commits": []}
S
cat > "$FIX/reviews_71.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: estate:3\nReviewed-SHA: sha-71", "createdAt": "2026-08-24T00:00:00Z"}]}
S
seed_daemon_author d-as71-daemon as71-daemon 71 71
register_tmux_lane estate:3 %40
out=$("$MERGE_PR" "$REPO" 71 2>&1)
rc=$?
if [ "$rc" -eq 0 ]; then ok "#605: batch d-<task>-authored PR + independent tmux review merges"; else bad "#605: d-<task>-authored PR merges" "got rc=$rc: $out"; fi
if [ -f "$MARKER" ]; then ok "#605: ...and actually calls gh pr merge"; else bad "#605: d-<task>-authored PR -- gh pr merge called" "$out"; fi

# --- a self-review through the daemon lane must still refuse (same-namespace
# machinery, unchanged): the reviewer stamps Review-Lane: daemon, matching
# the author exactly. -------------------------------------------------------
rm -f "$MARKER"
cat > "$FIX/head_72.json" <<'S'
{"headRefOid": "sha-72"}
S
green_checkruns sha-72
cat > "$FIX/author_72.json" <<'S'
{"headRefName": "d-as72-daemon", "closingIssuesReferences": [{"number": 72}], "commits": []}
S
cat > "$FIX/reviews_72.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: daemon\nReviewed-SHA: sha-72", "createdAt": "2026-08-24T00:00:00Z"}]}
S
seed_daemon_author daemon as72-daemon 72 72
out=$("$MERGE_PR" "$REPO" 72 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "#605: daemon reviewing its own daemon-authored PR still refused"; else bad "#605: daemon self-review refused" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "#605: ...and never merges"; else bad "#605: daemon self-review -- never merges" "$out"; fi

# --- #605's own explicit constraint: "do not touch the reviewer-side
# invented-id refusal path... an invented reviewer id was correctly refused
# today -- that must keep refusing". A hand-typed, never-registered
# daemon-shaped reviewer stamp (`Review-Lane: d-as73-neverdispatched`) is
# exactly that invented-id shape -- it must still refuse, and it refuses at
# the SAME place it always did (`_parse_review_lane` finding no registered
# lane for the token), never reaching this fix's new comparison at all. A
# DIFFERENT daemon-shaped string than the earlier PRs' -- "daemon" itself is
# already genuinely registered by seed_daemon_author above, in this same
# shared ledger, and reusing it here would test nothing.
rm -f "$MARKER"
cat > "$FIX/head_73.json" <<'S'
{"headRefOid": "sha-73"}
S
green_checkruns sha-73
cat > "$FIX/author_73.json" <<'S'
{"headRefName": "fix/73-thing", "closingIssuesReferences": [{"number": 73}], "commits": []}
S
cat > "$FIX/reviews_73.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: d-as73-neverdispatched\nReviewed-SHA: sha-73", "createdAt": "2026-08-24T00:00:00Z"}]}
S
seed_author estate:5 as73-author 73
out=$("$MERGE_PR" "$REPO" 73 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "#605: unregistered daemon-shaped reviewer id refuses unknown"; else bad "#605: unregistered daemon-shaped reviewer refuses" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "#605: ...and never merges"; else bad "#605: unregistered daemon-shaped reviewer -- never merges" "$out"; fi
echo "$out" | grep -q "could not parse lane id" && ok "#605: unregistered daemon-shaped reviewer refusal names the reason (the pre-existing invented-id path, untouched)" || bad "#605: unregistered daemon-shaped reviewer refusal named" "$out"

# --- THE mutation the decision explicitly forbids: a lane that is daemon-
# SHAPED (`d-as74-fake`) and even carries the empty pane_id EnsureLane
# writes, but was NOT actually written by supervisord's own EnsureLane
# (server_id='impostor', not 'supervisord') -- proves this fix checks the
# ledger row's own signature, not the shape or the string "daemon"/"d-"
# appearing anywhere. Must still refuse unknown against a genuine tmux
# reviewer, exactly as before this fix. ------------------------------------
rm -f "$MARKER"
cat > "$FIX/head_74.json" <<'S'
{"headRefOid": "sha-74"}
S
green_checkruns sha-74
cat > "$FIX/author_74.json" <<'S'
{"headRefName": "d-as74-fake", "closingIssuesReferences": [{"number": 74}], "commits": []}
S
cat > "$FIX/reviews_74.json" <<'S'
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: estate:9\nReviewed-SHA: sha-74", "createdAt": "2026-08-24T00:00:00Z"}]}
S
seed_fake_daemon_author d-as74-fake as74-fake-daemon 74 74
register_tmux_lane estate:9 %41
out=$("$MERGE_PR" "$REPO" 74 2>&1)
rc=$?
if [ "$rc" -eq 1 ]; then ok "#605: a daemon-shaped lane with no supervisord signature does not verify"; else bad "#605: unverified daemon-named lane refuses" "got rc=$rc: $out"; fi
if [ ! -f "$MARKER" ]; then ok "#605: ...and never merges"; else bad "#605: unverified daemon-named lane -- never merges" "$out"; fi
echo "$out" | grep -q "not comparable lane ids" && ok "#605: unverified daemon-named lane refusal names the reason" || bad "#605: unverified daemon-named lane refusal named" "$out"

rm -rf "$D"

echo "  -> $pass ok, $fail failed"
[ "$fail" -eq 0 ]
