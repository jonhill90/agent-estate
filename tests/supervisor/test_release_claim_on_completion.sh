#!/bin/bash
# agent-supervisor#359: nothing released the GitHub assignee `claim.sh take`
# wrote once a lane finished, failed, or died -- the claim outlived the lane,
# and `dispatch.sh` then correctly refused to re-dispatch the issue forever
# ("could not claim"). 16 of 37 claimed issues were measured stale in one
# audit, including #310 and #313.
#
# `cli.py complete`/`record-completion` (the SAME `Ledger.complete` call,
# reached by two different callers -- a caller-verified worker self-report,
# and `lane-done.sh`'s supervisor-side signal) and `cli.py cancel-open-task`
# (an operator recovering a lane by hand, #359's own "Done this tick"
# workaround) are the terminal paths every transport in this estate actually
# reaches -- every adapter's own brief tells the worker to call
# `hill90-supervisor complete` at the end (adapter.py's shared prompt
# template), and dispatch-claude-print.sh/dispatch-pi-rpc.sh's own delivery
# (agent-supervisor#278) detaches before the work even starts, so completion
# can only ever be reported this way, never observed at the dispatch script's
# own exit.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI="$HERE/../../scripts/supervisor/cli.py"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }

echo "cli.py releases the GitHub claim on every terminal completion path (#359)"

D=$(mktemp -d); mkdir -p "$D/bin"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cat > "$D/issues" <<'FIX'
211|jonhill90|author lane 1|OPEN
212|jonhill90|author lane 2|OPEN
213|jonhill90|multi-issue dispatch|OPEN
214|jonhill90|multi-issue dispatch|OPEN
215|jonhill90|a review PR's own issue|OPEN
216|jonhill90|an operator-cancelled lane|OPEN
FIX
: > "$D/prs"

env_run() { PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
            python3 "$CLI" --state-dir "$1" "${@:2}" 2>&1; }
holder() { PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
           bash "$HERE/../../scripts/supervisor/claim.sh" check "$1" acme/repo; }

# --- 1. `cli.py complete` (the caller-verified worker self-report) releases
#        the issue it closes ----------------------------------------------
S1="$D/state1"; mkdir -p "$S1"
env_run "$S1" record-dispatch --lane t:1 --task as211-author --summary "#211 author" \
  --pane-id %1 --pane-path "$D/repo" --command claude --server-id srv --session-id sess \
  --issue 211 --github acme/repo --harness claude >/dev/null
env_run "$S1" record-completion --task as211-author --note done >/dev/null
out=$(holder 211); rc=$?
want_exit "a completed task's issue claim is released" "$rc" 0 "$out"

# --- 2. an already-released claim (a second, idempotent completion record)
#        is not treated as an error, and does not touch a DIFFERENT lane's
#        legitimate re-claim of the same issue number -------------------
S2="$D/state2"; mkdir -p "$S2"
env_run "$S2" record-dispatch --lane t:1 --task as212-author --summary "#212 author" \
  --pane-id %1 --pane-path "$D/repo" --command claude --server-id srv --session-id sess \
  --issue 212 --github acme/repo --harness claude >/dev/null
env_run "$S2" record-completion --task as212-author --note done >/dev/null
PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
  bash "$HERE/../../scripts/supervisor/claim.sh" take 212 acme/repo lane-later >/dev/null
env_run "$S2" record-completion --task as212-author --note done >/dev/null   # idempotent replay
out=$(holder 212); rc=$?
want_exit "an idempotent replay of an already-complete task does not release a later lane's re-claim of the same issue" "$rc" 1 "$out"

# --- 3. every issue a multi-issue dispatch closes is released, not only
#        the primary one `source_ref` names (agent-dotfiles#112's evidence
#        line is what carries the rest) ------------------------------------
S3="$D/state3"; mkdir -p "$S3"
env_run "$S3" record-dispatch --lane t:1 --task ad213-two-issues --summary "two issues" \
  --pane-id %1 --pane-path "$D/repo" --command claude --server-id srv --session-id sess \
  --issue 213 --issue 214 --github acme/repo --harness claude >/dev/null
env_run "$S3" record-completion --task ad213-two-issues --note done >/dev/null
out=$(holder 213); rc=$?
want_exit "the primary issue of a multi-issue dispatch is released" "$rc" 0 "$out"
out=$(holder 214); rc=$?
want_exit "the secondary issue of a multi-issue dispatch is released too" "$rc" 0 "$out"

# --- 4. a PR-scoped task (a review or a fix pass) never held the issue's
#        claim, and completing it must not touch it -- dispatch.sh's own
#        `--pr`/`--reviews-pr` comment: that issue stays claimed by the
#        ORIGINAL work on purpose ------------------------------------------
S4="$D/state4"; mkdir -p "$S4"
# 215 is already claimed by the ORIGINAL work per the fixture above (jonhill90
# assigned it before this review task was ever dispatched) -- this task never
# held that claim itself.
env_run "$S4" record-dispatch --lane t:1 --task as215-review99 --summary "review of PR 99" \
  --pane-id %1 --pane-path "$D/repo" --command claude --server-id srv --session-id sess \
  --issue 215 --pr 99 --github acme/repo --harness claude >/dev/null
env_run "$S4" record-completion --task as215-review99 --note done >/dev/null
out=$(holder 215); rc=$?
want_exit "completing a PR-scoped (review) task leaves the original work's issue claim alone" "$rc" 1 "$out"

# --- 5. cancel-open-task -- the operator recovery path #359 itself used as
#        a same-day workaround ("released by hand") -- also releases -----
S5="$D/state5"; mkdir -p "$S5"
env_run "$S5" record-dispatch --lane t:1 --task as216-stuck --summary "#216 stuck lane" \
  --pane-id %1 --pane-path "$D/repo" --command claude --server-id srv --session-id sess \
  --issue 216 --github acme/repo --harness claude >/dev/null
env_run "$S5" cancel-open-task --lane t:1 >/dev/null
out=$(holder 216); rc=$?
want_exit "cancel-open-task releases the issue claim of the lane it frees" "$rc" 0 "$out"

echo
echo "cli.py claim release on completion: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
