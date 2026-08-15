#!/bin/bash
# send.sh is the one verified send primitive (agent-supervisor#178):
# `verified_type` types and confirms landing before Enter is ever risked;
# `verified_submit` sends Enter and confirms the box actually emptied.
# `dispatch.sh`, `director-route.sh` and `inbox-route.sh` all call these now
# instead of `tmux send-keys` directly -- their own suites cover the
# call-site behaviour. This file tests the primitive itself, directly,
# against the same stub `test_dispatch.sh` already trusts to model a pane's
# input buffer.
#
# THE STRAND THIS ISSUE IS ABOUT, REPRODUCED: `Enter` does not submit text a
# previous `send-keys` left sitting in the box. The stub's
# `DISPATCH_SWALLOW_ENTER` models exactly that -- the keys arrive, the box
# keeps the text, nothing runs -- and the test below puts text in the box,
# sends Enter, and asserts `verified_submit` DETECTS the strand rather than
# reporting success. That is #178's fail-closed acceptance, exercised
# directly rather than through a caller.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SEND="$HERE/../../scripts/supervisor/send.sh"
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }

echo "send.sh"

D=$(mktemp -d); mkdir -p "$D/bin"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"
cat > "$D/lanes" <<'FIX'
1|w|claude.exe|❯ ready|1|0
FIX
TARGET="t:1"

reset_pane() { rm -rf "$D/panes"; mkdir -p "$D/panes"; : > "$D/tmux.log"; }

run_send() { # run_send <extra env...> -- <function call...>
  ( PATH="$D/bin:$PATH" LANES_FIXTURE="$D/lanes" LANES_SESSION=t \
      TMUX_LOG="$D/tmux.log" TMUX_PANES="$D/panes" \
      "$@" )
}

# --- 1. THE STRAND, DETECTED --------------------------------------------
# Text already sits in the box (a previous, unrelated send-keys -- exactly
# #179's `merge the PR` shape: nobody here typed it THIS call). Enter is
# sent, but the stub's DISPATCH_SWALLOW_ENTER=1 models it being swallowed --
# the box keeps the text. verified_submit must fail, not report success.
reset_pane
printf '%s' 'merge the PR' > "$D/panes/1"
out=$(run_send env DISPATCH_SWALLOW_ENTER=1 bash -c "
  . '$SEND'
  verified_submit '$TARGET' --confirm-tries 2 --confirm-settle 0
  echo \"rc=\$? status=\$SEND_STATUS\"
")
if grep -q 'rc=3 status=stranded' <<<"$out"; then
  ok "a strand (text left in the box, Enter swallowed) is DETECTED, not reported as sent"
else
  bad "a strand should be detected as stranded (rc=3)" "$out"
fi
grep -q 'send-keys -t t:1 Enter$' "$D/tmux.log" 2>/dev/null \
  && ok "Enter was actually sent (this is not a case where nothing was tried)" \
  || bad "Enter was never sent -- this test proves nothing about #178" "$(cat "$D/tmux.log")"
[ "$(cat "$D/panes/1" 2>/dev/null)" = "merge the PR" ] \
  && ok "...and the text is CONFIRMED still sitting in the box afterward" \
  || bad "the box should still hold the unsubmitted text" "$(cat "$D/panes/1" 2>/dev/null)"
[ ! -e "$D/panes/1.submitted" ] \
  && ok "...and nothing was ever recorded as submitted" \
  || bad "something was wrongly recorded as submitted" "$(cat "$D/panes/1.submitted" 2>/dev/null)"

# --- 2. a genuinely empty box after Enter IS submitted -------------------
reset_pane
out=$(run_send bash -c "
  . '$SEND'
  verified_type '$TARGET' 'reply text' --settle 0 --retries 1
  t=\$?
  verified_submit '$TARGET' --confirm-tries 2 --confirm-settle 0
  echo \"type_rc=\$t submit_rc=\$? status=\$SEND_STATUS\"
")
want_contains() { grep -qF -- "$2" <<<"$3" && ok "$1" || bad "$1" "$3"; }
want_contains "typed text lands and Enter submits it cleanly" "type_rc=0 submit_rc=0 status=submitted" "$out"

# --- 3. THE RETRY: a dropped-prefix first attempt is rescued -------------
# DISPATCH_DROP_PREFIX truncates the FIRST type attempt over budget -- the
# proof token (the tail of the message) will not be present yet.
# verified_type's C-u-and-retype must recover it on the second attempt.
reset_pane
MESSAGE="this message is deliberately long enough that a dropped prefix changes what the tail check sees -- proof-tail-marker"
# Both ends, same as dispatch.sh's own proof tokens: the HEAD token is what
# `DISPATCH_DROP_PREFIX` actually eats on the first attempt (checking only
# the tail would pass a dropped prefix, which is the failure #178's loop
# exists to catch).
out=$(run_send env DISPATCH_DROP_PREFIX=20 bash -c "
  . '$SEND'
  verified_type '$TARGET' '$MESSAGE' --settle 0 --retries 2 --proof 'this message' --proof 'proof-tail-marker'
  echo \"rc=\$? status=\$SEND_STATUS\"
")
want_contains "a dropped-prefix first attempt is rescued by the retry and lands" "rc=0 status=landed" "$out"
grep -qx 'C-u' "$D/panes/1.keys" \
  && ok "...and the retry actually cleared the box with C-u before retyping" \
  || bad "no C-u was sent -- the retry never fired, so this proves nothing" "$(cat "$D/panes/1.keys" 2>/dev/null)"
# The proof tokens alone are not the whole story -- BOTH surviving a
# substring check is also what a corrupted "garbage-then-clean-retype"
# buffer would produce with no clearing in between. What C-u actually buys
# is a CLEAN box: exactly the message, nothing prepended.
[ "$(cat "$D/panes/1" 2>/dev/null)" = "$MESSAGE" ] \
  && ok "...and the box holds EXACTLY the clean message, not a dropped-prefix-plus-retype mess" \
  || bad "the box should hold exactly the retyped message" "$(cat "$D/panes/1" 2>/dev/null)"

# --- 4. MUTATION CHECK: remove the C-u-and-retype ------------------------
# agent-supervisor#178's acceptance. Patch a copy of send.sh with the retry
# loop's C-u-and-retype removed, rerun test 3's exact scenario, and confirm
# it now FAILS to land (red) -- proving the retry is what made test 3 pass,
# not incidental to it. Then the real, unmutated file is confirmed green
# again (it already was, above -- rerun here so both directions are in one
# place, as the brief asks).
MUTANT="$D/send-mutant.sh"
# send.sh sources ./input-box.sh relative to its OWN location -- the mutant
# needs a copy sitting next to it so that resolves, not the real send.sh's
# directory.
cp "$HERE/../../scripts/supervisor/input-box.sh" "$D/input-box.sh"
patch_rc=0
python3 - "$SEND" "$MUTANT" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''    if [ "$attempt" -lt "$retries" ]; then
      # Whatever landed partially (or nothing) is cleared before the retype
      # -- retyping ON TOP of a dropped prefix would just compound it.
      tmux send-keys -t "$target" C-u 2>/dev/null
      sleep "$settle"
    fi'''
assert marker in text, "the C-u-and-retype block not found -- send.sh shape changed"
assert text.count(marker) == 1, "the C-u-and-retype block not unique -- send.sh shape changed"
open(dst, "w").write(text.replace(marker, "    :", 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of send.sh with the C-u-and-retype removed" \
    "could not patch $SEND (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of send.sh with the C-u-and-retype removed"
  # The proof-token check alone cannot tell this apart from the real fix:
  # with no clear, the second attempt's full retype is simply APPENDED after
  # the first attempt's dropped-prefix leftovers, and both proof tokens
  # still turn up somewhere in that concatenation -- a substring check finds
  # them either way. What the retry's C-u actually buys is a CLEAN box, so
  # that -- exact content, not mere containment -- is what this mutation has
  # to break, the same way test 3's own extra assertion (above) checks it.
  reset_pane
  run_send env DISPATCH_DROP_PREFIX=20 bash -c "
    . '$MUTANT'
    verified_type '$TARGET' '$MESSAGE' --settle 0 --retries 2 --proof 'this message' --proof 'proof-tail-marker'
  " >/dev/null
  mut_box=$(cat "$D/panes/1" 2>/dev/null)
  if [ -n "$mut_box" ] && [ "$mut_box" != "$MESSAGE" ]; then
    ok "MUTATION CONFIRMED (red): without the C-u-and-retype, the retry just appends onto the dropped-prefix leftovers instead of retyping cleanly (test 3's exact-content assertion above would now be red)"
  else
    bad "mutation confirmed: without the clear-and-retype, the box should hold a corrupted, non-clean buffer" "got: '$mut_box' want: something other than the clean message '$MESSAGE'"
  fi

  # Restore direction: the real, unpatched file, same exact scenario, green.
  reset_pane
  run_send env DISPATCH_DROP_PREFIX=20 bash -c "
    . '$SEND'
    verified_type '$TARGET' '$MESSAGE' --settle 0 --retries 2 --proof 'this message' --proof 'proof-tail-marker'
  " >/dev/null
  restored_box=$(cat "$D/panes/1" 2>/dev/null)
  if [ "$restored_box" = "$MESSAGE" ]; then
    ok "RESTORED (green): the real send.sh, same scenario, lands the exact clean message via the retry again"
  else
    bad "the restored file should land exactly the clean message again" "got: '$restored_box'"
  fi
fi

# --- 5. MULTI-LINE MESSAGES ARE REFUSED, --literal OR NOT ----------------
# agent-supervisor#186: an embedded newline is read by tmux as a real Enter
# keystroke mid-type -- with or without `-l` -- so `verified_type` must
# refuse before ever calling `tmux send-keys`, not attempt the send and let
# a stray Enter fire partway through. Exercised with `--literal` too,
# because that is exactly the flag a caller might reach for believing it
# makes this safe (the finding's whole point: it does not).
for lit_flag in "" "--literal"; do
  reset_pane
  out=$(run_send bash -c "
    . '$SEND'
    verified_type '$TARGET' \$'echo pwned-line-one\necho line-two-should-be-unsent' --settle 0 --retries 1 $lit_flag
    echo \"rc=\$? status=\$SEND_STATUS\"
  ")
  label="a multi-line message is refused before any tmux call${lit_flag:+ (even with $lit_flag)}"
  want_contains "$label" "rc=1 status=send_failed" "$out"
  [ ! -s "$D/tmux.log" ] \
    && ok "...and tmux send-keys was never invoked (fail closed, not fail-after-trying)" \
    || bad "tmux was called despite the refusal -- the whole point is to refuse BEFORE sending" "$(cat "$D/tmux.log")"
  [ ! -e "$D/panes/1" ] \
    && ok "...and the box was never touched -- no partial type, no stray Enter" \
    || bad "the pane buffer should never have been written" "$(cat "$D/panes/1" 2>/dev/null)"
done

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
