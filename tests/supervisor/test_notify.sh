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

# --- mutation check: the gate must still refuse an unauthorised caller -----
# agent-dotfiles#193/#52. Extending the gate to a second value is exactly
# the kind of change that is easy to "fix" into accepting everything -- a
# one-line string comparison with no test that can tell "refuses everyone
# but the two named callers" apart from "refuses nobody". Patch the case
# statement so it accepts ANY caller, then confirm the very first assertion
# in this suite ("unauthorized caller exits non-zero") goes RED. If it does
# not, this suite's gate coverage is decorative.
NOTIFY_DIR="$(cd "$(dirname "$NOTIFY")" && pwd)"
MUTANT="$NOTIFY_DIR/.notify-mutant-open-gate.sh"
trap 'rm -f "$MUTANT"' EXIT
patch_rc=0
python3 - "$NOTIFY" "$MUTANT" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''case "$CALLER" in
  supervisor|director) ;;
  *)
    log "REFUSED — caller not authorized (AGENT_NOTIFY_CALLER=${CALLER:-<unset>}): $SUBJECT${BODY:+ — $BODY}"
    echo "NOTIFY REFUSED: only an authorized caller may notify Jon (set AGENT_NOTIFY_CALLER=supervisor or director)" >&2
    exit 1
    ;;
esac'''
assert marker in text, "caller gate case statement not found -- notify.sh shape changed"
assert text.count(marker) == 1, "caller gate case statement not unique -- notify.sh shape changed"
open(dst, "w").write(text.replace(marker, 'case "$CALLER" in *) ;; esac', 1))
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

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
