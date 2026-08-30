#!/bin/bash
# agent-supervisor#163: lanes.sh --json has never once run successfully under
# the watchdog LaunchAgent's environment, and the failure is invisible to
# every OTHER test in this suite because they all run against stubs/tmux-lanes
# -- a fake tmux that prints its own hardcoded tabs and never touches the REAL
# tmux binary's format engine. The two defects below are both properties of
# REAL tmux, not of lanes.sh's parsing logic, so a stub cannot reproduce
# either one. This file runs the genuine `tmux` binary against an isolated
# server (never the default socket -- CLAUDE.md invariant #4) and strips the
# calling environment the same way `env -i HOME=... PATH=...` does, which is
# exactly how #163 was measured on the live estate.
#
# Two defects, both reproduced here:
#
#   1. tmux missing from PATH entirely. lanes.sh must say so distinctly from
#      "the session does not exist" (#124/#126: ambiguity withholds -- "I
#      cannot see tmux" and "there is no session" are different facts) and
#      must not exit 0.
#
#   2. tmux found, but no LANG/LC_ALL at all (launchd's exact shape). tmux's
#      format engine silently substitutes '_' for the literal TAB byte
#      lanes.sh's own list-panes call asks for when it cannot confirm a
#      UTF-8-aware locale -- poller-window.sh hit the identical defect once
#      already for a two-field lookup (#28/#31); this call shares one
#      list-panes sample across eight fields, so JSON built from the
#      collapsed columns is not merely wrong, it is not even valid JSON.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANES="$HERE/../../scripts/supervisor/lanes.sh"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"; exit 0
fi
REAL_TMUX_DIR="$(cd "$(dirname "$(command -v tmux)")" && pwd)"

S="lanes-env-parity-$$"
RT="$(mktemp -d "${TMPDIR:-/tmp}/lanes-env-parity-tmux.XXXXXX")"
unset TMUX
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 - $2"; fail=$((fail+1)); }

cleanup() { unset TMUX; export TMUX_TMPDIR="$RT"; tmux kill-session -t "$S" 2>/dev/null; rm -rf "$RT"; }
trap cleanup EXIT INT TERM

echo "lanes.sh -- #163 env parity (real tmux, stripped environment)"

tmux new-session -d -s "$S" -x 200 -y 50 -n one
tmux new-window -t "$S" -n two -d

# --- Defect 1: tmux is not on PATH at all -----------------------------------
# The issue's own repro was env -i HOME=... PATH=/usr/bin:/bin -- but a
# literal /usr/bin:/bin is not portable proof of "tmux absent": Debian/Ubuntu
# (this suite's own CI runner) installs tmux to /usr/bin/tmux, so that PATH
# still resolves it there, and the test silently stopped simulating defect 1
# at all -- it fell through to a REAL `tmux has-session`, and because this
# env -i call carries no TMUX_TMPDIR, that call addressed the DEFAULT tmux
# socket, which CLAUDE.md invariant #4 forbids a test from ever touching. An
# empty directory is tmux-absent on every platform by construction, and since
# lanes.sh's own command -v check (#163) must reject it before any tmux call
# is made, the default socket is never at risk either.
# The session DOES exist (created above); lanes.sh cannot see tmux to know
# that, and must say so rather than claim it knows the session is absent.
NO_TMUX_PATH="$RT/no-tmux-on-this-path"
mkdir -p "$NO_TMUX_PATH"
out=$(env -i HOME="$HOME" PATH="$NO_TMUX_PATH" "$LANES" --json "$S" 2>"$RT/err1")
rc=$?
err=$(cat "$RT/err1")
if [ "$rc" -eq 0 ]; then
  bad "tmux missing from PATH must not exit 0" "rc=$rc out=$out err=$err"
else
  ok "tmux missing from PATH exits non-zero (rc=$rc)"
fi
if [ -n "$out" ]; then
  bad "tmux missing from PATH must print nothing to stdout" "stdout=$out"
else
  ok "tmux missing from PATH prints nothing to stdout"
fi
if grep -qi 'does not exist' <<<"$err"; then
  bad "tmux-missing must not be worded as session-absent (the #163 conflation)" "err=$err"
else
  ok "tmux-missing is NOT worded as 'session does not exist'"
fi
if grep -qi 'tmux' <<<"$err" && grep -qiE 'not found|unavailable|cannot' <<<"$err"; then
  ok "tmux-missing names tmux itself as the reason it cannot tell"
else
  bad "tmux-missing does not clearly name tmux as unavailable" "err=$err"
fi

# --- Defect 2: tmux found, but no LANG/LC_ALL (launchd's real shape) -------
out=$(env -i HOME="$HOME" TMUX_TMPDIR="$RT" \
  PATH="$REAL_TMUX_DIR:/usr/bin:/bin" \
  "$LANES" --json "$S" 2>"$RT/err2")
rc=$?
err=$(cat "$RT/err2")
if [ "$rc" -eq 0 ]; then
  ok "tmux found, no LANG/LC_ALL: exits 0"
else
  bad "tmux found, no LANG/LC_ALL: exits 0" "rc=$rc out=$out err=$err"
fi
if python3 -c 'import json,sys; json.loads(sys.argv[1])' "$out" 2>/dev/null; then
  ok "tmux found, no LANG/LC_ALL: stdout is valid JSON"
else
  bad "tmux found, no LANG/LC_ALL: stdout is valid JSON" "stdout=$out"
fi
count=$(python3 -c 'import json,sys; print(len(json.loads(sys.argv[1])))' "$out" 2>/dev/null || echo -1)
if [ "$count" = 2 ]; then
  ok "tmux found, no LANG/LC_ALL: both real windows are reported"
else
  bad "tmux found, no LANG/LC_ALL: both real windows are reported" "count=$count out=$out"
fi
if python3 -c '
import json, sys
rows = json.loads(sys.argv[1])
ok = all(isinstance(r.get("window"), int) and str(r.get("window_id","")).startswith("@") for r in rows)
sys.exit(0 if ok else 1)
' "$out" 2>/dev/null; then
  ok "tmux found, no LANG/LC_ALL: window and window_id fields are intact, not merged"
else
  bad "tmux found, no LANG/LC_ALL: window and window_id fields are intact, not merged" "out=$out"
fi

# --- Session genuinely absent, tmux fully available -------------------------
# The control case: distinct wording from BOTH cases above, still non-zero.
out=$(PATH="$REAL_TMUX_DIR:/usr/bin:/bin" TMUX_TMPDIR="$RT" "$LANES" --json "no-such-session-$$" 2>"$RT/err3")
rc=$?
err=$(cat "$RT/err3")
if [ "$rc" -ne 0 ] && grep -qi 'does not exist' <<<"$err"; then
  ok "a genuinely absent session (tmux available) still reads 'does not exist'"
else
  bad "a genuinely absent session (tmux available) still reads 'does not exist'" "rc=$rc err=$err"
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
