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

# --- agent-dotfiles#155: a deliberate, bounded run does not page Jon -------
# A run that hits INBOX_POLL_ITERATIONS and returns is only ever a test or a
# pre-flight -- production never sets ITERATIONS. It stays quiet regardless
# of INBOX_POLL_MIN_UPTIME (default 60s here, but this run finishes in well
# under a second either way). The record must still land: the status file
# and the log line are how a later reader learns the poller stopped even
# though nobody was paged about it.
cat > "$D/fixture-exit" <<'FIX'
ok:
FIX
run "$D/fixture-exit" 1 >"$D/out5" 2>&1
grep -qi 'poller stopped' "$D/notify.log" && bad "a deliberate, bounded run paged Jon" "$(cat "$D/notify.log")" \
  || ok "a deliberate, bounded run (INBOX_POLL_ITERATIONS reached) does not page Jon"
[ -f "$D/status" ] && grep -q 'state:.*stopped' "$D/status" && ok "the heartbeat file records the stop anyway" \
  || bad "no heartbeat status recorded" "$(cat "$D/status" 2>/dev/null)"

# --- agent-dotfiles#155: an unexpected death still pages Jon ---------------
# The issue's own wording is "kill -9 ... assert the alert path is taken",
# but SIGKILL cannot be caught by any process -- verified directly: a bash
# script with `trap ... EXIT` given `kill -9` never runs its trap, full stop,
# no exception. (This is also why the script's header already disclaims
# detecting SIGKILL and leans on a restart-on-crash launcher for it -- that
# limitation predates this issue and this change does not touch it.) What
# "unexpected death" can mean *inside this process* is any exit bash is
# still able to run its EXIT trap for. SIGTERM is the representative case --
# it is also what a plain `kill`, a service manager's stop, or an OOM
# killer's first pass sends -- so that is what this test sends. This test
# sets INBOX_POLL_MIN_UPTIME=0 so it is exercising the "was this deliberate"
# question in isolation from the "was this old enough" question, which gets
# its own test below.
cat > "$D/fixture-forever" <<'FIX'
FIX
rm -f "$D/notify.log" "$D/status" "$D/poll.log"
: > "$D/notify.log"
HOME="$D/state" INBOX_SCRIPT="$D/fixture-forever" ROUTE_LOG="$D/route.log" NOTIFY_LOG="$D/notify.log" \
  INBOX_POLL_ITERATIONS=0 INBOX_POLL_STATUS="$D/status" INBOX_POLL_LOG="$D/poll.log" \
  INBOX_POLL_BACKOFF_BASE=0 INBOX_POLL_MIN_UPTIME=0 \
  bash "$D/lane/inbox-poll.sh" t >"$D/out6" 2>&1 &
pid=$!
waited=0
while [ ! -s "$D/status" ] && [ "$waited" -lt 50 ]; do sleep 0.1; waited=$((waited + 1)); done
kill -TERM "$pid" 2>/dev/null
wait "$pid" 2>/dev/null
grep -qi 'poller stopped' "$D/notify.log" 2>/dev/null && ok "an unexpected (signal-terminated) death still pages Jon" \
  || bad "no stop notification after an unexpected SIGTERM" "$(cat "$D/notify.log" 2>/dev/null)"
grep -q 'state:.*stopped' "$D/status" 2>/dev/null && ok "...and the heartbeat file records it too" \
  || bad "no heartbeat status recorded after SIGTERM" "$(cat "$D/status" 2>/dev/null)"

# --- agent-dotfiles#155: a death too young to matter stays quiet, but is
# still recorded. This is the case the issue asked to be argued, not just
# implemented: a run that dies before INBOX_POLL_MIN_UPTIME (default 60s)
# was never distinguishable from a pre-flight, so it does not page -- even
# though this particular run is a real SIGTERM, not a clean stop. See the
# INBOX_POLL_MIN_UPTIME comment in inbox-poll.sh for what that trades away.
rm -f "$D/notify.log" "$D/status" "$D/poll.log"
: > "$D/notify.log"
HOME="$D/state" INBOX_SCRIPT="$D/fixture-forever" ROUTE_LOG="$D/route.log" NOTIFY_LOG="$D/notify.log" \
  INBOX_POLL_ITERATIONS=0 INBOX_POLL_STATUS="$D/status" INBOX_POLL_LOG="$D/poll.log" \
  INBOX_POLL_BACKOFF_BASE=0 INBOX_POLL_MIN_UPTIME=60 \
  bash "$D/lane/inbox-poll.sh" t >"$D/out7" 2>&1 &
pid=$!
waited=0
while [ ! -s "$D/status" ] && [ "$waited" -lt 50 ]; do sleep 0.1; waited=$((waited + 1)); done
kill -TERM "$pid" 2>/dev/null
wait "$pid" 2>/dev/null
grep -qi 'poller stopped' "$D/notify.log" 2>/dev/null && bad "paged for a death under INBOX_POLL_MIN_UPTIME" "$(cat "$D/notify.log")" \
  || ok "a death under INBOX_POLL_MIN_UPTIME does not page"
grep -q 'state:.*stopped' "$D/status" 2>/dev/null && ok "...but the heartbeat file still records it" \
  || bad "no heartbeat status recorded" "$(cat "$D/status" 2>/dev/null)"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
