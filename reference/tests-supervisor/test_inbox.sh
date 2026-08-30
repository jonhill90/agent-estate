#!/bin/bash
# Behaviour tests for inbox.sh: offset acknowledgement, --peek, the
# unreachable/empty distinction, and the lock that keeps two callers from
# racing on the same offset file (agent-dotfiles#142).
#
# curl is stubbed on PATH so no run touches the real Telegram API. The
# default stub honours `offset=` the same way Telegram does -- it returns
# only updates at or past the requested offset -- so a test that calls
# inbox.sh twice in a row is exercising the real ack/re-request cycle, not a
# canned fixture that happens to look right once.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INBOX="$HERE/../../scripts/supervisor/inbox.sh"
pass=0; fail=0
ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; fail=$((fail+1)); }

echo "inbox.sh"

D=$(mktemp -d)
mkdir -p "$D/bin" "$D/state"
cat > "$D/notify.env" <<'EOF'
AGENT_NOTIFY_TELEGRAM_TOKEN=fake-token
EOF

# Stub curl: reads the offset out of the URL (last arg) and returns only the
# two fixture updates (500, 501) at or past it, mirroring real getUpdates
# semantics. CURL_MODE switches to the failure/malformed shapes the other
# tests need.
cat > "$D/bin/curl" <<'EOF'
#!/bin/bash
url="${@: -1}"
echo "$url" >> "${CURL_LOG:-/dev/null}"
[ -n "${CURL_SLEEP:-}" ] && sleep "$CURL_SLEEP"
case "${CURL_MODE:-ok}" in
  unreachable) exit 7 ;;
  not-ok) echo '{"ok":false}'; exit 0 ;;
esac
offset=0
if [[ "$url" == *"offset="* ]]; then
  offset=$(sed -n 's/.*offset=\([0-9]*\).*/\1/p' <<<"$url")
fi
python3 - "$offset" <<'PY'
import json, sys
offset = int(sys.argv[1])
updates = [
    {"update_id": 500, "message": {"text": "hello", "chat": {"id": 1, "first_name": "Jon"}, "date": 1}},
    {"update_id": 501, "message": {"text": "world", "chat": {"id": 1, "first_name": "Jon"}, "date": 2}},
]
result = [u for u in updates if u["update_id"] >= offset]
print(json.dumps({"ok": True, "result": result}))
PY
EOF
chmod +x "$D/bin/curl"

run_inbox() { HOME="$D/state" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" "$@" bash "$INBOX"; }

# --- first call: both messages, offset advances ---------------------------
out=$(HOME="$D/state" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" bash "$INBOX" 2>"$D/err1")
rc=$?
[ "$rc" -eq 0 ] && ok "first call exits 0" || bad "first call exited $rc: $(cat "$D/err1")"
grep -q 'hello' <<<"$out" && grep -q 'world' <<<"$out" && ok "first call prints both messages" \
  || bad "first call missing a message: $out"
offset_file="$D/state/.local/state/agent-dotfiles-supervisor/.telegram-offset"
[ "$(cat "$offset_file" 2>/dev/null)" = "502" ] && ok "offset advanced to highest_id+1 (502)" \
  || bad "offset file reads $(cat "$offset_file" 2>/dev/null || echo '<missing>'), wanted 502"

# --- output is TEXT<TAB>DISPLAY, bare text first (agent-dotfiles#152) ------
# inbox-route.sh must route the bare reply, not the "[telegram ...]" framing
# -- this only works if the framing never contaminates field 1.
hello_line=$(grep 'hello' <<<"$out")
[ "$(cut -f1 <<<"$hello_line")" = "hello" ] && ok "field 1 is the bare message text, not the display framing" \
  || bad "field 1 was not the bare text" "$hello_line"
grep -q 'telegram' <<<"$(cut -f2 <<<"$hello_line")" && ok "field 2 carries the [telegram ...] display line" \
  || bad "field 2 missing the display framing" "$hello_line"

# --- second call: no duplicate delivery ------------------------------------
# This is the mutation-check case: if the ack write above were broken (offset
# never persisted, or persisted before the messages were read), this call
# would re-print hello/world instead of nothing.
out2=$(HOME="$D/state" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" bash "$INBOX" 2>"$D/err2")
rc2=$?
[ "$rc2" -eq 0 ] && [ -z "$out2" ] && ok "second call is empty — no duplicate delivery" \
  || bad "second call was not empty (duplicate delivery): rc=$rc2 out='$out2'"

# --- --peek does not acknowledge -------------------------------------------
rm -f "$offset_file"
peek_out=$(HOME="$D/state" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" bash "$INBOX" --peek 2>&1)
grep -q 'hello' <<<"$peek_out" && ok "--peek prints new messages" || bad "--peek printed nothing: $peek_out"
[ -f "$offset_file" ] && bad "--peek acknowledged (offset file written)" \
  || ok "--peek does not write the offset file"
# A real (non-peek) call afterwards still sees the messages --peek left unacked.
real_out=$(HOME="$D/state" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" bash "$INBOX" 2>&1)
grep -q 'hello' <<<"$real_out" && ok "messages --peek saw are still delivered for real" \
  || bad "a real call after --peek missed the messages: $real_out"

# --- unreachable is exit 1, distinct from "nothing new" --------------------
rm -f "$offset_file"
unreach_out=$(HOME="$D/state" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_MODE=unreachable bash "$INBOX" 2>"$D/err3")
rc3=$?
[ "$rc3" -eq 1 ] && ok "curl failure exits 1" || bad "curl failure exited $rc3"
grep -qi 'unreachable' "$D/err3" && ok "curl failure is logged as unreachable, not silence" \
  || bad "no 'unreachable' in stderr: $(cat "$D/err3")"

# --- ok:false is also exit 1 -----------------------------------------------
notok_out=$(HOME="$D/state" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_MODE=not-ok bash "$INBOX" 2>"$D/err4")
rc4=$?
[ "$rc4" -eq 1 ] && ok "ok=false exits 1" || bad "ok=false exited $rc4"

# --- missing token is exit 1 before any network call ------------------------
notoken_out=$(HOME="$D/state" PATH="$D/bin:$PATH" NOTIFY_ENV=/dev/null bash "$INBOX" 2>"$D/err5")
rc5=$?
[ "$rc5" -eq 1 ] && ok "no token configured exits 1" || bad "no-token case exited $rc5"

# --- INBOX_TIMEOUT reaches Telegram's own timeout param ---------------------
rm -f "$offset_file"
CURL_LOG="$D/curl.log"
HOME="$D/state" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_LOG="$CURL_LOG" INBOX_TIMEOUT=25 \
  bash "$INBOX" >/dev/null 2>&1
grep -q 'timeout=25' "$CURL_LOG" && ok "INBOX_TIMEOUT is passed through to getUpdates" \
  || bad "getUpdates URL missing timeout=25: $(cat "$CURL_LOG" 2>/dev/null)"

# --- two callers do not race on the offset file (the lock) -----------------
# A curl stub that sleeps before answering lets two inbox.sh calls launched
# back-to-back genuinely overlap. If the lock is doing its job, the second
# call waits its turn and sees the offset the first one already advanced --
# so across both calls each message is printed exactly once. Without the
# lock both calls could read offset=0 concurrently and both print both
# messages, or interleave their writes and lose one.
rm -f "$offset_file"
HOME="$D/state" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_SLEEP=0.3 \
  bash "$INBOX" >"$D/race1.out" 2>"$D/race1.err" &
p1=$!
HOME="$D/state" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" CURL_SLEEP=0.3 \
  bash "$INBOX" >"$D/race2.out" 2>"$D/race2.err" &
p2=$!
wait "$p1"; wait "$p2"

hello_count=$(grep -c 'hello' "$D/race1.out" "$D/race2.out" | awk -F: '{s+=$2} END{print s}')
world_count=$(grep -c 'world' "$D/race1.out" "$D/race2.out" | awk -F: '{s+=$2} END{print s}')
if [ "$hello_count" = "1" ] && [ "$world_count" = "1" ]; then
  ok "two concurrent callers deliver each message exactly once"
else
  bad "concurrent callers delivered hello=$hello_count world=$world_count (want 1 each) -- race1=$(cat "$D/race1.out") race2=$(cat "$D/race2.out")"
fi

# --- prove the offset-ack test above is load-bearing (agent-dotfiles#187) --
# #187 asked this to be mutation-checked specifically: it is the one test
# that would catch the poller-restart work silently duplicating or dropping
# Jon's replies across a restart, because a restart can only ever be safe if
# the offset write it relies on is real. Patch a copy of inbox.sh with the
# ack write turned into a no-op and confirm "second call is empty" now goes
# the other way -- re-delivering the same messages -- so the earlier
# assertion is proven to be exercising the persistence, not just agreeing
# with whatever inbox.sh happens to do.
BROKEN="$D/inbox-broken.sh"
patch_rc=0
python3 - "$INBOX" "$BROKEN" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''    if highest and not peek:
        tmp = offset_file + ".tmp"
        with open(tmp, "w") as handle:
            handle.write(str(highest + 1))
        os.replace(tmp, offset_file)'''
assert marker in text, "offset-ack block not found -- script shape changed"
assert text.count(marker) == 1, "offset-ack block not unique -- script shape changed"
text = text.replace(marker, "    pass  # agent-dotfiles#187 mutation check: offset never persisted", 1)
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched an ack-free copy of inbox.sh" "could not patch $INBOX (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched an ack-free copy of inbox.sh"
  rm -f "$offset_file"
  HOME="$D/state" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" bash "$BROKEN" >"$D/mut1.out" 2>&1
  out_m2=$(HOME="$D/state" PATH="$D/bin:$PATH" NOTIFY_ENV="$D/notify.env" bash "$BROKEN" 2>&1)
  if grep -q 'hello' <<<"$out_m2" && grep -q 'world' <<<"$out_m2"; then
    ok "mutation confirmed: an unpersisted offset re-delivers hello/world on the second call (the assertion above would now be red)"
  else
    bad "mutation confirmed: an unpersisted offset re-delivers hello/world on the second call" \
      "expected both messages again on the broken copy's second call, got: '$out_m2'"
  fi
  rm -f "$offset_file"
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
