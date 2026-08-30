#!/bin/bash
# mark-pr-director-authored.sh must GATE the "director-authored" claim on
# positive evidence AND on the caller's own live pane genuinely being the
# Director's window -- never accept it on an argument or an assertion alone.
#
# WHY (agent-estate#741, #740/#742, #745/#746, #748): `get_contributor_tasks_
# for_pr` requires a `tasks` row JOINed to a `source_tasks` row -- a
# director-authored PR has neither, by construction (no dispatch.sh/
# assign_task call ever ran). Before this fix there was no honest, gated
# ledger path to say "this PR was authored directly by the Director,
# verified, no lane contributed" -- #748 reproduced the exact failure:
# `resolve_pr_contributors` returns AUTHOR_LANES=[], CONTRIBUTORS_RESOLVED=
# (empty), and dispatch.sh --reviews-pr refuses forever.
#
# This suite proves mark-pr-director-authored.sh is the gate: same
# exhaustive resolution chain mark-pr-external.sh runs (shared, not
# reimplemented), PLUS the opposite identity check from mark-pr-external.sh
# -- requires $TMUX_PANE set AND that pane's own live window index to equal
# LANES_SUPERVISOR_WINDOW, refusing any other caller by name (wrong window
# index), never a generic error.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MARK="$HERE/../../scripts/supervisor/mark-pr-director-authored.sh"
DISPATCH="$HERE/../../scripts/supervisor/dispatch.sh"

export QUOTA_GATE="$HERE/stubs/quota-safe"
export SUPERVISOR_MAX_LOAD_PER_CORE=0
export SUPERVISOR_MIN_FREE_MEM_GB=0
export SUPERVISOR_MAX_AGENT_SESSIONS=0
export DISPATCH_LIVE_PANE=1

pass=0; fail=0
ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "mark-pr-director-authored.sh"

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

# `tmux display-message -t "$PANE" '#{pane_id}|#{window_index}'` is what
# mark-pr-director-authored.sh actually shells out to -- the fixture stub
# below answers it directly from PANE_WINDOW_INDEX, no real tmux server
# needed (same discipline as the gh-claim/tmux-dispatch stubs above: fast,
# deterministic, no live tmux dependency for a check that is pure text
# parsing over one command's output).
cat > "$D/bin/tmux-identity" <<'EOF'
#!/bin/bash
if [ "$1" = "display-message" ]; then
  # args: display-message -p -t <pane> '#{pane_id}|#{window_index}'
  echo "${PANE_ID:-%0}|${PANE_WINDOW_INDEX:-1}"
  exit 0
fi
exec "$REAL_TMUX" "$@"
EOF
chmod +x "$D/bin/tmux-identity"

ledger() { AGENT_SUPERVISOR_STATE_DIR="$STATE" python3 "$HERE/../../scripts/supervisor/cli.py" "$@"; }

# --- RED: no $TMUX_PANE at all ---------------------------------------------
STATE="$D/state-no-pane"
out=$(PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
      AGENT_SUPERVISOR_STATE_DIR="$STATE" \
      env -u TMUX_PANE bash "$MARK" acme/agent-dotfiles 900 "no pane at all" "$REPO" 2>&1); rc=$?
want_exit "RED: refuses outright when \$TMUX_PANE is not set" "$rc" 1 "$out"
want_contains "...names the actual reason" "\$TMUX_PANE is not set" "$out"
dir=$(ledger pr-director --repo acme/agent-dotfiles --pr 900)
want_contains "...and nothing was recorded" '"known":false' "$dir"

# --- RED: $TMUX_PANE set, but wrong window index (an ordinary worker lane) -
# The #741 mutation check's own "wrong caller" direction: a worker lane
# (window index 3, LANES_SUPERVISOR_WINDOW default 1) is refused, and the
# refusal names the REAL reason (wrong window index), not a generic error.
STATE="$D/state-wrong-window"
printf '901|Some fix, no issue reference at all|feat/nobody-dispatched-this\n' >> "$D/prs"
out=$(PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
      AGENT_SUPERVISOR_STATE_DIR="$STATE" AGENT_TMUX_BIN="$D/bin/tmux-identity" \
      REAL_TMUX="$D/bin/tmux" TMUX_PANE="%19" PANE_ID="%19" PANE_WINDOW_INDEX="3" \
      bash "$MARK" acme/agent-dotfiles 901 "a worker lane trying to claim director authorship" "$REPO" 2>&1); rc=$?
want_exit "RED: refuses when the pane's own window index is not the Director's (worker lane, window 3)" "$rc" 1 "$out"
want_contains "...names the real reason: wrong window index, not a generic error" \
  "window index 3, not the Director's own window" "$out"
dir=$(ledger pr-director --repo acme/agent-dotfiles --pr 901)
want_contains "...and nothing was recorded" '"known":false' "$dir"

# --- GREEN: $TMUX_PANE set AND window index genuinely matches the Director -
STATE="$D/state-director"
printf '902|Some fix, no issue reference at all|feat/also-nobody-dispatched-this\n' >> "$D/prs"
out=$(PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
      AGENT_SUPERVISOR_STATE_DIR="$STATE" AGENT_TMUX_BIN="$D/bin/tmux-identity" \
      REAL_TMUX="$D/bin/tmux" TMUX_PANE="%2" PANE_ID="%2" PANE_WINDOW_INDEX="1" \
      bash "$MARK" acme/agent-dotfiles 902 "authored directly by the Director, no lane ever dispatched" "$REPO" 2>&1); rc=$?
want_exit "GREEN: the Director's own window (index 1) succeeds once the chain runs clean" "$rc" 0 "$out"
want_contains "...says why: every resolution path came back empty" \
  "no lane contributor found by any resolution path" "$out"
dir=$(ledger pr-director --repo acme/agent-dotfiles --pr 902)
want_contains "...and the row exists now" '"known":true' "$dir"

# --- RED: the resolution chain still finds a real contributor -------------
# The identity check passing must NOT bypass the resolution chain -- the
# Director's own window is not a blank cheque; a PR the ledger already
# resolves a real contributor for is still refused.
STATE="$D/state-director-contributor"
printf '903|Fixes #40|fix/40-something\n' >> "$D/prs"
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
out=$(PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
      AGENT_SUPERVISOR_STATE_DIR="$STATE" AGENT_TMUX_BIN="$D/bin/tmux-identity" \
      REAL_TMUX="$D/bin/tmux" TMUX_PANE="%2" PANE_ID="%2" PANE_WINDOW_INDEX="1" \
      bash "$MARK" acme/agent-dotfiles 903 "trying to claim director authorship over a real contributor" "$REPO" 2>&1); rc=$?
want_exit "RED: the Director's own window still refuses when a real contributor is resolved" "$rc" 1 "$out"
want_contains "...names the contributor it found" "t:2 (task ad40-fix)" "$out"
dir=$(ledger pr-director --repo acme/agent-dotfiles --pr 903)
want_contains "...and the row was never written" '"known":false' "$dir"

# --- End to end: dispatch.sh --reviews-pr now dispatches ------------------
# #741's own acceptance criterion: once a PR is marked director-authored,
# resolve_pr_contributors resolves CONTRIBUTORS_RESOLVED=1 with an empty
# AUTHOR_LANES, and dispatch.sh --reviews-pr actually dispatches -- the #748
# shape, closed.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '41|| review PR #902, marked director-authored through the gate\n' >> "$D/issues"
out=$(PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
      LANES_FIXTURE="$D/lanes" LANES_SESSION=t TMUX_LOG="$D/tmux.log" \
      TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 DISPATCH_RESPAWN_SETTLE=0 \
      DISPATCH_LAUNCH_SETTLE=0 DISPATCH_PANE_COLS=60 DISPATCH_MESSAGE_BUDGET=430 \
      AGENT_SUPERVISOR_STATE_DIR="$D/state-director" STUB_PANE_PATH="$REPO" \
      DISPATCH_CONFIRM_TRIES=2 DISPATCH_SESSION_TIMEOUT=0 WORKTREE_ROOT="$D/roots" \
      bash "$DISPATCH" 41 rev-902 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 902 2>&1); rc=$?
want_exit "end to end: the review dispatches once the gate marked the PR director-authored" "$rc" 0 "$out"
want_contains "...uses the director-authored path's own distinct wording, not the external one" \
  "recorded as director-authored" "$out"

echo "mark-pr-director-authored.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
