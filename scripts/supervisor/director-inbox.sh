#!/bin/bash
# Out-of-band messages to the supervisor, without typing into its pane.
#
# WHY: a dynamic `/loop` stays alive by scheduling its own wakeup at the end
# of each turn. Any plain message sent to that pane REPLACES the loop prompt,
# so the next turn is an ordinary turn and nothing re-arms. The loop ends
# silently, and the watchdog cannot tell that from a crash — both look like
# "idle pane, agent alive, no pending wakeup".
#
# Measured 2026-08-11 (#85): 27 `/loop` messages since 09:00, zero
# ScheduleWakeup calls. The watchdog restarted three times, each restart did
# real work, each was ended by the Director sending a constraint, and the
# third tripped the escalation cap and paged Jon at 09:34:49Z — for a
# condition the Director itself kept re-creating.
#
# The pane is convention-single-writer: the Director is meant to append here
# and let the tick read and drain, rather than typing into the supervisor.
# Nothing enforces that yet -- `director-inbox.sh` and `loop-tick.md` are the
# only two places in the tree that reference this file. See #81/#88.
#
# Usage:
#   director-inbox.sh post "message"   append a message for the next tick
#   director-inbox.sh read             print undrained messages (no drain)
#   director-inbox.sh drain            print undrained messages, mark them
#                                       read, and stamp delivered_at on each
#                                       one drained for the first time
#   director-inbox.sh stats            print {"pending":N,"oldest_at":...,
#                                       "oldest_age_s":N} as JSON, no mutation
#   director-inbox.sh escalate <secs>  print (do not mark) pending messages
#                                       older than <secs> that have not
#                                       already been escalated; exit 0 if any,
#                                       1 if none
#   director-inbox.sh escalate-commit  mark escalated the rows whose "at"
#                                       timestamps are given one per line on
#                                       stdin, unless already read or already
#                                       escalated
#
# Drain marks rather than deletes: a message the supervisor read is still
# readable afterwards, because losing the record of an instruction is worse
# than re-reading one. The same reasoning applies to a line that fails to
# parse, or that parses to something other than a JSON object -- both are
# preserved verbatim with a warning on stderr, never dropped, and never
# allowed to crash a later read/drain (#90).
#
# agent-supervisor#42 review: `delivered_at` is set on drain, and ONLY on
# drain -- the moment a tick actually consumed the message -- never on
# escalate or the nudge in director-route.sh succeeding. Both of those are
# evidence delivery was ATTEMPTED, not that it happened; a field that is set
# on attempt cannot later distinguish a delivered message from a lost one.
# It is set once, the first time a row's `read` flips false->true, and never
# rewritten on a later drain of the same already-read row.
#
# agent-supervisor#42 review: `escalate` used to mark `escalated: true`
# itself, unconditionally, the instant a message crossed the staleness
# threshold -- before its caller (director-route.sh) had proven the page to
# Jon actually went out. A notify failure (unreachable channel, missing
# creds) then permanently suppressed the retry: the row was already
# `escalated: true`, so the next call's own not-already-escalated filter
# skipped it forever, and the message reporting it as lost was itself lost.
# That is the identical silent-loss shape #34 exists to fix, just moved one
# layer up. `escalate` is now detect-only (like `stats`, no lock write);
# `escalate-commit` is the separate, explicit mark step, and
# director-route.sh only calls it after `notify_jon` has actually returned
# success. A failed notification leaves the row exactly as it was --
# pending, not escalated -- so the next `--flush` (~25s later) detects it as
# stale again and retries, instead of forgetting it happened.
#
# agent-supervisor#34: `read`/`drain` only ever surface a message to whoever
# runs a Director tick, and a tick is exactly what stops happening when the
# Director's pane is never idle -- the failure this issue measured, 9
# messages held 12 hours with nothing downstream able to tell "queued" from
# "lost". `stats` and `escalate` answer a different, pane-independent
# question -- "how long has the oldest pending message been waiting" -- so a
# caller that is NOT the Director's own tick (digest.sh, inbox-poll.sh's
# per-iteration flush) can notice and say so out loud, without needing the
# pane to ever go idle. `escalated` is a field on the row, not a side file,
# for the same reason `read` already is: it must survive whatever holds the
# lock next, atomically, with the same rewrite this file already trusts.
#
# post/read/drain all take the same exclusive lock (via Python's fcntl,
# since `flock(1)` isn't available on macOS) around the file, and drain holds
# it for its whole read-modify-write cycle. Without that, drain reads the
# file, a concurrent post appends, and drain's truncate-and-rewrite from its
# stale in-memory snapshot erases the post with no trace (#88). No other
# script in this repo locks a jsonl file; this one does because the box is
# read-modify-written by drain, not just appended -- the failure mode that
# locking exists for.
#
# drain's rewrite writes to a temp file in the same directory and
# os.replace()s it over the box, rather than truncating the box in place.
# os.replace is atomic on POSIX, so a kill mid-write (SIGKILL, OOM, host
# restart) leaves the original box untouched instead of half-written or
# empty (#90).

set -uo pipefail
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
BOX="${DIRECTOR_INBOX:-$STATE/director-inbox.jsonl}"
LOCK="$BOX.lock"
mkdir -p "$(dirname "$BOX")" 2>/dev/null

now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

case "${1:-read}" in
  post)
    [ -n "${2:-}" ] || { echo "director-inbox: post needs a message" >&2; exit 1; }
    python3 - "$BOX" "$LOCK" "$(now)" "$2" <<'PY'
import fcntl, json, sys
box, lock, stamp, text = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
with open(lock, "a") as lockfile:
    fcntl.flock(lockfile, fcntl.LOCK_EX)
    with open(box, "a") as handle:
        handle.write(json.dumps({"at": stamp, "read": False, "text": text}) + "\n")
print(f"director-inbox: queued for the next tick ({stamp})")
PY
    ;;
  read|drain)
    [ -s "$BOX" ] || { echo "(no director messages)"; exit 0; }
    python3 - "$BOX" "$LOCK" "${1}" "$(now)" <<'PY'
import fcntl, json, os, sys

box, lock, mode, stamp = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]

with open(lock, "a") as lockfile:
    fcntl.flock(lockfile, fcntl.LOCK_EX)

    # Each entry is (raw_line, parsed_dict_or_None). A line that fails to
    # parse, or that parses to something other than a JSON object (a list,
    # a string, a number -- anything without .get()), keeps its raw text and
    # a None marker, so it survives drain's rewrite unchanged instead of
    # being silently dropped or crashing every read/drain after it (#90).
    rows = []
    for lineno, raw in enumerate(open(box), start=1):
        line = raw.rstrip("\n")
        if not line.strip():
            continue
        try:
            parsed = json.loads(line)
        except ValueError:
            print(
                f"director-inbox: malformed line {lineno} in {box}, "
                f"preserved verbatim: {line!r}",
                file=sys.stderr,
            )
            rows.append((line, None))
            continue
        if not isinstance(parsed, dict):
            print(
                f"director-inbox: malformed line {lineno} in {box} "
                f"(not a JSON object), preserved verbatim: {line!r}",
                file=sys.stderr,
            )
            rows.append((line, None))
            continue
        rows.append((line, parsed))

    pending = [r for _, r in rows if r is not None and not r.get("read")]
    if not pending:
        print("(no new director messages)")
        sys.exit(0)
    for r in pending:
        print(f"[director {r['at']}] {r['text']}")

    if mode == "drain":
        out_lines = []
        for line, r in rows:
            if r is None:
                out_lines.append(line)
            else:
                # delivered_at is set once, the first time this row is
                # actually drained -- never on a later drain of an
                # already-read row, and never anywhere but here (see the
                # header comment for why escalate/the nudge do not count).
                if not r.get("read"):
                    r["delivered_at"] = stamp
                r["read"] = True
                out_lines.append(json.dumps(r))
        tmp = box + ".tmp"
        with open(tmp, "w") as handle:
            for line in out_lines:
                handle.write(line + "\n")
        os.replace(tmp, box)
PY
    ;;
  stats)
    # Read-only, no lock needed -- nothing here is read-modify-write. A
    # missing/empty box is not an error: it is the correct, common "nothing
    # pending" state, distinct from an unreadable one (a caller's own
    # `command -v`/`-r` checks catch that).
    python3 - "$BOX" "$(now)" <<'PY'
import json, sys
from datetime import datetime, timezone

box, now_s = sys.argv[1], sys.argv[2]
now = datetime.strptime(now_s, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)

pending = []
try:
    for raw in open(box):
        line = raw.rstrip("\n")
        if not line.strip():
            continue
        try:
            r = json.loads(line)
        except ValueError:
            continue
        if not isinstance(r, dict) or r.get("read"):
            continue
        pending.append(r)
except FileNotFoundError:
    pass

if not pending:
    print(json.dumps({"pending": 0, "oldest_at": None, "oldest_age_s": None}))
else:
    oldest = min(pending, key=lambda r: r.get("at", ""))
    oldest_at = oldest.get("at")
    try:
        age = (now - datetime.strptime(oldest_at, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)).total_seconds()
    except (TypeError, ValueError):
        age = None
    print(json.dumps({
        "pending": len(pending),
        "oldest_at": oldest_at,
        "oldest_age_s": (int(age) if age is not None else None),
    }))
PY
    ;;
  escalate)
    # Detect-only, like `stats`: reports pending, not-yet-escalated rows
    # older than the threshold, but does NOT mark them. Marking happens in
    # `escalate-commit`, and only once the caller has proven the page it
    # builds from this output actually reached Jon -- see the header
    # comment. No lock is taken: nothing here is read-modify-write.
    [ -n "${2:-}" ] || { echo "director-inbox: escalate needs a threshold in seconds" >&2; exit 1; }
    [ -s "$BOX" ] || { echo "(no stale director messages)"; exit 1; }
    python3 - "$BOX" "$(now)" "$2" <<'PY'
import json, sys
from datetime import datetime, timezone

box, now_s, threshold = sys.argv[1], sys.argv[2], float(sys.argv[3])
now = datetime.strptime(now_s, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)

stale = []
for raw in open(box):
    line = raw.rstrip("\n")
    if not line.strip():
        continue
    try:
        r = json.loads(line)
    except ValueError:
        continue
    if not isinstance(r, dict) or r.get("read") or r.get("escalated"):
        continue
    try:
        age = (now - datetime.strptime(r.get("at", ""), "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)).total_seconds()
    except (TypeError, ValueError):
        age = None
    if age is not None and age >= threshold:
        stale.append(r)

if not stale:
    print("(no stale director messages)")
    sys.exit(1)

for r in stale:
    print(f"[director {r['at']}] {r['text']}")
PY
    ;;
  escalate-commit)
    # Marks escalated the rows whose "at" timestamp is given as an argument
    # here -- the exact set `escalate` just reported and the caller just
    # proved got through to Jon. Rows not in that set, already read, or
    # already escalated are left untouched, so a stale message that shows up
    # in the window between `escalate` and this call (and so was never in
    # the notification body) is NOT marked here -- it stays retryable for
    # the next detect/notify/commit cycle instead of being silently marked
    # escalated without ever having been announced.
    #
    # Targets are argv, not stdin: `python3 -` already reads the program
    # itself from stdin (that is what makes the heredoc below work), so
    # anything piped into this command would be consumed as (invalid)
    # program text, never reach the script's own logic, and this would
    # silently commit nothing every time.
    shift
    [ "$#" -gt 0 ] || exit 0
    python3 - "$BOX" "$LOCK" "$@" <<'PY'
import fcntl, json, os, sys

box, lock = sys.argv[1], sys.argv[2]
targets = set(sys.argv[3:])

with open(lock, "a") as lockfile:
    fcntl.flock(lockfile, fcntl.LOCK_EX)

    rows = []
    for raw in open(box):
        line = raw.rstrip("\n")
        if not line.strip():
            continue
        try:
            parsed = json.loads(line)
        except ValueError:
            rows.append((line, None))
            continue
        if not isinstance(parsed, dict):
            rows.append((line, None))
            continue
        rows.append((line, parsed))

    changed = False
    out_lines = []
    for line, r in rows:
        if r is not None and r.get("at") in targets and not r.get("read") and not r.get("escalated"):
            r["escalated"] = True
            changed = True
            out_lines.append(json.dumps(r))
        else:
            out_lines.append(line if r is None else json.dumps(r))

    if not changed:
        sys.exit(0)

    tmp = box + ".tmp"
    with open(tmp, "w") as handle:
        for line in out_lines:
            handle.write(line + "\n")
    os.replace(tmp, box)
PY
    ;;
  *) echo "usage: director-inbox.sh {post <msg>|read|drain|stats|escalate <secs>|escalate-commit}" >&2; exit 1 ;;
esac
