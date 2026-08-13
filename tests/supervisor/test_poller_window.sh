#!/bin/bash
# poller-window.sh is the shared tmux window lookup for advance-live.sh,
# lanes.sh, and poller-recover.sh. Duplicate poller windows are ambiguous:
# callers must get rc=2 and no guessed target.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HELPER="$HERE/../../scripts/supervisor/poller-window.sh"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"

S="poller-window-test-$$"
RT="$(mktemp -d "${TMPDIR:-/tmp}/poller-window-tmux.XXXXXX")"
unset TMUX
export TMUX_TMPDIR="$RT"
assert_isolated_tmux || exit 1
pass=0; fail=0

ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 - $2"; fail=$((fail+1)); }

cleanup() { unset TMUX; export TMUX_TMPDIR="$RT"; tmux kill-session -t "$S" 2>/dev/null; rm -rf "$RT"; }
trap cleanup EXIT INT TERM

if ! command -v tmux >/dev/null 2>&1; then
  echo "  SKIP no tmux on PATH"; exit 0
fi

echo "poller-window.sh"

tmux new-session -d -s "$S" -x 200 -y 50 -n not-poller
source "$HELPER"

out=$(poller_window_target "$S" 2>&1); rc=$?
[ "$rc" -eq 1 ] && [ -z "$out" ] && ok "zero poller windows returns 1 and prints nothing" \
  || bad "zero poller windows returns 1 and prints nothing" "rc=$rc out=$out"

tmux new-window -t "$S" -n inbox-poll -d
want=$(poller_window_ids "$S" | head -1)
out=$(poller_window_target "$S" 2>&1); rc=$?
[ "$rc" -eq 0 ] && [ "$out" = "$S:$want" ] && ok "one poller window returns its session-qualified window id" \
  || bad "one poller window returns its session-qualified window id" "rc=$rc out=$out want=$S:$want"

while IFS= read -r wid; do
  [ -n "$wid" ] && tmux kill-window -t "$S:$wid" 2>/dev/null
done < <(tmux list-windows -t "$S" -F '#{window_name}	#{window_id}' 2>/dev/null | awk -F'\t' '$1=="inbox-poll"{print $2}')

tmux new-window -t "$S" -n 'poller}window' -d
tmux new-window -t "$S" -n other-poller-window -d
brace_want=$(tmux list-windows -t "$S" -F '#{window_name}	#{window_id}' | awk -F'\t' '$1=="poller}window"{print $2}')
out=$(LANES_POLLER_WINDOW='poller}window' bash -c ". '$HELPER'; poller_window_ids '$S'" 2>&1); rc=$?
[ "$rc" -eq 0 ] && [ "$out" = "$brace_want" ] && ok "poller names containing a literal } return exactly the matching window id" \
  || bad "poller names containing a literal } return exactly the matching window id" "rc=$rc out=$out want=$brace_want"
out=$(LANES_POLLER_WINDOW='poller}window' bash -c ". '$HELPER'; poller_window_target '$S'" 2>&1); rc=$?
[ "$rc" -eq 0 ] && [ "$out" = "$S:$brace_want" ] && ok "poller target accepts a literal } name as a plain string" \
  || bad "poller target accepts a literal } name as a plain string" "rc=$rc out=$out want=$S:$brace_want"
tmux kill-window -t "$S:$brace_want" 2>/dev/null

# tmux's own -n/rename-window format-expand their argument, so a naive
# `-n 'poller#{window'` never creates a window literally named that --
# `#{window` isn't a valid variable reference and tmux silently truncates
# it to `poller`, which made this case pass identically whether the code
# was fixed or vulnerable (agent-supervisor#43 review, mutation-tested).
# `##` is tmux's own escape for a literal `#`, so `poller##{window` is the
# only `-n` argument that actually produces a window named `poller#{window`;
# verify that against tmux's own report before trusting it as the fixture.
tmux new-window -t "$S" -n 'poller##{window' -d
hash_want=$(tmux list-windows -t "$S" -F '#{window_name}	#{window_id}' | awk -F'\t' '$1=="poller#{window"{print $2}')
[ -n "$hash_want" ] || bad "fixture: a window literally named poller#{window exists" "no window matched"
out=$(LANES_POLLER_WINDOW='poller#{window' bash -c ". '$HELPER'; poller_window_ids '$S'" 2>&1); rc=$?
[ "$rc" -eq 0 ] && [ "$out" = "$hash_want" ] && ok "poller names containing #{ return exactly the matching window id" \
  || bad "poller names containing #{ return exactly the matching window id" "rc=$rc out=$out want=$hash_want"
tmux kill-window -t "$S:$hash_want" 2>/dev/null

tmux new-window -t "$S" -n inbox-poll -d
want=$(poller_window_ids "$S" | head -1)

# agent-supervisor#28: the watchdog LaunchAgent's environment sets only
# HOME, NOTIFY_ENV, and PATH -- no LANG/LC_ALL. Under that exact stripped
# environment, tmux sanitises the literal tab in a `"#{a}\t#{b}"` format
# string into '_', so a lookup built on splitting that tab returns EMPTY
# WITH RC=0 -- indistinguishable from "no poller window exists" -- even
# though the window is right there. Every case above inherits this shell's
# own LANG and cannot catch that; this one must run with it stripped.
strip_out=$(env -i HOME="$HOME" TMUX_TMPDIR="$RT" \
  PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin" \
  bash -c ". '$HELPER'; poller_window_target '$S'" 2>&1)
strip_rc=$?
[ "$strip_rc" -eq 0 ] && [ "$strip_out" = "$S:$want" ] \
  && ok "poller lookup finds the window under a stripped (no LANG/LC_ALL) environment" \
  || bad "poller lookup finds the window under a stripped (no LANG/LC_ALL) environment" "rc=$strip_rc out=$strip_out want=$S:$want"

tmux new-window -t "$S" -n inbox-poll -d
out=$(poller_window_target "$S" 2>&1); rc=$?
[ "$rc" -eq 2 ] && [ -z "$out" ] && ok "two poller windows returns 2 and prints nothing" \
  || bad "two poller windows returns 2 and prints nothing" "rc=$rc out=$out"

while IFS= read -r wid; do
  [ -n "$wid" ] && tmux kill-window -t "$S:$wid" 2>/dev/null
done < <(tmux list-windows -t "$S" -F '#{window_name}	#{window_id}' 2>/dev/null | awk -F'\t' '$1=="inbox-poll"{print $2}')

out=$(poller_window_target "$S" 2>&1); rc=$?
[ "$rc" -eq 1 ] && [ -z "$out" ] && ok "zero poller windows still returns 1 after escaped-name lookups" \
  || bad "zero poller windows still returns 1 after escaped-name lookups" "rc=$rc out=$out"

tmux new-window -t "$S" -n 'poller}window' -d
tmux new-window -t "$S" -n 'poller}window' -d
out=$(LANES_POLLER_WINDOW='poller}window' bash -c ". '$HELPER'; poller_window_target '$S'" 2>&1); rc=$?
[ "$rc" -eq 2 ] && [ -z "$out" ] && ok "duplicate literal } poller names still return 2 and print nothing" \
  || bad "duplicate literal } poller names still return 2 and print nothing" "rc=$rc out=$out"

# A query tmux could not answer (here: a session that does not exist) must
# be distinguishable from a confirmed-zero result -- the same fail-open
# shape #25's lsof probe had. rc=3 means "could not determine", never
# silently folded into rc=1's "confirmed zero poller windows".
out=$(poller_window_target "no-such-session-$$" 2>/dev/null); rc=$?
[ "$rc" -eq 3 ] && [ -z "$out" ] && ok "a session tmux cannot read returns 3 (could not determine), not 1 (confirmed zero)" \
  || bad "a session tmux cannot read returns 3 (could not determine), not 1 (confirmed zero)" "rc=$rc out=$out"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
