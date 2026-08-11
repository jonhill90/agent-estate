#!/bin/bash
# Behaviour tests for watchdog.sh using stub tmux/gh binaries.
#
# These exist because three bugs shipped in this script for want of a test:
# an inverted ghost-text comparison, a failed `gh` query counted as zero work,
# and a /loop delivered into a busy pane where it queues as inert plain text.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCHDOG="$HERE/../../scripts/supervisor/watchdog.sh"
STUBS="$HERE/stubs"
pass=0; fail=0
check() { # check <name> <expected-substring> <file>
  if grep -q "$2" "$3" 2>/dev/null; then echo "  ok   $1"; pass=$((pass+1));
  else echo "  FAIL $1 — expected '$2' in $(cat "$3" 2>/dev/null | tr '\n' ' ')"; fail=$((fail+1)); fi
}
run() { # run <state> <workdir>
  # An empty transcript dir by default: sleepcheck finds no pending wakeup, so
  # these tests exercise the watchdog's own decisions rather than the live
  # supervisor's sleep state. Without this the suite passes or fails depending
  # on whether the real loop happens to be asleep when it runs.
  rm -rf "$2"; mkdir -p "$2" "$2/transcripts"
  SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE="$1" STUB_SENT="$2/sent" \
  STUB_BUSY_AFTER="${STUB_BUSY_AFTER:-}" STUB_COUNTER="$2/counter" \
  SUPERVISOR_STATE="$2" SUPERVISOR_STATUS="$2/st" SUPERVISOR_LOG="$2/lg" \
  SUPERVISOR_STAMP="$2/stamp" SUPERVISOR_HISTORY="$2/hist" NOTIFY_ENV="$2/none.env" \
  SLEEPCHECK_DIR="${STUB_SLEEPCHECK_DIR:-$2/transcripts}" \
  bash "$WATCHDOG" >/dev/null 2>"$2/err"
}

echo "watchdog.sh"

# A busy supervisor is working, not dead. Nothing may be sent to it.
D=$(mktemp -d); run busy "$D/w"
check "busy pane reports working" "state:    working" "$D/w/st"
[ ! -s "$D/w/sent" ] && { echo "  ok   busy pane receives no keystrokes"; pass=$((pass+1)); } \
                     || { echo "  FAIL busy pane was sent: $(cat "$D/w/sent")"; fail=$((fail+1)); }
# A healthy tick carries no notify: line -- nothing was sent, so there is no
# send outcome to report. Asserted because the state: line alone does not
# prove the ordinary write path ran: while `notify:` was being added, a false
# test as the last command in the status group made every FIRST write fail
# ("WATCHDOG CANNOT WRITE STATUS") and only the failure-path rewrite produced
# a file at all -- and this suite stayed green through it.
if grep -q '^notify:' "$D/w/st" 2>/dev/null; then
  echo "  FAIL a healthy tick reported a notify outcome: $(grep '^notify:' "$D/w/st")"; fail=$((fail+1))
else
  echo "  ok   a healthy tick writes status with no notify: line"; pass=$((pass+1))
fi
if grep -q 'CANNOT WRITE STATUS' "$D/w/err" 2>/dev/null; then
  echo "  FAIL the first status write failed and was papered over"; fail=$((fail+1))
else
  echo "  ok   the first status write is the one that lands"; pass=$((pass+1))
fi

# An idle pane with work is a dead loop: restart it, and the /loop must
# actually be delivered.
D=$(mktemp -d); run idle "$D/w"
check "idle pane with work restarts" "state:    restarted" "$D/w/st"
check "restart delivers a /loop"     "/loop" "$D/w/sent"

# The race: idle when first checked, busy by the time the /loop is sent.
# Without the pre-send guard the command is queued as plain text, never
# parses as a slash command, and the loop silently never re-arms.
D=$(mktemp -d); STUB_BUSY_AFTER=1 run idle "$D/w"
check "pane that turns busy mid-probe is not sent to" "state:    working" "$D/w/st"
if grep -q '/loop' "$D/w/sent" 2>/dev/null; then
  echo "  FAIL a /loop was delivered into a busy pane"; fail=$((fail+1))
else
  echo "  ok   no /loop delivered into a busy pane"; pass=$((pass+1))
fi

# A loop with a pending wakeup is asleep, not dead. The watchdog must leave it
# alone even though the pane is idle and there is queued work -- restarting a
# sleeping loop is what churned the supervisor all night before #59.
D=$(mktemp -d); mkdir -p "$D/sleeping"
python3 - "$D/sleeping/t.jsonl" <<'PYEOF'
import json, sys, datetime
stamp = (datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(seconds=60)).strftime("%Y-%m-%dT%H:%M:%S.000Z")
rec = {"timestamp": stamp, "message": {"content": [
    {"type": "tool_use", "name": "ScheduleWakeup", "input": {"delaySeconds": 3600}}]}}
open(sys.argv[1], "w").write(json.dumps(rec) + "\n")
PYEOF
STUB_SLEEPCHECK_DIR="$D/sleeping" run idle "$D/w"
check "a sleeping loop is left alone" "state:    asleep" "$D/w/st"
if grep -q '/loop' "$D/w/sent" 2>/dev/null; then
  echo "  FAIL a sleeping loop was restarted"; fail=$((fail+1))
else
  echo "  ok   a sleeping loop receives no keystrokes"; pass=$((pass+1))
fi

# The status file must name the code that produced it. The LaunchAgent runs
# this script from the repo working tree, so the live guard is whatever branch
# is checked out -- an invisible dependency until it is printed.
D=$(mktemp -d); run idle "$D/w"
check "status names the running branch and sha" "^code:" "$D/w/st"

# A missing state directory used to make every status write fail silently:
# exit 0, and watchdog.status quietly stops updating -- indistinguishable from
# a dead cron, which is the condition this tool exists to detect.
D=$(mktemp -d); rm -rf "$D/absent"
SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=busy \
SUPERVISOR_STATE="$D/absent" SLEEPCHECK_DIR="$D/none" \
  bash "$WATCHDOG" >/dev/null 2>&1
if [ -f "$D/absent/watchdog.status" ]; then
  echo "  ok   a missing state directory is created, not failed into silently"; pass=$((pass+1))
else
  echo "  FAIL status was not written when the state directory was absent"; fail=$((fail+1))
fi

# The live copy runs from a DETACHED worktree, so 'git rev-parse --abbrev-ref'
# returns the literal "HEAD". Reporting that would make the provenance line
# useless exactly where it matters most.
D=$(mktemp -d); mkdir -p "$D/gitstub" "$D/w"
cat > "$D/gitstub/git" <<'GITEOF'
#!/bin/bash
for a in "$@"; do
  case "$a" in
    --abbrev-ref) echo "HEAD"; exit 0 ;;
    --points-at)  echo "main"; exit 0 ;;
    --short)      echo "deadbee"; exit 0 ;;
  esac
done
exit 0
GITEOF
chmod +x "$D/gitstub/git"
SUPERVISOR_PATH="$D/gitstub:$STUBS:/usr/bin:/bin" STUB_PANE_STATE=busy \
SUPERVISOR_STATE="$D/w" SLEEPCHECK_DIR="$D/none" \
  bash "$WATCHDOG" >/dev/null 2>&1
check "a detached worktree reports a real ref, not HEAD" "^code: *main" "$D/w/watchdog.status"

# --- escalation must survive an unreachable channel (#91) ------------------
# The one path that reaches a human, driven end to end through watchdog.sh
# with a STUB notifier -- never a real channel. Tick 1's notifier exits 1;
# tick 2's works and must still be called. Marking the episode notified on
# *attempt* meant tick 2 was deduped away and Jon was never paged.
escalate_run() { # escalate_run <workdir> <notify-script>
  rm -rf "$1"; mkdir -p "$1" "$1/transcripts"
  # MAX_RESTARTS restarts already inside ESCALATE_WINDOW -> the escalate branch.
  now=$(date +%s)
  for i in 1 2 3; do echo $((now - 60)); done > "$1/hist"
  SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=idle STUB_SENT="$1/sent" \
  SUPERVISOR_STATE="$1" SUPERVISOR_STATUS="$1/st" SUPERVISOR_LOG="$1/lg" \
  SUPERVISOR_STAMP="$1/stamp" SUPERVISOR_HISTORY="$1/hist" NOTIFY_ENV="$1/none.env" \
  SLEEPCHECK_DIR="$1/transcripts" NOTIFY_SCRIPT="$2" \
  bash "$WATCHDOG" >/dev/null 2>&1
}

D=$(mktemp -d)
cat > "$D/down.sh" <<'EOF'
#!/bin/bash
echo "attempted" >> "$(dirname "$0")/down-calls"
echo "no channel reachable" >&2
exit 1
EOF
cat > "$D/up.sh" <<'EOF'
#!/bin/bash
echo "$1|$2" >> "$(dirname "$0")/up-calls"
EOF
chmod +x "$D/down.sh" "$D/up.sh"

# Tick 1: escalate, channel down.
escalate_run "$D/w" "$D/down.sh"
check "escalate with a dead channel still reports escalate" "state:    escalate" "$D/w/st"
check "the failed send is named in watchdog.status" "^notify: *FAILED" "$D/w/st"
check "the failed send is named in the notify log" "NOTIFY-FAILED" "$D/w/watchdog-notify.log"
check "the notify log says a retry is coming" "will retry" "$D/w/watchdog-notify.log"
if grep -q '"notified": *false' "$D/w/.watchdog-escalate-episode.json" 2>/dev/null; then
  echo "  ok   a failed send does not consume the escalation episode"; pass=$((pass+1))
else
  echo "  FAIL episode marked notified after a failed send: $(cat "$D/w/.watchdog-escalate-episode.json" 2>/dev/null)"; fail=$((fail+1))
fi

# Tick 2: same escalation, channel back. The state dir is kept, so the episode
# flag written by tick 1 is the one this tick reads -- which is the whole bug.
SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=idle STUB_SENT="$D/w/sent" \
SUPERVISOR_STATE="$D/w" SUPERVISOR_STATUS="$D/w/st" SUPERVISOR_LOG="$D/w/lg" \
SUPERVISOR_STAMP="$D/w/stamp" SUPERVISOR_HISTORY="$D/w/hist" NOTIFY_ENV="$D/w/none.env" \
SLEEPCHECK_DIR="$D/w/transcripts" NOTIFY_SCRIPT="$D/up.sh" \
  bash "$WATCHDOG" >/dev/null 2>&1
check "the next tick retries the send"        "Supervisor escalation" "$D/up-calls"
check "and records that it was delivered"     "NOTIFY-SENT" "$D/w/watchdog-notify.log"

# Tick 3: delivered, so dedup takes over. One page per episode, not a burst.
SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=idle STUB_SENT="$D/w/sent" \
SUPERVISOR_STATE="$D/w" SUPERVISOR_STATUS="$D/w/st" SUPERVISOR_LOG="$D/w/lg" \
SUPERVISOR_STAMP="$D/w/stamp" SUPERVISOR_HISTORY="$D/w/hist" NOTIFY_ENV="$D/w/none.env" \
SLEEPCHECK_DIR="$D/w/transcripts" NOTIFY_SCRIPT="$D/up.sh" \
  bash "$WATCHDOG" >/dev/null 2>&1
if [ "$(wc -l < "$D/up-calls" | tr -d ' ')" = 1 ]; then
  echo "  ok   a delivered escalation is not re-sent every tick"; pass=$((pass+1))
else
  echo "  FAIL escalation sent $(wc -l < "$D/up-calls") times: $(cat "$D/up-calls")"; fail=$((fail+1))
fi


# --- the live copy is pinned, and staleness must be visible (#99) ----------
#
# The LaunchAgent runs watchdog.sh from a detached worktree that NOTHING in this
# repository updates, so a merged fix can sit on main while the live copy keeps
# running the old one. `code: detached @ <sha>` reads exactly as healthy as a
# current sha unless the reader already knows what main is.
#
# This needs its own git repository: the count is computed from the directory
# holding watchdog.sh, which for every other test in this file is the real
# checkout, whose relationship to origin/main is whatever the working session
# happens to be doing. A test that depends on that is not a test.
G="$D/gitrepo"; mkdir -p "$G/scripts/supervisor"
cp "$HERE/../../scripts/supervisor/watchdog.sh" "$G/scripts/supervisor/"
for dep in sleepcheck.py watchdog_notify.py loop-tick.md; do
  cp "$HERE/../../scripts/supervisor/$dep" "$G/scripts/supervisor/" 2>/dev/null
done
git -C "$G" init -q
git -C "$G" config user.email t@e.com; git -C "$G" config user.name T
git -C "$G" add -A >/dev/null 2>&1
git -C "$G" commit -q -m "live copy"

wd_run() { # runs the COPY, not this repository's own watchdog.sh
  rm -rf "$1"; mkdir -p "$1" "$1/transcripts"
  SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=idle STUB_SENT="$1/sent" \
  STUB_COUNTER="$1/counter" \
  SUPERVISOR_STATE="$1" SUPERVISOR_STATUS="$1/st" SUPERVISOR_LOG="$1/lg" \
  SUPERVISOR_STAMP="$1/stamp" SUPERVISOR_HISTORY="$1/hist" NOTIFY_ENV="$1/none.env" \
  SLEEPCHECK_DIR="$1/transcripts" \
  bash "$G/scripts/supervisor/watchdog.sh" >/dev/null 2>"$1/err"
}

git -C "$G" update-ref refs/remotes/origin/main HEAD
wd_run "$D/wcur"
if grep -q "behind origin/main" "$D/wcur/st" 2>/dev/null; then
  echo "  FAIL a current live copy is not labelled stale"; fail=$((fail+1))
else
  echo "  ok   a current live copy is not labelled stale"; pass=$((pass+1))
fi

# Advance origin/main past the live copy, exactly as a merge does.
echo newer > "$G/newfile"
git -C "$G" add -A >/dev/null 2>&1
git -C "$G" commit -q -m "merged after the live copy was pinned"
git -C "$G" update-ref refs/remotes/origin/main HEAD
git -C "$G" checkout -q HEAD~1 2>/dev/null
wd_run "$D/wstale"
if grep -q "1 behind origin/main" "$D/wstale/st" 2>/dev/null; then
  echo "  ok   a stale live copy says how far behind it is"; pass=$((pass+1))
else
  echo "  FAIL a stale live copy says how far behind it is"; fail=$((fail+1))
  sed 's/^/       /' "$D/wstale/st" 2>/dev/null
fi
# No fetch happens, so a zero is not proof of freshness. The wording must say
# the was not refetched by this check rather than implying a currency it cannot check.
if grep -q "not refetched by this check" "$D/wstale/st" 2>/dev/null; then
  echo "  ok   the status says the comparison was not refetched by this check"; pass=$((pass+1))
else
  echo "  FAIL the status says the comparison was not refetched by this check"; fail=$((fail+1))
fi


# An UNREADABLE comparison must not read as "up to date". The first version of
# this check wrote `''|0) code_note=""`, lumping a failed git call in with a
# genuine zero -- the exact defect the line exists to prevent, inside the fix
# for it. Caught in review before merge.
#
# Delete origin/main entirely: rev-list fails, `behind` is empty, and a status
# with no note would claim currency the check never established.
git -C "$G" update-ref -d refs/remotes/origin/main 2>/dev/null
wd_run "$D/wnoref"
if grep -q "cannot compare" "$D/wnoref/st" 2>/dev/null; then
  echo "  ok   an unreadable comparison says so instead of reading as current"; pass=$((pass+1))
else
  echo "  FAIL an unreadable comparison says so instead of reading as current"; fail=$((fail+1))
  sed 's/^/       /' "$D/wnoref/st" 2>/dev/null
fi
if grep -q "behind origin/main" "$D/wnoref/st" 2>/dev/null; then
  echo "  FAIL an unreadable comparison does not claim a behind-count"; fail=$((fail+1))
else
  echo "  ok   an unreadable comparison does not claim a behind-count"; pass=$((pass+1))
fi


# --- the watchdog advances the live copy it runs from (#130) ---------------
#
# Detecting the drift and being unable to fix it put the fixer in the loop --
# the component that goes down, and that is down BY DESIGN during an
# escalation. These cases drive the real watchdog.sh and the real
# advance-live.sh against a THROWAWAY live worktree; never the estate's own,
# because a test that corrupts the live copy takes the guard down with it.
A=$(mktemp -d)
git init -q --bare "$A/origin.git"
git clone -q "$A/origin.git" "$A/src" 2>/dev/null
SRC="$A/src"
git -C "$SRC" config user.email t@e.com; git -C "$SRC" config user.name T
git -C "$SRC" checkout -q -b main
mkdir -p "$SRC/scripts/supervisor"
for f in watchdog.sh advance-live.sh sleepcheck.py watchdog_notify.py loop-tick.md; do
  cp "$HERE/../../scripts/supervisor/$f" "$SRC/scripts/supervisor/"
done
# A tracked file no later commit here touches, so an uncommitted edit to it is
# the non-conflicting shape `git checkout --detach` carries silently forward --
# what makes advance-live.sh's dirty refusal load-bearing rather than a
# courtesy.
echo baseline >"$SRC/untouched.txt"
git -C "$SRC" add -A >/dev/null 2>&1
git -C "$SRC" commit -q -m "first"
git -C "$SRC" push -q -u origin main
LIVE="$A/live"
git -C "$SRC" worktree add -q --detach "$LIVE" origin/main

adv_commit() { # adv_commit <text> -- one more commit on origin/main
  echo "$1" >"$SRC/marker.txt"
  git -C "$SRC" add -A >/dev/null 2>&1
  git -C "$SRC" commit -q -m "$1"
  git -C "$SRC" push -q origin main
  git -C "$LIVE" fetch -q origin main
}
adv_run() { # adv_run <state-dir> [pane-state] [notify-script]
  rm -rf "$1"; mkdir -p "$1" "$1/transcripts"
  SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE="${2:-busy}" STUB_SENT="$1/sent" \
  SUPERVISOR_STATE="$1" SUPERVISOR_STATUS="$1/st" SUPERVISOR_LOG="$1/lg" \
  SUPERVISOR_STAMP="$1/stamp" SUPERVISOR_HISTORY="$1/hist" NOTIFY_ENV="$1/none.env" \
  SUPERVISOR_LIVE="$LIVE" SLEEPCHECK_DIR="$1/transcripts" NOTIFY_SCRIPT="${3:-}" \
  bash "$LIVE/scripts/supervisor/watchdog.sh" >/dev/null 2>"$1/err"
}
at_sha() { git -C "$LIVE" rev-parse HEAD; }
say_ok()  { echo "  ok   $1"; pass=$((pass+1)); }
say_bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }
want_exit() { # want_exit <name> <got> <want> [context]
  if [ "$2" = "$3" ]; then say_ok "$1"; else say_bad "$1" "expected exit $3, got $2: ${4:-}"; fi
}

# 1. behind origin/main: the watchdog advances it.
adv_commit second
t2=$(git -C "$LIVE" rev-parse origin/main); b2=$(at_sha)
adv_run "$A/s1"
if [ "$(at_sha)" = "$t2" ]; then say_ok "a watchdog tick advances a live copy that is behind"
else say_bad "a watchdog tick advances a live copy that is behind" "still at $(at_sha), wanted $t2 (was $b2); $(cat "$A/s1/st" 2>/dev/null | tr '\n' ' ')"; fi
check "the advance is reported in the status file" "^advance: *advanced" "$A/s1/st"
check "and the code: line still reports the drift this tick ran with" "1 behind origin/main" "$A/s1/st"

# 2. already current: nothing is touched, and the report still happens.
b3=$(at_sha)
adv_run "$A/s2"
if [ "$(at_sha)" = "$b3" ]; then say_ok "a current live copy is not touched"
else say_bad "a current live copy is not touched" "moved to $(at_sha)"; fi
check "a current live copy still reports its state" "state:    working" "$A/s2/st"
check "a current live copy says there was nothing to advance" "^advance: *current" "$A/s2/st"

# 3. a refused advance (dirty live tree) is a report, not a crash. The tick
#    must still do its normal job and say why it did not advance.
adv_commit third
t4=$(git -C "$LIVE" rev-parse origin/main); b4=$(at_sha)
echo "someone's unfinished edit" >>"$LIVE/untouched.txt"
adv_run "$A/s3"; rc3=$?
want_exit "a refused advance leaves the watchdog exiting 0" "$rc3" 0 "$(cat "$A/s3/err" 2>/dev/null)"
check "a refused advance still writes the tick's normal status" "state:    working" "$A/s3/st"
check "the refusal reason is in the status file" "^advance: *refused.*uncommitted changes" "$A/s3/st"
check "the refusal reason is in the watchdog log" "ADVANCE-REFUSED" "$A/s3/lg"
if [ "$(at_sha)" = "$b4" ]; then say_ok "a refused advance leaves live where it was"
else say_bad "a refused advance leaves live where it was" "moved to $(at_sha)"; fi
if [ -n "$(git -C "$LIVE" status --porcelain)" ]; then say_ok "the uncommitted edit survives the refusal"
else say_bad "the uncommitted edit survives the refusal" "live is clean — the edit was carried forward or lost"; fi
git -C "$LIVE" checkout -q -- untouched.txt

# 4. a candidate that fails advance-live.sh's run-gate is NOT checked out.
#    The gate is what answers the objection this design was first rejected
#    over: a watchdog that cannot run cannot become the live one.
BRK=$(mktemp -d); rm -rf "$BRK"
git -C "$SRC" worktree add -q --detach "$BRK" origin/main
printf '#!/bin/bash\nexit 1\n' >"$BRK/scripts/supervisor/watchdog.sh"
git -C "$BRK" -c user.email=t@e.com -c user.name=T commit -aq -m "break watchdog.sh"
brk_sha=$(git -C "$BRK" rev-parse HEAD)
git -C "$LIVE" update-ref refs/remotes/origin/main "$brk_sha"
b5=$(at_sha)
adv_run "$A/s4"
if [ "$(at_sha)" = "$b5" ]; then say_ok "a candidate that fails its run-gate is not checked out"
else say_bad "a candidate that fails its run-gate is not checked out" "live moved to $(at_sha) — the broken commit is now the guard"; fi
check "the failed run-gate is named in the status file" "^advance: *refused.*well-formed status" "$A/s4/st"
git -C "$LIVE" update-ref refs/remotes/origin/main "$t4"
git -C "$SRC" worktree remove --force "$BRK" >/dev/null 2>&1
git -C "$SRC" worktree prune >/dev/null 2>&1

# 5. during an escalation the code is FROZEN, deliberately: the sha a human was
#    paged with must still be the sha they find. The `code:` line keeps saying
#    how far behind it is -- the report is not what changes.
cat >"$A/notify.sh" <<'EOF'
#!/bin/bash
echo "$1" >> "$(dirname "$0")/pages"
EOF
chmod +x "$A/notify.sh"
b6=$(at_sha)
rm -rf "$A/s5"; mkdir -p "$A/s5" "$A/s5/transcripts"
nowsec=$(date +%s); for i in 1 2 3; do echo $((nowsec - 60)); done >"$A/s5/hist"
SUPERVISOR_PATH="$STUBS:/usr/bin:/bin" STUB_PANE_STATE=idle STUB_SENT="$A/s5/sent" \
SUPERVISOR_STATE="$A/s5" SUPERVISOR_STATUS="$A/s5/st" SUPERVISOR_LOG="$A/s5/lg" \
SUPERVISOR_STAMP="$A/s5/stamp" SUPERVISOR_HISTORY="$A/s5/hist" NOTIFY_ENV="$A/s5/none.env" \
SUPERVISOR_LIVE="$LIVE" SLEEPCHECK_DIR="$A/s5/transcripts" NOTIFY_SCRIPT="$A/notify.sh" \
  bash "$LIVE/scripts/supervisor/watchdog.sh" >/dev/null 2>"$A/s5/err"
check "an escalating tick still escalates" "state:    escalate" "$A/s5/st"
check "an escalating tick holds the advance on purpose" "^advance: *held" "$A/s5/st"
check "the hold is in the watchdog log" "ADVANCE-HELD" "$A/s5/lg"
check "the code: line still reports the drift during an escalation" "1 behind origin/main" "$A/s5/st"
if [ "$(at_sha)" = "$b6" ]; then say_ok "an escalation leaves the live copy where the page said it was"
else say_bad "an escalation leaves the live copy where the page said it was" "moved to $(at_sha)"; fi

# 5b. an unreadable origin/main: the `code:` line's three outcomes are
#     untouched by any of this, and an advance that cannot compare refuses.
git -C "$LIVE" update-ref -d refs/remotes/origin/main
b7=$(at_sha)
adv_run "$A/s6"
check "an unreadable comparison still says so" "cannot compare" "$A/s6/st"
check "and the advance refuses rather than guessing" "^advance: *refused" "$A/s6/st"
if [ "$(at_sha)" = "$b7" ]; then say_ok "an unreadable comparison leaves live untouched"
else say_bad "an unreadable comparison leaves live untouched" "moved to $(at_sha)"; fi

# --- the race gate itself: outside the post-tick window, do not advance (#135)
#
# Everything above reaches advance-live.sh through watchdog.sh's exit trap,
# which has just written a fresh `checked:` timestamp -- so `age` is ~0s and
# inside the window in every one of those cases, and the gate's SKIP branch is
# never taken. Three mutations of the window check (disabling it at its first
# occurrence, at the pre-mutation re-check, and both) left the suite at
# 50 passed, 0 failed. These two cases drive the skip outcome instead, by
# seeding a stale timestamp and calling advance-live.sh DIRECTLY -- the trap
# cannot produce a stale one.
stamp_at() { # stamp_at <seconds-ago> -- a watchdog `checked:` timestamp in the past
  python3 -c 'import datetime,sys
print((datetime.datetime.now(datetime.timezone.utc)
       - datetime.timedelta(seconds=int(sys.argv[1]))).strftime("%Y-%m-%dT%H:%M:%SZ"))' "$1"
}
seed_status() { # seed_status <status-file> <seconds-ago>
  mkdir -p "$(dirname "$1")"
  printf 'checked:  %s\nstate:    working\n' "$(stamp_at "$2")" >"$1"
}
adv_direct() { # adv_direct <state-dir> -- advance-live.sh alone, no watchdog tick
  SUPERVISOR_STATE="$1" SUPERVISOR_STATUS="$1/st" SUPERVISOR_LIVE="$LIVE" \
  ADVANCE_LOG="$1/advance.log" ADVANCE_ROLLBACK="$1/rollback" \
    bash "$LIVE/scripts/supervisor/advance-live.sh" >"$1/out" 2>&1
}

# origin/main was deleted by 5b; restore it so there is something to advance.
git -C "$LIVE" update-ref refs/remotes/origin/main "$t4"

# 6. the initial check: a tick that is long past leaves the advance for a
#    later pass rather than swapping the tree out from under the next one.
b8=$(at_sha)
SA="$A/s7"; rm -rf "$SA"; mkdir -p "$SA"
seed_status "$SA/st" 600
adv_direct "$SA"; rc8=$?
want_exit "a stale watchdog tick is a skip, not a failure" "$rc8" 0 "$(cat "$SA/out" 2>/dev/null)"
check "outside the post-tick window the advance is skipped" \
      "outside the 0-150s post-tick window" "$SA/out"
check "the skip is in the advance log" "SKIP:.*outside the 0-150s post-tick window" "$SA/advance.log"
if [ "$(at_sha)" = "$b8" ]; then say_ok "a skipped advance leaves the live copy where it was"
else say_bad "a skipped advance leaves the live copy where it was" "moved to $(at_sha) — the race gate did not hold"; fi
if [ -e "$SA/rollback" ]; then say_bad "a skipped advance records no rollback target" "wrote $(cat "$SA/rollback")"
else say_ok "a skipped advance records no rollback target"; fi

# 7. the pre-mutation re-check: the window was open when the gate was first
#    read and closed while the smoke test ran. That gap is variable-duration
#    by construction -- a `git worktree add` plus a whole candidate watchdog
#    run -- so the only check standing between a live tick and a tree swapped
#    underneath it is the SECOND one, at the point of mutation. A test that
#    only drives case 6 leaves this occurrence exactly as uncovered as before.
#    Made deterministic rather than timed: the candidate's own watchdog.sh
#    ages the live status while the gate is watching it.
SB="$A/s8"; rm -rf "$SB"; mkdir -p "$SB"
STALL=$(mktemp -d); rm -rf "$STALL"
git -C "$SRC" worktree add -q --detach "$STALL" "$t4"
stale_stamp=$(stamp_at 600)
cat >"$STALL/scripts/supervisor/watchdog.sh" <<EOF
#!/bin/bash
# Test fixture, not a watchdog. It writes the well-formed status advance-live.sh's
# run-gate demands, so the candidate passes the gate and the run reaches the
# pre-mutation re-check -- and it ages the LIVE status out of the post-tick
# window while it runs, which is what a tick overrunning its own cadence does.
printf 'checked:  %s\nstate:    working\n' "\$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"\$SUPERVISOR_STATUS"
printf 'checked:  %s\nstate:    working\n' '$stale_stamp' >'$SB/st'
EOF
git -C "$STALL" -c user.email=t@e.com -c user.name=T commit -aq -m "candidate whose run outlives the window"
stall_sha=$(git -C "$STALL" rev-parse HEAD)
git -C "$LIVE" update-ref refs/remotes/origin/main "$stall_sha"

b9=$(at_sha)
seed_status "$SB/st" 0
adv_direct "$SB"; rc9=$?
want_exit "a window that closes mid-smoke-test is a skip, not a failure" "$rc9" 0 "$(cat "$SB/out" 2>/dev/null)"
check "the run got past the first check to the smoke test" "smoke test at $stall_sha passed" "$SB/advance.log"
check "the window closing during the smoke test is caught before the checkout" \
      "window closed while the smoke test ran" "$SB/out"
if [ "$(at_sha)" = "$b9" ]; then say_ok "the re-check leaves the live copy where it was"
else say_bad "the re-check leaves the live copy where it was" "moved to $(at_sha) — checked out after the window closed"; fi

git -C "$LIVE" update-ref refs/remotes/origin/main "$t4"
git -C "$SRC" worktree remove --force "$STALL" >/dev/null 2>&1
git -C "$SRC" worktree prune >/dev/null 2>&1

leftover=$(git -C "$SRC" worktree list --porcelain | grep -c 'ad99-advance-smoke' || true)
if [ "$leftover" -eq 0 ]; then say_ok "no smoke-test worktrees left registered"
else say_bad "no smoke-test worktrees left registered" "$leftover still registered"; fi

# A run from somewhere that is NOT the pinned live copy must never check out
# over the tree it is running from -- this is what keeps the suite above, and
# a developer's own checkout, from being mutated by a test.
D=$(mktemp -d); run busy "$D/w"
if grep -q '^advance:' "$D/w/st" 2>/dev/null; then
  say_bad "a watchdog that is not the live copy does not advance anything" "reported $(grep '^advance:' "$D/w/st")"
else
  say_ok "a watchdog that is not the live copy does not advance anything"
fi

git -C "$SRC" worktree remove --force "$LIVE" >/dev/null 2>&1
rm -rf "$A"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
