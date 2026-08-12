#!/bin/bash
# director-route.sh must always queue Jon's Telegram message for the
# Director (never lose it), and must only ever nudge the Director's live
# pane when it is unambiguously safe to do so.
#
# agent-dotfiles#193. This drives the REAL director-inbox.sh and notify.sh
# through the same tmux/curl stubs the rest of the suite uses -- nothing
# about queuing or the caller gate is reimplemented or mocked away here,
# only tmux itself (via stubs/tmux-dispatch, the same input-box-aware stub
# inbox-route.sh's own tests use) and curl (so no test touches the network).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROUTE="$HERE/../../scripts/supervisor/director-route.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "director-route.sh"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/state/.local/state/agent-dotfiles-supervisor"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"

cat > "$D/bin/curl" <<'EOF'
#!/bin/bash
echo "curl $*" >> "${CURL_LOG:-/dev/null}"
exit 0
EOF
chmod +x "$D/bin/curl"
cat > "$D/notify.env" <<'EOF'
AGENT_NOTIFY_TELEGRAM_TOKEN=fake-token
AGENT_NOTIFY_TELEGRAM_CHAT_ID=fake-chat
EOF

INBOX_BOX="$D/state/director-inbox.jsonl"

run() {  # run <lanes-fixture-file> [message-or---flush] [more args...]
  local fixture="$1"; shift
  : > "$D/tmux.log"; rm -rf "$D/panes"; mkdir -p "$D/panes"
  : > "$D/curl.log"
  PATH="$D/bin:$PATH" LANES_FIXTURE="$fixture" \
    TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
    HOME="$D/state" NOTIFY_ENV="$D/notify.env" CURL_LOG="$D/curl.log" \
    DIRECTOR_INBOX="$INBOX_BOX" \
    bash "$ROUTE" "$@" t
}

fresh_inbox() { rm -f "$INBOX_BOX" "$INBOX_BOX.lock"; }

# --- Director idle: queued AND nudged, confirmed delivered -----------------
cat > "$D/idle" <<'FIX'
1|director|claude.exe|❯ |1|0
FIX
fresh_inbox
out=$(run "$D/idle" "reply now")
rc=$?
[ "$rc" -eq 0 ] && ok "idle Director: exits 0 (queued + nudged + confirmed)" || bad "exited $rc" "$out"
grep -q '"text": "Telegram from Jon: reply now"' "$INBOX_BOX" 2>/dev/null \
  && ok "the message was durably queued in director-inbox.sh's box" \
  || bad "director-inbox.sh box does not hold the message" "$(cat "$INBOX_BOX" 2>&1)"
grep -q 'Supervisor tick' "$D/panes/1.submitted" 2>/dev/null \
  && ok "the pane received the standing /loop nudge, not Jon's raw text" \
  || bad "the nudge never reached the pane" "$(cat "$D/panes/1.submitted" 2>&1)"
grep -qF "reply now" "$D/panes/1.submitted" 2>/dev/null \
  && bad "Jon's raw message text was typed into the pane -- exactly the loop-clobber hazard" "$(cat "$D/panes/1.submitted")" \
  || ok "Jon's raw text was never typed into the pane directly"
[ -s "$D/curl.log" ] && bad "notify.sh was called even though queuing and nudging both succeeded" "$(cat "$D/curl.log")" \
  || ok "no Telegram notification sent on a clean queue+nudge"

# --- Director mid-turn ('esc to interrupt'): queued, NOT nudged ------------
cat > "$D/busy" <<'FIX'
1|director|claude.exe|⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt · ← 1 agent|1|0
FIX
fresh_inbox
out=$(run "$D/busy" "reply while busy")
rc=$?
[ "$rc" -eq 2 ] && ok "busy Director: exits 2 (queued, not lost, not nudged)" || bad "exited $rc" "$out"
grep -q '"text": "Telegram from Jon: reply while busy"' "$INBOX_BOX" 2>/dev/null \
  && ok "the message reached the durable queue despite the Director being mid-turn" \
  || bad "the message never reached the durable queue" "$(cat "$INBOX_BOX" 2>&1)"
[ ! -s "$D/panes/1.submitted" ] && ok "nothing was ever submitted to the busy pane" \
  || bad "something was submitted to a pane that was mid-turn" "$(cat "$D/panes/1.submitted")"
grep -q 'send-keys .*t:1' "$D/tmux.log" 2>/dev/null \
  && bad "send-keys was called against the busy pane at all" "$(cat "$D/tmux.log")" \
  || ok "director-route.sh never called send-keys against the busy pane"

# --- Director's box holds real unsent text: queued, NOT clobbered ----------
cat > "$D/unsent" <<'FIX'
1|director|claude.exe|❯ |1|0
FIX
fresh_inbox
: > "$D/tmux.log"; rm -rf "$D/panes"; mkdir -p "$D/panes"
printf 'something Jon or a human is mid-typing' > "$D/panes/1"
out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/unsent" TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
  HOME="$D/state" NOTIFY_ENV="$D/notify.env" CURL_LOG="$D/curl.log" DIRECTOR_INBOX="$INBOX_BOX" \
  bash "$ROUTE" "reply while typing" t)
rc=$?
[ "$rc" -eq 2 ] && ok "unsent text in the box: exits 2 (queued, box left alone)" || bad "exited $rc" "$out"
grep -qx 'something Jon or a human is mid-typing' "$D/panes/1" 2>/dev/null \
  && ok "the real unsent text in the box was never clobbered" \
  || bad "the box's real unsent text was overwritten" "$(cat "$D/panes/1" 2>&1)"

# --- Director on a menu: queued, NOT nudged (same #159 posture) ------------
cat > "$D/menu" <<'FIX'
1|director|claude.exe|Do you want to proceed?\n❯ 1. Yes\n  2. No\n Esc to cancel · Tab to amend|1|0
FIX
fresh_inbox
out=$(run "$D/menu" "reply during a menu")
rc=$?
[ "$rc" -eq 2 ] && ok "Director on a menu: exits 2 (queued, not nudged)" || bad "exited $rc" "$out"
[ ! -s "$D/panes/1.submitted" ] && ok "nothing was submitted while the Director was on a menu" \
  || bad "something was submitted while the Director was on a menu" "$(cat "$D/panes/1.submitted")"

# --- --flush: idle pane, nothing new to queue, still nudges ----------------
cat > "$D/idle2" <<'FIX'
1|director|claude.exe|❯ |1|0
FIX
: > "$D/tmux.log"; rm -rf "$D/panes"; mkdir -p "$D/panes"; : > "$D/curl.log"
out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/idle2" TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
  HOME="$D/state" NOTIFY_ENV="$D/notify.env" CURL_LOG="$D/curl.log" DIRECTOR_INBOX="$INBOX_BOX" \
  bash "$ROUTE" --flush t)
rc=$?
[ "$rc" -eq 0 ] && ok "--flush against an idle pane: exits 0 (nudged)" || bad "exited $rc" "$out"
grep -q 'Supervisor tick' "$D/panes/1.submitted" 2>/dev/null \
  && ok "--flush delivers the standing nudge with no new message to post" \
  || bad "--flush did not nudge the idle pane" "$(cat "$D/panes/1.submitted" 2>&1)"

# --- --flush: busy pane, does nothing -----------------------------------
: > "$D/tmux.log"; rm -rf "$D/panes"; mkdir -p "$D/panes"
out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/busy" TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
  HOME="$D/state" NOTIFY_ENV="$D/notify.env" CURL_LOG="$D/curl.log" DIRECTOR_INBOX="$INBOX_BOX" \
  bash "$ROUTE" --flush t)
rc=$?
[ "$rc" -eq 2 ] && ok "--flush against a busy pane: exits 2, does nothing" || bad "exited $rc" "$out"
[ ! -s "$D/panes/1.submitted" ] && ok "--flush never touches a busy pane" \
  || bad "--flush touched a busy pane" "$(cat "$D/panes/1.submitted")"

# --- durable queue write failure: reported to Jon, exit 1 ------------------
BADSTATE="$D/badstate"; mkdir -p "$BADSTATE"
# A file where director-inbox.sh's box directory would go, so mkdir -p (and
# the write itself) fails -- models the queue write genuinely failing rather
# than modelling it by removing the queue step, which the mutation check
# below does instead.
: > "$BADSTATE/not-a-directory"
: > "$D/tmux.log"; rm -rf "$D/panes"; mkdir -p "$D/panes"; : > "$D/curl.log"
out=$(PATH="$D/bin:$PATH" LANES_FIXTURE="$D/idle" TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
  HOME="$D/state" NOTIFY_ENV="$D/notify.env" CURL_LOG="$D/curl.log" \
  DIRECTOR_INBOX="$BADSTATE/not-a-directory/box.jsonl" \
  bash "$ROUTE" "will the queue write fail" t 2>&1)
rc=$?
[ "$rc" -eq 1 ] && ok "a queue write failure exits 1" || bad "exited $rc, wanted 1" "$out"
[ -s "$D/curl.log" ] && ok "Jon is notified when the durable queue write itself fails" \
  || bad "no notify.sh call when the queue write failed" "$out"
grep -q 'will the queue write fail' <<<"$out" \
  && ok "the message text is preserved in the script's own output when the queue write fails" \
  || bad "the message text was not preserved anywhere on a queue-write failure" "$out"

# --- mutation-check 3: drop the durable queue and confirm the no-drop -----
# guarantee for a busy Director goes red. The brief's own words: "drop
# whatever preserves a message across a busy Director and confirm it goes
# red." Patches OUT the `director-inbox.sh post` call while leaving the rest
# of the script intact, then re-runs the busy-Director case: with the queue
# call gone, a message arriving while the Director is mid-turn is dropped
# outright -- nothing in the box, nothing on the pane, exit 2 looks
# identical to the correct behaviour from the outside. That indistinguishability
# is exactly why the box assertion above, not the exit code, is load-bearing.
ROUTE_DIR="$(cd "$(dirname "$ROUTE")" && pwd)"
MUTANT="$ROUTE_DIR/.director-route-mutant-noqueue.sh"
trap 'rm -f "$MUTANT"' EXIT
patch_rc=0
python3 - "$ROUTE" "$MUTANT" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''if [ -n "$MESSAGE" ]; then
  if ! "$HERE/director-inbox.sh" post "Telegram from Jon: $MESSAGE" >/dev/null; then
    echo "director-route: could not queue for the Director -- message was: $MESSAGE" >&2
    if notify_jon "Telegram message could not be queued for the Director" \\
         "director-inbox.sh post failed -- message was: $MESSAGE"; then
      exit 1
    fi
    echo "director-route: could not reach Jon either -- message was: $MESSAGE" >&2
    exit 1
  fi
fi'''
assert marker in text, "queue block not found -- director-route.sh shape changed"
assert text.count(marker) == 1, "queue block not unique -- director-route.sh shape changed"
open(dst, "w").write(text.replace(marker, "", 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a queue-free copy of director-route.sh" \
    "could not patch $ROUTE (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a queue-free copy of director-route.sh"
  fresh_inbox
  : > "$D/tmux.log"; rm -rf "$D/panes"; mkdir -p "$D/panes"
  PATH="$D/bin:$PATH" LANES_FIXTURE="$D/busy" TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
    HOME="$D/state" NOTIFY_ENV="$D/notify.env" CURL_LOG="$D/curl.log" DIRECTOR_INBOX="$INBOX_BOX" \
    bash "$MUTANT" "this must not vanish" t >/dev/null 2>&1
  if [ ! -s "$INBOX_BOX" ]; then
    ok "mutation confirmed: with the queue call removed, a message to a busy Director is dropped with no record anywhere (the box assertion above would be red)"
  else
    bad "mutation confirmed: removing the queue call should drop the message" \
      "the box still holds something: $(cat "$INBOX_BOX")"
  fi
fi

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
