#!/bin/bash
# protect-shared-checkout.sh must actually be wired up: the path
# .claude/settings.json points a PreToolUse hook at must resolve to a real,
# executable file, and the hook itself must block a branch switch inside the
# shared checkout while allowing everything else (commit, push, file
# restores, switches elsewhere).
#
# WHY: settings.json referenced .claude/hooks/protect-shared-checkout.sh
# while the script shipped at .claude/protect-shared-checkout.sh (no hooks/
# subdirectory). Claude Code fails OPEN on a missing hook script, so the
# guard silently did nothing -- the exact class of silent failure (a
# verified fix that stops working with no signal) the PR itself exists to
# prevent (#301).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$HERE/../.."
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }

echo "protect-shared-checkout.sh"

# --- the path settings.json points at must exist and be executable --------
HOOK_CMD=$(python3 -c '
import json
with open("'"$REPO"'/.claude/settings.json") as f:
    cfg = json.load(f)
print(cfg["hooks"]["PreToolUse"][0]["hooks"][0]["command"])
')
# Strip a $CLAUDE_PROJECT_DIR/ prefix if present; resolve relative to REPO
# otherwise, matching how Claude Code resolves a relative hook command
# (against cwd, i.e. the project root for a session started there).
RESOLVED="${HOOK_CMD/#\$CLAUDE_PROJECT_DIR\//}"
if [ -f "$REPO/$RESOLVED" ] && [ -x "$REPO/$RESOLVED" ]; then
  ok "settings.json command '$HOOK_CMD' resolves to an existing executable file"
else
  bad "settings.json command '$HOOK_CMD' resolves to an existing executable file" \
      "no such file: $REPO/$RESOLVED"
fi

# --- the hook script itself: block switches in the shared checkout --------
HOOK="$REPO/.claude/protect-shared-checkout.sh"
D=$(mktemp -d)
trap 'rm -rf "$D"' EXIT
git init -q "$D/shared"
export SUPERVISOR_SHARED_CHECKOUT="$D/shared"

run_hook() {
  printf '{"tool_input":{"command":%s}}' "$(python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$1")" \
    | (cd "$D/shared" && "$HOOK")
}

out=$(run_hook "git checkout other-branch" 2>&1); rc=$?
want_exit "blocks 'git checkout <branch>' in the shared checkout" "$rc" 2 "$out"

out=$(run_hook "git switch other-branch" 2>&1); rc=$?
want_exit "blocks 'git switch <branch>' in the shared checkout" "$rc" 2 "$out"

out=$(run_hook "git checkout -- some/file.sh" 2>&1); rc=$?
want_exit "allows 'git checkout -- <path>' (file restore, not a HEAD move)" "$rc" 0 "$out"

out=$(run_hook "git commit -m wip" 2>&1); rc=$?
want_exit "allows 'git commit'" "$rc" 0 "$out"

out=$(run_hook "git status" 2>&1); rc=$?
want_exit "allows 'git status'" "$rc" 0 "$out"

mkdir -p "$D/elsewhere"
git init -q "$D/elsewhere"
out=$(cd "$D/elsewhere" && printf '{"tool_input":{"command":"git checkout other-branch"}}' | "$HOOK" 2>&1); rc=$?
want_exit "allows 'git checkout <branch>' outside the shared checkout" "$rc" 0 "$out"

echo
echo "protect-shared-checkout.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
