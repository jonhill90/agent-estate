#!/bin/bash
# inbox-poll.sh must route a message the instant inbox.sh returns it, and
# must never exit without telling Jon it stopped -- and must page him, not
# stay quiet, once Telegram has been unreachable for a while.
#
# agent-dotfiles#142. inbox.sh and inbox-route.sh are both stubbed out here
# (real behaviour is covered by test_inbox.sh and test_inbox_route.sh); this
# suite is only about the loop that wires them together, its heartbeat, and
# its failure reporting.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
POLL="$HERE/../../scripts/supervisor/inbox-poll.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "inbox-poll.sh"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/state"

# Stub inbox.sh: reads its scripted behaviour from $INBOX_SCRIPT, one line of
# "ok:<message>" / "ok:" (nothing new) / "fail" per call, consumed in order.
cat > "$D/bin-inbox.sh" <<'EOF'
#!/bin/bash
SCRIPT="${INBOX_SCRIPT:?}"
STATE_FILE="${INBOX_SCRIPT}.pos"
pos=$(cat "$STATE_FILE" 2>/dev/null || echo 0)
line=$(sed -n "$((pos+1))p" "$SCRIPT")
echo $((pos+1)) > "$STATE_FILE"
case "$line" in
  fail) echo "stub-inbox: telegram unreachable" >&2; exit 1 ;;
  ok:*) msg="${line#ok:}"; [ -n "$msg" ] && echo "$msg"; exit 0 ;;
  *)    exit 0 ;;
esac
EOF
chmod +x "$D/bin-inbox.sh"

# Stub inbox-route.sh: records every message it was asked to route.
cat > "$D/bin-route.sh" <<'EOF'
#!/bin/bash
echo "$1" >> "${ROUTE_LOG:?}"
exit 0
EOF
chmod +x "$D/bin-route.sh"

# Stub notify.sh: records every notification, and honours the caller gate the
# real one enforces -- a poller that forgot AGENT_NOTIFY_CALLER=supervisor
# should show up as a test failure here, not a silent pass.
cat > "$D/bin-notify.sh" <<'EOF'
#!/bin/bash
if [ "${AGENT_NOTIFY_CALLER:-}" != "supervisor" ]; then
  echo "stub-notify: refused, no caller gate" >&2
  exit 1
fi
echo "$1|$2" >> "${NOTIFY_LOG:?}"
exit 0
EOF
chmod +x "$D/bin-notify.sh"

# inbox-poll.sh resolves its collaborators as "$HERE/inbox.sh" etc, next to
# its own file -- so the fakes above have to sit beside a copy of it, not on
# PATH.
mkdir -p "$D/lane"
cp "$POLL" "$D/lane/inbox-poll.sh"
cp "$D/bin-inbox.sh" "$D/lane/inbox.sh"
cp "$D/bin-route.sh" "$D/lane/inbox-route.sh"
cp "$D/bin-notify.sh" "$D/lane/notify.sh"
chmod +x "$D/lane"/*.sh

run() {  # run <inbox-script-fixture> <iterations> [extra env...]
  local fixture="$1" iters="$2"; shift 2
  ROUTE_LOG="$D/route.log"; : > "$ROUTE_LOG"
  NOTIFY_LOG="$D/notify.log"; : > "$NOTIFY_LOG"
  rm -f "${fixture}.pos"
  HOME="$D/state" INBOX_SCRIPT="$fixture" ROUTE_LOG="$ROUTE_LOG" NOTIFY_LOG="$NOTIFY_LOG" \
    INBOX_POLL_ITERATIONS="$iters" INBOX_POLL_STATUS="$D/status" INBOX_POLL_LOG="$D/poll.log" \
    INBOX_POLL_BACKOFF_BASE=0 \
    env "$@" bash "$D/lane/inbox-poll.sh" t
}

# --- a message is routed the moment inbox.sh returns it --------------------
# agent-dotfiles#152: inbox.sh emits TEXT and the "[telegram ...]" display
# line tab-separated on one line (see inbox.sh's own usage comment). The
# production bug this fixture used to lock in was routing the WHOLE line --
# framing included -- instead of the bare reply; the fixture below models the
# real inbox.sh output shape so this test can actually catch that again.
printf 'ok:yes\t[telegram 1 from Jon] yes\n' > "$D/fixture-basic"
run "$D/fixture-basic" 1 >"$D/out1" 2>&1
[ "$(cat "$D/route.log" 2>/dev/null)" = "yes" ] && ok "the bare reply text is handed to inbox-route.sh" \
  || bad "route.log should contain only the bare reply \"yes\", not the framing" "$(cat "$D/route.log" 2>/dev/null)"

# --- nothing new: nothing routed, no notification ---------------------------
cat > "$D/fixture-empty" <<'FIX'
ok:
FIX
run "$D/fixture-empty" 1 >"$D/out2" 2>&1
[ ! -s "$D/route.log" ] && ok "nothing new -> nothing routed" || bad "routed something on an empty tick" "$(cat "$D/route.log")"
# The exit-time "poller stopped" report (asserted separately below) is
# expected on every run of this test harness, since INBOX_POLL_ITERATIONS
# always ends the loop. What must NOT appear on an ordinary empty tick is an
# outage page -- that is the thing worth failing loudly over.
grep -qi 'cannot reach telegram' "$D/notify.log" && bad "an ordinary empty tick paged an outage" "$(cat "$D/notify.log")" \
  || ok "nothing new -> no outage notification"

# --- a poller failure is reported, not silent (repeated inbox.sh failures) -
cat > "$D/fixture-fail" <<'FIX'
fail
fail
fail
FIX
run "$D/fixture-fail" 3 INBOX_POLL_FAIL_THRESHOLD=3 >"$D/out3" 2>&1
grep -qi 'cannot reach telegram' "$D/notify.log" && ok "repeated Telegram failures are reported to Jon" \
  || bad "no outage notification after threshold failures" "$(cat "$D/notify.log" 2>/dev/null)"

# One failure alone (below threshold) must NOT page -- a flapping connection
# would otherwise notify on every retry.
cat > "$D/fixture-onefail" <<'FIX'
fail
ok:
FIX
run "$D/fixture-onefail" 2 INBOX_POLL_FAIL_THRESHOLD=3 >"$D/out4" 2>&1
grep -qi 'cannot reach telegram' "$D/notify.log" && bad "paged on a single failure" "$(cat "$D/notify.log")" \
  || ok "a single failure below threshold does not page"

# --- the poller reports itself on exit --------------------------------------
cat > "$D/fixture-exit" <<'FIX'
ok:
FIX
run "$D/fixture-exit" 1 >"$D/out5" 2>&1
grep -qi 'poller stopped' "$D/notify.log" && ok "the poller reports its own exit, not silence" \
  || bad "no stop notification on exit" "$(cat "$D/notify.log" 2>/dev/null)"
[ -f "$D/status" ] && grep -q 'state:.*stopped' "$D/status" && ok "the heartbeat file records the stop" \
  || bad "no heartbeat status recorded" "$(cat "$D/status" 2>/dev/null)"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
