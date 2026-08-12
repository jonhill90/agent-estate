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
  # batch:<msg1>|<msg2>|... models a single getUpdates drain returning
  # several queued messages at once (agent-dotfiles#186's storm shape) --
  # every one of these lands in the SAME call to inbox-poll.sh's drain loop,
  # unlike stacking multiple "ok:" lines, which are separate iterations.
  batch:*)
    items="${line#batch:}"
    IFS='|' read -ra msgs <<<"$items"
    for m in "${msgs[@]}"; do printf '%s\t[telegram] %s\n' "$m" "$m"; done
    exit 0
    ;;
  *)    exit 0 ;;
esac
EOF
chmod +x "$D/bin-inbox.sh"

# Stub inbox-route.sh: records every message it was asked to route, and
# exits whatever ROUTE_EXIT says (default 0 = delivered) so #164's
# ROUTED/REFUSED/ROUTE-FAILED log branching can be driven from the outside.
# agent-dotfiles#186: a message text prefixed RC0:/RC2:/RC3: overrides
# ROUTE_EXIT for just that one message, so a single batch can mix outcomes
# (some delivered, some no-lane-waiting) the way a real mixed drain does.
# REQUIRE_BATCH_FLAG=1 makes the stub refuse (exit 1) unless the caller set
# INBOX_ROUTE_BATCH -- used to prove inbox-poll.sh actually asks for batched
# behaviour rather than relying on inbox-route.sh's default.
cat > "$D/bin-route.sh" <<'EOF'
#!/bin/bash
echo "$1" >> "${ROUTE_LOG:?}"
if [ "${REQUIRE_BATCH_FLAG:-}" = "1" ] && [ "${INBOX_ROUTE_BATCH:-}" != "1" ]; then
  echo "stub-route: refused, caller did not set INBOX_ROUTE_BATCH" >&2
  exit 1
fi
case "$1" in
  RC0:*) exit 0 ;;
  RC2:*) exit 2 ;;
  RC3:*) exit 3 ;;
  *) exit "${ROUTE_EXIT:-0}" ;;
esac
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
rm -f "$D/poll.log"
run "$D/fixture-basic" 1 >"$D/out1" 2>&1
[ "$(cat "$D/route.log" 2>/dev/null)" = "yes" ] && ok "the bare reply text is handed to inbox-route.sh" \
  || bad "route.log should contain only the bare reply \"yes\", not the framing" "$(cat "$D/route.log" 2>/dev/null)"
grep -q ' ROUTED: ' "$D/poll.log" 2>/dev/null && ok "a real delivery (exit 0) is logged ROUTED" \
  || bad "no ROUTED line for a real delivery" "$(cat "$D/poll.log" 2>/dev/null)"

# --- agent-dotfiles#164: a refusal (exit 2) is logged REFUSED, not ROUTED --
# inbox-route.sh exits 2 when it deliberately did not type anything anywhere
# but told Jon why (a menu refusal, zero lanes waiting, ambiguity). Before
# #164 that shared exit 0 with a real delivery and the poller logged it
# ROUTED regardless -- a message the log records as routed when it was
# deliberately not.
printf 'ok:yes\t[telegram 1 from Jon] yes\n' > "$D/fixture-refused"
rm -f "$D/poll.log"
run "$D/fixture-refused" 1 ROUTE_EXIT=2 >"$D/out1b" 2>&1
grep -q ' REFUSED: ' "$D/poll.log" 2>/dev/null && ok "a refusal (exit 2) is logged REFUSED, not ROUTED" \
  || bad "no REFUSED line for a refusal" "$(cat "$D/poll.log" 2>/dev/null)"
grep -q ' ROUTED: ' "$D/poll.log" 2>/dev/null && bad "a refusal was also logged ROUTED" "$(cat "$D/poll.log" 2>/dev/null)" \
  || ok "a refusal is never logged ROUTED"

# --- a hard failure (exit 1: could not even notify) is still ROUTE FAILED -
# The three-way branch must not collapse 1 and 2 together either -- exit 1
# means neither delivery nor notification happened, which is the one case
# that genuinely deserves "FAILED".
printf 'ok:yes\t[telegram 1 from Jon] yes\n' > "$D/fixture-hardfail"
rm -f "$D/poll.log"
run "$D/fixture-hardfail" 1 ROUTE_EXIT=1 >"$D/out1c" 2>&1
grep -q ' ROUTE FAILED: ' "$D/poll.log" 2>/dev/null && ok "a hard failure (exit 1) is still logged ROUTE FAILED" \
  || bad "no ROUTE FAILED line for exit 1" "$(cat "$D/poll.log" 2>/dev/null)"
grep -qE ' (ROUTED|REFUSED): ' "$D/poll.log" 2>/dev/null && bad "a hard failure was logged as ROUTED or REFUSED" "$(cat "$D/poll.log" 2>/dev/null)" \
  || ok "a hard failure is never logged ROUTED or REFUSED"

# Mutation check: reverting inbox-route.sh's exit code to the pre-#164 shape
# (0 for both delivered and refused) is exactly the historical bug -- with
# THIS poller unchanged, a refusal reported that way logs ROUTED, not
# REFUSED. This is the regression #164 closes, reproduced on demand rather
# than only asserted against.
rm -f "$D/poll.log"
run "$D/fixture-refused" 1 ROUTE_EXIT=0 >"$D/out1d" 2>&1
if grep -q ' ROUTED: ' "$D/poll.log" 2>/dev/null && ! grep -q ' REFUSED: ' "$D/poll.log" 2>/dev/null; then
  ok "mutation confirmed: a pre-#164-shaped inbox-route.sh (exit 0 on refusal) mislogs a refusal as ROUTED (the assertions above would be red)"
else
  bad "mutation confirmed: pre-#164 exit-0-on-refusal shape" "$(cat "$D/poll.log" 2>/dev/null)"
fi

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

# --- agent-dotfiles#186: a burst with no lane waiting sends ONE notification
# per drain, not one per message -- 23 inbound messages producing 21
# identical pings in ~21 seconds is the storm this closes. -----------------

# 1. One message, no lane waiting -> exactly one notification, same wording
# as before batching existed (nothing here should change the N=1 case).
printf 'ok:RC3:reply1\t[telegram] reply1\n' > "$D/fixture-noLane-one"
run "$D/fixture-noLane-one" 1 REQUIRE_BATCH_FLAG=1 >"$D/out8" 2>&1
[ "$(wc -l < "$D/notify.log" | tr -d ' ')" = "1" ] && ok "one no-lane message -> exactly one notification" \
  || bad "wanted exactly 1 notification for one no-lane message" "$(cat "$D/notify.log" 2>/dev/null)"
grep -q 'no lane is waiting' "$D/notify.log" 2>/dev/null && ok "the single-message notice text is unchanged" \
  || bad "no-lane notice text changed" "$(cat "$D/notify.log" 2>/dev/null)"

# 2. N messages in ONE drain, no lane waiting -> ONE notification, and it
# says N -- the actual storm shape from the issue, just smaller.
printf 'batch:RC3:m1|RC3:m2|RC3:m3|RC3:m4|RC3:m5\n' > "$D/fixture-batch5"
run "$D/fixture-batch5" 1 REQUIRE_BATCH_FLAG=1 >"$D/out9" 2>&1
[ "$(wc -l < "$D/notify.log" | tr -d ' ')" = "1" ] && ok "five no-lane messages in one drain -> exactly one notification" \
  || bad "wanted exactly 1 notification for a 5-message drain" "$(cat "$D/notify.log" 2>/dev/null)"
grep -q '5 messages received, no lane waiting' "$D/notify.log" 2>/dev/null && ok "the summary notice names the count (5)" \
  || bad "summary notice did not name the count" "$(cat "$D/notify.log" 2>/dev/null)"
[ "$(wc -l < "$D/route.log" | tr -d ' ')" = "5" ] && ok "all five messages were individually handed to inbox-route.sh" \
  || bad "not all five messages reached inbox-route.sh" "$(cat "$D/route.log" 2>/dev/null)"

# 3. Messages that DO route to a lane are unaffected -- still delivered,
# still produce no notification at all.
printf 'batch:RC0:m1|RC0:m2|RC0:m3\n' > "$D/fixture-batch-routed"
rm -f "$D/poll.log"
run "$D/fixture-batch-routed" 1 REQUIRE_BATCH_FLAG=1 >"$D/out10" 2>&1
[ ! -s "$D/notify.log" ] && ok "a batch that all routes to lanes produces no notification" \
  || bad "a fully-routed batch notified Jon anyway" "$(cat "$D/notify.log" 2>/dev/null)"
[ "$(grep -c ' ROUTED: ' "$D/poll.log" 2>/dev/null)" = "3" ] && ok "all three routed messages are logged ROUTED" \
  || bad "not all routed messages logged ROUTED" "$(cat "$D/poll.log" 2>/dev/null)"

# 4. A mixed batch (some routable, some not) notifies ONLY about the
# unroutable ones -- one summary naming just those, not the routed ones.
printf 'batch:RC0:ok1|RC3:no1|RC0:ok2|RC3:no2|RC3:no3\n' > "$D/fixture-batch-mixed"
rm -f "$D/poll.log"
run "$D/fixture-batch-mixed" 1 REQUIRE_BATCH_FLAG=1 >"$D/out11" 2>&1
[ "$(wc -l < "$D/notify.log" | tr -d ' ')" = "1" ] && ok "a mixed batch still produces exactly one notification" \
  || bad "mixed batch notification count wrong" "$(cat "$D/notify.log" 2>/dev/null)"
grep -q '3 messages received, no lane waiting' "$D/notify.log" 2>/dev/null && ok "the mixed-batch notice counts only the 3 unroutable messages" \
  || bad "mixed-batch notice did not count only the unroutable messages" "$(cat "$D/notify.log" 2>/dev/null)"

# 5. No-drop guarantee (#142) holds: every message in the mixed batch is
# either delivered (ROUTED) or accounted for (REFUSED, which the summary
# notice above covers) -- nothing simply vanishes from the log.
routed=$(grep -c ' ROUTED: ' "$D/poll.log" 2>/dev/null || true)
refused=$(grep -c ' REFUSED: ' "$D/poll.log" 2>/dev/null || true)
[ "$((routed + refused))" = "5" ] && ok "no-drop holds: all five mixed-batch messages are delivered or accounted for" \
  || bad "some mixed-batch messages were neither ROUTED nor REFUSED" "$(cat "$D/poll.log" 2>/dev/null)"

# Mutation check: removing the batch summary (so five no-lane messages send
# zero notifications instead of one) must fail the N=5 assertion above --
# proving that assertion actually depends on the summary-notify code, not on
# some other path. This is #186's mutation-check-2, aimed at this poller's
# own new code rather than at inbox-route.sh (already mutation-checked in
# test_inbox_route.sh).
MUTANT="$D/lane/inbox-poll.sh"
python3 - "$MUTANT" <<'PYEOF'
import re, sys
path = sys.argv[1]
text = open(path).read()
marker = 'if [ "$no_lane_count" -gt 0 ]; then'
assert text.count(marker) == 1, "summary-notify guard not found or not unique -- inbox-poll.sh shape changed"
start = text.index(marker)
end = text.index("\n      fi\n", start) + len("\n      fi\n")
mutated = text[:start] + "if false; then\n        :\n      fi\n" + text[end:]
open(path, "w").write(mutated)
PYEOF
run "$D/fixture-batch5" 1 REQUIRE_BATCH_FLAG=1 >"$D/out12" 2>&1
if [ ! -s "$D/notify.log" ]; then
  ok "mutation confirmed: stripping the batch summary leaves 5 no-lane messages with zero notifications (the assertion above would be red)"
else
  bad "mutation confirmed: batch-summary removal" "$(cat "$D/notify.log" 2>/dev/null)"
fi
cp "$POLL" "$D/lane/inbox-poll.sh"; chmod +x "$D/lane/inbox-poll.sh"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
