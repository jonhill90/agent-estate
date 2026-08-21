#!/bin/bash
# agent-supervisor#466: a correct "refusing to guess" is not itself the
# defect this issue reports -- 37 consecutive refusals over 17 hours with
# NOTHING escalating is. director-loop.sh already pages on several
# unrecoverable states (#273), each rate-limited by a time cooldown, but
# nothing counted CONSECUTIVE stale-target refusals specifically and paged
# once a standing incident was proven (not a single blip). This test pins
# that counter: it must survive across separate invocations (director-loop.sh
# runs once and exits every tick -- nothing in-process persists between
# ticks), reset the moment resolution succeeds, and re-arm for the next
# incident rather than escalating once, ever.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOOP="$HERE/../../scripts/supervisor/director-loop.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_count() {
  local label="$1" needle="$2" file="$3" expect="$4"
  local got; got=$(grep -cF -- "$needle" "$file" 2>/dev/null || true)
  if [ "$got" = "$expect" ]; then ok "$label"; else bad "$label" "expected $expect of '$needle' in $file, got $got:
$(cat "$file" 2>/dev/null)"; fi
}

echo "director-loop.sh -- escalates after N CONSECUTIVE stale-target refusals, resets on recovery (#466)"

D=$(mktemp -d)
trap 'rm -rf "$D"' EXIT

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/src" >/dev/null 2>&1
SRC="$D/src"
git -C "$SRC" config user.email test@example.com
git -C "$SRC" config user.name "Test"
git -C "$SRC" checkout -q -b main
mkdir -p "$SRC/scripts/supervisor"
: > "$SRC/scripts/supervisor/.keep"
git -C "$SRC" add -A
git -C "$SRC" commit -q -m base
git -C "$SRC" push -q -u origin main
LIVE="$D/live"
git -C "$SRC" worktree add -q --detach "$LIVE" origin/main

QUOTA_SAFE="$HERE/stubs/quota-safe"
cp "$HERE/stubs/tmux-director-loop-name-target" "$D/tmux"; chmod +x "$D/tmux"
cp "$HERE/stubs/notify-quota-watch" "$D/notify.sh"; chmod +x "$D/notify.sh"

# Two windows, neither named 'supervisor' -- genuinely, durably ambiguous,
# so every tick refuses until the fixture is changed. TMUX_GOOD_TARGET unset
# means nothing is reachable directly either.
WINS_AMBIGUOUS='@11\tsanity-output\n@23\tsanity-blocker'
WINS_RESOLVED='@11\tsanity-output\n@40\tsupervisor'

tick() { # tick <script> <state-dir> <log-file> <windows> <good-target>
  PATH="$D:$PATH" TMUX_LOG="$2/tmux.log" TMUX_SESSIONS="director" \
    TMUX_WINDOWS_NAMED="$4" TMUX_GOOD_TARGET="$5" \
    SUPERVISOR_STATE="$2" SUPERVISOR_LIVE="$LIVE" SUPERVISOR_REPO="$SRC" \
    QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="director:@3" \
    DIRECTOR_LOOP_NOTIFY_SCRIPT="$D/notify.sh" NOTIFY_LOG="$3" \
    DIRECTOR_LOOP_STALE_ESCALATE_AFTER=3 DIRECTOR_LOOP_ALARM_COOLDOWN=0 \
    bash "$1" >>"$2/out.log" 2>&1
}

# --- GREEN: three consecutive refusals escalate on the third, not sooner --
STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.green"; : > "$NLOG"
tick "$LOOP" "$STATE" "$NLOG" "$WINS_AMBIGUOUS" ""
want_count "1st consecutive refusal: no page yet" "director-loop: ambiguous Director window" "$NLOG" 0
tick "$LOOP" "$STATE" "$NLOG" "$WINS_AMBIGUOUS" ""
want_count "2nd consecutive refusal: still no page" "director-loop: ambiguous Director window" "$NLOG" 0
tick "$LOOP" "$STATE" "$NLOG" "$WINS_AMBIGUOUS" ""
want_count "3rd consecutive refusal (the configured threshold): pages exactly once" "director-loop: ambiguous Director window" "$NLOG" 1
if grep -q "3 consecutive refusals" "$NLOG"; then
  ok "the page names the streak length"
else
  bad "the page names the streak length" "$(cat "$NLOG")"
fi

# --- recovery resets the streak: a successful resolution clears it, so the
# NEXT incident needs its own 3 refusals before paging again -------------
tick "$LOOP" "$STATE" "$NLOG" "$WINS_RESOLVED" "director:@40"
if [ "$(cat "$STATE/.director-loop-stale-streak.state" 2>/dev/null)" = "0" ]; then
  ok "a successful resolution resets the streak counter to 0"
else
  bad "a successful resolution resets the streak counter to 0" "streak file: $(cat "$STATE/.director-loop-stale-streak.state" 2>/dev/null)"
fi

tick "$LOOP" "$STATE" "$NLOG" "$WINS_AMBIGUOUS" ""
tick "$LOOP" "$STATE" "$NLOG" "$WINS_AMBIGUOUS" ""
want_count "after recovery, two more refusals still do not re-page (streak restarted)" "director-loop: ambiguous Director window" "$NLOG" 1
tick "$LOOP" "$STATE" "$NLOG" "$WINS_AMBIGUOUS" ""
want_count "...and the SECOND incident pages again once it also reaches 3" "director-loop: ambiguous Director window" "$NLOG" 2

# --- RED: mutate the escalation threshold check so it can never fire -----
# proves this suite's paging assertions actually depend on the counter, not
# on send_takeover_alarm's own (unrelated) time cooldown.
BROKEN="$D/director-loop-broken-streak.sh"
sed 's/if \[ "$streak" -ge "$STALE_ESCALATE_AFTER" \]; then/if [ "$streak" -ge 999999 ]; then/' \
  "$LOOP" > "$BROKEN"
if grep -qF 'if [ "$streak" -ge 999999 ]; then' "$BROKEN"; then
  ok "constructed a mutated copy whose escalation threshold can never be reached"
else
  bad "constructed a mutated copy whose escalation threshold can never be reached" "sed did not match -- check director-loop.sh's exact source line"
fi
chmod +x "$BROKEN"

STATE=$(mktemp -d "$D/state.XXXXXX"); NLOG="$D/nlog.red"; : > "$NLOG"
tick "$BROKEN" "$STATE" "$NLOG" "$WINS_AMBIGUOUS" ""
tick "$BROKEN" "$STATE" "$NLOG" "$WINS_AMBIGUOUS" ""
tick "$BROKEN" "$STATE" "$NLOG" "$WINS_AMBIGUOUS" ""
tick "$BROKEN" "$STATE" "$NLOG" "$WINS_AMBIGUOUS" ""
tick "$BROKEN" "$STATE" "$NLOG" "$WINS_AMBIGUOUS" ""
want_count "RED: with the counter broken, five consecutive refusals still never page" "director-loop: ambiguous Director window" "$NLOG" 0
if [ "$(cat "$STATE/.director-loop-stale-streak.state" 2>/dev/null)" -ge 5 ]; then
  ok "RED: ...even though the streak itself kept counting (proves the mutation is in the escalate GATE, not the counter)"
else
  bad "RED: ...even though the streak itself kept counting" "streak file: $(cat "$STATE/.director-loop-stale-streak.state" 2>/dev/null)"
fi

echo
echo "director-loop stale streak: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
