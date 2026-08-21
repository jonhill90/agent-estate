#!/bin/bash
# agent-supervisor#104: test runs leaked long-lived inbox-poll.sh processes
# that survived SIGTERM and polluted duplicate detection. Two things were
# true at once and both are pinned here:
#
#   1. The suites that started a real poller (#57, #75, #154) either had no
#      trap at all, or a trap that fired on EXIT only -- not INT/TERM -- and
#      reaped by a bare `pkill -KILL -f`, fire-and-forget, no verification.
#   2. tests/supervisor/test_shell_suites.py, the harness that actually runs
#      every one of those suites in CI, used `subprocess.run(timeout=300)`,
#      whose timeout handling sends the suite process SIGKILL directly.
#      SIGKILL cannot be caught, so even a suite with a correct trap never
#      gets to run it if the HARNESS is what kills it.
#
# Fixing only the suites (1) leaves every one of them still exposed to (2).
# Fixing only the harness (2) does nothing for a suite whose own signal
# ultimately still reaches its stand-in poller by name or not at all. This
# file exercises both: reap-verified.sh (the primitive now used by #57, #75,
# #154) directly, and the exact SIGKILL-vs-SIGTERM asymmetry that made (2)
# the more dangerous of the two -- a suite killed the way the OLD harness
# killed it leaks; the same suite killed the way the FIXED harness kills it
# does not.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
pass=0; fail=0
say_ok()  { echo "  ok   $1"; pass=$((pass+1)); }
say_bad() { echo "  FAIL $1 -- $2"; fail=$((fail+1)); }

echo "reap-verified.sh: agent-supervisor#104 -- poller leak cleanup"

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"
  echo "  0 passed, 0 failed"
  exit 0
fi

# shellcheck source=../../scripts/supervisor/tmux-isolation.sh
source "$SUP/tmux-isolation.sh"
# shellcheck source=./lib/reap-verified.sh
source "$HERE/lib/reap-verified.sh"

RT="$(mktemp -d "${TMPDIR:-/tmp}/poller-leak-104-tmux.XXXXXX")"
OLD_TMUX="${TMUX-}"
OLD_TMUX_TMPDIR="${TMUX_TMPDIR-}"
unset TMUX
export TMUX_TMPDIR="$RT"
cleanup_all() {
  unset TMUX; export TMUX_TMPDIR="$RT"
  tmux kill-server >/dev/null 2>&1
  rm -rf "$RT"
  [ -n "$OLD_TMUX_TMPDIR" ] && export TMUX_TMPDIR="$OLD_TMUX_TMPDIR" || unset TMUX_TMPDIR
  [ -n "$OLD_TMUX" ] && export TMUX="$OLD_TMUX"
}
trap cleanup_all EXIT INT TERM

if ! assert_isolated_tmux; then
  say_bad "setup: isolated tmux socket for #104" "TMUX_TMPDIR=$TMUX_TMPDIR"
  echo "  $pass passed, $fail failed"
  exit 1
fi
say_ok "setup: isolated tmux socket for #104"

# =============================================================================
# Part 1 -- reap_pid_verified's own three properties, direct and fast.
# =============================================================================

# 1a. A healthy, cooperative process: SIGTERM alone must suffice. No KILL.
D=$(mktemp -d "$RT/cooperative.XXXXXX")
cat >"$D/poller.sh" <<'EOF'
#!/bin/bash
trap 'exit 0' TERM
while :; do sleep 0.1; done
EOF
chmod +x "$D/poller.sh"
"$D/poller.sh" & coop_pid=$!
sleep 0.3
out=$(reap_pid_verified "$coop_pid" "$D" 3 2>&1); rc=$?
if [ "$rc" -eq 0 ] && ! kill -0 "$coop_pid" 2>/dev/null && grep -q "exited on SIGTERM" <<<"$out"; then
  say_ok "a healthy process is reaped by SIGTERM alone, no escalation"
else
  say_bad "a healthy process is reaped by SIGTERM alone, no escalation" "rc=$rc alive=$(kill -0 "$coop_pid" 2>/dev/null && echo yes || echo no) out=$out"
fi

# 1b. A process that ignores TERM (agent-supervisor#104's own measured
#     symptom -- both leaked pollers needed SIGKILL): escalated, verified,
#     reported.
D=$(mktemp -d "$RT/stubborn.XXXXXX")
cat >"$D/poller.sh" <<'EOF'
#!/bin/bash
trap '' TERM
while :; do sleep 0.1; done
EOF
chmod +x "$D/poller.sh"
"$D/poller.sh" & stub_pid=$!
sleep 0.3
out=$(reap_pid_verified "$stub_pid" "$D" 2 2>&1); rc=$?
if [ "$rc" -eq 0 ] && ! kill -0 "$stub_pid" 2>/dev/null && grep -q "ignored SIGTERM, exited on SIGKILL" <<<"$out"; then
  say_ok "a process that ignores TERM is escalated to KILL, verified, and reported"
else
  say_bad "a process that ignores TERM is escalated to KILL, verified, and reported" "rc=$rc alive=$(kill -0 "$stub_pid" 2>/dev/null && echo yes || echo no) out=$out"
fi

# 1c. THE SAFETY PROPERTY -- more important than the leak itself (per #104's
#     own brief). A pid standing in for the live poller (running from
#     OUTSIDE the sandbox this reap call was given) must never be signaled,
#     even when it is sitting right there in the same pid-history file as
#     genuine litter that DOES belong to this test.
LIVE_LOOKING=$(mktemp -d "$RT/not-sandboxed.XXXXXX")
cat >"$LIVE_LOOKING/inbox-poll.sh" <<'EOF'
#!/bin/bash
trap 'exit 0' TERM
while :; do sleep 0.1; done
EOF
chmod +x "$LIVE_LOOKING/inbox-poll.sh"
"$LIVE_LOOKING/inbox-poll.sh" & live_pid=$!
sleep 0.3

SANDBOX=$(mktemp -d "$RT/mine.XXXXXX")
cat >"$SANDBOX/inbox-poll.sh" <<'EOF'
#!/bin/bash
trap 'exit 0' TERM
while :; do sleep 0.1; done
EOF
chmod +x "$SANDBOX/inbox-poll.sh"
"$SANDBOX/inbox-poll.sh" & mine_pid=$!
sleep 0.3

HIST="$RT/mixed-pid-history"
{ printf '%s\n' "$live_pid"; printf '%s\n' "$mine_pid"; } >"$HIST"
out=$(reap_pid_history_verified "$HIST" "$SANDBOX" 3 2>&1)

live_untouched=0; kill -0 "$live_pid" 2>/dev/null && live_untouched=1
mine_reaped=0; kill -0 "$mine_pid" 2>/dev/null || mine_reaped=1
refused=0; grep -q "REFUSING to signal $live_pid" <<<"$out" && refused=1

if [ "$live_untouched" -eq 1 ] && [ "$mine_reaped" -eq 1 ] && [ "$refused" -eq 1 ]; then
  say_ok "the live-looking pid is never signaled, even mixed into the same pid-history file as genuine litter, and the refusal is reported"
else
  say_bad "the live-looking pid is never signaled" \
    "live_untouched=$live_untouched(want 1) mine_reaped=$mine_reaped(want 1) refused=$refused(want 1) out=$out"
fi
kill -KILL "$live_pid" 2>/dev/null  # this test's own stand-in, not the reap primitive's job to clean up
kill -KILL "$mine_pid" 2>/dev/null  # already reaped above; belt-and-suspenders against a false pass

# =============================================================================
# Part 2 -- the harness asymmetry that made #104 possible: a suite killed the
# way test_shell_suites.py's OLD timeout handling killed it (SIGKILL, direct,
# no trap ever runs) leaks a real, tmux-hosted poller. The SAME suite killed
# the way the FIXED harness kills it (SIGTERM, the trap gets to run) does
# not. This is #104's own "red first" case, reproduced live rather than
# asserted from reading the code.
# =============================================================================
SESSION="poller-leak-104-$$"
tmux new-session -d -s "$SESSION" -x 80 -y 24 -- sleep 300

# fixture_suite <workdir> -- a minimal stand-in for #57/#75/#154's own shape:
# starts a real, tmux-hosted "poller" process and installs the SAME kind of
# verified, trap-based cleanup those suites now have.
fixture_suite() {
  local wd="$1"
  cat >"$wd/fixture.sh" <<FIXTURE
#!/bin/bash
set -uo pipefail
source "$HERE/lib/reap-verified.sh"
STAND_IN="$wd/inbox-poll.sh"
cat >"\$STAND_IN" <<'EOF'
#!/bin/bash
trap 'exit 0' TERM
while :; do sleep 0.1; done
EOF
chmod +x "\$STAND_IN"
export TMUX_TMPDIR="$RT"
tmux new-window -t "$SESSION" -n "fixture-\$\$" -d -- "\$STAND_IN"
sleep 0.3
pid=\$(pgrep -f "\$STAND_IN" | head -1)
echo "\$pid" > "$wd/pid"
cleanup() { [ -n "\$pid" ] && reap_pid_verified "\$pid" "$wd" 3; }
trap cleanup EXIT INT TERM
# The long-running part of a real suite this stands in for -- a sleep here,
# not a busy loop, so a SIGTERM landing while bash is blocked in \`wait\`
# reaches the trap exactly the way inbox-poll.sh's own header documents
# (agent-dotfiles#187) and the way test_shell_suites.py's GRACE_AFTER_TERM
# gives every real suite the chance to do.
sleep 300 &
wait \$!
FIXTURE
  chmod +x "$wd/fixture.sh"
}

# 2a. RED: killed the OLD way (SIGKILL, direct pid, no process group) --
#     the trap never runs, the poller outlives the suite.
D=$(mktemp -d "$RT/harness-red.XXXXXX")
fixture_suite "$D"
"$D/fixture.sh" >"$D/out.log" 2>&1 &
suite_pid=$!
deadline=$((SECONDS + 5))
while [ ! -s "$D/pid" ] && [ "$SECONDS" -lt "$deadline" ]; do sleep 0.1; done
red_poller_pid=$(cat "$D/pid" 2>/dev/null)
if [ -n "$red_poller_pid" ] && kill -0 "$red_poller_pid" 2>/dev/null; then
  kill -KILL "$suite_pid" 2>/dev/null   # subprocess.run(timeout=...)'s own .kill()
  wait "$suite_pid" 2>/dev/null
  sleep 1
  if kill -0 "$red_poller_pid" 2>/dev/null; then
    say_ok "RED (pre-#104 harness behaviour): SIGKILLing the suite directly leaks its poller -- the trap never ran"
  else
    say_bad "RED (pre-#104 harness behaviour): SIGKILLing the suite directly leaks its poller" \
      "poller pid $red_poller_pid is unexpectedly gone -- this case is not exercising the bug"
  fi
  reap_pid_verified "$red_poller_pid" "$D" 3 >/dev/null 2>&1  # this test's own cleanup, not the primitive under test
else
  say_bad "RED (pre-#104 harness behaviour) setup" "fixture poller never started: $(cat "$D/out.log" 2>/dev/null)"
fi

# 2b. GREEN: killed the FIXED way (SIGTERM first, grace, only escalate if
#     still alive) -- the trap runs, the poller is gone with the suite.
D=$(mktemp -d "$RT/harness-green.XXXXXX")
fixture_suite "$D"
"$D/fixture.sh" >"$D/out.log" 2>&1 &
suite_pid=$!
deadline=$((SECONDS + 5))
while [ ! -s "$D/pid" ] && [ "$SECONDS" -lt "$deadline" ]; do sleep 0.1; done
green_poller_pid=$(cat "$D/pid" 2>/dev/null)
if [ -n "$green_poller_pid" ] && kill -0 "$green_poller_pid" 2>/dev/null; then
  kill -TERM "$suite_pid" 2>/dev/null   # the fixed harness's first move
  deadline=$((SECONDS + 10))
  while [ "$SECONDS" -lt "$deadline" ] && kill -0 "$green_poller_pid" 2>/dev/null; do sleep 0.2; done
  wait "$suite_pid" 2>/dev/null
  if ! kill -0 "$green_poller_pid" 2>/dev/null; then
    say_ok "GREEN (#104-fixed harness behaviour): SIGTERM to the suite lets its own trap reap the poller -- nothing survives"
  else
    say_bad "GREEN (#104-fixed harness behaviour): SIGTERM to the suite lets its own trap reap the poller" \
      "poller pid $green_poller_pid is still alive"
    reap_pid_verified "$green_poller_pid" "$D" 3 >/dev/null 2>&1
  fi
else
  say_bad "GREEN (#104-fixed harness behaviour) setup" "fixture poller never started: $(cat "$D/out.log" 2>/dev/null)"
fi

# =============================================================================
# Part 3 -- agent-supervisor#382: the single-owner guard (inbox-poll.sh's own
# lock) and the production cleanup tool (poller-leak-cleanup.sh), both
# exercised black-box against the real scripts, not just reasoned about.
# =============================================================================

# 3a. Single-owner guard: two start attempts, one live process. Checked
# literally by counting live processes matching this fixture's own
# inbox-poll.sh path, not only by reading the refusal message.
D382=$(mktemp -d "$RT/single-owner-382.XXXXXX")
LIVE382="$D382/live"; mkdir -p "$LIVE382/scripts/supervisor"
cp "$SUP/inbox-poll.sh" "$SUP/session-defaults.sh" "$LIVE382/scripts/supervisor/"
chmod +x "$LIVE382/scripts/supervisor/inbox-poll.sh"
cat >"$LIVE382/scripts/supervisor/inbox.sh" <<'EOF'
#!/bin/bash
sleep 0.2
exit 1
EOF
cat >"$LIVE382/scripts/supervisor/director-route.sh" <<'EOF'
#!/bin/bash
exit 0
EOF
cat >"$LIVE382/scripts/supervisor/notify.sh" <<'EOF'
#!/bin/bash
exit 0
EOF
chmod +x "$LIVE382/scripts/supervisor/inbox.sh" "$LIVE382/scripts/supervisor/director-route.sh" \
  "$LIVE382/scripts/supervisor/notify.sh"
STATE382="$D382/state"; mkdir -p "$STATE382"

env SUPERVISOR_STATE="$STATE382" INBOX_POLL_BACKOFF_BASE=0 INBOX_POLL_FAIL_THRESHOLD=100000 \
  "$LIVE382/scripts/supervisor/inbox-poll.sh" "poller-owner-382-$$" >"$D382/attempt1.log" 2>&1 &
attempt1_pid=$!
deadline=$((SECONDS + 8))
while [ ! -d "$STATE382/.inbox-poll.lock" ] && [ "$SECONDS" -lt "$deadline" ]; do sleep 0.1; done

out2=$(env SUPERVISOR_STATE="$STATE382" INBOX_POLL_BACKOFF_BASE=0 timeout 5 \
  "$LIVE382/scripts/supervisor/inbox-poll.sh" "poller-owner-382-$$" 2>&1); rc2=$?
# agent-supervisor#444: raw `pgrep -f <path> | wc -l` is not a live-poller
# count -- it is a *cmdline-substring* count, and attempt1's own main loop
# forks a child (the `$(... inbox.sh)` command substitution, the
# director-route.sh call) every iteration. Between fork() and the child's
# own exec(), the kernel still shows the CHILD carrying attempt1's inherited
# argv -- the full "bash .../inbox-poll.sh poller-owner-..." command line --
# so pgrep -f matches it too, for a window measured in microseconds. This
# was reproduced live against the real, on-machine Telegram poller (not a
# fixture): a `ppid`-labelled sample caught its transient fork child
# matching the same pattern the instant before the child exec'd into `cat`/
# `date`/etc. and stopped matching. That is reading (a) from #444's brief --
# a test race in the counting method -- not reading (b): the guard's own
# refusal (rc2, the "refusing to start" message) is correct on every
# sample; only the black-box process count was unreliable. Excluding any
# match whose ppid is attempt1_pid removes exactly that transient noise
# without weakening what the count is meant to prove (that no SECOND
# process is running the poll loop) -- a real second live poller would
# never have attempt1_pid as its parent.
live_count=99
count_deadline=$((SECONDS + 3))
while [ "$SECONDS" -lt "$count_deadline" ]; do
  live_count=0
  for cand_pid in $(pgrep -f "$LIVE382/scripts/supervisor/inbox-poll.sh" 2>/dev/null); do
    cand_ppid=$(ps -o ppid= -p "$cand_pid" 2>/dev/null | tr -d ' ')
    [ "$cand_ppid" = "$attempt1_pid" ] && continue
    live_count=$((live_count + 1))
  done
  [ "$live_count" -eq 1 ] && break
  sleep 0.1
done

if [ "$rc2" -ne 0 ] && grep -qi 'refusing to start' <<<"$out2" && [ "$live_count" -eq 1 ]; then
  say_ok "single-owner guard: two start attempts yield exactly one live process (agent-supervisor#382)"
else
  say_bad "single-owner guard: two start attempts yield exactly one live process" \
    "rc2=$rc2 live_count=$live_count out2=$out2"
fi

# 3b. poller-leak-cleanup.sh, report mode: the STATE382 lock holder from 3a
# is named and excluded; an unrelated leaked fixture (never started through
# inbox-poll.sh's own lock at all) is named as a candidate. Report mode must
# never signal anything -- both are confirmed alive afterward.
LEAKDIR=$(mktemp -d "$RT/leaked-382.XXXXXX")
cat >"$LEAKDIR/inbox-poll.sh" <<'EOF'
#!/bin/bash
trap 'exit 0' TERM
while :; do sleep 0.1; done
EOF
chmod +x "$LEAKDIR/inbox-poll.sh"
"$LEAKDIR/inbox-poll.sh" & leak_pid=$!
sleep 0.3

report_out=$(SUPERVISOR_STATE="$STATE382" bash "$SUP/poller-leak-cleanup.sh" 2>&1)
protected_named=0; grep -q "pid $attempt1_pid holds .*never a cleanup target" <<<"$report_out" && protected_named=1
candidate_named=0; grep -q "CANDIDATE pid $leak_pid " <<<"$report_out" && candidate_named=1
both_alive=0; kill -0 "$attempt1_pid" 2>/dev/null && kill -0 "$leak_pid" 2>/dev/null && both_alive=1

if [ "$protected_named" -eq 1 ] && [ "$candidate_named" -eq 1 ] && [ "$both_alive" -eq 1 ]; then
  say_ok "poller-leak-cleanup.sh report mode: names the lock holder as protected and the leak as a candidate, signals neither"
else
  say_bad "poller-leak-cleanup.sh report mode: names the lock holder as protected and the leak as a candidate, signals neither" \
    "protected_named=$protected_named candidate_named=$candidate_named both_alive=$both_alive out=$report_out"
fi

# 3c. poller-leak-cleanup.sh --reap: the SAME real STATE382 lock holder and
# leaked fixture, but with pgrep replaced by a test-only stub scoped to
# exactly these two pids -- never the real machine's inbox-poll.sh
# population, so this is safe to run even while a real poller (or other
# leaked fixtures from earlier suites) happens to be alive on the host. The
# lock holder must survive untouched; the leak must be gone.
STUBBIN=$(mktemp -d "$RT/stub-bin-382.XXXXXX")
cat >"$STUBBIN/pgrep" <<STUBPGREP
#!/bin/bash
# test-only: scoped to agent-supervisor#382's own spawned pids, never the
# real machine's inbox-poll.sh population.
printf '%s\n' "$attempt1_pid" "$leak_pid"
STUBPGREP
chmod +x "$STUBBIN/pgrep"

reap_out=$(PATH="$STUBBIN:$PATH" SUPERVISOR_STATE="$STATE382" bash "$SUP/poller-leak-cleanup.sh" --reap --grace 2 2>&1)
deadline=$((SECONDS + 5))
while kill -0 "$leak_pid" 2>/dev/null && [ "$SECONDS" -lt "$deadline" ]; do sleep 0.2; done
lock_holder_survived=0; kill -0 "$attempt1_pid" 2>/dev/null && lock_holder_survived=1
leak_reaped=0; kill -0 "$leak_pid" 2>/dev/null || leak_reaped=1

if [ "$lock_holder_survived" -eq 1 ] && [ "$leak_reaped" -eq 1 ]; then
  say_ok "poller-leak-cleanup.sh --reap: the live lock holder survives untouched, the leaked candidate is reaped"
else
  say_bad "poller-leak-cleanup.sh --reap: the live lock holder survives untouched, the leaked candidate is reaped" \
    "lock_holder_survived=$lock_holder_survived(want 1) leak_reaped=$leak_reaped(want 1) out=$reap_out"
fi

reap_pid_verified "$attempt1_pid" "$LIVE382" 5 >/dev/null 2>&1
reap_pid_verified "$leak_pid" "$LEAKDIR" 3 >/dev/null 2>&1  # belt-and-suspenders if 3c's own reap failed

# 3d. agent-supervisor#387: an independent review DEMONSTRATED that
# reap_pid_verified's only safety check ("does the pid's command line
# contain the sandbox substring") was trivially satisfied by the very
# substring match that put a pid on the candidate list in the first place --
# it killed pid 33757, a real process that merely carried inbox-poll.sh's
# path as a trailing ARGUMENT and never executed it. Reproduced here the same
# way: a process that references the path without running it must now be
# EXCLUDED, and a genuinely leaked poller sitting right next to it in the
# same pgrep-stub output must still be REAPED -- both assertions required,
# because a reaper that reaps nothing is not a fix either.
D387="$RT/fix387"
mkdir -p "$D387"
STATE387=$(mktemp -d "$RT/state-387.XXXXXX")

# a process that carries inbox-poll.sh's path as an ARGUMENT it never runs --
# python3's own interpreter is what's actually executing, not inbox-poll.sh.
python3 -c "import time; time.sleep(60)" "$SUP/inbox-poll.sh" >/dev/null 2>&1 &
referencing_pid=$!

# a genuinely leaked poller: inbox-poll.sh really is what got exec'd.
LEAKDIR387=$(mktemp -d "$RT/leaked-387.XXXXXX")
cat >"$LEAKDIR387/inbox-poll.sh" <<'EOF'
#!/bin/bash
trap 'exit 0' TERM
while :; do sleep 0.1; done
EOF
chmod +x "$LEAKDIR387/inbox-poll.sh"
"$LEAKDIR387/inbox-poll.sh" & real_leak_pid=$!
sleep 0.3

STUBBIN387=$(mktemp -d "$RT/stub-bin-387.XXXXXX")
cat >"$STUBBIN387/pgrep" <<STUBPGREP387
#!/bin/bash
# test-only: scoped to agent-supervisor#387's own spawned pids, never the
# real machine's inbox-poll.sh population.
printf '%s\n' "$referencing_pid" "$real_leak_pid"
STUBPGREP387
chmod +x "$STUBBIN387/pgrep"

fix387_out=$(PATH="$STUBBIN387:$PATH" SUPERVISOR_STATE="$STATE387" bash "$SUP/poller-leak-cleanup.sh" --reap --grace 2 2>&1)
deadline=$((SECONDS + 5))
while kill -0 "$real_leak_pid" 2>/dev/null && [ "$SECONDS" -lt "$deadline" ]; do sleep 0.2; done

referencing_excluded=0
kill -0 "$referencing_pid" 2>/dev/null && grep -q "REFUSING to signal $referencing_pid" <<<"$fix387_out" && referencing_excluded=1
real_leak_reaped=0; kill -0 "$real_leak_pid" 2>/dev/null || real_leak_reaped=1

if [ "$referencing_excluded" -eq 1 ] && [ "$real_leak_reaped" -eq 1 ]; then
  say_ok "poller-leak-cleanup.sh --reap: a process merely referencing inbox-poll.sh's path is excluded, a genuinely leaked poller is still reaped (agent-supervisor#387)"
else
  say_bad "poller-leak-cleanup.sh --reap: a process merely referencing inbox-poll.sh's path is excluded, a genuinely leaked poller is still reaped (agent-supervisor#387)" \
    "referencing_excluded=$referencing_excluded(want 1) real_leak_reaped=$real_leak_reaped(want 1) out=$fix387_out"
fi

kill -KILL "$referencing_pid" 2>/dev/null
reap_pid_verified "$real_leak_pid" "$LEAKDIR387" 3 >/dev/null 2>&1  # belt-and-suspenders if 3d's own reap failed

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
