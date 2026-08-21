#!/bin/bash
# agent-supervisor#466: director-loop.sh must re-resolve its stale target by
# window NAME, not merely by "exactly one window survives" -- the real
# incident had FOUR windows after the restart (the Director's own plus three
# unrelated sanity-* cron panes), so the old single-window fallback refused
# every single tick even though exactly one of those four was unambiguously
# the Director by name (`supervisor`, bootstrap-session.sh's own
# LANES_SUPERVISOR_NAME convention -- the same marker quota-watch.sh already
# resolves its equivalent target by, see test_quota_watch_target.sh).
#
# Each case is mutation-checked: a one-line sed mutation on a COPY of the
# real script proves the assertion actually depends on the code under test,
# not on fixture luck.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOOP="$HERE/../../scripts/supervisor/director-loop.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }

echo "director-loop.sh -- re-resolves the stale target by window NAME among several candidates (#466)"

D=$(mktemp -d)
trap 'rm -rf "$D"' EXIT

# Fixture repo standing in for the shared checkout SUPERVISOR_REPO names, so
# the live/ registration gate is a no-op and these cases reach the
# window-resolution logic under test -- same shape every other
# director-loop.sh test in this suite uses.
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/src"
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

run() { # run <script> <state-dir> <windows-named> <good-target>
  PATH="$D:$PATH" TMUX_LOG="$2/tmux.log" TMUX_SESSIONS="director" \
    TMUX_WINDOWS_NAMED="$3" TMUX_GOOD_TARGET="$4" \
    SUPERVISOR_STATE="$2" SUPERVISOR_LIVE="$LIVE" SUPERVISOR_REPO="$SRC" \
    QUOTA_GATE="$QUOTA_SAFE" DIRECTOR_LOOP_TARGET="director:@3" \
    bash "$1" 2>&1
}

# The real incident's shape: the Director's own window plus three unrelated
# sanity-* cron panes, none of which is @3 any more.
WINS_FOUR='@71\tsanity-output\n@40\tsupervisor\n@72\tsanity-blocker\n@73\tsanity-codex-cross-check'

# --- GREEN: real script resolves by name among four windows, sends there ---
STATE=$(mktemp -d "$D/state.XXXXXX")
out=$(run "$LOOP" "$STATE" "$WINS_FOUR" "director:@40")
if grep -q "resolved to director:@40 by window name 'supervisor'" <<<"$out"; then
  ok "GREEN: resolves the stale target to the ONE window named 'supervisor' among four"
else
  bad "GREEN: resolves the stale target to the ONE window named 'supervisor' among four" "$out"
fi
if grep -q '^send-keys -t =director:@40 -l Director tick' "$STATE/tmux.log"; then
  ok "GREEN: the tick was sent to the resolved (renamed-by-identity) window, not the stale @3"
else
  bad "GREEN: the tick was sent to the resolved window" "$(cat "$STATE/tmux.log")"
fi

# --- RED: mutate name-count computation so it never reads as exactly one --
# forces the fix's name-based path off; with four windows and no exact
# single-window fallback available either, the old-style refusal is the only
# path left -- proving this test actually depends on the name resolution.
BROKEN_NAME="$D/director-loop-broken-name.sh"
sed 's/name_count=$(printf.*/name_count=0/' "$LOOP" > "$BROKEN_NAME"
if grep -q 'name_count=0$' "$BROKEN_NAME"; then
  ok "constructed a mutated copy with name-count resolution disabled"
else
  bad "constructed a mutated copy with name-count resolution disabled" "sed did not match -- check director-loop.sh's exact source line"
fi
chmod +x "$BROKEN_NAME"
STATE=$(mktemp -d "$D/state.XXXXXX")
out=$(run "$BROKEN_NAME" "$STATE" "$WINS_FOUR" "director:@40")
rc=$?
want_exit "RED: with name-resolution disabled, four windows is still ambiguous -- refuses" "$rc" 3 "$out"
if grep -q '^send-keys' "$STATE/tmux.log" 2>/dev/null; then
  bad "RED: nothing gets sent when name-resolution cannot fire" "$(cat "$STATE/tmux.log")"
else
  ok "RED: nothing gets sent when name-resolution cannot fire"
fi

# --- ambiguous: TWO windows both named 'supervisor' -- refuses, no guess ---
WINS_DUP='@11\tsupervisor\n@23\tsupervisor'

STATE=$(mktemp -d "$D/state.XXXXXX")
out=$(run "$LOOP" "$STATE" "$WINS_DUP" "")
rc=$?
want_exit "two windows named 'supervisor' -- refuses rather than guessing which" "$rc" 3 "$out"
if grep -q "2 windows in director are named 'supervisor' -- refusing to guess" <<<"$out"; then
  ok "names the ambiguity reason"
else
  bad "names the ambiguity reason" "$out"
fi
if grep -q '^send-keys' "$STATE/tmux.log" 2>/dev/null; then
  bad "nothing is sent when two candidates tie" "$(cat "$STATE/tmux.log")"
else
  ok "nothing is sent when two candidates tie"
fi

# --- RED: mutate the exactness guard (-eq 1 -> -ge 1) so two ties "resolve"
# instead of refusing -- proves the ambiguity test depends on that guard. ---
BROKEN_TIE="$D/director-loop-broken-tie.sh"
sed 's/if \[ "$name_count" -eq 1 \]; then/if [ "$name_count" -ge 1 ]; then/' "$LOOP" > "$BROKEN_TIE"
if grep -qF 'if [ "$name_count" -ge 1 ]; then' "$BROKEN_TIE"; then
  ok "constructed a mutated copy that accepts >1 name match as if it were exact"
else
  bad "constructed a mutated copy that accepts >1 name match as if it were exact" "sed did not match -- check director-loop.sh's exact source line"
fi
chmod +x "$BROKEN_TIE"
STATE=$(mktemp -d "$D/state.XXXXXX")
out=$(run "$BROKEN_TIE" "$STATE" "$WINS_DUP" "")
if grep -q "refusing to guess" <<<"$out"; then
  bad "RED: with the exactness guard broken, two ties still correctly refuses (mutation had no effect -- test cannot catch a broken guard)" "$out"
else
  ok "RED: with the exactness guard broken, it treats the tie as resolved instead of refusing"
fi
if grep -q "resolved to director:" <<<"$out"; then
  ok "RED: ...concretely, it claims a (bogus, concatenated) resolution rather than refusing"
else
  bad "RED: ...concretely, it claims a (bogus, concatenated) resolution rather than refusing" "$out"
fi

echo
echo "director-loop name resolution: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
