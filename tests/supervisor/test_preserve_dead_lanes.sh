#!/bin/bash
# preserve-dead-lanes.sh must commit and push a DEAD lane's dirty worktree,
# and must leave a LIVE lane's dirty worktree completely untouched --
# agent-supervisor#651.
#
# Mutation-checked both directions, by construction (a sweep that preserves
# everything passes only the first, a sweep that preserves nothing passes
# only the second):
#   1. A dead claude-print-shaped lane (dirty tree, no process references
#      it anywhere) -- MUST be committed and pushed.
#   2. A live claude-print-shaped lane (dirty tree, a process holds the
#      worktree path in its OWN argv) -- MUST be left alone.
#   3. A live tmux-pane-shaped lane (dirty tree, a real tmux pane's cwd is
#      the worktree -- nothing in argv at all) -- MUST be left alone. This
#      is the shape #651's own brief says a naive pgrep-only sweep misses.
#   4. A clean worktree, regardless of liveness -- nothing to preserve, left
#      alone and not even counted as "dead".
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SWEEP="$HERE/../../scripts/supervisor/preserve-dead-lanes.sh"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "preserve-dead-lanes.sh"

D=$(mktemp -d)

# A private tmux server, never the operator's own attached one (invariant 4)
# -- `-f /dev/null` skips ~/.tmux.conf the same way test_worktree.sh's own
# rtmux() does. Established (and verified via assert_isolated_tmux, not just
# claimed) BEFORE the EXIT trap is armed, so the guard's own static scanner
# (tmux_verb_guard.py) sees isolation in effect above the trap's
# `kill-server`, not merely inline env-var scoping it cannot parse as proof.
RT="$D/tmux-rt"
mkdir -p "$RT"
# This test's own shell may itself be running inside an attached tmux
# session (a lane IS a tmux pane) -- unset TMUX here, once, so
# assert_isolated_tmux judges the throwaway server we are about to start,
# never the operator's own. Every tmux call this script makes from here on
# already scopes TMUX_TMPDIR/-u TMUX per-invocation (rtmux(), run_sweep());
# this only removes the ambient TMUX so the precheck itself is honest.
unset TMUX
TMUX_TMPDIR="$RT" assert_isolated_tmux || { echo "  FATAL: tmux isolation precheck failed" >&2; rm -rf "$D"; exit 2; }

trap 'kill $LIVE_PID 2>/dev/null; unset TMUX; TMUX_TMPDIR="$RT" tmux -f /dev/null kill-server 2>/dev/null; rm -rf "$D"' EXIT

rtmux() { env -u TMUX TMUX_TMPDIR="$RT" tmux -f /dev/null "$@"; }
if ! rtmux new-session -d -s anchor -c "$D" 2>/dev/null; then
  echo "  FATAL: could not start a throwaway tmux server -- tmux-pane liveness cannot be tested for real" >&2
  exit 2
fi

# Origin + a clone standing in for a lane's own checkout.
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo"
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo one >"$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -q -m initial
git -C "$REPO" push -q -u origin main

mk_worktree() {   # mk_worktree <branch> <dest>
  git -C "$REPO" worktree add -q -b "$1" "$2" main
}

# --- Fixture 1: DEAD claude-print shape -- dirty, no process anywhere ---
DEAD=$D/dead-argv
mk_worktree lane/fixture-dead "$DEAD"
echo dirty >"$DEAD/new-file.txt"

# --- Fixture 2: LIVE claude-print shape -- a process's own argv names it ---
LIVE_ARGV=$D/live-argv
mk_worktree lane/fixture-live-argv "$LIVE_ARGV"
echo dirty >"$LIVE_ARGV/new-file.txt"
python3 -c "import time,sys; time.sleep(60)" "$LIVE_ARGV" &
LIVE_PID=$!
# Give the OS a moment to make the new process visible to pgrep/lsof before
# the sweep ever queries it.
for _ in $(seq 1 20); do pgrep -f -- "$LIVE_ARGV" >/dev/null 2>&1 && break; sleep 0.1; done

# --- Fixture 3: LIVE tmux-pane shape -- cwd only, nothing in argv ---
LIVE_TMUX=$D/live-tmux
mk_worktree lane/fixture-live-tmux "$LIVE_TMUX"
echo dirty >"$LIVE_TMUX/new-file.txt"
rtmux new-window -t anchor -c "$LIVE_TMUX" -n live-tmux-lane

# --- Fixture 4: clean tree, no changes at all ---
CLEAN=$D/clean
mk_worktree lane/fixture-clean "$CLEAN"

# Fake ledger: `open-worktrees` reports all four as in-flight tasks.
mkdir -p "$D/bin"
cat >"$D/bin/fake-python3" <<PYEOF
#!/bin/bash
cat <<JSON
{"tasks":[
  {"id":"as901-fixture-dead","lane":"as901-fixture-dead","worktree_path":"$DEAD"},
  {"id":"as902-fixture-live-argv","lane":"as902-fixture-live-argv","worktree_path":"$LIVE_ARGV"},
  {"id":"as903-fixture-live-tmux","lane":"as903-fixture-live-tmux","worktree_path":"$LIVE_TMUX"},
  {"id":"as904-fixture-clean","lane":"as904-fixture-clean","worktree_path":"$CLEAN"}
]}
JSON
PYEOF
chmod +x "$D/bin/fake-python3"

run_sweep() {   # run_sweep [--dry-run]
  AGENT_PYTHON_BIN="$D/bin/fake-python3" \
  TMUX_TMPDIR="$RT" TMUX_BIN=tmux \
  env -u TMUX bash "$SWEEP" "$@"
}

# --- dry run first: report only, nothing committed or pushed ---
dry_out=$(run_sweep --dry-run 2>&1); dry_rc=$?
if [ "$dry_rc" -eq 0 ]; then ok "dry run exits 0"; else bad "dry run exits 0" "$dry_out"; fi
if grep -q "would preserve $DEAD" <<<"" ; then :; fi   # no-op, keeps shellcheck-style symmetry
if grep -q "would preserve as901-fixture-dead" <<<"$dry_out"; then
  ok "dry run reports it would preserve the dead lane"
else
  bad "dry run reports it would preserve the dead lane" "$dry_out"
fi
if grep -q "LIVE.*as902-fixture-live-argv\|skip as902-fixture-live-argv.*LIVE" <<<"$dry_out"; then
  ok "dry run reports the argv-live lane as skipped/LIVE"
else
  bad "dry run reports the argv-live lane as skipped/LIVE" "$dry_out"
fi
if grep -q "skip as903-fixture-live-tmux.*LIVE" <<<"$dry_out"; then
  ok "dry run reports the tmux-pane-live lane as skipped/LIVE"
else
  bad "dry run reports the tmux-pane-live lane as skipped/LIVE" "$dry_out"
fi
dead_status_after_dry=$(git -C "$DEAD" status --porcelain)
if [ -n "$dead_status_after_dry" ]; then
  ok "dry run left the dead lane's tree uncommitted (nothing changed)"
else
  bad "dry run left the dead lane's tree uncommitted (nothing changed)" "dry run must not mutate anything"
fi

# --- real run: dead lane preserved and pushed; live lanes untouched ---
real_out=$(run_sweep 2>&1); real_rc=$?
if [ "$real_rc" -eq 0 ]; then ok "real run exits 0"; else bad "real run exits 0" "$real_out"; fi

dead_status=$(git -C "$DEAD" status --porcelain)
if [ -z "$dead_status" ]; then
  ok "the dead lane's worktree is committed (git status is now clean)"
else
  bad "the dead lane's worktree is committed (git status is now clean)" "$dead_status"
fi
dead_subject=$(git -C "$DEAD" log -1 --format=%s 2>/dev/null)
if grep -q "^wip(" <<<"$dead_subject" && grep -q "agent-supervisor#901" <<<"$dead_subject"; then
  ok "the preservation commit subject is wip(...)... (agent-supervisor#901)"
else
  bad "the preservation commit subject is wip(...)... (agent-supervisor#901)" "$dead_subject"
fi
dead_body=$(git -C "$DEAD" log -1 --format=%b 2>/dev/null)
if grep -q "Preservation commit made by the estate loop, not by the lane" <<<"$dead_body" \
   && grep -q "Incomplete and unverified" <<<"$dead_body"; then
  ok "the preservation commit body marks it unverified, made by the estate loop"
else
  bad "the preservation commit body marks it unverified, made by the estate loop" "$dead_body"
fi
pushed=$(git -C "$D/origin.git" log -1 --format=%s lane/fixture-dead 2>/dev/null)
if [ "$pushed" = "$dead_subject" ]; then
  ok "the dead lane's commit reached origin (pushed)"
else
  bad "the dead lane's commit reached origin (pushed)" "origin has: '$pushed', local has: '$dead_subject'"
fi

live_argv_status=$(git -C "$LIVE_ARGV" status --porcelain)
if [ -n "$live_argv_status" ]; then
  ok "the argv-live lane's dirty tree is untouched"
else
  bad "the argv-live lane's dirty tree is untouched" "expected still-dirty, got clean -- it was wrongly committed"
fi
live_argv_pushed=$(git -C "$D/origin.git" show-ref --verify --quiet refs/heads/lane/fixture-live-argv; echo $?)
if [ "$live_argv_pushed" != "0" ]; then
  ok "the argv-live lane's branch was never pushed to origin"
else
  bad "the argv-live lane's branch was never pushed to origin" "origin has lane/fixture-live-argv -- it was wrongly pushed"
fi

live_tmux_status=$(git -C "$LIVE_TMUX" status --porcelain)
if [ -n "$live_tmux_status" ]; then
  ok "the tmux-pane-live lane's dirty tree is untouched"
else
  bad "the tmux-pane-live lane's dirty tree is untouched" "expected still-dirty, got clean -- it was wrongly committed"
fi

clean_log_count=$(git -C "$CLEAN" log --oneline | wc -l | tr -d ' ')
if [ "$clean_log_count" = "1" ]; then
  ok "the clean lane got no extra commit"
else
  bad "the clean lane got no extra commit" "expected 1 commit (the initial), got $clean_log_count"
fi

# --- never main, never a PR/merge -- no invocation of either verb anywhere
# in the sweep's own source, not just absent from this run's output.
if ! grep -qi "gh pr create\|gh pr merge\|git push.*main" "$SWEEP"; then
  ok "the sweep's own source never invokes opening a PR or merging"
else
  bad "the sweep's own source never invokes opening a PR or merging" "$(grep -ni 'gh pr create\|gh pr merge\|git push.*main' "$SWEEP")"
fi

echo "preserve-dead-lanes.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
