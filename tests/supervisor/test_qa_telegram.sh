#!/bin/bash
# Behaviour tests for the QA Telegram path (agent-supervisor#138).
#
# The design is "same code path, different credential": inbox.sh and
# notify.sh are not forked for QA, they grow an AGENT_NOTIFY_MODE=qa branch
# that (a) reads a second credential from the same env file and (b) refuses
# outright when that credential is missing, rather than falling back to the
# production one. This suite proves both properties, plus the one the issue
# calls out as most likely to go wrong: a QA poller and a production poller
# must never share -- or silently interact through -- the same offset file.
#
# curl is stubbed on PATH so no run touches the real Telegram API.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INBOX="$HERE/../../scripts/supervisor/inbox.sh"
NOTIFY="$HERE/../../scripts/supervisor/notify.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; fail=$((fail+1)); }

echo "QA telegram path (#138)"

D=$(mktemp -d)
mkdir -p "$D/bin" "$D/state"

# Both credentials live in the SAME env file, as the issue requires -- the
# QA token is not a second secrets store to forget.
cat > "$D/notify.env" <<'EOF'
AGENT_NOTIFY_TELEGRAM_TOKEN=prod-token
AGENT_NOTIFY_TELEGRAM_CHAT_ID=prod-chat
AGENT_NOTIFY_TELEGRAM_TOKEN_QA=qa-token
AGENT_NOTIFY_TELEGRAM_CHAT_ID_QA=qa-chat
EOF

cat > "$D/no-qa-notify.env" <<'EOF'
AGENT_NOTIFY_TELEGRAM_TOKEN=prod-token
AGENT_NOTIFY_TELEGRAM_CHAT_ID=prod-chat
EOF

# Stub curl for inbox.sh's getUpdates: honours `offset=` like real Telegram,
# and records which bot (token, from the URL path) was actually called.
cat > "$D/bin/curl-inbox" <<'EOF'
#!/bin/bash
url="${@: -1}"
echo "$url" >> "${CURL_LOG:-/dev/null}"
offset=0
if [[ "$url" == *"offset="* ]]; then
  offset=$(sed -n 's/.*offset=\([0-9]*\).*/\1/p' <<<"$url")
fi
python3 - "$offset" <<'PY'
import json, sys
offset = int(sys.argv[1])
updates = [
    {"update_id": 700, "message": {"text": "hello", "chat": {"id": 1, "first_name": "Jon"}, "date": 1}},
    {"update_id": 701, "message": {"text": "world", "chat": {"id": 1, "first_name": "Jon"}, "date": 2}},
]
result = [u for u in updates if u["update_id"] >= offset]
print(json.dumps({"ok": True, "result": result}))
PY
EOF
chmod +x "$D/bin/curl-inbox"

# Stub curl for notify.sh's sendMessage: records the full invocation
# (including the bot token embedded in the URL) so a test can prove which
# credential was actually used.
cat > "$D/bin/curl-notify" <<'EOF'
#!/bin/bash
echo "curl called: $*" >> "${CURL_LOG:-/dev/null}"
exit 0
EOF
chmod +x "$D/bin/curl-notify"

# ============================================================================
# notify.sh: QA mode
# ============================================================================

# --- qa mode with no QA credential refuses, never falls back to prod -------
cp "$D/bin/curl-notify" "$D/bin/curl"
NOREF="$D/no-qa-refuse"; mkdir -p "$NOREF/.local/state/agent-dotfiles-supervisor"
CURL_LOG="$NOREF/curl.log"
HOME="$NOREF" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/no-qa-notify.env" CURL_LOG="$CURL_LOG" \
  AGENT_NOTIFY_CALLER=supervisor AGENT_NOTIFY_MODE=qa \
  bash "$NOTIFY" "subject" "body" >"$NOREF/out" 2>"$NOREF/err"
rc=$?
[ "$rc" -ne 0 ] && ok "notify.sh: qa mode with no QA credential exits non-zero" \
  || bad "notify.sh: qa mode with no QA credential exited 0"
[ ! -s "$CURL_LOG" ] && ok "notify.sh: qa mode with no QA credential never touches curl (no silent fallback to prod)" \
  || bad "notify.sh: curl was invoked despite no QA credential: $(cat "$CURL_LOG")"
grep -q 'REFUSED' "$NOREF/.local/state/agent-dotfiles-supervisor/notify.log" 2>/dev/null && \
  ok "notify.sh: missing-QA-credential refusal is logged" \
  || bad "notify.sh: no REFUSED line in notify.log"

# --- qa mode with a QA credential configured sends via the QA bot, not prod ---
QAOK="$D/qa-ok"; mkdir -p "$QAOK/.local/state/agent-dotfiles-supervisor"
CURL_LOG="$QAOK/curl.log"
HOME="$QAOK" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_LOG="$CURL_LOG" \
  AGENT_NOTIFY_CALLER=supervisor AGENT_NOTIFY_MODE=qa \
  bash "$NOTIFY" "subject" "body" >"$QAOK/out" 2>"$QAOK/err"
rc=$?
[ "$rc" -eq 0 ] && ok "notify.sh: qa mode with a QA credential exits zero" \
  || bad "notify.sh: qa mode with a QA credential exited $rc: $(cat "$QAOK/err")"
grep -q 'botqa-token' "$CURL_LOG" && ok "notify.sh: qa mode calls the QA bot token" \
  || bad "notify.sh: curl log does not show the QA token: $(cat "$CURL_LOG")"
grep -q 'botprod-token' "$CURL_LOG" && bad "notify.sh: qa mode leaked the production bot token" \
  || ok "notify.sh: qa mode never touches the production bot token"
grep -q 'chat_id=qa-chat' "$CURL_LOG" && ok "notify.sh: qa mode uses the QA chat id" \
  || bad "notify.sh: curl log does not show the QA chat id: $(cat "$CURL_LOG")"

# --- live (default) mode is unchanged: no AGENT_NOTIFY_MODE at all ----------
LIVE="$D/live"; mkdir -p "$LIVE/.local/state/agent-dotfiles-supervisor"
CURL_LOG="$LIVE/curl.log"
HOME="$LIVE" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_LOG="$CURL_LOG" \
  AGENT_NOTIFY_CALLER=supervisor \
  bash "$NOTIFY" "subject" "body" >"$LIVE/out" 2>"$LIVE/err"
grep -q 'botprod-token' "$CURL_LOG" && ok "notify.sh: unset AGENT_NOTIFY_MODE still uses the production bot (unchanged default)" \
  || bad "notify.sh: default mode did not use the production token: $(cat "$CURL_LOG")"

# --- mutation check: make qa mode fall back to the prod token, watch it fail ---
# The whole point of the refusal above is that it is load-bearing. Patch a
# copy of notify.sh so a missing QA credential falls back to the production
# token/chat-id instead of refusing, then confirm the "never touches curl"
# assertion's shape (curl invoked with no QA credential present) goes RED
# against the mutant -- i.e. the mutant DOES send, silently, through prod.
NOTIFY_DIR="$(cd "$(dirname "$NOTIFY")" && pwd)"
MUTANT="$NOTIFY_DIR/.notify-mutant-qa-fallback.sh"
trap 'rm -f "$MUTANT"' EXIT
patch_rc=0
python3 - "$NOTIFY" "$MUTANT" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''  qa)
    tg_token="${AGENT_NOTIFY_TELEGRAM_TOKEN_QA:-}"
    tg_chat="${AGENT_NOTIFY_TELEGRAM_CHAT_ID_QA:-}"
    if [ -z "$tg_token" ] || [ -z "$tg_chat" ]; then
      log "REFUSED — QA mode requested but no QA credential configured (AGENT_NOTIFY_TELEGRAM_TOKEN_QA/_CHAT_ID_QA): $SUBJECT${BODY:+ — $BODY}"
      echo "NOTIFY REFUSED: QA mode has no QA credential configured (set AGENT_NOTIFY_TELEGRAM_TOKEN_QA and AGENT_NOTIFY_TELEGRAM_CHAT_ID_QA) -- refusing rather than falling back to the production bot" >&2
      exit 1
    fi
    ;;'''
assert marker in text, "qa refusal block not found -- notify.sh shape changed"
assert text.count(marker) == 1, "qa refusal block not unique -- notify.sh shape changed"
mutant = '''  qa)
    tg_token="${AGENT_NOTIFY_TELEGRAM_TOKEN_QA:-${AGENT_NOTIFY_TELEGRAM_TOKEN:-}}"
    tg_chat="${AGENT_NOTIFY_TELEGRAM_CHAT_ID_QA:-${AGENT_NOTIFY_TELEGRAM_CHAT_ID:-}}"
    ;;'''
open(dst, "w").write(text.replace(marker, mutant, 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a fallback-on-missing-QA-credential copy of notify.sh"
else
  ok "setup: patched a fallback-on-missing-QA-credential copy of notify.sh"
  MUT="$D/mutant-qa"; mkdir -p "$MUT/.local/state/agent-dotfiles-supervisor"
  MUT_LOG="$MUT/curl.log"
  HOME="$MUT" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/no-qa-notify.env" CURL_LOG="$MUT_LOG" \
    AGENT_NOTIFY_CALLER=supervisor AGENT_NOTIFY_MODE=qa \
    bash "$MUTANT" "subject" "body" >"$MUT/out" 2>"$MUT/err"
  mut_rc=$?
  echo "  MUTATION: qa mode, no QA credential, fallback-patched notify.sh -> exit $mut_rc, curl: $(cat "$MUT_LOG" 2>/dev/null)"
  if [ "$mut_rc" -eq 0 ] && grep -q 'botprod-token' "$MUT_LOG"; then
    ok "mutation confirmed: a fallback-on-missing-QA-credential notify.sh silently pages Jon through the production bot (the refusal assertions above would be red)"
  else
    bad "mutation confirmed: fallback silently uses the production bot" \
      "expected exit 0 and a botprod-token curl call, got rc=$mut_rc log='$(cat "$MUT_LOG" 2>/dev/null)'"
  fi
fi

# ============================================================================
# inbox.sh: QA mode and offset isolation
# ============================================================================

cp "$D/bin/curl-inbox" "$D/bin/curl"

# --- qa mode with no QA token refuses before any network call --------------
STATE_A="$D/state-a"; mkdir -p "$STATE_A"
out=$(HOME="$D/state" SUPERVISOR_STATE="$STATE_A" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/no-qa-notify.env" \
  AGENT_NOTIFY_MODE=qa CURL_LOG="$STATE_A/curl.log" bash "$INBOX" 2>"$STATE_A/err")
rc=$?
[ "$rc" -eq 1 ] && ok "inbox.sh: qa mode with no QA token exits 1" \
  || bad "inbox.sh: qa mode with no QA token exited $rc"
[ ! -s "$STATE_A/curl.log" ] && ok "inbox.sh: qa mode with no QA token never calls Telegram" \
  || bad "inbox.sh: curl was invoked despite no QA token: $(cat "$STATE_A/curl.log")"
grep -qi 'refus' "$STATE_A/err" && ok "inbox.sh: missing-QA-token refusal is stated, not silent" \
  || bad "inbox.sh: no refusal message on stderr: $(cat "$STATE_A/err")"

# --- offset isolation: a QA poll does not touch the production offset file,
#     and does not consume/advance past what the production poller has
#     already acked -- each mode gets its own, independent view of the feed.
STATE_B="$D/state-b"; mkdir -p "$STATE_B"
prod_offset="$STATE_B/.telegram-offset"
qa_offset="$STATE_B/.telegram-offset-qa"

# Production consumes both messages first, advancing ITS offset only.
HOME="$D/state" SUPERVISOR_STATE="$STATE_B" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" \
  bash "$INBOX" >"$STATE_B/prod1.out" 2>"$STATE_B/prod1.err"
[ "$(cat "$prod_offset" 2>/dev/null)" = "702" ] && ok "inbox.sh: production offset advances to 702 after consuming both messages" \
  || bad "inbox.sh: production offset reads $(cat "$prod_offset" 2>/dev/null || echo '<missing>'), wanted 702"
[ ! -e "$qa_offset" ] && ok "inbox.sh: a production-only run never creates a QA offset file" \
  || bad "inbox.sh: a QA offset file appeared after a production-only run"

# QA, run afterwards against the SAME state dir, still sees both messages --
# it was never gated by the offset production already advanced.
qa_out=$(HOME="$D/state" SUPERVISOR_STATE="$STATE_B" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" \
  AGENT_NOTIFY_MODE=qa bash "$INBOX" 2>"$STATE_B/qa1.err")
grep -q 'hello' <<<"$qa_out" && grep -q 'world' <<<"$qa_out" && \
  ok "inbox.sh: a QA poll is not blocked by the production offset -- still sees both messages" \
  || bad "inbox.sh: QA poll missed messages the production offset had already consumed: '$qa_out'"
[ "$(cat "$qa_offset" 2>/dev/null)" = "702" ] && ok "inbox.sh: QA poll advances its OWN offset file to 702" \
  || bad "inbox.sh: QA offset file reads $(cat "$qa_offset" 2>/dev/null || echo '<missing>'), wanted 702"
[ "$(cat "$prod_offset" 2>/dev/null)" = "702" ] && ok "inbox.sh: the QA poll did not further advance (or reset) the production offset file" \
  || bad "inbox.sh: production offset file was mutated by a QA poll: $(cat "$prod_offset" 2>/dev/null)"

# A second QA call sees nothing new -- QA's own ack is real, not a no-op.
qa_out2=$(HOME="$D/state" SUPERVISOR_STATE="$STATE_B" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" \
  AGENT_NOTIFY_MODE=qa bash "$INBOX" 2>"$STATE_B/qa2.err")
[ -z "$qa_out2" ] && ok "inbox.sh: a second QA poll is empty -- QA's own ack persisted" \
  || bad "inbox.sh: second QA poll re-delivered messages: '$qa_out2'"

# --- mutation check: make qa mode share the production offset file, watch
#     the isolation assertion above go red. This is the acceptance criterion
#     the issue calls "the thing most likely to go wrong" -- break it on
#     purpose and confirm the suite notices.
INBOX_DIR="$(cd "$(dirname "$INBOX")" && pwd)"
MUTANT_INBOX="$INBOX_DIR/.inbox-mutant-shared-offset.sh"
trap 'rm -f "$MUTANT_INBOX"' EXIT
patch_rc=0
python3 - "$INBOX" "$MUTANT_INBOX" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''case "$MODE" in
  qa) DEFAULT_OFFSET="$STATE/.telegram-offset-qa" ;;
  *)  DEFAULT_OFFSET="$STATE/.telegram-offset" ;;
esac'''
assert marker in text, "MODE-based offset selection not found -- inbox.sh shape changed"
assert text.count(marker) == 1, "MODE-based offset selection not unique -- inbox.sh shape changed"
mutant = 'DEFAULT_OFFSET="$STATE/.telegram-offset"  # mutation: qa no longer gets its own offset file'
open(dst, "w").write(text.replace(marker, mutant, 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a shared-offset copy of inbox.sh"
else
  ok "setup: patched a shared-offset copy of inbox.sh"
  STATE_C="$D/state-c"; mkdir -p "$STATE_C"
  shared_offset="$STATE_C/.telegram-offset"
  HOME="$D/state" SUPERVISOR_STATE="$STATE_C" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" \
    bash "$MUTANT_INBOX" >"$STATE_C/prod.out" 2>&1
  mut_qa_out=$(HOME="$D/state" SUPERVISOR_STATE="$STATE_C" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" \
    AGENT_NOTIFY_MODE=qa bash "$MUTANT_INBOX" 2>&1)
  echo "  MUTATION: shared-offset inbox.sh, production poll then qa poll -> qa saw: '$mut_qa_out'"
  if [ -z "$mut_qa_out" ]; then
    ok "mutation confirmed: sharing the offset file makes a QA poll silently see nothing after production already acked (the isolation assertion above would be red)"
  else
    bad "mutation confirmed: sharing the offset file starves the QA poll" \
      "expected the QA poll to come back empty once offsets are shared, got: '$mut_qa_out'"
  fi
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
