#!/bin/bash
# agent-supervisor#422: a repo whose session was never bootstrapped is
# silently undispatchable. Every `lanes.sh` call dispatch.sh makes discards
# lanes.sh's own stderr ("lanes: session '$SESSION' does not exist") to get
# at its stdout classification table, so that message never reaches anyone;
# what surfaces instead is the same generic "no free lane" refusal a fully
# busy estate produces. This suite proves dispatch.sh now checks for the
# session BEFORE any of that, and either auto-bootstraps it or fails loudly
# naming the exact command to run -- distinguishably from "all lanes busy".
#
# `bootstrap-session.sh` itself is real (bootstrap creation mechanics belong
# to test_bootstrap_session.sh, not here) but is replaced in a copied
# `scripts/supervisor` tree by a controllable fake: this suite is about
# dispatch.sh's DECISION to call it (or not) and how it reacts to the
# result, not about tmux session-creation mechanics a second time.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISPATCH_SRC="$HERE/../../scripts/supervisor/dispatch.sh"
export QUOTA_GATE="$HERE/stubs/quota-safe"
export DISPATCH_LIVE_PANE=1
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "dispatch.sh: session-missing distinguished from lanes-all-busy (agent-supervisor#422)"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
trap 'rm -rf "$D"' EXIT INT TERM
export SUPERVISOR_STATE="$D/pa-state"
mkdir -p "$SUPERVISOR_STATE/results"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"
cp "$HERE/stubs/claude" "$D/bin/claude"

# A copy of scripts/supervisor so bootstrap-session.sh ($HERE-relative from
# dispatch.sh's own resolved path) can be swapped for a fake that records
# its own invocation instead of actually driving tmux session creation.
SCRIPTS_COPY="$D/scripts-supervisor"
cp -R "$(dirname "$DISPATCH_SRC")" "$SCRIPTS_COPY"
DISPATCH="$SCRIPTS_COPY/dispatch.sh"
BOOTSTRAP_LOG="$D/bootstrap.log"
BOOTSTRAP_MODE_FILE="$D/bootstrap-mode"  # "ok" or "fail"

cat > "$SCRIPTS_COPY/bootstrap-session.sh" <<'FAKE'
#!/bin/bash
echo "bootstrap-session(fake): $*" >> "$BOOTSTRAP_LOG"
mode="$(cat "$BOOTSTRAP_MODE_FILE" 2>/dev/null || echo ok)"
if [ "$mode" = ok ]; then
  [ -n "${STUB_SESSION_MARKER:-}" ] && : > "$STUB_SESSION_MARKER"
  exit 0
fi
echo "bootstrap-session(fake): simulated failure" >&2
exit 1
FAKE
chmod +x "$SCRIPTS_COPY/bootstrap-session.sh"

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
300|| session-missing landing test
301|| session-missing no-bootstrap test
302|| session-missing bootstrap-fails test
303|| session-missing mutation-check
FIX
: > "$D/prs"
echo "do the thing" > "$D/brief.md"

# Window 3 is the only free lane -- read once the (fake-bootstrapped)
# session exists.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX

run() {
  : > "$D/tmux.log"; : > "$BOOTSTRAP_LOG"
  rm -f "$D/session-marker"
  rm -rf "$D/panes"; mkdir -p "$D/panes"
  BOOTSTRAP_LOG="$BOOTSTRAP_LOG" BOOTSTRAP_MODE_FILE="$BOOTSTRAP_MODE_FILE" \
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION="" \
    TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
    DISPATCH_RESPAWN_SETTLE=0 DISPATCH_LAUNCH_SETTLE=0 \
    AGENT_SUPERVISOR_STATE_DIR="$(mktemp -d "$D/state.XXXXXX")" \
    STUB_PANE_PATH="$REPO_PATH" DISPATCH_CONFIRM_TRIES=2 DISPATCH_SESSION_TIMEOUT=0 \
    WORKTREE_ROOT="$D/roots" STUB_SESSION_MISSING=1 \
    STUB_SESSION_MARKER="$D/session-marker" \
    bash "${DISPATCH_SCRIPT:-$DISPATCH}" "$@" 2>&1
}

# --- 1. session missing, bootstrap succeeds -> dispatch proceeds and lands -
echo ok > "$BOOTSTRAP_MODE_FILE"
out=$(run 300 lands-after-bootstrap "$D/brief.md" jonhill90/agent-tui "$REPO_PATH"); rc=$?
want_contains "names the missing session before doing anything else" \
  "dispatch: session 'agent-tui' does not exist -- #300 cannot be dispatched (agent-supervisor#422)" "$out"
want_contains "auto-bootstraps by default" \
  "dispatch: auto-bootstrapping session 'agent-tui'" "$out"
want_exit "dispatch succeeds once the session is bootstrapped" "$rc" 0 "$out"
want_contains "the fake bootstrap-session.sh was actually invoked with the repo's session and cwd" \
  "--session agent-tui" "$(cat "$BOOTSTRAP_LOG")"
want_contains "the brief still lands in the (now-existing) session" \
  "send-keys -t agent-tui:@103 Enter" "$(cat "$D/tmux.log")"
want_missing "the generic busy-estate refusal never fires -- this is a distinct message" \
  "no free lane in session" "$out"

# --- 2. DISPATCH_NO_AUTO_BOOTSTRAP=1 refuses loudly, names the exact fix ---
echo ok > "$BOOTSTRAP_MODE_FILE"
out=$(DISPATCH_NO_AUTO_BOOTSTRAP=1 run 301 no-auto-bootstrap "$D/brief.md" jonhill90/agent-tui "$REPO_PATH"); rc=$?
want_exit "DISPATCH_NO_AUTO_BOOTSTRAP=1 refuses rather than dispatching" "$rc" 2 "$out"
want_contains "names the exact bootstrap-session.sh command to run by hand" \
  "bootstrap-session.sh --session agent-tui --lanes 3 --cwd $REPO_PATH" "$out"
if [ -s "$BOOTSTRAP_LOG" ]; then
  bad "bootstrap-session.sh is never invoked when auto-bootstrap is disabled" "$(cat "$BOOTSTRAP_LOG")"
else
  ok "bootstrap-session.sh is never invoked when auto-bootstrap is disabled"
fi

# --- 3. auto-bootstrap attempted but fails -> loud failure, exact fix ------
echo fail > "$BOOTSTRAP_MODE_FILE"
out=$(run 302 bootstrap-fails "$D/brief.md" jonhill90/agent-tui "$REPO_PATH"); rc=$?
want_exit "a failed auto-bootstrap is a refusal, not a silent fall-through" "$rc" 2 "$out"
want_contains "says the auto-bootstrap attempt failed" \
  "dispatch: auto-bootstrap of session 'agent-tui' FAILED -- run by hand:" "$out"
want_contains "still names the exact command to run by hand" \
  "bootstrap-session.sh --session agent-tui --lanes 3 --cwd $REPO_PATH" "$out"

# --- mutation-check: revert dispatch.sh to have no session-existence check -
# Proves this suite actually exercises the new check rather than passing
# regardless of it: with the check removed, a missing session falls straight
# through to the pre-#422 behaviour -- the generic "no free lane" refusal,
# with no distinguishing message and no bootstrap attempt.
MUTDIR="$D/scripts-mutant"
cp -R "$SCRIPTS_COPY" "$MUTDIR"
MUT="$MUTDIR/dispatch.sh"
python3 - "$MUT" <<'PY'
import re, sys
path = sys.argv[1]
text = open(path).read()
start = text.index("# --- -1. the session itself must exist")
end = text.index("# --- 0. the ledger must be readable")
assert start < end, "agent-supervisor#422 session-existence block not found"
text = text[:start] + text[end:]
open(path, 'w').write(text)
PY
echo ok > "$BOOTSTRAP_MODE_FILE"
mut_out=$(DISPATCH_SCRIPT="$MUT" run 303 mutant-run "$D/brief.md" jonhill90/agent-tui "$REPO_PATH")
want_contains "mutation-check: without the fix, a missing session reads as the generic busy refusal" \
  "dispatch: no free lane in session 'agent-tui' -- not dispatching #303" "$mut_out"
if [ -s "$BOOTSTRAP_LOG" ]; then
  bad "mutation-check: without the fix, bootstrap-session.sh is never even considered" "$(cat "$BOOTSTRAP_LOG")"
else
  ok "mutation-check: without the fix, bootstrap-session.sh is never even considered"
fi

if [ "$fail" -eq 0 ]; then
  echo "PASS dispatch.sh: session-missing distinguished ($pass checks)"
  exit 0
else
  echo "FAIL dispatch.sh: session-missing distinguished ($fail failures, $pass passed)"
  exit 1
fi
