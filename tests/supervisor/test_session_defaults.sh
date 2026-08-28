#!/bin/bash
# agent-supervisor#99: scripts in this repository must default new lane work to
# the agent-supervisor tmux session, while keeping LANES_SESSION as the override.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
DEFAULTS="$ROOT/scripts/supervisor/session-defaults.sh"
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "want '$2', got '$3'"; fi; }

echo "session-defaults"

old_defaults="$(grep -RIn 'LANES_SESSION:-agent-dotfiles' "$ROOT/scripts/supervisor" || true)"
if [ -z "$old_defaults" ]; then
  ok "no script carries the old agent-dotfiles lane-session default"
else
  bad "no script carries the old agent-dotfiles lane-session default" "$old_defaults"
fi

if [ -f "$DEFAULTS" ]; then
  ok "shared session-defaults.sh exists"
else
  bad "shared session-defaults.sh exists" "$DEFAULTS"
fi

if [ -f "$DEFAULTS" ]; then
  out=$(env -i HOME="$HOME" NOTIFY_ENV= PATH="/usr/bin:/bin" bash -c '. "$1"; lanes_session_or_default' _ "$DEFAULTS" 2>&1)
  rc=$?
  [ "$rc" -eq 0 ] && want "scrubbed env resolves the default session" "agent-estate" "$out" \
    || bad "scrubbed env resolves the default session" "rc=$rc out=$out"

  out=$(env -i HOME="$HOME" NOTIFY_ENV= PATH="/usr/bin:/bin" LANES_SESSION=custom-session \
    bash -c '. "$1"; lanes_session_or_default' _ "$DEFAULTS" 2>&1)
  rc=$?
  [ "$rc" -eq 0 ] && want "LANES_SESSION still overrides the default" "custom-session" "$out" \
    || bad "LANES_SESSION still overrides the default" "rc=$rc out=$out"
fi

if [ -f "$DEFAULTS" ]; then
  D=$(mktemp -d)
  trap 'rm -rf "$D"' EXIT INT TERM
  cp "$DEFAULTS" "$D/session-defaults.sh"
  python3 - "$D/session-defaults.sh" <<'PY'
import sys
path = sys.argv[1]
text = open(path).read()
# #739: the pool converged (session renamed in place, no pane touched), so
# this now derives from AGENT_SUPERVISOR_DEFAULT_REPO instead of carrying
# its own pinned literal (#734's interim state, retired here). Target the
# actual derivation line, not a bare literal -- there is no longer a
# standalone 'agent-supervisor' string on this line to mutate.
old = 'AGENT_SUPERVISOR_DEFAULT_LANES_SESSION:-$AGENT_SUPERVISOR_DEFAULT_REPO}'
assert old in text, "shared lanes-session default derivation not found"
new = 'AGENT_SUPERVISOR_DEFAULT_LANES_SESSION:-agent-dotfiles}'
open(path, 'w').write(text.replace(old, new, 1))
PY
  out=$(env -i HOME="$HOME" NOTIFY_ENV= PATH="/usr/bin:/bin" bash -c '. "$1"; lanes_session_or_default' _ "$D/session-defaults.sh" 2>&1)
  want "mutation-check: breaking the shared default is detected" "agent-dotfiles" "$out"
fi

# agent-supervisor#111: session_for_repo -- one tmux session per repo, named
# for the repo. Same env -i discipline as lanes_session_or_default above: only
# HOME, NOTIFY_ENV, PATH survive, so a hidden dependency on some other
# ambient variable would show up here rather than only on a real developer
# machine that happens to already export it.
if [ -f "$DEFAULTS" ]; then
  out=$(env -i HOME="$HOME" NOTIFY_ENV= PATH="/usr/bin:/bin" \
    bash -c '. "$1"; session_for_repo "jonhill90/agent-tui"' _ "$DEFAULTS" 2>&1)
  rc=$?
  [ "$rc" -eq 0 ] && want "session_for_repo names the session for owner/repo" "agent-tui" "$out" \
    || bad "session_for_repo names the session for owner/repo" "rc=$rc out=$out"

  out=$(env -i HOME="$HOME" NOTIFY_ENV= PATH="/usr/bin:/bin" \
    bash -c '. "$1"; session_for_repo "agent-tui"' _ "$DEFAULTS" 2>&1)
  rc=$?
  [ "$rc" -eq 0 ] && want "session_for_repo names the session for a bare repo name" "agent-tui" "$out" \
    || bad "session_for_repo names the session for a bare repo name" "rc=$rc out=$out"

  out=$(env -i HOME="$HOME" NOTIFY_ENV= PATH="/usr/bin:/bin" LANES_SESSION=custom-session \
    bash -c '. "$1"; session_for_repo "jonhill90/agent-tui"' _ "$DEFAULTS" 2>&1)
  rc=$?
  [ "$rc" -eq 0 ] && want "LANES_SESSION overrides session_for_repo too" "custom-session" "$out" \
    || bad "LANES_SESSION overrides session_for_repo too" "rc=$rc out=$out"

  out=$(env -i HOME="$HOME" NOTIFY_ENV= PATH="/usr/bin:/bin" \
    bash -c '. "$1"; session_for_repo ""' _ "$DEFAULTS" 2>&1)
  rc=$?
  [ "$rc" -eq 0 ] && want "session_for_repo falls back to the shared default with no repo" "agent-estate" "$out" \
    || bad "session_for_repo falls back to the shared default with no repo" "rc=$rc out=$out"
fi

# mutation-check: a session_for_repo that forgot to strip the owner/ prefix
# would name the session "jonhill90/agent-tui" instead of "agent-tui" -- not
# just cosmetically wrong, but a session tmux itself would refuse to create
# (`/` is not a `:` or `.`, so bootstrap-session.sh's own guard would not
# catch it either). This proves the test above actually reads the derived
# name rather than always passing.
if [ -f "$DEFAULTS" ]; then
  D2=$(mktemp -d)
  trap 'rm -rf "$D" "$D2"' EXIT INT TERM
  cp "$DEFAULTS" "$D2/session-defaults.sh"
  python3 - "$D2/session-defaults.sh" <<'PY'
import sys
path = sys.argv[1]
text = open(path).read()
old = 'local name="${repo##*/}"'
assert old in text, "session_for_repo's basename derivation not found"
open(path, 'w').write(text.replace(old, 'local name="$repo"', 1))
PY
  out=$(env -i HOME="$HOME" NOTIFY_ENV= PATH="/usr/bin:/bin" \
    bash -c '. "$1"; session_for_repo "jonhill90/agent-tui"' _ "$D2/session-defaults.sh" 2>&1)
  want "mutation-check: breaking the owner/ strip is detected" "jonhill90/agent-tui" "$out"
fi

if [ "$fail" -eq 0 ]; then
  echo "PASS session-defaults ($pass checks)"
  exit 0
else
  echo "FAIL session-defaults ($fail failures, $pass passed)"
  exit 1
fi
