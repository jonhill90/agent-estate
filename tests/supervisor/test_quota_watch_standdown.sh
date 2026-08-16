#!/bin/bash
# agent-supervisor#261: quota-watch.sh must act on SAFE -> WINDDOWN, not just
# WINDDOWN -> SAFE. Measured: the estate logged that exact transition at
# 09:40:13 and did nothing -- the wake-up half (resume on WINDDOWN -> SAFE)
# was built after the $80 -> $8 burn, but the stand-down half was left to a
# human, or to a supervisor tick happening to land on the gate at the right
# moment.
#
# This drives quota-watch.sh --once repeatedly against a stub quota gate
# (QUOTA_GATE), one tick per invocation, sharing SUPERVISOR_STATE across
# calls so the on-disk stamp carries the "previous confirmed reading"
# forward exactly as the real long-running loop would between polls.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
QW="$HERE/../../scripts/supervisor/quota-watch.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
# Count actual MESSAGES sent, not raw tmux calls -- one message is three
# calls (C-u, the literal text via -l, Enter), and only the -l call carries
# the payload, so that is the one to count.
count_sends()   { local n; n=$(grep -c -- ' -l ' "$1" 2>/dev/null); printf '%s' "${n:-0}"; }

echo "quota-watch.sh -- the stand-down half (#261)"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/state"

# A minimal tmux stub: log every send-keys call, and answer capture-pane
# with a shape containing 'esc to interrupt' so send_message's delivery
# check succeeds -- this test is about WHICH message gets sent and how
# often, not about the delivery-verification path.
cat > "$D/bin/tmux" <<'FIX'
#!/bin/bash
LOG="${TMUX_LOG:?}"
case "$1" in
  send-keys)    printf 'send-keys %s\n' "$*" >> "$LOG" ;;
  capture-pane) printf '  ⏵⏵ bypass permissions on · esc to interrupt · ← 1 agent\n' ;;
esac
exit 0
FIX
chmod +x "$D/bin/tmux"

for code in 0 1 2 3 127 42; do
  cat > "$D/bin/quota-$code" <<FIX
#!/bin/bash
exit $code
FIX
  chmod +x "$D/bin/quota-$code"
done

TMUX_LOG="$D/tmux.log"; : > "$TMUX_LOG"
tick() {
  # $1: which quota-<code> stub to answer this tick with.
  PATH="$D/bin:$PATH" TMUX_LOG="$TMUX_LOG" SUPERVISOR_STATE="$D/state" \
    QUOTA_GATE="$D/bin/quota-$1" QUOTA_WATCH_TARGET="sess:@1" \
    bash "$QW" --once 2>&1
}

# --- cold start: first-ever reading never fires anything --------------------
OUT=$(tick 0); RC=$?
want_exit "cold-start SAFE reading exits 0" "$RC" "0" "$OUT"
[ "$(count_sends "$TMUX_LOG")" = 0 ] && ok "...and sends nothing (no prior state to transition from)" \
  || bad "...and sends nothing" "$(cat "$TMUX_LOG")"

# --- a second SAFE reading in a row: still nothing, no transition -----------
# Steady state logs nothing at all (state == prev, no line fires) -- the
# thing to prove here is silence, not particular wording.
tick 0 >/dev/null
[ "$(count_sends "$TMUX_LOG")" = 0 ] && ok "...a repeated SAFE reading still sends nothing" \
  || bad "...a repeated SAFE reading still sends nothing" "$(cat "$TMUX_LOG")"

# --- THE LOAD-BEARING CASE: confirmed SAFE -> WINDDOWN fires exactly one ----
# stand-down. Break the transition branch in quota-watch.sh and watch this
# go red; restore it and watch it go green.
OUT=$(tick 1)
want_contains "SAFE -> WINDDOWN logs the transition" "SAFE -> WINDDOWN" "$OUT"
want_contains "...names it a stand-down, not a resume" "stand-down" "$OUT"
SENT=$(count_sends "$TMUX_LOG")
[ "$SENT" = 1 ] && ok "...and exactly one send-keys line lands in tmux" \
  || bad "...and exactly one send-keys line lands in tmux" "saw $SENT: $(cat "$TMUX_LOG")"
want_contains "...the broadcast tells the lane to push" "push your branch" "$(cat "$TMUX_LOG")"
want_contains "...and to post exactly one comment" "post ONE comment" "$(cat "$TMUX_LOG")"
want_contains "...and not to stop mid-turn" "do not stop mid-turn" "$(cat "$TMUX_LOG")"

# --- idempotency: WINDDOWN -> WINDDOWN fires no second broadcast ------------
tick 1 >/dev/null
SENT2=$(count_sends "$TMUX_LOG")
[ "$SENT2" = 1 ] && ok "...tmux log is unchanged -- still exactly one send-keys line" \
  || bad "...tmux log is unchanged" "now saw $SENT2: $(cat "$TMUX_LOG")"

# --- and firing the tool a third time in a row is still idempotent ---------
tick 1 >/dev/null
SENT3=$(count_sends "$TMUX_LOG")
[ "$SENT3" = 1 ] && ok "...and a third consecutive WINDDOWN tick still sends nothing new" \
  || bad "...and a third consecutive WINDDOWN tick still sends nothing new" "now saw $SENT3"

# --- UNKNOWN / 127: never stands down silently, never proceeds silently ----
# Reset to a fresh confirmed SAFE before EACH code, so every one of them is
# its own SAFE -> UNKNOWN transition (state == prev logs nothing, which
# would hide the "cannot tell" line this checks for). Includes the exact
# #227 shape -- a missing/unexecutable gate, bash's own exit 127.
for code in 2 3 127 42; do
  rm -f "$D/state"/*
  tick 0 >/dev/null   # confirmed SAFE baseline
  : > "$TMUX_LOG"
  OUT=$(tick "$code")
  want_contains "exit $code is read as UNKNOWN, not WIND DOWN" "cannot tell" "$OUT"
  SENT=$(count_sends "$TMUX_LOG")
  [ "$SENT" = 0 ] && ok "...and sends no stand-down on an unreadable sample (exit $code)" \
    || bad "...and sends no stand-down on an unreadable sample (exit $code)" "$(cat "$TMUX_LOG")"
done

# --- a WINDDOWN seen only after an unreadable sample does not fire either --
# "cannot tell" must not silently become "wind down": the transition guard
# requires the IMMEDIATELY PRIOR confirmed reading to be SAFE, so SAFE ->
# UNKNOWN -> WINDDOWN must not fire (unlike a direct SAFE -> WINDDOWN, which
# already does, above).
rm -f "$D/state"/*
tick 0 >/dev/null    # confirmed SAFE
tick 127 >/dev/null  # unreadable sample in between
: > "$TMUX_LOG"
OUT=$(tick 1)
SENT=$(count_sends "$TMUX_LOG")
[ "$SENT" = 0 ] && ok "WINDDOWN reached only via an unreadable sample sends nothing" \
  || bad "WINDDOWN reached only via an unreadable sample sends nothing" "$(cat "$TMUX_LOG")"

echo
echo "quota-watch stand-down: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
