#!/bin/bash
# agent-supervisor#199 / PR#205 review: the prior verification for "the
# guarded suite never lands a session on the shared socket" was a single
# post-run snapshot (and, before that, a hand-run before/after pair) --
# exactly the shape #177 and #180 both closed on while the leak kept
# recurring. #199's own acceptance is an OBSERVED ABSENCE: continuous
# sampling across a real run, not a point-in-time check. This file commits
# that sampling as a rerunnable test instead of a one-time claim in a PR
# body.
#
# It reproduces the THREE conditions #199 names together, against the
# CURRENT, guarded copy of test_bootstrap_session.sh (the file whose
# `bootstrap-test-$$` sessions are the ones #199, #177 and #180 all
# measured):
#   1. inside a tmux pane, with $TMUX genuinely set for the child process
#      (a lane's own condition) -- not simulated by unsetting/resetting
#      env vars by hand;
#   2. run from a real, on-disk `git worktree add` checkout (a lane never
#      runs its tests from the shared clone) rather than in place;
#   3. interrupted mid-run with SIGKILL, which no EXIT/INT/TERM trap can
#      catch -- the condition that turned a self-cleaning transient leak
#      (case in the PR body: run to completion, session created and gone
#      before anyone could look) into a persistent one against the
#      pre-guard checkout.
#
# What makes this SAFE to run as an ordinary test, per CLAUDE.md invariant
# 4 ("never address the default tmux socket in a test"): "the default
# socket" is never the operator's real one. A scratch TMUX_TMPDIR
# ($SHARED_RT below) stands in for it -- tmux resolves "default" purely
# relative to TMUX_TMPDIR, so redirecting that variable redirects what
# "default" even means, the same technique test_bootstrap_session.sh
# itself already relies on. We sample THIS stand-in continuously, the same
# way you would want to sample the real one, at zero risk to it.
#
# The property under test is not "assert_isolated_tmux refuses when $TMUX
# is set" (true, but a single instant check, and already exercised
# elsewhere) -- it's that regardless of the CALLER's TMUX_TMPDIR (here,
# the shared stand-in, inherited by the child the same way a lane's own
# environment would be inherited), test_bootstrap_session.sh always
# re-points itself at its OWN fresh mktemp'd directory before touching
# tmux. A session it creates or leaks under SIGKILL therefore lands in
# that private directory, never in the caller's -- which is what this test
# samples for, continuously, for the whole run.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 -- $2"; fail=$((fail+1)); }

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"; exit 0
fi

# Everything below that would, on a real lane, resolve against the
# operator's shared default socket is redirected here instead.
# A short, fixed /tmp prefix, not $TMPDIR -- $TMPDIR's own path (e.g. macOS's
# /private/var/folders/.../T/) plus a descriptive name reliably overflows
# AF_UNIX's ~104-byte sun_path limit once tmux appends "/tmux-$UID/default"
# to it, and this variable becomes the socket directory, not just a temp
# file. tmux's own error for that ("File name too long") looks like a setup
# bug, not a path-length ceiling, so keep this short and explicit.
SHARED_RT="$(mktemp -d /tmp/oa-rt.XXXXXX)"
unset TMUX
export TMUX_TMPDIR="$SHARED_RT"
assert_isolated_tmux || { echo "  FAIL setup -- assert_isolated_tmux refused its own isolated dir"; exit 1; }

DRIVER="observed-absence-driver-$$"
WT_PARENT="$(mktemp -d "${TMPDIR:-/tmp}/oa-wt.XXXXXX")"
WT_DIR="$WT_PARENT/wt"
RUN_LOG="$(mktemp "${TMPDIR:-/tmp}/oa-run.XXXXXX")"
SAMPLE_LOG="$(mktemp "${TMPDIR:-/tmp}/oa-samples.XXXXXX")"
SAMPLER_PID=""

cleanup() {
  [ -n "$SAMPLER_PID" ] && kill "$SAMPLER_PID" 2>/dev/null
  unset TMUX; export TMUX_TMPDIR="$SHARED_RT"
  assert_isolated_tmux && tmux kill-server 2>/dev/null
  git -C "$REPO_ROOT" worktree remove --force "$WT_DIR" >/dev/null 2>&1
  rm -rf "$SHARED_RT" "$WT_PARENT" "$RUN_LOG" "$SAMPLE_LOG"
}
trap cleanup EXIT INT TERM

# Condition 2: a real on-disk worktree, pinned to HEAD -- the guarded
# code, checked out the way a lane actually gets its copy.
if ! git -C "$REPO_ROOT" worktree add --quiet --detach "$WT_DIR" HEAD >/dev/null 2>&1; then
  bad "setup: git worktree add" "could not create $WT_DIR"
  echo "  $pass passed, $fail failed"; exit 1
fi
ok "setup: worktree checked out on disk, pinned to HEAD ($(git -C "$REPO_ROOT" rev-parse HEAD))"

if ! grep -q assert_isolated_tmux "$WT_DIR/tests/supervisor/test_bootstrap_session.sh" 2>/dev/null; then
  bad "setup: guard present at HEAD" "test_bootstrap_session.sh in the worktree has no assert_isolated_tmux call -- nothing to prove here"
  echo "  $pass passed, $fail failed"; exit 1
fi
ok "setup: HEAD's test_bootstrap_session.sh carries the guard (confirms this run exercises the guarded code, not the pre-guard shape)"

# Condition 1 (part one): a real tmux session on the shared-socket
# stand-in -- everything spawned inside it inherits a genuine $TMUX and
# this TMUX_TMPDIR, a lane's own attached condition.
tmux new-session -d -s "$DRIVER" -n driver >/dev/null 2>&1
if ! tmux has-session -t "$DRIVER" 2>/dev/null; then
  bad "setup: driver session" "failed to create on the shared-socket stand-in"
  echo "  $pass passed, $fail failed"; exit 1
fi
ok "setup: driver session up on the shared-socket stand-in ($SHARED_RT)"

# The continuous sampler -- the entire point. #199 rejects a before/after
# snapshot; this samples >=0.5s for the WHOLE run, background, independent
# of whatever the runner window does.
(
  while true; do
    ts="$(date '+%Y-%m-%dT%H:%M:%S.%3N')"
    names="$(tmux list-sessions -F '#{session_name}' 2>/dev/null | tr '\n' ',' )"
    echo "$ts $names" >>"$SAMPLE_LOG"
    sleep 0.5
  done
) &
SAMPLER_PID=$!

sleep 1

# Condition 1 (part two): run the guarded suite as a new WINDOW in that
# same attached session -- its process genuinely inherits $TMUX and this
# TMUX_TMPDIR, exactly like a lane's own shell.
tmux new-window -t "$DRIVER" -n runner \
  "bash '$WT_DIR/tests/supervisor/test_bootstrap_session.sh' >'$RUN_LOG' 2>&1; echo EXIT=\$? >>'$RUN_LOG'" \
  >/dev/null 2>&1

# Let it get partway into the suite (multiple cases, multiple real tmux
# calls against ITS OWN private dir) before...
sleep 1.5
runner_pid="$(tmux list-panes -t "$DRIVER:runner" -F '#{pane_pid}' 2>/dev/null | head -1)"

# Condition 3: SIGKILL, mid-run, which no trap -- including
# test_bootstrap_session.sh's own `trap cleanup_all EXIT INT TERM` -- can
# catch. Kill the whole process group so a child mid-tmux-call dies too,
# not just the top shell.
if [ -n "$runner_pid" ]; then
  kill -9 -- "-$runner_pid" 2>/dev/null
  kill -9 "$runner_pid" 2>/dev/null
  ok "mid-run: sent SIGKILL to the runner (pid $runner_pid) before it could complete or trap"
else
  bad "mid-run: SIGKILL" "could not resolve the runner window's pane pid"
fi

# Dwell after the kill -- an async leak (e.g. a tmux server-side session
# create that outlives the client process that requested it) would still
# show up in a sample taken after the kill, not just during it.
sleep 2

kill "$SAMPLER_PID" 2>/dev/null
wait "$SAMPLER_PID" 2>/dev/null
SAMPLER_PID=""

sample_count="$(wc -l <"$SAMPLE_LOG" | tr -d ' ')"
if [ "${sample_count:-0}" -ge 6 ]; then
  ok "sampler ran continuously: $sample_count samples at >=0.5s intervals across the run"
else
  bad "sampler ran continuously" "only $sample_count samples captured, expected >=6"
fi

# THE ASSERTION: at every sample, across the whole run, the shared-socket
# stand-in shows the driver session and nothing else -- no
# bootstrap-test-* (or any other) session from the guarded suite ever
# touched it, transient or persistent, before or after the SIGKILL.
foreign_lines=0
while IFS= read -r line; do
  [ -n "$line" ] || continue
  names="${line#* }"
  IFS=',' read -ra parts <<<"$names"
  for n in "${parts[@]}"; do
    [ -n "$n" ] || continue
    if [ "$n" != "$DRIVER" ]; then
      echo "  FOREIGN SESSION: $line" >&2
      foreign_lines=$((foreign_lines + 1))
    fi
  done
done <"$SAMPLE_LOG"

if [ "$foreign_lines" -eq 0 ]; then
  ok "OBSERVED ABSENCE: zero foreign sessions on the shared-socket stand-in across $sample_count samples (tmux pane + stale worktree + mid-run SIGKILL)"
else
  bad "OBSERVED ABSENCE" "$foreign_lines sample(s) showed a foreign session -- see FOREIGN SESSION lines above, and $SAMPLE_LOG before cleanup removes it"
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
