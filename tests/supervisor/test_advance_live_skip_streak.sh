#!/bin/bash
# agent-estate#800: 486 passing smoke tests, 302 SKIPs, and every one of
# them individually well-formed and exit-0 -- indistinguishable, one line at
# a time, from 302 legitimate "not this tick"s. Only the RUN of them is the
# signal that something is stuck, and nothing counted that run before this.
# This pins down the counter and the once-per-streak page layered on top of
# skip()/CURRENT/ADVANCED; test_advance_live.sh already owns the guard
# logic itself (window, dirty tree, fetch, ff-only) and this reuses its
# fixture shape rather than duplicating it inline.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ADVANCE="$HERE/../../scripts/supervisor/advance-live.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }

echo "advance-live.sh: skip streak + escalation (agent-estate#800)"

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
{
  printf 'checked:  %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'state:    pane_unreadable\n'
} >"$STATUS"
exit 0
EOF
chmod +x "$SRC/scripts/supervisor/watchdog.sh"
git -C "$SRC" add -A
git -C "$SRC" commit -q -m "base"
git -C "$SRC" push -q -u origin main

LIVE="$D/live"
git -C "$SRC" worktree add -q --detach "$LIVE" origin/main

# A second commit so LIVE is genuinely behind on every call below and every
# invocation reaches the window gate -- never the "already current"
# shortcut, which is CURRENT, not SKIP, and is exercised separately below.
echo two >"$SRC/file.txt"
git -C "$SRC" add file.txt
git -C "$SRC" commit -q -m "second commit"
git -C "$SRC" push -q origin main
git -C "$LIVE" fetch -q origin main
target_sha=$(git -C "$LIVE" rev-parse origin/main)

# A stub notify.sh that records every call instead of touching a real
# channel. ADVANCE_NOTIFY_SCRIPT is this PR's override seam, same
# convention as heartbeat.sh's HEARTBEAT_NOTIFY_SCRIPT (agent-supervisor#273).
NOTIFY_DIR=$(mktemp -d)
NOTIFY_CALLS="$NOTIFY_DIR/calls"
NOTIFY_STUB="$NOTIFY_DIR/notify.sh"
cat >"$NOTIFY_STUB" <<EOF
#!/bin/bash
printf '%s|%s|%s\n' "\${AGENT_NOTIFY_CALLER:-}" "\$1" "\$2" >>"$NOTIFY_CALLS"
exit 0
EOF
chmod +x "$NOTIFY_STUB"

stale_status() { # stale_status <state-dir> <seconds-ago>
  mkdir -p "$1"
  local ts
  ts=$(date -u -v-"$2"S +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "$2 seconds ago" +%Y-%m-%dT%H:%M:%SZ)
  printf 'checked:  %s\nstate:    working\n' "$ts" >"$1/watchdog.status"
}
fresh_status() { # fresh_status <state-dir>
  mkdir -p "$1"
  printf 'checked:  %s\nstate:    working\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$1/watchdog.status"
}

# One shared state dir across every call below, the same way one
# SUPERVISOR_STATE persists across real supervisor ticks -- the streak file
# only means anything if it survives between invocations of this
# exit-after-every-call script.
S=$(mktemp -d)
# Well outside the 150s window on every call, deterministically -- the
# #709 fail-fast-on-entry path, cheapest and least flaky to repeat N times.
stale_status "$S" 999

run() {
  ADVANCE_SKIP_ESCALATE_AFTER=3 \
  ADVANCE_NOTIFY_SCRIPT="$NOTIFY_STUB" \
  SUPERVISOR_STATE="$S" \
    bash "$ADVANCE" "$LIVE" 2>&1
}

# --- streak climbs across repeated skips, no page below threshold ---------
out1=$(run); rc1=$?
want_exit "skip 1 exits 0" "$rc1" 0 "$out1"
streak1=$(cat "$S/.advance-live-skip-streak" 2>/dev/null)
[ "$streak1" = "1" ] && ok "streak file reads 1 after the first skip" || bad "streak file reads 1 after the first skip" "got '$streak1'"
[ ! -s "$NOTIFY_CALLS" ] && ok "no page after 1 skip (below threshold 3)" || bad "no page after 1 skip (below threshold 3)" "$(cat "$NOTIFY_CALLS" 2>/dev/null)"

out2=$(run); rc2=$?
want_exit "skip 2 exits 0" "$rc2" 0 "$out2"
streak2=$(cat "$S/.advance-live-skip-streak" 2>/dev/null)
[ "$streak2" = "2" ] && ok "streak file reads 2 after the second skip" || bad "streak file reads 2 after the second skip" "got '$streak2'"
[ ! -s "$NOTIFY_CALLS" ] && ok "still no page after 2 skips (below threshold 3)" || bad "still no page after 2 skips (below threshold 3)" "$(cat "$NOTIFY_CALLS" 2>/dev/null)"

# --- crossing the threshold pages exactly once -----------------------------
out3=$(run); rc3=$?
want_exit "skip 3 (crosses threshold) exits 0" "$rc3" 0 "$out3"
streak3=$(cat "$S/.advance-live-skip-streak" 2>/dev/null)
[ "$streak3" = "3" ] && ok "streak file reads 3 at the threshold" || bad "streak file reads 3 at the threshold" "got '$streak3'"
if [ -s "$NOTIFY_CALLS" ]; then ok "a page fires on crossing the threshold"; else bad "a page fires on crossing the threshold" "no calls recorded"; fi
if grep -q "^supervisor|" "$NOTIFY_CALLS" 2>/dev/null; then ok "the page uses the authorized supervisor caller (notify.sh's own gate)"; else bad "the page uses the authorized supervisor caller (notify.sh's own gate)" "$(cat "$NOTIFY_CALLS" 2>/dev/null)"; fi
if grep -qi "3 consecutive" "$NOTIFY_CALLS" 2>/dev/null; then ok "the page names the streak length"; else bad "the page names the streak length" "$(cat "$NOTIFY_CALLS" 2>/dev/null)"; fi

# --- further skips past the threshold do NOT page again (dedup) -----------
calls_before=$(wc -l <"$NOTIFY_CALLS")
out4=$(run); rc4=$?
want_exit "skip 4 (past threshold) exits 0" "$rc4" 0 "$out4"
streak4=$(cat "$S/.advance-live-skip-streak" 2>/dev/null)
[ "$streak4" = "4" ] && ok "streak file keeps counting past the threshold" || bad "streak file keeps counting past the threshold" "got '$streak4'"
run >/dev/null
calls_after=$(wc -l <"$NOTIFY_CALLS")
if [ "$calls_after" -eq "$calls_before" ]; then
  ok "no second page for the same streak (dedup -- without it, 302 skips would page 282 times, per the orphaned lane's own finding)"
else
  bad "no second page for the same streak (dedup)" "calls before=$calls_before after=$calls_after"
fi

# --- a genuine advance resets the streak -----------------------------------
fresh_status "$S"
out_adv=$(run); rc_adv=$?
want_exit "a fresh tick with a good candidate advances (exit 0)" "$rc_adv" 0 "$out_adv"
after_adv=$(git -C "$LIVE" rev-parse HEAD)
if [ "$after_adv" = "$target_sha" ]; then ok "advance actually moved live to the target"; else bad "advance actually moved live to the target" "at $after_adv, wanted $target_sha"; fi
if [ ! -f "$S/.advance-live-skip-streak" ]; then ok "the streak file is cleared on a real advance"; else bad "the streak file is cleared on a real advance" "still reads $(cat "$S/.advance-live-skip-streak")"; fi
if [ ! -f "$S/.advance-live-escalate-episode" ]; then ok "the escalation dedup marker is cleared on a real advance"; else bad "the escalation dedup marker is cleared on a real advance" "still present"; fi

# --- a genuinely NEW streak pages again -- proves the dedup is per-streak,
# --- not "never page again" ------------------------------------------------
echo three >"$SRC/file.txt"
git -C "$SRC" add file.txt
git -C "$SRC" commit -q -m "third commit"
git -C "$SRC" push -q origin main
git -C "$LIVE" fetch -q origin main
stale_status "$S" 999
calls_before2=$(wc -l <"$NOTIFY_CALLS")
for _ in 1 2 3; do run >/dev/null; done
calls_after2=$(wc -l <"$NOTIFY_CALLS")
if [ "$calls_after2" -gt "$calls_before2" ]; then
  ok "a genuinely new streak pages again after the reset (not muted forever)"
else
  bad "a genuinely new streak pages again after the reset (not muted forever)" "before=$calls_before2 after=$calls_after2"
fi

# --- CURRENT (nothing to advance) also resets the streak, same as ADVANCED.
# LIVE2 is pinned at origin/main with nothing behind, so every call takes
# the CURRENT shortcut immediately -- this seeds a fake leftover streak
# (the shape a prior task's state dir reuse could leave) and confirms
# CURRENT clears it rather than treating it as sticky.
D2=$(mktemp -d)
git init -q --bare "$D2/origin.git"
git clone -q "$D2/origin.git" "$D2/src"
SRC2="$D2/src"
git -C "$SRC2" config user.email test@example.com
git -C "$SRC2" config user.name "Test"
git -C "$SRC2" checkout -q -b main
mkdir -p "$SRC2/scripts/supervisor"
cp "$SRC/scripts/supervisor/watchdog.sh" "$SRC2/scripts/supervisor/watchdog.sh"
git -C "$SRC2" add -A
git -C "$SRC2" commit -q -m base
git -C "$SRC2" push -q -u origin main
LIVE2="$D2/live"
git -C "$SRC2" worktree add -q --detach "$LIVE2" origin/main
S2=$(mktemp -d)
printf '5\n' >"$S2/.advance-live-skip-streak"
out_cur=$(SUPERVISOR_STATE="$S2" bash "$ADVANCE" "$LIVE2" 2>&1); rc_cur=$?
want_exit "already-current exits 0" "$rc_cur" 0 "$out_cur"
if grep -qi "^advance-live: current" <<<"$out_cur"; then ok "already-current is reported as CURRENT, not a skip"; else bad "already-current is reported as CURRENT, not a skip" "$out_cur"; fi
if [ ! -f "$S2/.advance-live-skip-streak" ]; then ok "CURRENT clears a leftover streak file rather than treating it as sticky"; else bad "CURRENT clears a leftover streak file rather than treating it as sticky" "still reads $(cat "$S2/.advance-live-skip-streak")"; fi

# --- mutation: without the wiring, repeated skips never page --------------
# Proves the assertions above can actually fail, not merely that they pass
# -- same discipline the rest of this suite already applies to its guards.
UNWIRED="$D/advance-live-unwired.sh"
sed 's/bump_and_report_skip_streak; exit 0; }/exit 0; }/' "$ADVANCE" >"$UNWIRED"
chmod +x "$UNWIRED"
if grep -q "bump_and_report_skip_streak; exit 0; }" "$UNWIRED"; then
  bad "setup: the unwired copy's skip() no longer calls the streak bump" "sed substitution did not apply"
else
  ok "setup: the unwired copy's skip() no longer calls the streak bump"
fi
S_MUT=$(mktemp -d)
stale_status "$S_MUT" 999
NOTIFY_DIR2=$(mktemp -d)
NOTIFY_CALLS2="$NOTIFY_DIR2/calls"
NOTIFY_STUB2="$NOTIFY_DIR2/notify.sh"
cat >"$NOTIFY_STUB2" <<EOF
#!/bin/bash
printf '%s|%s|%s\n' "\${AGENT_NOTIFY_CALLER:-}" "\$1" "\$2" >>"$NOTIFY_CALLS2"
exit 0
EOF
chmod +x "$NOTIFY_STUB2"
mut_rc=0
for _ in 1 2 3 4 5; do
  ADVANCE_SKIP_ESCALATE_AFTER=3 ADVANCE_NOTIFY_SCRIPT="$NOTIFY_STUB2" SUPERVISOR_STATE="$S_MUT" bash "$UNWIRED" "$LIVE" >/dev/null 2>&1
  mut_rc=$?
done
if [ "$mut_rc" -eq 0 ] && [ ! -s "$NOTIFY_CALLS2" ]; then
  ok "mutation confirmed: without the wiring, 5 skips past the threshold never page (the assertions above would now be red)"
else
  bad "mutation confirmed: without the wiring, 5 skips past the threshold never page (the assertions above would now be red)" "rc=$mut_rc calls=$(cat "$NOTIFY_CALLS2" 2>/dev/null)"
fi

rm -rf "$D" "$D2" "$NOTIFY_DIR" "$NOTIFY_DIR2" "$S_MUT" "$S2"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
