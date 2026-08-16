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
# director-inbox.sh does not source anything relative to its own path, so
# the mutant below has no need to sit beside the real script -- it lives
# under $D and is covered by this one recursive cleanup (agent-supervisor#220).
trap 'rm -rf "$D"' EXIT
run() { DIRECTOR_INBOX="$D/box.jsonl" bash "$INBOX" "$@"; }

# --- no-argument invocation defaults to "read" (documented) — #113 ---------
# `case "${1:-read}"` only defaults which BRANCH is selected. The read/drain
# branch then re-reads the literal `$1` for its own use (to tell python which
# of read/drain it is) -- and that reference is still unset when no argument
# was given, so it crashes under `set -u`. The documented no-argument form
# was the one form that failed. The box must be non-empty to hit this: an
# empty box returns via the `[ -s "$BOX" ]` short-circuit before `$1` is ever
# touched again, which is why this needs its own message posted first.
rm -f "$D/box.jsonl"
run post "default form" >/dev/null
before=$(cat "$D/box.jsonl")
out=$(DIRECTOR_INBOX="$D/box.jsonl" bash "$INBOX" 2>&1); rc=$?
after=$(cat "$D/box.jsonl")
want_exit "no-argument invocation exits 0" "$rc" 0
want_contains "no-argument invocation shows the pending message (defaults to read)" "default form" "$out"
[ "$before" = "$after" ] && ok "no-argument invocation does not mutate the file (defaults to read, not drain)" \
  || bad "no-argument invocation does not mutate the file (defaults to read, not drain)" "before:$before"$'\n'"after:$after"

# Same, under a minimal launchd-like environment -- #163 was exactly a check
# that passed with a developer PATH and could never run where installed.
out=$(env -i HOME="$D" PATH=/usr/bin:/bin DIRECTOR_INBOX="$D/box.jsonl" bash "$INBOX" 2>&1); rc=$?
want_exit "no-argument invocation exits 0 under env -i (launchd-like PATH)" "$rc" 0
want_contains "no-argument invocation under env -i shows the pending message" "default form" "$out"

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

# --- drain stamps delivered_at, once, only on the row it actually drains ---
# agent-supervisor#42 review: delivered_at must exist and must be set only
# when a message is actually delivered (drained), never on read (no
# mutation), never on escalate/escalate-commit (an attempt, not a success).
rm -f "$D/box.jsonl"
run post "stamp me" >/dev/null
out=$(run read)
want_not_contains "read alone never sets delivered_at" "delivered_at" "$(cat "$D/box.jsonl")"
run drain >/dev/null
want_contains "drain sets delivered_at on the row it just delivered" "delivered_at" "$(cat "$D/box.jsonl")"
first_stamp=$(python3 -c "import json; print(json.loads(open('$D/box.jsonl').read().strip())['delivered_at'])")
[ -n "$first_stamp" ] && ok "delivered_at is a non-empty timestamp" \
  || bad "delivered_at is a non-empty timestamp" "$(cat "$D/box.jsonl")"
sleep 1
run drain >/dev/null
second_stamp=$(python3 -c "import json; print(json.loads(open('$D/box.jsonl').read().strip())['delivered_at'])")
[ "$first_stamp" = "$second_stamp" ] && ok "a later drain of an already-read row does not overwrite delivered_at" \
  || bad "a later drain of an already-read row does not overwrite delivered_at" "first:$first_stamp second:$second_stamp"

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

# --- stats: pending count and oldest age, without depending on pane state --
# agent-supervisor#34. `stats` is what lets a caller other than a Director
# tick (digest.sh) answer "how long has this been waiting" -- it must not
# mutate the file (a read used only to report must never also be the write
# that marks something delivered).
rm -f "$D/box.jsonl"
out=$(run stats)
want_contains "stats on an empty box reports zero pending" '"pending": 0' "$out"

run post "fresh one" >/dev/null
out=$(run stats)
want_contains "stats reports one pending after a post" '"pending": 1' "$out"
before=$(cat "$D/box.jsonl")
run stats >/dev/null
after=$(cat "$D/box.jsonl")
[ "$before" = "$after" ] && ok "stats does not mutate the box" \
  || bad "stats does not mutate the box" "before:$before"$'\n'"after:$after"

# Backdate the message so its age is deterministic and clearly past a
# threshold, the same trick digest.sh's own tests use for status timestamps.
python3 -c "
import json
row = json.loads(open('$D/box.jsonl').read().strip())
row['at'] = '2020-01-01T00:00:00Z'
open('$D/box.jsonl', 'w').write(json.dumps(row) + chr(10))
"
out=$(run stats)
want_contains "stats reports the backdated message's oldest_at" '"oldest_at": "2020-01-01T00:00:00Z"' "$out"
age=$(python3 -c "import json,sys; print(json.loads(sys.stdin.read())['oldest_age_s'])" <<<"$out")
[ "$age" -gt 1000000 ] && ok "stats computes a large oldest_age_s for a years-old message" \
  || bad "stats computes oldest_age_s" "$out"

# --- escalate: detect-only, never marks by itself --------------------------
# agent-supervisor#42 review: escalate used to mark the row escalated the
# instant it was detected, before any caller had proven a notification
# actually went out -- a failed page then permanently suppressed the retry.
# escalate is now read-only, like stats: it reports, it does not mutate. The
# load-bearing claim is unchanged from #34 -- escalate must fire for a
# message that is old but whose delivery has nothing to do with the pane --
# independent of director-route.sh's idle check entirely (no tmux stub
# touched anywhere in this test).
out=$(run escalate 60); rc=$?
want_exit "escalate past its threshold exits 0" "$rc" 0
want_contains "escalate reports the stale message's text" "fresh one" "$out"
want_not_contains "escalate alone does not mark the row escalated" '"escalated": true' "$(cat "$D/box.jsonl")"

out2=$(run escalate 60); rc2=$?
want_exit "calling escalate again without a commit still reports it (nothing marked it yet)" "$rc2" 0
want_contains "the uncommitted escalate keeps reporting the same stale message" "fresh one" "$out2"

# --- escalate-commit: marks only the "at" timestamps given as arguments ----
at=$(python3 -c "import json; print(json.loads(open('$D/box.jsonl').read().strip())['at'])")
run escalate-commit "$at"
want_contains "escalate-commit marks the row escalated in the box" '"escalated": true' "$(cat "$D/box.jsonl")"

out3=$(run escalate 60); rc3=$?
want_exit "escalating a now-committed message exits 1 the second time" "$rc3" 1
want_contains "the escalate call after commit reports nothing new (no re-page)" "no stale director messages" "$out3"

# escalate-commit is idempotent and a no-op for an unknown/blank target.
run escalate-commit "$at"
want_contains "re-committing an already-escalated row is a no-op, not an error" '"escalated": true' "$(cat "$D/box.jsonl")"
out4=$(run escalate-commit); rc4=$?
want_exit "escalate-commit with no target arguments exits 0" "$rc4" 0

# A message younger than the threshold is not escalated.
rm -f "$D/box.jsonl"
run post "too young to escalate" >/dev/null
out=$(run escalate 999999); rc=$?
want_exit "a fresh message under threshold exits 1 (not escalated)" "$rc" 1
want_not_contains "a fresh message under threshold is not reported as stale" "too young to escalate" "$out"

# A message already drained (read=true) is never escalated, even if old --
# it already reached the Director through the normal tick path.
rm -f "$D/box.jsonl"
run post "already delivered" >/dev/null
run drain >/dev/null
python3 -c "
import json
row = json.loads(open('$D/box.jsonl').read().strip())
row['at'] = '2020-01-01T00:00:00Z'
open('$D/box.jsonl', 'w').write(json.dumps(row) + chr(10))
"
out=$(run escalate 60); rc=$?
want_exit "an already-drained old message is not escalated" "$rc" 1
want_not_contains "a drained message never reports as stale" "already delivered" "$out"

# --- mutation-check: drop the escalate-commit marking and confirm re-paging
# goes red. The brief's own bar: mutate the fix and watch a test go red.
# Patch out the `r["escalated"] = True` write so a caller that committed the
# same message twice would be told to page Jon twice for it -- the exact
# repeat-page failure the `escalated` field exists to prevent.
MUTANT="$D/.director-inbox-mutant-noescalated.sh"
patch_rc=0
python3 - "$INBOX" "$MUTANT" <<'PY' || patch_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = '            r["escalated"] = True\n            changed = True'
replacement = '            changed = True'
assert marker in text, "escalate-commit marking line not found -- director-inbox.sh shape changed"
assert text.count(marker) == 1, "escalate-commit marking line not unique -- director-inbox.sh shape changed"
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$patch_rc" -ne 0 ]; then
  bad "setup: patched an escalated-marking-free copy of director-inbox.sh" \
    "could not patch $INBOX (exit $patch_rc) -- treating as a failure, not a skip"
else
  ok "setup: patched an escalated-marking-free copy of director-inbox.sh"
  rm -f "$D/box.jsonl"
  DIRECTOR_INBOX="$D/box.jsonl" bash "$MUTANT" post "would re-page forever" >/dev/null
  python3 -c "
import json
row = json.loads(open('$D/box.jsonl').read().strip())
row['at'] = '2020-01-01T00:00:00Z'
open('$D/box.jsonl', 'w').write(json.dumps(row) + chr(10))
"
  at3=$(python3 -c "import json; print(json.loads(open('$D/box.jsonl').read().strip())['at'])")
  DIRECTOR_INBOX="$D/box.jsonl" bash "$MUTANT" escalate-commit "$at3" >/dev/null
  out3=$(DIRECTOR_INBOX="$D/box.jsonl" bash "$MUTANT" escalate 60); rc3=$?
  if [ "$rc3" -eq 0 ]; then
    ok "mutation confirmed: without the escalated marking, the same stale message escalates again (a real fix would exit 1 here)"
  else
    bad "mutation confirmed: removing the escalated marking should cause a re-escalation" "$out3"
  fi
fi

rm -rf "$D"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
