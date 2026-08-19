#!/bin/bash
# Behaviour tests for notify.sh's caller gate.
#
# agent-dotfiles#52: only the supervisor/watchdog sender may message Jon.
# Nothing technically stopped a worker's Bash tool from shelling out to
# notify.sh directly -- same $HOME, same credentials, same script. This
# suite locks that down: notify.sh must refuse to touch any channel unless
# invoked with AGENT_NOTIFY_CALLER=supervisor, and must never claim success
# (SENT in the log) for a refused call.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NOTIFY="$HERE/../../scripts/supervisor/notify.sh"
pass=0; fail=0
check() { # check <name> <expected-substring> <file>
  if grep -q "$2" "$3" 2>/dev/null; then echo "  ok   $1"; pass=$((pass+1));
  else echo "  FAIL $1 — expected '$2' in $(cat "$3" 2>/dev/null | tr '\n' ' ')"; fail=$((fail+1)); fi
}

echo "notify.sh"

D=$(mktemp -d)
mkdir -p "$D/bin"
# notify.sh does not source anything relative to its own path, so the
# mutants below have no need to sit beside the real script -- they live
# under $D and a single recursive cleanup on exit covers all of them
# (agent-supervisor#220).
trap 'rm -rf "$D"' EXIT
cat > "$D/bin/curl" <<'EOF'
#!/bin/bash
echo "curl called: $*" >> "$CURL_LOG"
exit 0
EOF
chmod +x "$D/bin/curl"

cat > "$D/notify.env" <<'EOF'
AGENT_NOTIFY_TELEGRAM_TOKEN=fake-token
AGENT_NOTIFY_TELEGRAM_CHAT_ID=fake-chat
EOF

# --- an unauthorized caller (no AGENT_NOTIFY_CALLER) is refused --------
UNAUTH="$D/unauth"; mkdir -p "$UNAUTH/.local/state/agent-dotfiles-supervisor"
CURL_LOG="$UNAUTH/curl.log"
HOME="$UNAUTH" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_LOG="$CURL_LOG" \
  bash "$NOTIFY" "subject" "body" >"$UNAUTH/out" 2>"$UNAUTH/err"
rc=$?
if [ "$rc" -ne 0 ]; then echo "  ok   unauthorized caller exits non-zero"; pass=$((pass+1));
else echo "  FAIL unauthorized caller exited 0"; fail=$((fail+1)); fi
if [ ! -s "$CURL_LOG" ]; then echo "  ok   unauthorized caller never touches curl"; pass=$((pass+1));
else echo "  FAIL curl was invoked for an unauthorized caller: $(cat "$CURL_LOG")"; fail=$((fail+1)); fi
STATE_LOG="$UNAUTH/.local/state/agent-dotfiles-supervisor/notify.log"
check "unauthorized caller is logged as refused" "REFUSED" "$STATE_LOG"
if grep -q "SENT" "$STATE_LOG" 2>/dev/null; then
  echo "  FAIL an unauthorized call was logged as SENT"; fail=$((fail+1))
else
  echo "  ok   an unauthorized call is never logged as SENT"; pass=$((pass+1))
fi

# --- a caller identifying as the supervisor is allowed through ---------
AUTH="$D/auth"; mkdir -p "$AUTH/.local/state/agent-dotfiles-supervisor"
CURL_LOG="$AUTH/curl.log"
HOME="$AUTH" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_LOG="$CURL_LOG" \
  AGENT_NOTIFY_CALLER=supervisor \
  bash "$NOTIFY" "subject" "body" >"$AUTH/out" 2>"$AUTH/err"
rc=$?
if [ "$rc" -eq 0 ]; then echo "  ok   supervisor caller exits zero"; pass=$((pass+1));
else echo "  FAIL supervisor caller exited $rc: $(cat "$AUTH/err")"; fail=$((fail+1)); fi
if [ -s "$CURL_LOG" ]; then echo "  ok   supervisor caller reaches curl"; pass=$((pass+1));
else echo "  FAIL curl was never invoked for the supervisor caller"; fail=$((fail+1)); fi
check "supervisor caller is logged as sent" "SENT telegram (supervisor)" "$AUTH/.local/state/agent-dotfiles-supervisor/notify.log"

# --- a caller identifying as the director is allowed through, and logged ---
# distinctly from supervisor (agent-dotfiles#193/#57): the caller gate is
# extended, not duplicated, and who actually sent a message must stay
# legible in notify.log even though both tiers share the one bot identity.
DIR="$D/director"; mkdir -p "$DIR/.local/state/agent-dotfiles-supervisor"
CURL_LOG="$DIR/curl.log"
HOME="$DIR" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_LOG="$CURL_LOG" \
  AGENT_NOTIFY_CALLER=director \
  bash "$NOTIFY" "subject" "body" >"$DIR/out" 2>"$DIR/err"
rc=$?
if [ "$rc" -eq 0 ]; then echo "  ok   director caller exits zero"; pass=$((pass+1));
else echo "  FAIL director caller exited $rc: $(cat "$DIR/err")"; fail=$((fail+1)); fi
if [ -s "$CURL_LOG" ]; then echo "  ok   director caller reaches curl"; pass=$((pass+1));
else echo "  FAIL curl was never invoked for the director caller"; fail=$((fail+1)); fi
check "director caller is logged as sent, distinctly from supervisor" "SENT telegram (director)" \
  "$DIR/.local/state/agent-dotfiles-supervisor/notify.log"

# --- a caller identifying as the watchdog is allowed through, and logged ---
# distinctly from supervisor/director (agent-supervisor#300): the watchdog
# runs OUTSIDE the loop and is the one caller able to notice the whole
# estate has stopped, so its authorization needs the same dedicated
# assertion the director's got in #193 -- not just an inert allow-list entry.
WD="$D/watchdog"; mkdir -p "$WD/.local/state/agent-dotfiles-supervisor"
CURL_LOG="$WD/curl.log"
HOME="$WD" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_LOG="$CURL_LOG" \
  AGENT_NOTIFY_CALLER=watchdog \
  bash "$NOTIFY" "subject" "body" >"$WD/out" 2>"$WD/err"
rc=$?
if [ "$rc" -eq 0 ]; then echo "  ok   watchdog caller exits zero"; pass=$((pass+1));
else echo "  FAIL watchdog caller exited $rc: $(cat "$WD/err")"; fail=$((fail+1)); fi
if [ -s "$CURL_LOG" ]; then echo "  ok   watchdog caller reaches curl"; pass=$((pass+1));
else echo "  FAIL curl was never invoked for the watchdog caller"; fail=$((fail+1)); fi
check "watchdog caller is logged as sent, distinctly from supervisor/director" "SENT telegram (watchdog)" \
  "$WD/.local/state/agent-dotfiles-supervisor/notify.log"

# --- mutation check: the gate must still refuse an unauthorised caller -----
# agent-dotfiles#193/#52. Extending the gate to a second value is exactly
# the kind of change that is easy to "fix" into accepting everything -- a
# one-line string comparison with no test that can tell "refuses everyone
# but the two named callers" apart from "refuses nobody". Patch the case
# statement so it accepts ANY caller, then confirm the very first assertion
# in this suite ("unauthorized caller exits non-zero") goes RED. If it does
# not, this suite's gate coverage is decorative.
MUTANT="$D/.notify-mutant-open-gate.sh"
patch_rc=0
python3 - "$NOTIFY" "$MUTANT" <<'PY' || patch_rc=$?
import re
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
# Matched by shape (the case block on $CALLER, ending at the first esac),
# not by a byte-for-byte literal -- comments and authorized-caller values
# inside the block are free to change without breaking this mutant setup
# (agent-supervisor#300: an authorization-comment-only diff false-failed
# this exact assert on a literal marker).
matches = list(re.finditer(r'case "\$CALLER" in\n.*?\nesac', text, re.DOTALL))
assert matches, "caller gate case statement not found -- notify.sh shape changed"
assert len(matches) == 1, "caller gate case statement not unique -- notify.sh shape changed"
block = matches[0].group(0)
assert "REFUSED" in block, "caller gate case statement missing refusal arm -- notify.sh shape changed"
open(dst, "w").write(text[:matches[0].start()] + 'case "$CALLER" in *) ;; esac' + text[matches[0].end():])
PY
if [ "$patch_rc" -ne 0 ]; then
  echo "  FAIL setup: patched an open-gate copy of notify.sh"; fail=$((fail+1))
else
  echo "  ok   setup: patched an open-gate copy of notify.sh (accepts any AGENT_NOTIFY_CALLER)"; pass=$((pass+1))
  MUT_HOME="$D/mutant-unauth"; mkdir -p "$MUT_HOME/.local/state/agent-dotfiles-supervisor"
  MUT_CURL_LOG="$MUT_HOME/curl.log"
  HOME="$MUT_HOME" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_LOG="$MUT_CURL_LOG" \
    bash "$MUTANT" "subject" "body" >"$MUT_HOME/out" 2>"$MUT_HOME/err"
  mut_rc=$?
  echo "  MUTATION: bash \"$MUTANT\" \"subject\" \"body\" (no AGENT_NOTIFY_CALLER set) -> exit $mut_rc"
  echo "  MUTATION OUTPUT: $(cat "$MUT_HOME/out" 2>/dev/null) $(cat "$MUT_HOME/err" 2>/dev/null)"
  if [ "$mut_rc" -eq 0 ] && [ -s "$MUT_CURL_LOG" ]; then
    echo "  ok   mutation confirmed: an open gate lets an unauthorized (unset AGENT_NOTIFY_CALLER) caller through -- exit 0, curl invoked (the 'unauthorized caller exits non-zero' assertion above would be red against this mutant)"
    pass=$((pass+1))
  else
    echo "  FAIL mutation confirmed: open gate did not let an unauthorized caller through -- rc=$mut_rc curl_log='$(cat "$MUT_CURL_LOG" 2>/dev/null)'"
    fail=$((fail+1))
  fi
fi

# --- a flag-shaped subject is refused, never paged (as#145) -----------------
# 2026-08-14: a caller used gh-style syntax (`notify.sh --body-file - /dev/stdin`)
# against this script's positional interface, and ten Telegram messages reached
# Jon whose entire subject was the literal text `--body-file`. This must fail
# closed before the caller gate even runs, and touch no channel.
FLAG="$D/flag"; mkdir -p "$FLAG/.local/state/agent-dotfiles-supervisor"
CURL_LOG="$FLAG/curl.log"
HOME="$FLAG" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_LOG="$CURL_LOG" \
  AGENT_NOTIFY_CALLER=supervisor \
  bash "$NOTIFY" "--body-file" "-" >"$FLAG/out" 2>"$FLAG/err"
rc=$?
if [ "$rc" -eq 2 ]; then echo "  ok   flag-shaped subject exits 2"; pass=$((pass+1));
else echo "  FAIL flag-shaped subject exited $rc (want 2): $(cat "$FLAG/err")"; fail=$((fail+1)); fi
if [ ! -s "$CURL_LOG" ]; then echo "  ok   flag-shaped subject never touches curl"; pass=$((pass+1));
else echo "  FAIL curl was invoked for a flag-shaped subject: $(cat "$CURL_LOG")"; fail=$((fail+1)); fi
FLAG_LOG="$FLAG/.local/state/agent-dotfiles-supervisor/notify.log"
if [ ! -s "$FLAG_LOG" ]; then echo "  ok   flag-shaped subject never even reaches notify.log"; pass=$((pass+1));
else echo "  FAIL a flag-shaped subject was logged: $(cat "$FLAG_LOG")"; fail=$((fail+1)); fi
check "flag-shaped subject explains the positional usage on stderr" "usage is positional" "$FLAG/err"

# --- a normal positional call is unaffected by the flag guard --------------
NORMAL="$D/normal-subject"; mkdir -p "$NORMAL/.local/state/agent-dotfiles-supervisor"
CURL_LOG="$NORMAL/curl.log"
HOME="$NORMAL" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_LOG="$CURL_LOG" \
  AGENT_NOTIFY_CALLER=supervisor \
  bash "$NOTIFY" "a normal subject" "body" >"$NORMAL/out" 2>"$NORMAL/err"
rc=$?
if [ "$rc" -eq 0 ]; then echo "  ok   a normal positional subject still exits zero"; pass=$((pass+1));
else echo "  FAIL a normal positional subject exited $rc: $(cat "$NORMAL/err")"; fail=$((fail+1)); fi
if [ -s "$CURL_LOG" ]; then echo "  ok   a normal positional subject still reaches curl"; pass=$((pass+1));
else echo "  FAIL curl was never invoked for a normal positional subject"; fail=$((fail+1)); fi

# --- mutation check: removing the flag guard must turn the refusal test red -
FLAG_MUTANT="$D/.notify-mutant-no-flag-guard.sh"
flag_patch_rc=0
python3 - "$NOTIFY" "$FLAG_MUTANT" <<'PY' || flag_patch_rc=$?
import re, sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
pattern = re.compile(r'case "\$\{1:-\}" in\n  --\*\)\n.*?\n    \;\;\n esac\n'.replace(' esac', 'esac'), re.S)
new_text, n = pattern.subn('', text)
assert n == 1, f"flag guard case statement not found exactly once (found {n}) -- notify.sh shape changed"
open(dst, "w").write(new_text)
PY
if [ "$flag_patch_rc" -ne 0 ]; then
  echo "  FAIL setup: patched a no-flag-guard copy of notify.sh"; fail=$((fail+1))
else
  echo "  ok   setup: patched a no-flag-guard copy of notify.sh"; pass=$((pass+1))
  MUT_FLAG="$D/mutant-flag"; mkdir -p "$MUT_FLAG/.local/state/agent-dotfiles-supervisor"
  MUT_FLAG_CURL="$MUT_FLAG/curl.log"
  HOME="$MUT_FLAG" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_LOG="$MUT_FLAG_CURL" \
    AGENT_NOTIFY_CALLER=supervisor \
    bash "$FLAG_MUTANT" "--body-file" "-" >"$MUT_FLAG/out" 2>"$MUT_FLAG/err"
  mut_rc=$?
  echo "  MUTATION: bash \"$FLAG_MUTANT\" \"--body-file\" \"-\" (guard removed) -> exit $mut_rc"
  echo "  MUTATION OUTPUT: $(cat "$MUT_FLAG/out" 2>/dev/null) $(cat "$MUT_FLAG/err" 2>/dev/null)"
  if [ "$mut_rc" -ne 2 ]; then
    echo "  ok   mutation confirmed: without the guard, a flag-shaped subject no longer exits 2 (the 'flag-shaped subject exits 2' assertion above would be red against this mutant)"
    pass=$((pass+1))
  else
    echo "  FAIL mutation confirmed: removing the guard still exited 2 -- guard coverage is decorative"
    fail=$((fail+1))
  fi
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
