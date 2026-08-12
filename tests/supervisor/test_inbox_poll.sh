#!/bin/bash
# inbox-poll.sh must route a message the instant inbox.sh returns it, and
# must never exit without telling Jon it stopped -- and must page him, not
# stay quiet, once Telegram has been unreachable for a while.
#
# agent-dotfiles#142. inbox.sh and director-route.sh are both stubbed out
# here (real behaviour is covered by test_inbox.sh and
# test_director_route.sh); this suite is only about the loop that wires them
# together, its heartbeat, and its failure reporting.
#
# agent-dotfiles#193: this poller now calls director-route.sh instead of
# inbox-route.sh for every message -- there is no more "no lane waiting"
# case to batch-suppress (#186's whole reason for existing), because there
# is always exactly one recipient (the Director) and director-route.sh
# itself durably queues before it ever risks touching a pane. The #186
# batch-summary tests this file used to carry are gone with the code path
# they tested; what replaces them below is a check that `--flush` is called
# once per loop iteration, which is the mechanism that now closes the
# "Director was busy, nobody retried" gap #186's batching used to leave.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
POLL="$HERE/../../scripts/supervisor/inbox-poll.sh"
pass=0; fail=0
# Every assertion writes to fd 3, a dup of the suite's real stdout, NOT to
# whatever stdout happens to be at the call site. run() below is invoked as
# `run ... >"$D/out1" 2>&1` at nearly every one of its twelve sites, so a
# failure reported from inside it with a plain `echo` lands in a scratch file
# nobody reads -- a bound that fires silently, which is the stated worst case
# (#213's `stderr is clean`). Caught by this change's own RUN_MAX_SECONDS=0
# mutation check: 23 failures, zero FAIL lines on stdout, until this dup.
exec 3>&1
ok()  { echo "  ok   $1" >&3; pass=$((pass+1)); }
bad() { { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; } >&3; fail=$((fail+1)); }

# agent-dotfiles#227: this suite backgrounds inbox-poll.sh a dozen times to
# exercise its SIGTERM handling, and every one of those tests used to close
# with a bare `wait "$pid"` -- no bound of its own. Under real contention
# (measured: a resource-constrained CI runner, reproduced locally by
# throttling the container to 2 CPUs) that wait can sit for minutes instead
# of the near-instant exit SIGTERM normally produces: the killed process is
# still alive and still scheduled eventually, so `wait` is not deadlocked,
# it is just waiting arbitrarily long behind everything else fighting for
# the CPU -- and CI's own 300s harness timeout has no visibility into any
# of that, so the whole suite (and every suite queued after it in the same
# `unittest discover` run) reads as a silent hang instead of a reported
# failure. reap() gives that wait a wall-clock bound: escalate to SIGKILL
# and fail LOUDLY, naming the pid and what it was waiting for, rather than
# ever blocking past REAP_MAX_SECONDS. Measured normal case (an inbox-poll.sh
# that traps SIGTERM correctly, no contention): 0/300 misses, exit in low
# tens of milliseconds -- 30s is three orders of magnitude of margin above
# that, not a tight bound being tuned to the failure.
#
# reap() alone was not enough, because it only ever bounds the
# *background-and-kill* shape. The suite's other, larger blocking surface is
# run() below, which used to invoke inbox-poll.sh in the FOREGROUND at twelve
# call sites with no bound of any kind. That surface is not hypothetical:
# measured under contention on this branch's head, one foreground run()
# invocation sat for 239 seconds before RUN_MAX_SECONDS killed it and named
# the fixture. Bounding five background waits while twelve foreground calls
# stayed unbounded showed the suite did not block in one set of runs; it did
# not establish that it could not.
#
# What this file does NOT have, deliberately: a suite-wide wall-clock budget.
# One was written here (SUITE_MAX_SECONDS=270, clamping every per-site
# deadline and aborting through a give_up()), on the argument that per-site
# bounds can sum past test_shell_suites.py's timeout=300 in the pathological
# case. Measured, it cost more than it bought: at 270s it aborted 3 of 5
# runs that were proceeding correctly -- 26, 14 and 7 green assertions deep
# -- in the same emulated 1-CPU container it had been calibrated in. The
# calibration itself was the problem. The suite's own run-to-run variance
# there was measured at 228-382s, wider than the margin the budget left, and
# that container is ~36x slower than the machine CI actually runs on, so any
# number tuned in it is tuned to the wrong box. A budget that cannot be
# calibrated where it runs is a new flake source, introduced by a change
# whose whole purpose is to make a flaky suite honest.
#
# The concern it was reaching for is real and is already owned one level up:
# test_shell_suites.py invokes this file with timeout=300, which bounds the
# suite as a whole from outside, where the harness -- not this file -- can
# see it. Arguing the suite budget back in needs a calibration measured
# where CI runs, not where the repro runs. (agent-dotfiles#227, PR #232.)
#
# Two earlier readings from this PR's history should not be cited again:
# "hung 15/15 at a 40s bound" and the review's "7/15 before, 7/15 after".
# Both are accurate as readings and wrong as conclusions -- at a 40s bound
# the fixed and unfixed trees fail at the same rate, at the same assertion,
# with identical logs, which says 40s is less than this suite needs on that
# box, not that the defect is present. The evidence this file rests on is
# the 239s foreground stall above, which is the mechanism rather than a
# symptom.
#
# Every bound here is WALL-CLOCK ($SECONDS, a deadline computed up front),
# not a count of `sleep 0.1` iterations. That distinction is not academic:
# the iteration-counting version of this patch was measured hanging in the
# same emulated 1-CPU container below for 13+ minutes without its "10s"
# bound ever firing. Under that contention each loop pass -- a `sleep` fork
# plus a `kill -0` -- costs seconds, not the nominal 0.1s, so "100
# iterations" is 100 * however-slow-the-box-is, which is exactly the
# unbounded quantity the bound exists to remove. A deadline in $SECONDS
# cannot be stretched that way.
REAP_MAX_SECONDS=${REAP_MAX_SECONDS:-30}
RUN_MAX_SECONDS=${RUN_MAX_SECONDS:-60}
STATUS_MAX_SECONDS=${STATUS_MAX_SECONDS:-15}
EXIT_MAX_SECONDS=${EXIT_MAX_SECONDS:-15}

reap() {  # reap <pid> <label> -- bounded replacement for `wait "$pid"`
  local pid="$1" label="$2" started="$SECONDS" deadline=$(( SECONDS + REAP_MAX_SECONDS ))
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      bad "$label: pid $pid still alive $((SECONDS - started))s after SIGTERM -- escalating to SIGKILL instead of hanging" ""
      kill -KILL "$pid" 2>/dev/null
      wait "$pid" 2>/dev/null
      return 1
    fi
    sleep 0.1
  done
  wait "$pid" 2>/dev/null
  return 0
}

await_status() {  # await_status <label> -- wait for the poller's heartbeat
                  # file to appear, and SAY SO if the wait expires.
  # Every one of these four waits used to fall through silently on expiry.
  # The run still went red -- some assertion below the wait would fail
  # because the status file it reads was never written -- but it went red
  # with a misleading reason, blaming its own subject for a wait that had
  # already given up. An expiry has to name itself, the way RUN_MAX_SECONDS
  # does, or the failure it produces sends the reader to the wrong place.
  local label="$1" started="$SECONDS" deadline=$(( SECONDS + STATUS_MAX_SECONDS ))
  while [ ! -s "$D/status" ] && [ "$SECONDS" -lt "$deadline" ]; do sleep 0.1; done
  [ -s "$D/status" ] && return 0
  bad "$label: the ${STATUS_MAX_SECONDS}s wait for the heartbeat file expired after $((SECONDS - started))s -- assertions below this point are reporting THAT, not their own subject" ""
  return 1
}

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

# Stub director-route.sh: records every message it was asked to route (or,
# for a `--flush` call with no message, records that a flush happened), and
# exits whatever ROUTE_EXIT says (default 0 = delivered+nudged). A message
# text prefixed RC0:/RC2:/RC1: overrides ROUTE_EXIT for just that one
# message, so a single batch can mix outcomes (some delivered, some merely
# queued) the way a real mixed drain does -- director-route.sh's own exit
# contract: 0 delivered, 2 queued but not yet live (not lost), 1 the durable
# queue write itself failed.
cat > "$D/bin-route.sh" <<'EOF'
#!/bin/bash
if [ "${1:-}" = "--flush" ]; then
  echo "FLUSH" >> "${ROUTE_LOG:?}"
  exit "${FLUSH_EXIT:-0}"
fi
echo "$1" >> "${ROUTE_LOG:?}"
case "$1" in
  RC0:*) exit 0 ;;
  RC1:*) exit 1 ;;
  RC2:*) exit 2 ;;
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
cp "$D/bin-route.sh" "$D/lane/director-route.sh"
cp "$D/bin-notify.sh" "$D/lane/notify.sh"
chmod +x "$D/lane"/*.sh

# run() is the suite's OTHER blocking shape, and the larger one: twelve of
# these against five reap() sites. It used to invoke inbox-poll.sh in the
# foreground, where the shell waits on it exactly as it waits on any
# foreground command -- no bound, no timeout, nothing reap() can help with,
# because there is no `wait "$pid"` here to replace. That is the call
# measured stalling for 239 seconds under a throttled, CPU-starved
# container. So it now backgrounds the poller and waits on it the same
# bounded way reap() does: escalate to SIGKILL and fail loudly, naming the
# fixture, at RUN_MAX_SECONDS.
# Measured normal case: every one of these twelve calls runs 1-3 iterations
# of a stubbed poller and returns in well under a second (whole suite, no
# contention: ~5s wall clock). 60s is deliberately looser than that margin
# needs to be, so it only ever fires on genuine pathology and not on a
# merely slow box: measured, the reap()-only suite needs ~210s end to end
# under the emulated 1-CPU container, so a tight per-site bound would turn a
# slow-but-correct run red for no benefit.
run() {  # run <inbox-script-fixture> <iterations> [extra env...]
  local fixture="$1" iters="$2"; shift 2
  ROUTE_LOG="$D/route.log"; : > "$ROUTE_LOG"
  NOTIFY_LOG="$D/notify.log"; : > "$NOTIFY_LOG"
  rm -f "${fixture}.pos"
  HOME="$D/state" INBOX_SCRIPT="$fixture" ROUTE_LOG="$ROUTE_LOG" NOTIFY_LOG="$NOTIFY_LOG" \
    INBOX_POLL_ITERATIONS="$iters" INBOX_POLL_STATUS="$D/status" INBOX_POLL_LOG="$D/poll.log" \
    INBOX_POLL_BACKOFF_BASE=0 \
    env "$@" bash "$D/lane/inbox-poll.sh" t &
  local pid=$! started="$SECONDS" deadline=$(( SECONDS + RUN_MAX_SECONDS ))
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      bad "run ${fixture##*/} x${iters}: pid $pid still running after $((SECONDS - started))s -- killing instead of blocking on a foreground poller that never returned" ""
      kill -KILL "$pid" 2>/dev/null
      wait "$pid" 2>/dev/null
      return 1
    fi
    sleep 0.1
  done
  wait "$pid" 2>/dev/null
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
[ "$(grep -vx FLUSH "$D/route.log" 2>/dev/null)" = "yes" ] && ok "the bare reply text is handed to director-route.sh" \
  || bad "route.log should contain only the bare reply \"yes\", not the framing" "$(cat "$D/route.log" 2>/dev/null)"
grep -q ' ROUTED: ' "$D/poll.log" 2>/dev/null && ok "a real delivery (exit 0) is logged ROUTED" \
  || bad "no ROUTED line for a real delivery" "$(cat "$D/poll.log" 2>/dev/null)"

# --- agent-dotfiles#193: a queued-not-yet-delivered reply (exit 2) is
# logged QUEUED, not ROUTED -- director-route.sh exits 2 when the Director
# was busy/blocked and the message is durably queued but not live-nudged.
# This is the expected, common outcome, not a failure -- unlike the old
# lane-routing REFUSED, it must never trigger a notify.sh call, because the
# whole point of #193 is that there is no more "nobody is listening" case.
printf 'ok:yes\t[telegram 1 from Jon] yes\n' > "$D/fixture-queued"
rm -f "$D/poll.log"
run "$D/fixture-queued" 1 ROUTE_EXIT=2 >"$D/out1b" 2>&1
grep -q ' QUEUED: ' "$D/poll.log" 2>/dev/null && ok "a queued-not-delivered reply (exit 2) is logged QUEUED, not ROUTED" \
  || bad "no QUEUED line for exit 2" "$(cat "$D/poll.log" 2>/dev/null)"
grep -q ' ROUTED: ' "$D/poll.log" 2>/dev/null && bad "a queued reply was also logged ROUTED" "$(cat "$D/poll.log" 2>/dev/null)" \
  || ok "a queued reply is never logged ROUTED"
[ -s "$D/notify.log" ] && bad "a queued (not-lost) reply triggered a notify.sh call" "$(cat "$D/notify.log")" \
  || ok "a queued reply does not page Jon -- director-route.sh already handled durability"

# --- a hard failure (exit 1: the durable queue write itself failed) is
# still ROUTE FAILED. The three-way branch must not collapse 1 and 2
# together -- exit 1 is director-route.sh's own "this really might be lost"
# case, distinct from the ordinary queued-while-busy case above.
printf 'ok:yes\t[telegram 1 from Jon] yes\n' > "$D/fixture-hardfail"
rm -f "$D/poll.log"
run "$D/fixture-hardfail" 1 ROUTE_EXIT=1 >"$D/out1c" 2>&1
grep -q ' ROUTE FAILED: ' "$D/poll.log" 2>/dev/null && ok "a hard failure (exit 1) is still logged ROUTE FAILED" \
  || bad "no ROUTE FAILED line for exit 1" "$(cat "$D/poll.log" 2>/dev/null)"
grep -qE ' (ROUTED|QUEUED): ' "$D/poll.log" 2>/dev/null && bad "a hard failure was logged as ROUTED or QUEUED" "$(cat "$D/poll.log" 2>/dev/null)" \
  || ok "a hard failure is never logged ROUTED or QUEUED"

# Mutation check: reverting director-route.sh's exit code to look like a
# delivery (0) when it was actually only queued is exactly the shape #164
# fixed for the old lane router -- with THIS poller unchanged, that
# mislogs a queued-not-delivered reply as ROUTED. Reproduced on demand
# rather than only asserted against.
rm -f "$D/poll.log"
run "$D/fixture-queued" 1 ROUTE_EXIT=0 >"$D/out1d" 2>&1
if grep -q ' ROUTED: ' "$D/poll.log" 2>/dev/null && ! grep -q ' QUEUED: ' "$D/poll.log" 2>/dev/null; then
  ok "mutation confirmed: a director-route.sh that always exits 0 mislogs a queued reply as ROUTED (the assertions above would be red)"
else
  bad "mutation confirmed: always-exit-0 director-route.sh shape" "$(cat "$D/poll.log" 2>/dev/null)"
fi

# --- nothing new: nothing routed, no notification ---------------------------
cat > "$D/fixture-empty" <<'FIX'
ok:
FIX
run "$D/fixture-empty" 1 >"$D/out2" 2>&1
# The per-iteration --flush retry (agent-dotfiles#193) still runs on an
# empty tick -- that is its whole point -- so route.log legitimately holds
# one FLUSH line here. What must NOT appear is an actual message.
[ "$(grep -vx FLUSH "$D/route.log" 2>/dev/null)" = "" ] && ok "nothing new -> nothing routed (only the standing --flush retry)" \
  || bad "routed something on an empty tick" "$(cat "$D/route.log")"
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
await_status "SIGTERM death test"
kill -TERM "$pid" 2>/dev/null
reap "$pid" "SIGTERM death test"
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
await_status "MIN_UPTIME death test"
kill -TERM "$pid" 2>/dev/null
reap "$pid" "MIN_UPTIME death test"
grep -qi 'poller stopped' "$D/notify.log" 2>/dev/null && bad "paged for a death under INBOX_POLL_MIN_UPTIME" "$(cat "$D/notify.log")" \
  || ok "a death under INBOX_POLL_MIN_UPTIME does not page"
grep -q 'state:.*stopped' "$D/status" 2>/dev/null && ok "...but the heartbeat file still records it" \
  || bad "no heartbeat status recorded" "$(cat "$D/status" 2>/dev/null)"

# --- agent-dotfiles#193: several messages in one drain each reach the
# Director independently -- no batching, no summarising, because there is
# no longer a "nobody is listening" case to suppress. Every message gets
# its own ROUTED/QUEUED/ROUTE-FAILED line and, unlike the old lane router,
# a queued (exit 2) message never itself pages Jon.
printf 'batch:RC0:m1|RC2:m2|RC0:m3\n' > "$D/fixture-batch-mixed"
rm -f "$D/poll.log"
run "$D/fixture-batch-mixed" 1 >"$D/out8" 2>&1
[ "$(grep -c ' ROUTED: ' "$D/poll.log" 2>/dev/null)" = "2" ] && ok "both delivered messages in a mixed drain are logged ROUTED" \
  || bad "wanted 2 ROUTED lines" "$(cat "$D/poll.log" 2>/dev/null)"
[ "$(grep -c ' QUEUED: ' "$D/poll.log" 2>/dev/null)" = "1" ] && ok "the queued message in the same drain is logged QUEUED" \
  || bad "wanted 1 QUEUED line" "$(cat "$D/poll.log" 2>/dev/null)"
[ ! -s "$D/notify.log" ] && ok "no-drop holds without paging Jon: nothing in a mixed drain triggers a notification" \
  || bad "a mixed drain notified Jon anyway" "$(cat "$D/notify.log" 2>/dev/null)"

# --- agent-dotfiles#193: --flush runs once per loop iteration, whether or
# not this iteration saw a new message -- this is what closes the "Director
# was busy, nobody retried until Jon wrote again" gap. Two iterations, no
# new messages in either, must still show two FLUSH calls.
rm -f "$D/poll.log"
cat > "$D/fixture-two-empty" <<'FIX'
ok:
ok:
FIX
run "$D/fixture-two-empty" 2 >"$D/out9" 2>&1
[ "$(grep -cx FLUSH "$D/route.log" 2>/dev/null)" = "2" ] && ok "director-route.sh --flush is called once per iteration, including empty ones" \
  || bad "wanted 2 --flush calls for 2 iterations" "$(cat "$D/route.log" 2>/dev/null)"

# Mutation check: removing the --flush call (so a queued message is never
# retried once the Director frees up unless Jon happens to send another
# message) must fail the assertion above -- proving it actually depends on
# the retry call, not on some other path.
MUTANT="$D/lane/inbox-poll.sh"
python3 - "$MUTANT" <<'PYEOF'
import sys
path = sys.argv[1]
text = open(path).read()
marker = '  "$HERE/director-route.sh" --flush "$SESSION" >>"$LOG" 2>&1\n'
assert text.count(marker) == 1, "flush call not found or not unique -- inbox-poll.sh shape changed"
open(path, "w").write(text.replace(marker, "", 1))
PYEOF
rm -f "$D/poll.log"
run "$D/fixture-two-empty" 2 >"$D/out10" 2>&1
if [ ! -s "$D/route.log" ] || [ "$(grep -cx FLUSH "$D/route.log" 2>/dev/null)" = "0" ]; then
  ok "mutation confirmed: removing the --flush call leaves zero retries across 2 iterations (the assertion above would be red)"
else
  bad "mutation confirmed: --flush call removal" "$(cat "$D/route.log" 2>/dev/null)"
fi
cp "$POLL" "$D/lane/inbox-poll.sh"; chmod +x "$D/lane/inbox-poll.sh"

# --- agent-dotfiles#187: SIGTERM must be trapped, not left to EXIT ---------
# The two heartbeat assertions above (SIGTERM pages, SIGTERM records
# `stopped`) were flaky, and turned this branch's CI red: an UNTRAPPED
# SIGTERM only reaches an EXIT trap when bash is waiting on a foreground
# child. When it lands while bash is in its own builtins, the default
# disposition kills the shell and the EXIT trap never runs -- no page, no
# `stopped` heartbeat. Measured on this script before the fix: 4 of 150
# SIGTERMs sent nothing at all; after trapping TERM/INT/HUP explicitly,
# 0 of 300.
#
# This assertion is structural on purpose. The behavioural version cannot be
# made deterministic: the miss rate is a few percent per kill, so no loop of
# a length worth running in CI turns a deleted trap reliably red -- it would
# just reintroduce the flake it exists to prevent. Deleting any trap line
# below turns THIS red every time, which is the regression worth catching.
# The behavioural half is the pair of assertions above, plus the short loop
# after this one.
for sig in TERM INT HUP; do
  grep -qE "^trap '.*report_stop.*' .*\b$sig\b" "$POLL" \
    && ok "inbox-poll.sh traps $sig explicitly, not only EXIT" \
    || bad "no explicit $sig trap in inbox-poll.sh" "$(grep -n '^trap ' "$POLL")"
done

# The cheap behavioural backstop: every one of a dozen SIGTERMs must page.
# Not sensitive enough to catch the race on its own (see above) -- it is here
# to catch gross breakage of the stop path, which it does deterministically.
term_misses=0
for n in $(seq 1 12); do
  rm -f "$D/notify.log" "$D/status" "$D/poll.log"; : > "$D/notify.log"
  HOME="$D/state" INBOX_SCRIPT="$D/fixture-forever" ROUTE_LOG="$D/route.log" NOTIFY_LOG="$D/notify.log" \
    INBOX_POLL_ITERATIONS=0 INBOX_POLL_STATUS="$D/status" INBOX_POLL_LOG="$D/poll.log" \
    INBOX_POLL_BACKOFF_BASE=0 INBOX_POLL_MIN_UPTIME=0 \
    bash "$D/lane/inbox-poll.sh" t >/dev/null 2>&1 &
  pid=$!
  await_status "SIGTERM loop iteration $n/12"
  kill -TERM "$pid" 2>/dev/null
  reap "$pid" "SIGTERM loop iteration $n/12"
  grep -qi 'poller stopped' "$D/notify.log" 2>/dev/null || term_misses=$((term_misses + 1))
done
[ "$term_misses" -eq 0 ] && ok "twelve consecutive SIGTERMs each paged Jon" \
  || bad "a SIGTERM died without paging Jon" "$term_misses of 12 sent nothing"

# --- agent-dotfiles#187: the status file records the running commit --------
# advance-live.sh's poller-restart check compares THIS line against LIVE's
# own head sha to decide whether the poller is stale. It has to be the real
# commit, not a guess -- a lane copy checked out to a real sha proves it.
git -C "$D/lane" init -q
git -C "$D/lane" -c user.email=t@t -c user.name=t commit -q --allow-empty -m "lane commit"
lane_sha=$(git -C "$D/lane" rev-parse HEAD)
printf 'ok:\n' > "$D/fixture-sha"
run "$D/fixture-sha" 1 >"$D/out13" 2>&1
recorded=$(grep -m1 '^sha:' "$D/status" 2>/dev/null | awk '{print $2}')
[ "$recorded" = "$lane_sha" ] && ok "the status file's sha: line matches the commit the poller is running from" \
  || bad "status sha mismatch" "recorded='$recorded' wanted='$lane_sha'"

# --- agent-dotfiles#187: a version-triggered restart is deliberate ---------
# advance-live.sh's maybe_restart_poller writes INBOX_POLL_RESTART_FLAG and
# queues a relaunch; this end only has to notice the flag, exit cleanly
# between iterations, and not page Jon about it -- the same DELIBERATE gate
# #155/#160 already built for the INBOX_POLL_ITERATIONS path above.
rm -f "$D/notify.log" "$D/status" "$D/poll.log"
: > "$D/notify.log"
RESTART_FLAG="$D/restart-flag"
rm -f "$RESTART_FLAG"
HOME="$D/state" INBOX_SCRIPT="$D/fixture-forever" ROUTE_LOG="$D/route.log" NOTIFY_LOG="$D/notify.log" \
  INBOX_POLL_ITERATIONS=0 INBOX_POLL_STATUS="$D/status" INBOX_POLL_LOG="$D/poll.log" \
  INBOX_POLL_BACKOFF_BASE=0 INBOX_POLL_MIN_UPTIME=0 INBOX_POLL_RESTART_FLAG="$RESTART_FLAG" \
  bash "$D/lane/inbox-poll.sh" t >"$D/out14" 2>&1 &
pid=$!
await_status "restart-flag test"
: > "$RESTART_FLAG"
exit_started="$SECONDS"
exit_deadline=$(( SECONDS + EXIT_MAX_SECONDS ))
while kill -0 "$pid" 2>/dev/null && [ "$SECONDS" -lt "$exit_deadline" ]; do sleep 0.1; done
if kill -0 "$pid" 2>/dev/null; then
  bad "a restart-flag request makes the poller exit on its own" "still running after $((SECONDS - exit_started))s"
  kill -TERM "$pid" 2>/dev/null; reap "$pid" "restart-flag cleanup"
else
  reap "$pid" "restart-flag exit"
  ok "a restart-flag request makes the poller exit on its own"
fi
grep -qi 'poller stopped' "$D/notify.log" 2>/dev/null && bad "a version-triggered restart paged Jon" "$(cat "$D/notify.log")" \
  || ok "a version-triggered restart does not page Jon"
[ -f "$RESTART_FLAG" ] && bad "the poller left the restart flag behind" "" \
  || ok "the poller consumes (removes) the restart flag"
grep -q 'state:.*stopped' "$D/status" 2>/dev/null && ok "the heartbeat file records the restart stop anyway" \
  || bad "no heartbeat status recorded for the restart" "$(cat "$D/status" 2>/dev/null)"
grep -qi 'version-triggered restart' "$D/poll.log" 2>/dev/null && ok "the log names it a version-triggered restart" \
  || bad "no version-triggered restart line in the log" "$(cat "$D/poll.log" 2>/dev/null)"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
