#!/bin/bash
# director-inbox.sh's own stated goal (director-inbox.sh:24-26) is "losing the
# record of an instruction is worse than re-reading one." Two paths broke that
# goal, both found by independent review of PR #86 and filed as #88:
#
#   1. drain silently and permanently deleted a line it could not json.loads
#      -- exactly the shape of a torn/partial write, the case most worth
#      keeping for hand inspection.
#   2. drain's read-modify-write over the whole file had no lock, so a `post`
#      landing between drain's read and its truncate-and-rewrite was
#      overwritten out of existence with no trace.
#
# The two `want_exit ... 1` assertions below (malformed-line preservation,
# post-racing-drain survival) are the load-bearing regression tests -- they
# fail against the pre-#88 script and pass after the fix.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INBOX="$HERE/../../scripts/supervisor/director-inbox.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_contains()    { if grep -q -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "$3"; fi }
want_not_contains() { if grep -q -- "$2" <<<"$3"; then bad "$1" "$3"; else ok "$1"; fi }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }

echo "director-inbox.sh"

D=$(mktemp -d)
run() { DIRECTOR_INBOX="$D/box.jsonl" bash "$INBOX" "$@"; }

# --- post then read: message appears, read does not mutate the file --------
run post "first message" >/dev/null
before=$(cat "$D/box.jsonl")
out=$(run read)
after=$(cat "$D/box.jsonl")
want_contains "read shows a posted message" "first message" "$out"
[ "$before" = "$after" ] && ok "read does not mutate the file" \
  || bad "read does not mutate the file" "before:$before"$'\n'"after:$after"

# --- post then drain then drain: pin idempotency ----------------------------
out=$(run drain)
want_contains "first drain prints the message" "first message" "$out"
snapshot=$(cat "$D/box.jsonl")
out2=$(run drain)
want_contains "second drain reports nothing new" "no new director messages" "$out2"
[ "$snapshot" = "$(cat "$D/box.jsonl")" ] && ok "second drain leaves the file unchanged" \
  || bad "second drain leaves the file unchanged" "$(cat "$D/box.jsonl")"

# --- missing/empty box: read and drain already handled, pin it -------------
rm -f "$D/box.jsonl"
out=$(run read); rc=$?
want_exit "read against a missing box exits 0" "$rc" 0
want_contains "read against a missing box says so" "no director messages" "$out"
out=$(run drain); rc=$?
want_exit "drain against a missing box exits 0" "$rc" 0

: > "$D/box.jsonl"
out=$(run drain); rc=$?
want_exit "drain against an empty box exits 0" "$rc" 0
want_contains "drain against an empty box says so" "no director messages" "$out"

# --- malformed line: preserved verbatim across drain, with a warning -------
rm -f "$D/box.jsonl"
run post "good message" >/dev/null
printf '{"at": "bad", "read": false, "tex\n' >> "$D/box.jsonl"
out=$(run drain 2>"$D/stderr.txt")
want_contains "drain still surfaces the well-formed message" "good message" "$out"
want_contains "drain warns about the malformed line" "malformed" "$(cat "$D/stderr.txt")"
want_contains "the malformed line survives the drain verbatim" \
  '{"at": "bad", "read": false, "tex' "$(cat "$D/box.jsonl")"

# --- non-object JSON value: preserved verbatim, does not jam read/drain ----
# `[1,2,3]`, a bare string, or a number all parse fine and pass `r is not
# None`, then crash on `r.get("read")` unless routed through the same
# preserve-verbatim path a malformed line takes (#90 finding 2).
rm -f "$D/box.jsonl"
run post "legit message" >/dev/null
printf '[1,2,3]\n' >> "$D/box.jsonl"
out=$(run drain 2>"$D/stderr.txt"); rc=$?
want_exit "drain does not crash on a non-object JSON value" "$rc" 0
want_contains "drain still surfaces the well-formed message" "legit message" "$out"
want_contains "drain warns about the non-object value" "not a JSON object" "$(cat "$D/stderr.txt")"
want_contains "the non-object line survives the drain verbatim" '[1,2,3]' "$(cat "$D/box.jsonl")"
out2=$(run read 2>"$D/stderr2.txt"); rc2=$?
want_exit "a subsequent read does not crash either" "$rc2" 0

# --- kill mid-write: drain's rewrite is atomic, a kill leaves the box intact
# Patch a copy of the script to write the first line to the temp file, sleep,
# then SIGKILL it there -- before the second line is written and before the
# atomic os.replace() over the box. If drain truncated the box in place
# instead of write-temp-then-rename, this is exactly the window that leaves
# the box empty with nothing on stderr (#90 finding 1).
rm -f "$D/box.jsonl"
run post "keep-1" >/dev/null
run post "keep-2" >/dev/null
before=$(cat "$D/box.jsonl")
SLOW2="$D/slow-drain-write.sh"
patch_rc=0
python3 - "$INBOX" "$SLOW2" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = (
    '            for line in out_lines:\n'
    '                handle.write(line + "\\n")\n'
    '        os.replace(tmp, box)'
)
replacement = (
    '            for _i, line in enumerate(out_lines):\n'
    '                handle.write(line + "\\n")\n'
    '                handle.flush()\n'
    '                if _i == 0:\n'
    '                    import time; time.sleep(1)\n'
    '        os.replace(tmp, box)'
)
assert marker in text, "drain write-loop marker not found -- script shape changed"
assert text.count(marker) == 1, "drain write-loop marker not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  # If the write-temp-then-rename shape is gone, the marker this patch
  # depends on is gone too, and silently doing nothing here would report a
  # trivial "before == after" pass -- exactly the false-green shape #90
  # finding 3 is about. Fail loudly instead of skipping quietly.
  bad "a kill mid-write leaves the box exactly as it was" \
    "setup could not patch $INBOX for this test (patch script exited $patch_rc) -- treating as a failure, not a skip"
  bad "keep-1 survives a kill mid-write" "skipped: patch setup failed"
  bad "keep-2 survives a kill mid-write" "skipped: patch setup failed"
else
  DIRECTOR_INBOX="$D/box.jsonl" bash "$SLOW2" drain >/dev/null 2>&1 &
  drain_pid=$!
  sleep 0.3
  # `drain_pid` is the bash wrapper; the actual write happens in the python3
  # child it spawned via heredoc, and killing the parent alone leaves that
  # child running to finish the sleep and complete the write, contaminating
  # every test that runs after this one. Kill the child first.
  child_pid=$(pgrep -P "$drain_pid" 2>/dev/null)
  kill -9 $child_pid "$drain_pid" 2>/dev/null
  wait "$drain_pid" 2>/dev/null
  after=$(cat "$D/box.jsonl")
  [ "$before" = "$after" ] && ok "a kill mid-write leaves the box exactly as it was" \
    || bad "a kill mid-write leaves the box exactly as it was" "before:$before"$'\n'"after:$after"
  want_contains "keep-1 survives a kill mid-write" "keep-1" "$after"
  want_contains "keep-2 survives a kill mid-write" "keep-2" "$after"
fi

# --- two concurrent posts: both survive -------------------------------------
rm -f "$D/box.jsonl"
run post "concurrent-a" >/dev/null &
p1=$!
run post "concurrent-b" >/dev/null &
p2=$!
wait "$p1" "$p2"
final=$(cat "$D/box.jsonl")
want_contains "concurrent post a survives" "concurrent-a" "$final"
want_contains "concurrent post b survives" "concurrent-b" "$final"
lines=$(grep -c . "$D/box.jsonl")
[ "$lines" = 2 ] && ok "both concurrent posts landed as two lines" \
  || bad "both concurrent posts landed as two lines" "$final"

# --- post racing drain: the post survives -----------------------------------
# Patch a copy of the script to sleep AFTER drain has finished reading (rows
# and pending are already a fixed in-memory snapshot) and BEFORE it writes
# that snapshot out -- the actual read-modify-write window a concurrent post
# can land in. A sleep placed before the read (right after the lock is taken,
# as this sub-test used to do) proves nothing: any post that lands during it
# is still picked up by the read that follows, lock or no lock, so the
# sub-test would pass even with the race protection fully reverted. See #90
# finding 3 -- mutation testing found this exact sub-test load-bearing on
# nothing.
rm -f "$D/box.jsonl"
run post "msg-1" >/dev/null
SLOW="$D/slow-drain.sh"
patch_rc=0
python3 - "$INBOX" "$SLOW" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = 'if mode == "drain":'
replacement = 'import time; time.sleep(1)\n    if mode == "drain":'
assert marker in text, "drain mode marker not found -- script shape changed"
assert text.count(marker) == 1, "drain mode marker not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "the racing drain still reports msg-1" \
    "setup could not patch $INBOX for this test (patch script exited $patch_rc) -- treating as a failure, not a skip"
  bad "the post racing drain survives in the file" "skipped: patch setup failed"
else
  DIRECTOR_INBOX="$D/box.jsonl" bash "$SLOW" drain > "$D/drain_out.txt" 2>&1 &
  drain_pid=$!
  sleep 0.3
  run post "msg-2 racing the drain" > "$D/post_out.txt" 2>&1
  wait "$drain_pid"
  final=$(cat "$D/box.jsonl")
  want_contains "the racing drain still reports msg-1" "msg-1" "$(cat "$D/drain_out.txt")"
  want_contains "the post racing drain survives in the file" "msg-2 racing the drain" "$final"
fi

rm -rf "$D"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
