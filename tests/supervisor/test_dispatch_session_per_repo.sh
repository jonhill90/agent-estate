#!/bin/bash
# agent-supervisor#111: one tmux session per repo, named for the repo. A lane
# working in jonhill90/agent-tui runs in session agent-tui, not whatever
# session the supervisor's own repo happens to use -- the same shape #99 fixed
# one layer up (the session DEFAULT naming the parent repo; this is the
# session ITSELF naming whichever repo a lane is actually dispatched to).
#
# LANES_SESSION is deliberately left UNSET for every case in this file except
# the one that exists to prove it still overrides -- every other test in
# tests/supervisor/test_dispatch.sh pins LANES_SESSION=t, which would hide
# this exact regression (SESSION reverting to the shared default) since the
# override always wins regardless of which default computed it.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISPATCH="$HERE/../../scripts/supervisor/dispatch.sh"
# agent-supervisor#227: give every dispatch here a deterministic SAFE quota
# verdict instead of calling the real codexbar. See test_dispatch.sh for why.
export QUOTA_GATE="$HERE/stubs/quota-safe"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }

echo "dispatch.sh: session-per-repo (agent-supervisor#111)"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
trap 'rm -rf "$D"' EXIT INT TERM
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo" 2>/dev/null
REPO_PATH="$D/repo"
git -C "$REPO_PATH" config user.email test@example.com
git -C "$REPO_PATH" config user.name "Test"
git -C "$REPO_PATH" checkout -q -b main
echo one > "$REPO_PATH/file.txt"
git -C "$REPO_PATH" add file.txt
git -C "$REPO_PATH" commit -q -m "initial"
git -C "$REPO_PATH" push -q -u origin main
git -C "$REPO_PATH" remote set-url origin "git@github.com:jonhill90/agent-tui.git"

cat > "$D/issues" <<'FIX'
200|| session-per-repo landing test
201|| session-per-repo override test
202|| session-per-repo mutation-check
FIX
: > "$D/prs"
echo "do the thing" > "$D/brief.md"

# Window 3 is the only free lane. The stub only ever reads the window INDEX
# out of a target (see stubs/tmux-dispatch's idx_for) -- it has no notion of
# which session it was addressed under -- so this fixture works unmodified no
# matter what session name dispatch.sh resolves.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX

run() {
  : > "$D/tmux.log"
  rm -rf "$D/panes"; mkdir -p "$D/panes"
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION="${RUN_LANES_SESSION:-}" \
    TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
    DISPATCH_RESPAWN_SETTLE=0 DISPATCH_LAUNCH_SETTLE=0 \
    AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" \
    STUB_PANE_PATH="$REPO_PATH" DISPATCH_CONFIRM_TRIES=2 DISPATCH_SESSION_TIMEOUT=0 \
    WORKTREE_ROOT="$D/roots" bash "${DISPATCH_SCRIPT:-$DISPATCH}" "$@" 2>&1
}

# --- the load-bearing case: no LANES_SESSION override at all ---------------
unset RUN_LANES_SESSION
out=$(run 200 lands-per-repo "$D/brief.md" jonhill90/agent-tui "$REPO_PATH"); rc=$?
want_exit "a dispatch to jonhill90/agent-tui succeeds with no LANES_SESSION set" "$rc" 0 "$out"
log=$(cat "$D/tmux.log")
want_contains "the brief lands in a session named for the repo, not the shared default" \
  "send-keys -t agent-tui:@103 Enter" "$log"
if grep -qF 'agent-supervisor:' <<<"$log"; then
  bad "the dispatch never touches the shared default session" "$log"
else
  ok "the dispatch never touches the shared default session"
fi

# --- LANES_SESSION, when set, still wins outright ---------------------------
RUN_LANES_SESSION="operator-override"
out=$(run 201 override-wins "$D/brief.md" jonhill90/agent-tui "$REPO_PATH"); rc=$?
unset RUN_LANES_SESSION
want_exit "LANES_SESSION override still dispatches" "$rc" 0 "$out"
log=$(cat "$D/tmux.log")
want_contains "an explicit LANES_SESSION still wins over the repo-derived name" \
  "send-keys -t operator-override:@103 Enter" "$log"

# --- mutation-check: revert dispatch.sh to the pre-#111 global default -----
# Proves this suite actually exercises the per-repo derivation rather than
# passing regardless of it: reverting dispatch.sh's SESSION assignment to the
# old `lanes_session_or_default` call must turn the first case red.
# dispatch.sh resolves its sibling scripts (session-defaults.sh, cli.py, ...)
# relative to its OWN path, so the mutant needs the whole directory copied
# alongside it -- a lone copy of dispatch.sh cannot find them and fails
# before it ever reaches the line under test.
MUTDIR="$D/scripts-mutant"
cp -R "$(dirname "$DISPATCH")" "$MUTDIR"
MUT="$MUTDIR/dispatch.sh"
python3 - "$MUT" <<'PY'
import sys
path = sys.argv[1]
text = open(path).read()
old = 'SESSION="$(session_for_repo "$NAME_PART")"'
assert old in text, "dispatch.sh's per-repo SESSION assignment not found"
open(path, 'w').write(text.replace(old, 'SESSION="$(lanes_session_or_default)"', 1))
PY
unset RUN_LANES_SESSION
mut_out=$(DISPATCH_SCRIPT="$MUT" run 202 mutant-run "$D/brief.md" jonhill90/agent-tui "$REPO_PATH")
mut_log=$(cat "$D/tmux.log")
want_contains "mutation-check: reverting to the global default is detected" \
  "send-keys -t agent-supervisor:@103 Enter" "$mut_log"

if [ "$fail" -eq 0 ]; then
  echo "PASS dispatch.sh: session-per-repo ($pass checks)"
  exit 0
else
  echo "FAIL dispatch.sh: session-per-repo ($fail failures, $pass passed)"
  exit 1
fi
