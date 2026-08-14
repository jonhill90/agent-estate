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
  [ "$rc" -eq 0 ] && want "scrubbed env resolves the default session" "agent-supervisor" "$out" \
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
old = 'agent-supervisor'
assert old in text, "shared default literal not found"
open(path, 'w').write(text.replace(old, 'agent-dotfiles', 1))
PY
  out=$(env -i HOME="$HOME" NOTIFY_ENV= PATH="/usr/bin:/bin" bash -c '. "$1"; lanes_session_or_default' _ "$D/session-defaults.sh" 2>&1)
  want "mutation-check: breaking the shared default is detected" "agent-dotfiles" "$out"
fi

if [ "$fail" -eq 0 ]; then
  echo "PASS session-defaults ($pass checks)"
  exit 0
else
  echo "FAIL session-defaults ($fail failures, $pass passed)"
  exit 1
fi
