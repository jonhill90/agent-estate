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
check "supervisor caller is logged as sent" "SENT telegram" "$AUTH/.local/state/agent-dotfiles-supervisor/notify.log"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
