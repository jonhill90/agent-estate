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
#   director-inbox.sh drain            print undrained messages and mark them
#
# Drain marks rather than deletes: a message the supervisor read is still
# readable afterwards, because losing the record of an instruction is worse
# than re-reading one. The same reasoning applies to a line that fails to
# parse, or that parses to something other than a JSON object -- both are
# preserved verbatim with a warning on stderr, never dropped, and never
# allowed to crash a later read/drain (#90).
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
    python3 - "$BOX" "$LOCK" "${1}" <<'PY'
import fcntl, json, os, sys

box, lock, mode = sys.argv[1], sys.argv[2], sys.argv[3]

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
                r["read"] = True
                out_lines.append(json.dumps(r))
        tmp = box + ".tmp"
        with open(tmp, "w") as handle:
            for line in out_lines:
                handle.write(line + "\n")
        os.replace(tmp, box)
PY
    ;;
  *) echo "usage: director-inbox.sh {post <msg>|read|drain}" >&2; exit 1 ;;
esac
