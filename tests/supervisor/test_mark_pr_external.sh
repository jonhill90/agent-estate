#!/bin/bash
# mark-pr-external.sh must GATE the "authored outside the lane system" claim
# on positive evidence, never accept it on the caller's word alone.
#
# WHY: agent-supervisor#308 item 3 / #321's own independent review, item 5.
# `cli.py mark-pr-external` writes the escape-hatch row with no caller
# verification at all -- any lane with shell access could call it against a
# PR it contributed to ITSELF, launder that PR as "no lane contributed", and
# then have any lane (including itself) "review" it -- the self-review
# shape agent-supervisor#190 exists to refuse. #308's own brief named the
# fix directly: "what gates it, and what stops it being used to launder a
# self-review... an escape hatch with no gate is the guard removed by
# another name."
#
# This suite proves mark-pr-external.sh IS that gate: it runs the same
# exhaustive resolution chain dispatch.sh's --reviews-pr guard runs
# (resolve-pr-contributors.sh, shared -- not reimplemented, see #321's
# review item 4 for the cost of a drifted copy) before ever writing the
# external-authorship row, and refuses outright when that chain finds a
# real contributor OR cannot complete at all.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MARK="$HERE/../../scripts/supervisor/mark-pr-external.sh"
DISPATCH="$HERE/../../scripts/supervisor/dispatch.sh"

export QUOTA_GATE="$HERE/stubs/quota-safe"
export DISPATCH_LIVE_PANE=1

pass=0; fail=0
ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "mark-pr-external.sh"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo" 2>/dev/null
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo one > "$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -q -m "initial"
git -C "$REPO" push -q -u origin main
git -C "$REPO" remote set-url origin "git@github.com:acme/agent-dotfiles.git"

: > "$D/issues"
: > "$D/prs"
echo "do the thing" > "$D/brief.md"

mark() {
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    AGENT_SUPERVISOR_STATE_DIR="$STATE" \
    bash "$MARK" "$@" 2>&1
}
ledger() { AGENT_SUPERVISOR_STATE_DIR="$STATE" python3 "$HERE/../../scripts/supervisor/cli.py" "$@"; }

# --- RED/refusal: the ledger already resolves a real contributor ----------
# The laundering shape: PR #500 genuinely closes issue #40, and task
# t:2/ad40-fix already contributed to it (an ordinary, honest dispatch --
# nothing suspicious about the setup itself). If mark-pr-external.sh let a
# caller mark this PR external anyway, that would ERASE the real contributor
# from the guard's view: a later `dispatch.sh --reviews-pr 500` would treat
# every free lane, including t:2, as a valid independent reviewer of its own
# work. This is exactly the exploit #321's review named -- proven refused.
STATE="$D/state-contributor"
printf '500|Fixes #40|fix/40-something\n' >> "$D/prs"
AGENT_SUPERVISOR_STATE_DIR="$STATE" python3 - "$HERE/../../scripts/supervisor" <<PY
import sys, os
sys.path.insert(0, sys.argv[1])
from core import Ledger
ledger = Ledger(os.environ["AGENT_SUPERVISOR_STATE_DIR"])
ledger.register_lane(lane="t:2", pane_id="%2", nonce="n2", harness="claude", repo="$REPO",
                      server_id="seed:1700000000", session_id="s0", command="claude.exe")
ledger.reconstruct_task(task_id="ad40-fix", source_kind="issue",
                         source_url="https://github.com/acme/agent-dotfiles/issues/40",
                         source_ref="40", summary="genuine fix for #40", source_state="OPEN",
                         status="created", evidence=["seed"], status_marker=None)
ledger.assign(task_id="ad40-fix", lane="t:2", pane_nonce="n2", summary="genuine fix for #40")
PY

out=$(mark acme/agent-dotfiles 500 "trying to launder my own PR" "$REPO"); rc=$?
want_exit "RED: refuses to mark external -- ledger already resolves a real contributor" "$rc" 1 "$out"
want_contains "...names the contributor it found" "t:2 (task ad40-fix)" "$out"
want_contains "...names WHY, not just that it refused" "self-review-laundering" "$out"
ext=$(ledger pr-external --repo acme/agent-dotfiles --pr 500)
want_contains "...and the row was never written" '"known":false' "$ext"

# --- RED/refusal: the PR cannot be read at all -----------------------------
# Unreadable is UNKNOWN, never treated as "checked, found nobody" --
# agent-supervisor#190's fail-closed posture applies here exactly as it does
# to dispatch.sh's own guard.
STATE="$D/state-unreadable"
out=$(mark acme/agent-dotfiles 999 "no such PR in the fixture" "$REPO"); rc=$?
want_exit "RED: an unreadable PR refuses rather than proceeding" "$rc" 1 "$out"
want_contains "...says checked-and-empty is not the same as could-not-check" \
  "not the same as checked-and-empty" "$out"
ext=$(ledger pr-external --repo acme/agent-dotfiles --pr 999)
want_contains "...and nothing was recorded" '"known":false' "$ext"

# --- GREEN: a genuinely external PR is marked, then becomes reviewable ----
# PR #600 closes no issue any lane ever touched, and no worktree in this repo
# ever sat on its branch -- the #300/#301/#316 shape from #308's own brief.
STATE="$D/state-external"
printf '600|Some fix, no issue reference at all|feat/nobody-dispatched-this\n' >> "$D/prs"
out=$(mark acme/agent-dotfiles 600 "authored directly by a human, no lane ever dispatched against it" "$REPO"); rc=$?
want_exit "GREEN: a genuinely external PR is marked, once the chain runs clean" "$rc" 0 "$out"
want_contains "...says why: every resolution path came back empty" \
  "no lane contributor found by any resolution path" "$out"
ext=$(ledger pr-external --repo acme/agent-dotfiles --pr 600)
want_contains "...and the row exists now" '"known":true' "$ext"

# End to end: dispatch.sh --reviews-pr now dispatches this PR's review to
# any free lane -- the whole point of #308 item 3, reached through the gate
# instead of an unchecked cli.py call.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '41|| review PR #600, marked external through the gate\n' >> "$D/issues"
out=$(PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
      LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
      TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 DISPATCH_RESPAWN_SETTLE=0 \
      DISPATCH_LAUNCH_SETTLE=0 DISPATCH_PANE_COLS=60 DISPATCH_MESSAGE_BUDGET=430 \
      AGENT_SUPERVISOR_STATE_DIR="$STATE" STUB_PANE_PATH="$REPO" \
      DISPATCH_CONFIRM_TRIES=2 DISPATCH_SESSION_TIMEOUT=0 WORKTREE_ROOT="$D/roots" \
      bash "$DISPATCH" 41 rev-600 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 600 2>&1); rc=$?
want_exit "end to end: the review dispatches once the gate marked it external" "$rc" 0 "$out"

echo "mark-pr-external.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
