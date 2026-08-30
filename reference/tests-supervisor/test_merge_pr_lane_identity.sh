#!/bin/bash
# agent-supervisor#520 / #513, driven through the REAL `merge-pr.sh` against a
# REAL (isolated) tmux server.
#
# `tests/supervisor/test_merge_pr.sh` already covers the merge gate's four
# decisions, but every lane in it is a fixture row -- there is no tmux server
# behind any of them, so it cannot exercise the one thing #520 is about:
# whether the ledger's `pane_id` for the reviewing lane is still TRUE. That is
# what this suite adds, and it is why it pays for a live server.
#
# The five cases, all with CI green so only the independence gate is under
# test:
#   1. a genuine cross-lane verdict at the current head        -> MERGES
#   2. no verdict at all                                       -> refuses
#   3. the author lane reviewing its own PR                    -> refuses
#   4. a verdict against a SHA the head has moved past         -> refuses
#   5. a reviewer whose registration the live server disproves -> refuses (new)
#
# Case 5 is the estate:2..estate:5 shape measured on 2026-08-23: a row naming
# a pane (`%38`) that does not exist on the server the row itself names. It
# used to be indistinguishable from case 1.
#
# Case 1 runs BOTH before and after case 5's mutation, so "merges" is proved to
# be a measurement rather than the default -- a gate that had simply stopped
# checking would pass case 1 and fail nothing.
#
# INVARIANT 4: creates a session, so `TMUX_TMPDIR` is set and
# `assert_isolated_tmux` gates it. Nothing outside this socket is addressed.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
MERGE_PR="$SUP/merge-pr.sh"
LEDGER_CLI="$SUP/cli.py"
source "$SUP/tmux-isolation.sh"
REPO="acme/repo"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "merge-pr.sh x lane identity (live tmux)"
# A SKIP is honest on a laptop without tmux; on CI it is the defect this
# repo names most -- an instrument that cannot see a thing looks exactly like
# the thing being absent, and a suite that silently skips every run is green
# forever while testing nothing. On CI the dependencies are installed by
# validate.yml, so a missing one is a broken workflow, not an environment to
# tolerate: skip locally, FAIL there.
missing() {  # missing <what>
  if [ -n "${CI:-}" ]; then
    echo "  FAIL $1 -- required on CI, where validate.yml installs it; a silent skip here would be a suite that tests nothing"
    exit 1
  fi
  echo "  SKIP $1"
  exit 0
}
command -v tmux >/dev/null 2>&1 || missing "no tmux on PATH"
command -v jq   >/dev/null 2>&1 || missing "no jq"
# See test_lane_identity.sh's own note: the pane's command must be a real
# entry in `adapter.HARNESS_COMMANDS`, and `node` is the one reliably
# available here (a copied binary dies to code-signing on macOS, a symlink
# reports its target's name).
command -v node >/dev/null 2>&1 || missing "no node on PATH"

S="merge-identity-test-$$"
RT="$(mktemp -d "${TMPDIR:-/tmp}/merge-identity-tmux.XXXXXX")"
D="$(mktemp -d "${TMPDIR:-/tmp}/merge-identity.XXXXXX")"
BIN="$D/bin"; FIX="$D/fixtures"; STATE="$D/state"; WORK="$D/work"
mkdir -p "$BIN" "$FIX" "$STATE" "$WORK"
MARKER="$D/merged"
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1

cleanup() {
  unset TMUX TMUX_PANE
  export TMUX_TMPDIR="$RT"
  tmux kill-session -t "$S" 2>/dev/null
  rm -rf "$RT" "$D"
}
trap cleanup EXIT INT TERM

# The same fake `gh` shape test_merge_pr.sh uses -- fixture files keyed on PR
# number, because each case needs its own answer to the three DIFFERENT
# `gh pr view --json ...` calls `ci_gate.py`, `author_lane_for` and
# `verdict.py` each make.
cat > "$BIN/gh" <<'FAKE'
#!/bin/bash
set -uo pipefail
FIX="${GH_FIX:?}"
if [ "$1 $2" = "pr view" ]; then
  num="$3"; fields=""; prev=""
  for a in "$@"; do [ "$prev" = "--json" ] && fields="$a"; prev="$a"; done
  case "$fields" in
    headRefOid) f="$FIX/head_$num.json"; [ -f "$f" ] && cat "$f" || echo '{"headRefOid":null}' ;;
    *closingIssuesReferences*) f="$FIX/author_$num.json"; [ -f "$f" ] && cat "$f" || echo '{"headRefName":"","closingIssuesReferences":[],"commits":[]}' ;;
    *) f="$FIX/reviews_$num.json"; [ -f "$f" ] && cat "$f" || echo '{"reviews":[],"comments":[]}' ;;
  esac
  exit 0
fi
if [ "$1 $2" = "pr merge" ]; then echo merged > "$MARKER"; exit 0; fi
if [ "$1" = "api" ]; then
  case "$2" in
    */check-runs) sha="${2%/check-runs}"; sha="${sha##*/commits/}"
      f="$FIX/checkruns_$sha.json"; [ -f "$f" ] && cat "$f" || echo '[]' ;;
    */status) echo '{"statuses": []}' ;;
    *) echo "fake gh: unexpected api path: $2" >&2; exit 1 ;;
  esac
  exit 0
fi
echo "fake gh: unexpected command: $*" >&2
exit 1
FAKE
chmod +x "$BIN/gh"
export PATH="$BIN:$PATH"
export GH_FIX="$FIX" MARKER SUPERVISOR_STATE="$STATE"

green() { cat > "$FIX/checkruns_$1.json" <<S
[{"name": "test", "head_sha": "$1", "status": "completed", "conclusion": "success"}]
S
}
head_is() { printf '{"headRefOid": "%s"}\n' "$2" > "$FIX/head_$1.json"; }
closes()  { printf '{"headRefName": "fix/%s-thing", "closingIssuesReferences": [{"number": %s}], "commits": []}\n' "$2" "$2" > "$FIX/author_$1.json"; }
verdict_comment() {  # verdict_comment <pr> <lane> <reviewed-sha>
  cat > "$FIX/reviews_$1.json" <<S
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\nReview-Lane: $2\nReviewed-SHA: $3", "createdAt": "2026-08-23T00:00:00Z"}]}
S
}
no_comments() { printf '{"reviews": [], "comments": []}\n' > "$FIX/reviews_$1.json"; }
seed_author() {  # seed_author <lane> <task> <issue>
  python3 "$LEDGER_CLI" --state-dir "$STATE" record-dispatch \
    --lane "$1" --task "$2" --summary seed --pane-id "$4" --pane-path "$WORK" \
    --command node --server-id srv --session-id sess --issue "$3" --github "$REPO" \
    --harness codex >/dev/null 2>&1
  python3 "$LEDGER_CLI" --state-dir "$STATE" record-completion --task "$2" --note done >/dev/null 2>&1
}
set_pane() {  # set_pane <lane> <pane_id> <server_id>
  python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
ledger = Ledger(sys.argv[2])
row = ledger.get_lane(sys.argv[3]) or {}
ledger.register_lane(
    lane=sys.argv[3], pane_id=sys.argv[4], nonce=row.get("nonce") or "n", harness="codex",
    repo=sys.argv[6], server_id=sys.argv[5], session_id="$0", command="node", transport="send-keys",
)
' "$SUP" "$STATE" "$1" "$2" "$3" "$WORK"
}

# --- a live server with two real panes -------------------------------------
PANE_CMD="node -e 'setTimeout(function(){}, 600000)'"
tmux new-session -d -s "$S" -c "$WORK" "$PANE_CMD" || { echo "  FAIL could not create the isolated session"; exit 1; }
tmux new-window -t "$S" -c "$WORK" "$PANE_CMD"
for _ in $(seq 1 40); do
  [ "$(tmux list-panes -a -F '#{pane_current_command}' 2>/dev/null | sort -u | tr '\n' ' ')" = "node " ] && break
  sleep 0.25
done
W1=$(tmux list-windows -t "$S" -F '#{window_index}' | head -n1)
W2=$(tmux list-windows -t "$S" -F '#{window_index}' | tail -n1)
AUTHOR_LANE="$S:$W1"; REVIEWER_LANE="$S:$W2"
AUTHOR_PANE=$(tmux display-message -p -t "$S:$W1" '#{pane_id}')
REVIEWER_PANE=$(tmux display-message -p -t "$S:$W2" '#{pane_id}')
SERVER_ID=$(tmux display-message -p -t "$S:$W2" '#{socket_path}:#{session_created}')

# Both lanes registered the way `register-lane-self.sh` would: against the
# panes that really exist, on the server that is really running.
seed_author "$AUTHOR_LANE" t520-author 520 "$AUTHOR_PANE"
set_pane "$AUTHOR_LANE" "$AUTHOR_PANE" "$SERVER_ID"
set_pane "$REVIEWER_LANE" "$REVIEWER_PANE" "$SERVER_ID"

run_merge() { rm -f "$MARKER"; OUT=$("$MERGE_PR" "$REPO" "$1" 2>&1); RC=$?; }
merged() { [ -f "$MARKER" ]; }

# ============================================================================
# 1. a genuine cross-lane verdict at the current head -> MERGES
# ============================================================================
head_is 520 sha-520; green sha-520; closes 520 520
verdict_comment 520 "$REVIEWER_LANE" sha-520
run_merge 520
{ [ "$RC" -eq 0 ] && merged; } \
  && ok "1/4 genuine cross-lane verdict at head: merges" \
  || bad "1/4 genuine cross-lane verdict at head: merges" "rc=$RC merged=$(merged && echo yes || echo no): $OUT"
grep -q "independence confirmed" <<<"$OUT" \
  && ok "1/4 ...and says independence, not just CI green" || bad "1/4 names independence" "$OUT"

# ============================================================================
# 2. no verdict at all -> REFUSES
# ============================================================================
head_is 521 sha-521; green sha-521; closes 521 520; no_comments 521
run_merge 521
{ [ "$RC" -eq 1 ] && ! merged; } \
  && ok "2/4 no verdict on record: refuses and never merges" \
  || bad "2/4 no verdict on record refuses" "rc=$RC merged=$(merged && echo yes || echo no): $OUT"

# ============================================================================
# 3. a same-lane (self) verdict -> REFUSES
# ============================================================================
head_is 522 sha-522; green sha-522; closes 522 520
verdict_comment 522 "$AUTHOR_LANE" sha-522
run_merge 522
{ [ "$RC" -eq 1 ] && ! merged; } \
  && ok "3/4 self-review (author lane == reviewer lane): refuses and never merges" \
  || bad "3/4 self-review refuses" "rc=$RC merged=$(merged && echo yes || echo no): $OUT"
grep -q "reviewed its own PR" <<<"$OUT" \
  && ok "3/4 ...and names it as a self-review" || bad "3/4 names self-review" "$OUT"

# ============================================================================
# 4. a verdict against a STALE SHA -> REFUSES
# ============================================================================
# Same reviewer, same registration, same CI -- the ONLY difference from case 1
# is that the Reviewed-SHA trailer names the commit before the head moved.
head_is 523 sha-523-new; green sha-523-new; closes 523 520
verdict_comment 523 "$REVIEWER_LANE" sha-523-old
run_merge 523
{ [ "$RC" -eq 1 ] && ! merged; } \
  && ok "4/4 verdict against a stale SHA: refuses and never merges" \
  || bad "4/4 stale-SHA verdict refuses" "rc=$RC merged=$(merged && echo yes || echo no): $OUT"
grep -q "sha-523-new" <<<"$OUT" \
  && ok "4/4 ...and names the head actually being merged" || bad "4/4 names the head sha" "$OUT"

# ============================================================================
# 5. #520: a reviewer whose registration the live server disproves -> REFUSES
# ============================================================================
# The estate:2..estate:5 shape: the row still exists and still has a pane_id,
# so every pre-#520 check passes; the pane simply is not on the server the row
# names. Only the reviewer's row is mutated -- the verdict, the CI state, the
# author and the SHA are byte-identical to case 1, which merged.
head_is 524 sha-524; green sha-524; closes 524 520
verdict_comment 524 "$REVIEWER_LANE" sha-524
set_pane "$REVIEWER_LANE" "%99999" "$SERVER_ID"
run_merge 524
{ [ "$RC" -eq 1 ] && ! merged; } \
  && ok "5: reviewer registered on a pane the live server does not have: refuses and never merges" \
  || bad "5: contradicted reviewer registration refuses" "rc=$RC merged=$(merged && echo yes || echo no): $OUT"
grep -q "contradicts" <<<"$OUT" \
  && ok "5: ...and names the contradiction rather than a vague unknown" || bad "5: names the contradiction" "$OUT"
grep -q "register-lane-self.sh" <<<"$OUT" \
  && ok "5: ...and names the remedy" || bad "5: names the remedy" "$OUT"

# --- the mutation is load-bearing in BOTH directions -----------------------
# Restore the reviewer's true pane and re-run case 1's exact fixtures. If this
# did not merge again, case 5 would prove nothing -- the refusal could have
# come from anything the mutation disturbed.
set_pane "$REVIEWER_LANE" "$REVIEWER_PANE" "$SERVER_ID"
run_merge 524
{ [ "$RC" -eq 0 ] && merged; } \
  && ok "5: restoring the true pane_id merges again -- the refusal was the registration, nothing else" \
  || bad "5: restoring the true pane_id merges again" "rc=$RC merged=$(merged && echo yes || echo no): $OUT"

echo "  -> $pass ok, $fail failed"
[ "$fail" -eq 0 ]
