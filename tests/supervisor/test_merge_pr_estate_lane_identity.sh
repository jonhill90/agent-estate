#!/bin/bash
# agent-supervisor#538, driven through the REAL `merge-pr.sh` against a REAL
# (isolated) tmux server -- the same discipline test_merge_pr_lane_identity.sh
# uses for #520, applied to the OTHER unresolved-authorship shape: a PR that
# closes no issue at all (agent-supervisor#531's own shape) because the work
# was dispatched by the estate loop, outside `dispatch.sh`, with no GitHub
# issue behind it.
#
# Before this fix, `author_lane_for` (verdict-independence.sh) had no path
# that could ever resolve such a PR -- every one of its five resolution paths
# keys off an issue number, a PR-scoped `source_tasks` row `dispatch.sh --pr`
# would have written, or a branch-name convention, and none of those exist
# for a brief-file dispatch. `merge-pr.sh` refused with "independence unknown
# -- PR author lane unresolved" regardless of who reviewed it -- a genuinely
# independent review was indistinguishable from a self-review to this gate.
#
# The four cases:
#   1. PR closes no issue, author self-registered via
#      register-pr-dispatch-self.sh, reviewed by a DIFFERENT lane -> MERGES
#   2. same shape, but the "reviewer" is the same lane as the author -> REFUSES
#   3. same shape, nobody ever ran register-pr-dispatch-self.sh -> REFUSES
#      (the fail-closed default is unchanged for a PR nothing recorded)
#   4. case 1's exact fixtures, re-run after case 2/3 -> MERGES again, proving
#      the earlier refusals were the missing/matching registration and
#      nothing else the mutation happened to disturb.
#
# INVARIANT 4: creates a session, so `TMUX_TMPDIR` is set and
# `assert_isolated_tmux` gates it. Nothing outside this socket is addressed.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
MERGE_PR="$SUP/merge-pr.sh"
SELF="$SUP/register-pr-dispatch-self.sh"
LEDGER_CLI="$SUP/cli.py"
source "$SUP/tmux-isolation.sh"
REPO="acme/repo"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "merge-pr.sh x estate-lane (issue-less) authorship (#538, live tmux)"

missing() {
  if [ -n "${CI:-}" ]; then
    echo "  FAIL $1 -- required on CI, where validate.yml installs it; a silent skip here would be a suite that tests nothing"
    exit 1
  fi
  echo "  SKIP $1"
  exit 0
}
command -v tmux >/dev/null 2>&1 || missing "no tmux on PATH"
command -v jq   >/dev/null 2>&1 || missing "no jq"
command -v node >/dev/null 2>&1 || missing "no node on PATH"

S="merge-estate-test-$$"
RT="$(mktemp -d "${TMPDIR:-/tmp}/merge-estate-tmux.XXXXXX")"
D="$(mktemp -d "${TMPDIR:-/tmp}/merge-estate.XXXXXX")"
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

# Same fake `gh` shape as test_merge_pr_lane_identity.sh.
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
export GH_FIX="$FIX" MARKER SUPERVISOR_STATE="$STATE" AGENT_SUPERVISOR_STATE_DIR="$STATE"

green() { cat > "$FIX/checkruns_$1.json" <<S
[{"name": "test", "head_sha": "$1", "status": "completed", "conclusion": "success"}]
S
}
head_is() { printf '{"headRefOid": "%s"}\n' "$2" > "$FIX/head_$1.json"; }
# #531's own shape: closingIssuesReferences is EMPTY and there are no commits
# to grep a "fixes #N" out of either -- a PR that closes nothing.
closes_nothing() { printf '{"headRefName": "fix/estate-thing", "closingIssuesReferences": [], "commits": []}\n' > "$FIX/author_$1.json"; }
verdict_comment() {  # verdict_comment <pr> <lane> <reviewed-sha>
  cat > "$FIX/reviews_$1.json" <<S
{"reviews": [], "comments": [{"author": {"login": "jonhill90"}, "body": "**Verdict: APPROVE**\n\nReview-Lane: $2\nReviewed-SHA: $3", "createdAt": "2026-08-23T00:00:00Z"}]}
S
}

# --- a live server with two real panes --------------------------------------
PANE_CMD="node -e 'setTimeout(function(){}, 600000)'"
tmux new-session -d -s "$S" -c "$WORK" "$PANE_CMD" || { echo "  FAIL could not create the isolated session"; exit 1; }
tmux new-window -t "$S" -c "$WORK" "$PANE_CMD"
for _ in $(seq 1 40); do
  [ "$(tmux list-panes -a -F '#{pane_current_command}' 2>/dev/null | sort -u | tr '\n' ' ')" = "node " ] && break
  sleep 0.25
done
# Neither window here is the supervisor's own -- both are real lanes going
# through register-lane-self.sh (via register-pr-dispatch-self.sh), which
# refuses to register `LANES_SUPERVISOR_WINDOW`'s index. This must be an
# index NEITHER real window uses -- and which one that is depends on the
# server's own `base-index`, which this test cannot assume: a hardcoded
# `SUP_IDX=0` (this test's own prior shape) only avoids the two real
# windows on a server configured `base-index 1` (a common but NOT universal
# `.tmux.conf` setting -- true on at least one dev machine this was
# verified against). CI's runner installs tmux fresh with no `.tmux.conf`
# at all, so it runs on tmux's own actual default, `base-index 0` -- there,
# the first `tmux new-session` window IS index 0, colliding with the
# hardcoded value and refusing the author's own registration 100% of the
# time (measured: shell-suites shard 3 failed on exactly this, both CI
# runs, "pane %0 is window index 0, the supervisor's own window"). Fixed by
# reading the two real indices FIRST, then picking a sentinel far outside
# any index a 2-window session can ever produce -- the same "99" sentinel
# already proven safe on CI by this same PR's own
# test_lanes_execution_mode.sh, rather than reasoning about base-index at
# all.
W1=$(tmux list-windows -t "$S" -F '#{window_index}' | head -n1)
W2=$(tmux list-windows -t "$S" -F '#{window_index}' | tail -n1)
SUP_IDX=99
export LANES_SUPERVISOR_WINDOW="$SUP_IDX"
AUTHOR_LANE="$S:$W1"; REVIEWER_LANE="$S:$W2"
AUTHOR_PANE=$(tmux display-message -p -t "$S:$W1" '#{pane_id}')
REVIEWER_PANE=$(tmux display-message -p -t "$S:$W2" '#{pane_id}')

# The author lane registers ITSELF as PR #531's author -- exactly the fix,
# exactly how a real estate lane would run it, no --issue anywhere.
OUT=$(TMUX_PANE="$AUTHOR_PANE" LANES_SUPERVISOR_WINDOW="$SUP_IDX" \
      bash "$SELF" --pr 531 --repo "$REPO" --harness codex 2>&1); RC=$?
[ "$RC" -eq 0 ] || { echo "  FAIL setup: register-pr-dispatch-self.sh for PR 531"; echo "$OUT"; exit 1; }
# The reviewer lane registers itself too (register-lane-self.sh alone would
# do; using the same wrapper against a DIFFERENT PR number keeps this test
# from depending on a second script existing solely to call the first half).
OUT=$(TMUX_PANE="$REVIEWER_PANE" LANES_SUPERVISOR_WINDOW="$SUP_IDX" \
      bash "$SUP/register-lane-self.sh" --harness codex 2>&1); RC=$?
[ "$RC" -eq 0 ] || { echo "  FAIL setup: register-lane-self.sh for the reviewer"; echo "$OUT"; exit 1; }

run_merge() { rm -f "$MARKER"; OUT=$("$MERGE_PR" "$REPO" "$1" 2>&1); RC=$?; }
merged() { [ -f "$MARKER" ]; }

# ============================================================================
# 1. PR closes no issue, self-registered author, DIFFERENT reviewer -> MERGES
# ============================================================================
head_is 531 sha-531; green sha-531; closes_nothing 531
verdict_comment 531 "$REVIEWER_LANE" sha-531
run_merge 531
{ [ "$RC" -eq 0 ] && merged; } \
  && ok "1/3 #531's own shape (no closing issue), independent review: merges" \
  || bad "1/3 issue-less PR with independent review merges" "rc=$RC merged=$(merged && echo yes || echo no): $OUT"
grep -q "independence confirmed" <<<"$OUT" \
  && ok "1/3 ...and says independence, not just CI green" || bad "1/3 names independence" "$OUT"

# ============================================================================
# 2. same shape, reviewer IS the author lane -> REFUSES
# ============================================================================
head_is 532 sha-532; green sha-532; closes_nothing 532
# Re-register the author lane against PR #532 too (a second PR from the same
# lane) -- this proves the gate is refusing the SAME-LANE relationship, not
# merely "no registration for 532".
OUT=$(TMUX_PANE="$AUTHOR_PANE" LANES_SUPERVISOR_WINDOW="$SUP_IDX" \
      bash "$SELF" --pr 532 --repo "$REPO" --harness codex 2>&1); RC=$?
[ "$RC" -eq 0 ] || { echo "  FAIL setup: register-pr-dispatch-self.sh for PR 532"; echo "$OUT"; exit 1; }
verdict_comment 532 "$AUTHOR_LANE" sha-532
run_merge 532
{ [ "$RC" -eq 1 ] && ! merged; } \
  && ok "2/3 issue-less PR, self-review (author lane == reviewer lane): refuses" \
  || bad "2/3 self-review on an issue-less PR refuses" "rc=$RC merged=$(merged && echo yes || echo no): $OUT"
grep -q "reviewed its own PR" <<<"$OUT" \
  && ok "2/3 ...and names it as a self-review, not a generic unresolved refusal" \
  || bad "2/3 names self-review" "$OUT"

# ============================================================================
# 3. same shape, nobody ever self-registered -> REFUSES (unchanged default)
# ============================================================================
head_is 533 sha-533; green sha-533; closes_nothing 533
verdict_comment 533 "$REVIEWER_LANE" sha-533
run_merge 533
{ [ "$RC" -eq 1 ] && ! merged; } \
  && ok "3/3 issue-less PR with NO self-registration at all: still refuses (fail-closed unchanged)" \
  || bad "3/3 unregistered issue-less PR refuses" "rc=$RC merged=$(merged && echo yes || echo no): $OUT"
grep -q "independence unknown -- PR author lane unresolved" <<<"$OUT" \
  && ok "3/3 ...with the same unresolved-authorship message as before this fix" \
  || bad "3/3 names unresolved authorship" "$OUT"

# --- the mutation is load-bearing in BOTH directions ------------------------
# Re-run case 1's exact fixtures. If this did not merge again, cases 2/3
# would prove nothing -- the refusal could have come from anything the
# intervening calls disturbed (a corrupted lane row, a wrong PATH, etc).
run_merge 531
{ [ "$RC" -eq 0 ] && merged; } \
  && ok "case 1 merges again after cases 2/3: the refusals were the registration, nothing else" \
  || bad "case 1 remerges after cases 2/3" "rc=$RC merged=$(merged && echo yes || echo no): $OUT"

echo "  -> $pass ok, $fail failed"
[ "$fail" -eq 0 ]
