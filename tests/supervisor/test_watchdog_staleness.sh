#!/bin/bash
# agent-supervisor#24: the watchdog's own absence must be loud.
#
# WHY: on 2026-08-13 the watchdog LaunchAgent was unloaded for 91 minutes
# (10:14:38Z-11:45:36Z) and nothing noticed -- watchdog.status's `checked:`
# line went stale and no code ever compared it to the clock. It was found
# only because a human happened to `cat` the file by hand. advance-live.sh
# already runs on every supervisor tick (loop-tick.md's first step) and
# already parses this exact file for its own post-tick race gate, so that is
# where this check was added: no new script for the tick to remember to call.
#
# This suite never touches the live estate: everything below runs against a
# scratch SUPERVISOR_STATE and a throwaway git origin/clone/live-worktree
# triple, the same fixture shape test_advance_live.sh uses.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ADVANCE="$HERE/../../scripts/supervisor/advance-live.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }

echo "advance-live.sh -- watchdog staleness (agent-supervisor#24)"

D=$(mktemp -d)
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/src"
SRC="$D/src"
git -C "$SRC" config user.email test@example.com
git -C "$SRC" config user.name "Test"
git -C "$SRC" checkout -q -b main
mkdir -p "$SRC/scripts/supervisor"
cat >"$SRC/scripts/supervisor/watchdog.sh" <<'EOF'
#!/bin/bash
set -uo pipefail
STATUS="${SUPERVISOR_STATUS:?}"
mkdir -p "$(dirname "$STATUS")"
printf 'checked:  %s\nstate:    pane_unreadable\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$STATUS"
exit 0
EOF
chmod +x "$SRC/scripts/supervisor/watchdog.sh"
git -C "$SRC" add -A
git -C "$SRC" commit -q -m "base"
git -C "$SRC" push -q -u origin main

LIVE="$D/live"
git -C "$SRC" worktree add -q --detach "$LIVE" origin/main

status_at() { # status_at <state-dir> <seconds-ago>
  mkdir -p "$1"
  local ts
  ts=$(date -u -v-"$2"S +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "$2 seconds ago" +%Y-%m-%dT%H:%M:%SZ)
  printf 'checked:  %s\nstate:    working\n' "$ts" >"$1/watchdog.status"
}

# --- stub `log`/`launchctl` (agent-supervisor#37) ---------------------------
# advance-live.sh's bootout classifier shells out to the real launchd unified
# log and `launchctl print`. Neither may ever be allowed to run for real in
# this suite: this machine's actual com.jonhill.supervisor-watchdog label is
# the SAME label the classifier defaults to, and this repo's own #24 incident
# means a real bootout record for it can genuinely exist in this host's own
# unified log on any given day -- an unstubbed run would be reading real
# system state into what is supposed to be a hermetic fixture. Every
# classifying call below goes through these stubs, on a PATH that never
# reaches the real binaries.
mkdir -p "$D/bin"
cat >"$D/bin/log" <<'EOF'
#!/bin/bash
# STUB_LOG_MODE: bootout | none | unreadable (default: none)
case "${STUB_LOG_MODE:-none}" in
  bootout)
    echo "2026-08-13 10:14:38.000000-0700 launchd[1] com.apple.xpc.launchd Bootout com.jonhill.supervisor-watchdog"
    exit 0
    ;;
  unreadable)
    echo "log: log_show: fatal error" >&2
    exit 1
    ;;
  *) exit 0 ;;
esac
EOF
chmod +x "$D/bin/log"
cat >"$D/bin/launchctl" <<'EOF'
#!/bin/bash
# STUB_LAUNCHCTL_MODE: loaded | absent | unreadable (default: loaded)
case "$1" in
  print)
    case "${STUB_LAUNCHCTL_MODE:-loaded}" in
      loaded) echo "state = running"; exit 0 ;;
      absent) echo "Could not find service \"$2\" in domain for port" >&2; exit 3 ;;
      unreadable) echo "launchctl: internal error" >&2; exit 70 ;;
    esac
    ;;
  *) exit 0 ;;
esac
EOF
chmod +x "$D/bin/launchctl"
run() { PATH="$D/bin:$PATH" SUPERVISOR_STATE="$1" bash "$ADVANCE" "$LIVE"; } # run <state-dir>

# --- RED: the current tick must be able to miss this ------------------------
# Reproduce the shape #24 was filed over directly against a copy of
# advance-live.sh with the staleness check's body replaced by a no-op --
# i.e. exactly what shipped before this fix. A 91-minute-old watchdog.status
# (the real incident's own duration) must NOT be reported as a problem by
# that copy: this is the failing test the brief asked for, pinned against the
# pre-fix shape rather than merely asserted in prose.
PRE_FIX="$D/advance-live-pre-fix.sh"
patch_rc=0
python3 - "$ADVANCE" "$PRE_FIX" <<'PY' || patch_rc=$?
import re
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
text = re.sub(
    r"watchdog_stale_check\(\) \{.*?\n\}\n",
    "watchdog_stale_check() { return 0; }\n",
    text,
    count=1,
    flags=re.S,
)
assert "watchdog_stale_check() { return 0; }" in text, "watchdog_stale_check body not replaced -- script shape changed"
open(dst, "w").write(text)
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a pre-fix (staleness-blind) copy of advance-live.sh" \
    "could not patch $ADVANCE (exit $patch_rc)"
else
  ok "setup: patched a pre-fix (staleness-blind) copy of advance-live.sh"
  chmod +x "$PRE_FIX"
  S=$(mktemp -d); status_at "$S" 5460   # 91 minutes, the real incident's own gap
  out=$(SUPERVISOR_STATE="$S" bash "$PRE_FIX" "$LIVE" 2>&1); rc=$?
  want_exit "RED: pre-fix copy reports nothing wrong on a 91-minute-stale watchdog" "$rc" 0 "$out"
fi

# --- GREEN: the same state now fails loudly on the real script -------------
S=$(mktemp -d); status_at "$S" 5460
out=$(run "$S" 2>&1); rc=$?
want_exit "GREEN: a 91-minute-stale watchdog fails loudly (nonzero exit)" "$rc" 1 "$out"
if grep -qi "WATCHDOG STALE" <<<"$out"; then ok "the failure names itself as watchdog staleness"; else bad "the failure names itself as watchdog staleness" "$out"; fi
if grep -q "5460" <<<"$out" || grep -qi "91" <<<"$out"; then ok "the failure names the age"; else bad "the failure names the age" "$out"; fi
if grep -qE '[0-9]{4}-[0-9]{2}-[0-9]{2}T' <<<"$out"; then ok "the failure names the last-checked timestamp"; else bad "the failure names the last-checked timestamp" "$out"; fi
after=$(git -C "$LIVE" rev-parse HEAD)
before=$(git -C "$LIVE" rev-parse HEAD)
[ "$after" = "$before" ] && ok "a stale-watchdog refusal leaves live untouched" || bad "a stale-watchdog refusal leaves live untouched" "moved"

# --- agent-supervisor#37 (review of #24): stopped is not dead --------------
# The reviewer's finding, verbatim in substance: `:211` detects staleness but
# `:216` said the LaunchAgent "may be unloaded or dead", collapsing a
# deliberate Director-initiated stop and a real death into one alarm. These
# four cases must fail RED against the code as it stood when PR #37 was
# opened (that code called `fail()` unconditionally on any staleness, with no
# classifier to consult, so a bootout-attributable stale watchdog would have
# been reported as the DEAD alarm rather than as STOPPED, exit 1 not 0).

# STOPPED: a bootout record in the unified log is attributable evidence of a
# deliberate stop -- report it as stopped, exit 0, not the alarm.
S=$(mktemp -d); status_at "$S" 5460
out=$(PATH="$D/bin:$PATH" STUB_LOG_MODE=bootout STUB_LAUNCHCTL_MODE=absent SUPERVISOR_STATE="$S" bash "$ADVANCE" "$LIVE" 2>&1); rc=$?
want_exit "STOPPED: an attributable bootout record reports stopped, exit 0, not an alarm" "$rc" 0 "$out"
if grep -qi "WATCHDOG STOPPED" <<<"$out"; then ok "the report names itself as stopped"; else bad "the report names itself as stopped" "$out"; fi
if grep -qi "WATCHDOG STALE" <<<"$out"; then bad "a stopped watchdog is not reported as the DEAD/STALE alarm" "$out"; else ok "a stopped watchdog is not reported as the DEAD/STALE alarm"; fi

# STOPPED via the OTHER attributable leg the brief named: launchctl print
# showing the label absent, with the log itself readable but silent (neither
# confirms nor denies) -- absence from launchctl print, on a label
# watchdog_age() already proved was ticking before, is attributable on its
# own.
S=$(mktemp -d); status_at "$S" 5460
out=$(PATH="$D/bin:$PATH" STUB_LOG_MODE=none STUB_LAUNCHCTL_MODE=absent SUPERVISOR_STATE="$S" bash "$ADVANCE" "$LIVE" 2>&1); rc=$?
want_exit "STOPPED via launchctl absence alone (no log evidence either way)" "$rc" 0 "$out"
if grep -qi "WATCHDOG STOPPED" <<<"$out"; then ok "launchctl absence alone is attributable to a stop"; else bad "launchctl absence alone is attributable to a stop" "$out"; fi

# DEAD: the shape the review actually cared about -- no bootout in the log,
# and `launchctl print` says the label is STILL loaded. Still loaded plus
# stale is not explained by a deliberate stop at all; it is a hung or
# crash-looping process. This is the loud case.
S=$(mktemp -d); status_at "$S" 5460
out=$(PATH="$D/bin:$PATH" STUB_LOG_MODE=none STUB_LAUNCHCTL_MODE=loaded SUPERVISOR_STATE="$S" bash "$ADVANCE" "$LIVE" 2>&1); rc=$?
want_exit "DEAD: no attributable bootout, still loaded per launchctl, fails loudly" "$rc" 1 "$out"
if grep -qi "WATCHDOG STALE" <<<"$out" && grep -qi "DEAD" <<<"$out"; then ok "the failure names itself as dead, not merely stale"; else bad "the failure names itself as dead, not merely stale" "$out"; fi

# CANNOT TELL: both signals unreadable must fail closed as the loud case, not
# silently downgrade a possible death (the brief's explicit third case).
S=$(mktemp -d); status_at "$S" 5460
out=$(PATH="$D/bin:$PATH" STUB_LOG_MODE=unreadable STUB_LAUNCHCTL_MODE=unreadable SUPERVISOR_STATE="$S" bash "$ADVANCE" "$LIVE" 2>&1); rc=$?
want_exit "CANNOT TELL: both signals unreadable fails loudly, not silently downgraded" "$rc" 1 "$out"
if grep -qi "UNKNOWN" <<<"$out"; then ok "the failure says explicitly it could not tell"; else bad "the failure says explicitly it could not tell" "$out"; fi

# --- default threshold: just past 3x the 180s default tick interval --------
S=$(mktemp -d); status_at "$S" 541
out=$(run "$S" 2>&1); rc=$?
want_exit "541s (just over the default 540s threshold) fails" "$rc" 1 "$out"

# --- a HEALTHY watchdog must never trip this (#22's lesson) -----------------
S=$(mktemp -d); status_at "$S" 30
out=$(run "$S" 2>&1); rc=$?
want_exit "a fresh (30s-old) watchdog tick does not trip the alarm" "$rc" 0 "$out"
if grep -qi "WATCHDOG STALE" <<<"$out"; then bad "a fresh tick is not reported as stale" "$out"; else ok "a fresh tick is not reported as stale"; fi

S=$(mktemp -d); status_at "$S" 539
out=$(run "$S" 2>&1); rc=$?
if grep -qi "WATCHDOG STALE" <<<"$out"; then bad "539s (just under the default 540s threshold) is not yet stale" "$out"; else ok "539s (just under the default 540s threshold) is not yet stale"; fi

# --- no watchdog.status at all: not yet ticked, not "dead" ------------------
S=$(mktemp -d)
out=$(run "$S" 2>&1); rc=$?
want_exit "no watchdog.status file at all is not treated as staleness" "$rc" 0 "$out"
if grep -qi "WATCHDOG STALE" <<<"$out"; then bad "a missing status file is not reported as staleness" "$out"; else ok "a missing status file is not reported as staleness"; fi

# --- MUTATION: make the freshness comparison always pass --------------------
# Patch a copy with the staleness comparison forced to "never stale" and
# confirm the 91-minute case above now goes green on the mutant -- i.e. the
# GREEN assertion above is actually pinned to this comparison, not to
# something else in the script.
MUT="$D/advance-live-mutant.sh"
mut_rc=0
python3 - "$ADVANCE" "$MUT" <<'PY' || mut_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '[ "$age" -gt "$STALE_AFTER" ] || return 0'
replacement = '[ "$age" -gt "$STALE_AFTER" ] && false || return 0'
assert marker in text, "staleness comparison not found -- script shape changed"
assert text.count(marker) == 1, "staleness comparison not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$mut_rc" -ne 0 ]; then
  bad "setup: patched a copy of advance-live.sh whose freshness comparison always passes" \
    "could not patch $ADVANCE (exit $mut_rc)"
else
  ok "setup: patched a copy of advance-live.sh whose freshness comparison always passes"
  chmod +x "$MUT"
  S=$(mktemp -d); status_at "$S" 5460
  out=$(SUPERVISOR_STATE="$S" bash "$MUT" "$LIVE" 2>&1); rc=$?
  if [ "$rc" -eq 0 ] && ! grep -qi "WATCHDOG STALE" <<<"$out"; then
    ok "mutation confirmed: a comparison that always passes lets a 91-minute-stale watchdog through silently (the GREEN assertion above would now be red)"
  else
    bad "mutation confirmed: a comparison that always passes lets a 91-minute-stale watchdog through silently" \
      "expected exit 0 with no WATCHDOG STALE line, got rc=$rc: $out"
  fi
fi

# --- MUTATION: force the bootout classifier to always say "stopped" --------
# Same bar the reviewer held #24's own staleness comparison to: force the
# thing under test to always pass and confirm a real death goes RED. If this
# mutant's DEAD case (no bootout, still loaded per launchctl) goes green, the
# classifier's actual signals are decoration and every dead watchdog would be
# reported as a harmless stop -- the exact collapse #37 was opened over.
MUT2="$D/advance-live-mutant-stopped.sh"
mut2_rc=0
python3 - "$ADVANCE" "$MUT2" <<'PY' || mut2_rc=$?
import re
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
newtext, n = re.subn(
    r"watchdog_bootout_classify\(\) \{.*?\n\}\n",
    "watchdog_bootout_classify() {\n  echo stopped\n  return 0\n}\n",
    text,
    count=1,
    flags=re.S,
)
assert n == 1, "watchdog_bootout_classify body not replaced -- script shape changed"
open(dst, "w").write(newtext)
PY
if [ "$mut2_rc" -ne 0 ]; then
  bad "setup: patched a copy of advance-live.sh whose bootout classifier always says stopped" \
    "could not patch $ADVANCE (exit $mut2_rc)"
else
  ok "setup: patched a copy of advance-live.sh whose bootout classifier always says stopped"
  chmod +x "$MUT2"
  S=$(mktemp -d); status_at "$S" 5460
  out=$(PATH="$D/bin:$PATH" STUB_LOG_MODE=none STUB_LAUNCHCTL_MODE=loaded SUPERVISOR_STATE="$S" bash "$MUT2" "$LIVE" 2>&1); rc=$?
  if [ "$rc" -eq 0 ] && grep -qi "WATCHDOG STOPPED" <<<"$out"; then
    ok "mutation confirmed: a classifier that always says stopped lets a genuinely dead watchdog through silently (the DEAD assertion above would now be red)"
  else
    bad "mutation confirmed: a classifier that always says stopped lets a genuinely dead watchdog through silently" \
      "expected exit 0 with WATCHDOG STOPPED, got rc=$rc: $out"
  fi
fi

rm -rf "$D"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
