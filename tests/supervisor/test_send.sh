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
# agent-supervisor#209: `bash` unqualified on this machine's PATH resolves to
# Homebrew's bash (5.x), NOT production's `/bin/bash` (Apple's 3.2.57) --
# `run_send`'s inner `bash -c "..."` calls below were therefore exercising
# send.sh under a newer bash than production ever runs, the exact #163 shape
# the issue asked to check for. Pinning a `bash` shim in front of PATH that
# is really `/bin/bash` makes every `run_send ... bash -c ...` call below
# actually run under the interpreter this suite claims to test.
ln -sf /bin/bash "$D/bin/bash"
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

# --- 6. POSITION-AWARE PROOF: a junk-prefixed message must NOT read as
# landed (agent-supervisor#193) -----------------------------------------
# `at25-rev33`'s reproduced shape: a `/clear` whose Enter never submitted
# leaves "/clear" sitting in the box; a retype WITHOUT `--preclear` (how
# `dispatch.sh` called `verified_type` before agent-supervisor#240 -- it
# relied on the prior `/clear` alone) then lands the brief GLUED onto it:
# "/clearRead <brief>...".
# `Read <brief>` is still a true substring of that -- the ORIGINAL bug -- so
# this is reproduced first against the pre-#193 `--proof` check to show the
# false positive live, then against `--proof-head` to show the fix.
reset_pane
printf '%s' '/clear' > "$D/panes/1"
MESSAGE="Read /brief.md and do exactly what it says. Work in the worktree, never in the shared checkout at /repo."
out=$(run_send bash -c "
  . '$SEND'
  verified_type '$TARGET' '$MESSAGE' --settle 0 --retries 1 --proof 'Read /brief.md' --proof 'shared checkout'
  echo \"rc=\$? status=\$SEND_STATUS\"
")
want_contains "REPRODUCED: the pre-#193 whole-pane --proof check wrongly reports a junk-prefixed message as landed" "rc=0 status=landed" "$out"

reset_pane
printf '%s' '/clear' > "$D/panes/1"
out=$(run_send bash -c "
  . '$SEND'
  verified_type '$TARGET' '$MESSAGE' --settle 0 --retries 1 --proof-head 'Read /brief.md' --proof 'shared checkout'
  echo \"rc=\$? status=\$SEND_STATUS\"
")
want_contains "FIXED: --proof-head refuses the same junk-prefixed message -- it does not START the box" "rc=2 status=not_landed" "$out"

# ...and with the retry dispatch.sh actually uses (--retries 2), the
# C-u-and-retype this file already trusts (test 3) recovers it: the second
# attempt starts from a clean box, so the head token opens it for real.
reset_pane
printf '%s' '/clear' > "$D/panes/1"
out=$(run_send bash -c "
  . '$SEND'
  verified_type '$TARGET' '$MESSAGE' --settle 0 --retries 2 --proof-head 'Read /brief.md' --proof 'shared checkout'
  echo \"rc=\$? status=\$SEND_STATUS\"
")
want_contains "the retry recovers a junk-prefixed message into a clean, anchored send" "rc=0 status=landed" "$out"
[ "$(cat "$D/panes/1" 2>/dev/null)" = "$MESSAGE" ] \
  && ok "...and the box holds EXACTLY the clean message, not the glued garbage" \
  || bad "the box should hold exactly the retyped message" "$(cat "$D/panes/1" 2>/dev/null)"

# --- MUTATION CHECK: de-anchor _send_head_matches -------------------------
# Patch a copy of send.sh so the head check becomes a plain substring test
# (the pre-#193 shape) and rerun test 6's exact --retries 1 scenario --
# confirm it goes back to a false "landed" (red), proving the anchor, not
# something else, is what makes that assertion pass.
MUTANT_HEAD="$D/send-mutant-head.sh"
cp "$HERE/../../scripts/supervisor/input-box.sh" "$D/input-box.sh"
patch_rc=0
python3 - "$SEND" "$MUTANT_HEAD" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '  [ -n "$body" ] && [[ "$body" == "$needle"* ]]'
assert marker in text, "_send_head_matches's anchor line not found -- send.sh shape changed"
assert text.count(marker) == 1, "the anchor line is not unique -- send.sh shape changed"
mutated = '  [ -n "$body" ] && [[ "$body" == *"$needle"* ]]'
open(dst, "w").write(text.replace(marker, mutated, 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of send.sh with the head anchor de-anchored" \
    "could not patch $SEND (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of send.sh with the head anchor de-anchored"
  reset_pane
  printf '%s' '/clear' > "$D/panes/1"
  out=$(run_send bash -c "
    . '$MUTANT_HEAD'
    verified_type '$TARGET' '$MESSAGE' --settle 0 --retries 1 --proof-head 'Read /brief.md' --proof 'shared checkout'
    echo \"rc=\$? status=\$SEND_STATUS\"
  ")
  want_contains "MUTATION CONFIRMED (red): a de-anchored head check reports the junk-prefixed message landed again" "rc=0 status=landed" "$out"

  reset_pane
  printf '%s' '/clear' > "$D/panes/1"
  out=$(run_send bash -c "
    . '$SEND'
    verified_type '$TARGET' '$MESSAGE' --settle 0 --retries 1 --proof-head 'Read /brief.md' --proof 'shared checkout'
    echo \"rc=\$? status=\$SEND_STATUS\"
  ")
  want_contains "RESTORED (green): the real send.sh, same scenario, refuses again" "rc=2 status=not_landed" "$out"
fi

# --- 7. VERIFIED PRE-CLEAR (agent-supervisor#193) -------------------------
# `dispatch.sh` used to send `/clear` + Enter blind, with no confirmation the
# screen actually blanked -- exactly the gap `at25-rev33` fell through: the
# Enter never submitted, and nothing downstream of the send noticed until
# the corrupted retype (test 6, above) either. `verified_preclear` closes
# that: `/clear` + Enter, then confirm the box reads EMPTY before returning
# success.
reset_pane
out=$(run_send bash -c "
  . '$SEND'
  verified_preclear '$TARGET' --settle 0 --retries 1
  echo \"rc=\$? status=\$SEND_STATUS\"
")
want_contains "a clean /clear is confirmed landed" "rc=0 status=landed" "$out"
[ ! -s "$D/panes/1" ] \
  && ok "...and the box is confirmed EMPTY afterward" \
  || bad "the box should be empty after a clean pre-clear" "$(cat "$D/panes/1" 2>/dev/null)"

# The failure #193 actually hit: /clear's own Enter is swallowed (the stub's
# narrower DISPATCH_SWALLOW_PRECLEAR_ENTER models exactly that -- only the
# Enter that would submit a buffer holding EXACTLY "/clear"). Retries
# exhausted with the swallow persisting every attempt -- verified_preclear
# must abort, not type over the unsubmitted "/clear".
reset_pane
out=$(run_send env DISPATCH_SWALLOW_PRECLEAR_ENTER=1 bash -c "
  . '$SEND'
  verified_preclear '$TARGET' --settle 0 --retries 2
  echo \"rc=\$? status=\$SEND_STATUS\"
")
want_contains "a /clear whose Enter never submits is DETECTED, not reported as landed" "rc=2 status=not_landed" "$out"
[ "$(cat "$D/panes/1" 2>/dev/null)" = "/clear" ] \
  && ok "...and the box is CONFIRMED still holding the unsubmitted /clear afterward" \
  || bad "the box should still hold the unsubmitted /clear" "$(cat "$D/panes/1" 2>/dev/null)"
retry_count=$(grep -c '^send-keys -t t:1 /clear Enter$' "$D/tmux.log" 2>/dev/null || echo 0)
[ "$retry_count" -eq 2 ] \
  && ok "...and it actually retried (--retries 2), not just failed once" \
  || bad "expected 2 /clear attempts logged, got $retry_count" "$(cat "$D/tmux.log")"

# --- MUTATION CHECK: skip the post-/clear confirmation --------------------
MUTANT_PRECLEAR="$D/send-mutant-preclear.sh"
patch_rc=0
python3 - "$SEND" "$MUTANT_PRECLEAR" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '''    SEND_BOX_STATE=$(tmux capture-pane -pe -t "$target" 2>/dev/null | input_box_state)
    if [ "$SEND_BOX_STATE" = empty ]; then
      SEND_STATUS=landed
      return 0
    fi
  done
  SEND_STATUS=not_landed
  return 2
}

# verified_submit'''
assert marker in text, "verified_preclear's confirmation block not found -- send.sh shape changed"
assert text.count(marker) == 1, "the confirmation block is not unique -- send.sh shape changed"
mutated = '''    SEND_STATUS=landed
    return 0
  done
  SEND_STATUS=not_landed
  return 2
}

# verified_submit'''
open(dst, "w").write(text.replace(marker, mutated, 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched a copy of send.sh with the pre-clear confirmation removed" \
    "could not patch $SEND (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched a copy of send.sh with the pre-clear confirmation removed"
  reset_pane
  out=$(run_send env DISPATCH_SWALLOW_PRECLEAR_ENTER=1 bash -c "
    . '$MUTANT_PRECLEAR'
    verified_preclear '$TARGET' --settle 0 --retries 2
    echo \"rc=\$? status=\$SEND_STATUS\"
  ")
  want_contains "MUTATION CONFIRMED (red): without the confirmation, an unsubmitted /clear reports landed" "rc=0 status=landed" "$out"

  reset_pane
  out=$(run_send env DISPATCH_SWALLOW_PRECLEAR_ENTER=1 bash -c "
    . '$SEND'
    verified_preclear '$TARGET' --settle 0 --retries 2
    echo \"rc=\$? status=\$SEND_STATUS\"
  ")
  want_contains "RESTORED (green): the real send.sh, same scenario, detects it again" "rc=2 status=not_landed" "$out"
fi

# --- 8. verified_send REACHES verified_type/verified_submit CLEANLY -----
# agent-supervisor#209: `verified_send` builds `type_args`/`submit_args` as
# bash arrays and, when a caller supplies none of a given kind (e.g.
# `--proof` only, no `--confirm-tries`/`--confirm-settle`), the array stays
# genuinely empty. Forwarding an empty array must add ZERO arguments to the
# downstream call -- not a single empty-string argument, which
# `verified_submit`'s option parser reads as `unknown option` (with nothing
# after it: the empty string itself). Exercised for all four combinations,
# under the `/bin/bash` shim pinned above, with `verified_type`/
# `verified_submit` wrapped to record exactly what they were called with.
send_probe() { # send_probe <label> <verified_send-args...>
  local label="$1"; shift
  reset_pane
  out=$(run_send bash -c "
    . '$SEND'
    orig_type=\$(declare -f verified_type); eval \"real_\${orig_type}\"
    verified_type() { echo \"TYPE_ARGC=\$#\" >> '$D/probe.log'; real_verified_type \"\$@\"; }
    orig_submit=\$(declare -f verified_submit); eval \"real_\${orig_submit}\"
    verified_submit() { echo \"SUBMIT_ARGC=\$#\" >> '$D/probe.log'; real_verified_submit \"\$@\"; }
    verified_send '$TARGET' 'probe message' $*
    echo \"rc=\$? status=\$SEND_STATUS\"
  " 2>&1)
  probe=$(cat "$D/probe.log" 2>/dev/null)
  if grep -q 'unknown option' <<<"$out$probe"; then
    bad "$label: an empty forwarded array must not read as 'unknown option'" "$out"$'\n'"$probe"
    return
  fi
  ok "$label: no phantom empty argument reached verified_type/verified_submit"
}

: > "$D/probe.log"; send_probe "no options at all"
: > "$D/probe.log"; send_probe "--proof only (the case #209's live repro broke)" --proof CHECKPOINT
: > "$D/probe.log"; send_probe "--confirm-tries only" --confirm-tries 2
: > "$D/probe.log"; send_probe "both --proof and --confirm-tries" --proof CHECKPOINT --confirm-tries 2

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
