#!/bin/bash
# agent-supervisor#116/#141: a test fixture that writes a wall-clock
# timestamp and a script that later reads/recomputes an age from it are two
# INDEPENDENT `date`/epoch reads separated by a real process launch. A tick
# of the real clock between them is routine, not a bug -- an exact-match
# assertion built on that pair (a literal grep for "5460", `[ "$age" -eq
# 5460 ]`) fails on that routine tick. #116 measured it in CI (5460 ->
# 5461); #141 named it as a shared root cause across two tests wobbling the
# same day and asked for one helper instead of each test hand-rolling its
# own tolerance.
#
# assert_age_near <actual> <expected> <tolerance>
# True (exit 0) iff <actual> is an integer within <expected> +/- <tolerance>.
# Prints nothing and makes no ok/bad call itself -- every test file in this
# suite has its own ok()/bad() shape, so the caller reports the result:
#
#   if assert_age_near "$age_s" 5460 3; then
#     ok "the failure names the age"
#   else
#     bad "the failure names the age" "want 5460+/-3, got '$age_s': $out"
#   fi
assert_age_near() {
  local actual="$1" expected="$2" tolerance="$3"
  case "$actual" in '' | *[!0-9]*) return 1 ;; esac
  case "$expected" in '' | *[!0-9]*) return 1 ;; esac
  case "$tolerance" in '' | *[!0-9]*) return 1 ;; esac
  local lo=$((expected - tolerance)) hi=$((expected + tolerance))
  [ "$actual" -ge "$lo" ] && [ "$actual" -le "$hi" ]
}
