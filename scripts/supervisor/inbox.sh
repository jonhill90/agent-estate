#!/bin/bash
# Read messages Jon has sent TO the supervisor's Telegram bot.
#
# The outbound half (notify.sh) was built first because escalation was the
# urgent gap. This is the inbound half: without it the bot is a pager, and Jon
# replying to it reaches nobody -- which is worse than no channel, because a
# reply that is silently dropped looks exactly like one that was read.
#
# Offset semantics matter. Telegram's getUpdates returns the same updates
# forever until they are acknowledged by asking for `offset = last_id + 1`.
# Without persisting that offset, every poll re-reads Jon's whole history and
# every message looks new on every tick. The offset file is the acknowledgement.
#
# Usage:  inbox.sh          print any new messages, one per line, then ack them
#         inbox.sh --peek   print without acknowledging (safe to run twice)
#
# Each line is two tab-separated fields: the bare message TEXT, then the
# human-readable "[telegram <ts> from <who>] <text>" display line (unchanged
# from before this field was added). agent-dotfiles#152: a caller that needs
# to act on the reply itself -- inbox-route.sh does, typing it into a lane --
# must use field 1. Re-deriving TEXT by parsing field 2 back apart is what
# broke routing in the first place; TEXT is already known here, before the
# display line is even built, so callers get it directly instead of
# reconstructing it downstream.
#
# Exit 0 with output = new messages. Exit 0 with no output = nothing new.
# Exit 1 = could not reach Telegram, which is NOT the same as "no messages"
# and must never be reported as silence.
#
# Config: INBOX_TIMEOUT   seconds passed to Telegram's getUpdates as its own
#         `timeout` parameter. Default 0 preserves the original behaviour --
#         the call returns immediately, which is what a Director tick wants.
#         `inbox-poll.sh` (agent-dotfiles#142) sets this high so the call
#         long-polls: the HTTP request stays open and returns the moment a
#         message arrives, instead of Jon's reply sitting unread until the
#         next tick calls this script on a schedule.
#
# Locking (agent-dotfiles#142). The whole read-request-acknowledge cycle below
# -- read the offset, ask Telegram, print, write the new offset -- runs under
# an exclusive file lock, the same fcntl technique director-inbox.sh already
# uses because macOS ships no `flock(1)`. Two callers on the same offset file
# is exactly the failure this script's header already warns about: without
# the lock, a long-polling `inbox-poll.sh` holding an open request and a
# Director tick calling this script directly could both read the same
# unacknowledged offset and print the same message twice, or interleave their
# acknowledgements and drop one. The lock is held across the network call on
# purpose -- a caller that has to wait out someone else's long poll is a
# short delay; a caller that races it is a dropped or duplicated message, and
# a dropped message is worse than a slow one.

set -uo pipefail
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
OFFSET_FILE="${INBOX_OFFSET:-$STATE/.telegram-offset}"
LOCK_FILE="${INBOX_LOCK:-$OFFSET_FILE.lock}"
ENVFILE="${NOTIFY_ENV:-$STATE/notify.env}"
# shellcheck source=/dev/null
[ -r "$ENVFILE" ] && { set -a; . "$ENVFILE"; set +a; }

PEEK=""
[ "${1:-}" = "--peek" ] && PEEK=1

token="${AGENT_NOTIFY_TELEGRAM_TOKEN:-}"
if [ -z "$token" ]; then
  echo "inbox: no AGENT_NOTIFY_TELEGRAM_TOKEN configured" >&2
  exit 1
fi

# Telegram's own long-poll timeout, in seconds. 0 (default) is "return
# immediately" -- the original behaviour. Not validated beyond "is it a
# non-negative integer" -- an odd value is a caller bug to fix, not something
# worth this script's complexity to guard against.
timeout="${INBOX_TIMEOUT:-0}"
case "$timeout" in
  ''|*[!0-9]*) timeout=0 ;;
esac

mkdir -p "$(dirname "$OFFSET_FILE")" 2>/dev/null

# curl's own -m must exceed Telegram's `timeout` param, or this process gives
# up before Telegram would have answered. +20 matches the original fixed `-m
# 20` exactly when timeout=0, so the default path's network behaviour is
# unchanged.
CURL_MAX_TIME=$((timeout + 20))

TOKEN="$token" OFFSET_FILE="$OFFSET_FILE" LOCK_FILE="$LOCK_FILE" PEEK="$PEEK" \
  TIMEOUT="$timeout" CURL_MAX_TIME="$CURL_MAX_TIME" python3 <<'PY'
import fcntl, json, os, subprocess, sys

offset_file = os.environ["OFFSET_FILE"]
lock_file = os.environ["LOCK_FILE"]
token = os.environ["TOKEN"]
peek = bool(os.environ.get("PEEK"))
timeout = os.environ["TIMEOUT"]
curl_max_time = os.environ["CURL_MAX_TIME"]

# Held across the curl call deliberately -- see the header comment above.
with open(lock_file, "a") as lockfile:
    fcntl.flock(lockfile, fcntl.LOCK_EX)

    try:
        with open(offset_file) as f:
            offset = int(f.read().strip() or "0")
    except (OSError, ValueError):
        offset = 0

    url = f"https://api.telegram.org/bot{token}/getUpdates?timeout={timeout}"
    if offset > 0:
        url += f"&offset={offset}"

    try:
        result = subprocess.run(
            ["curl", "-fsS", "-m", curl_max_time, url],
            capture_output=True,
            timeout=int(curl_max_time) + 5,
        )
    except (subprocess.TimeoutExpired, OSError):
        print("inbox: telegram unreachable", file=sys.stderr)
        sys.exit(1)

    if result.returncode != 0:
        print("inbox: telegram unreachable", file=sys.stderr)
        sys.exit(1)

    try:
        data = json.loads(result.stdout)
    except ValueError:
        print("inbox: telegram returned unparseable response", file=sys.stderr)
        sys.exit(1)

    if not data.get("ok"):
        print("inbox: telegram returned ok=false", file=sys.stderr)
        sys.exit(1)

    updates = data.get("result", [])
    highest = 0
    for update in updates:
        highest = max(highest, update.get("update_id", 0))
        message = update.get("message") or update.get("channel_post") or {}
        text = (message.get("text") or "").strip()
        if not text:
            continue
        chat = message.get("chat") or {}
        who = chat.get("title") or chat.get("first_name") or chat.get("id")
        when = message.get("date", "")
        display = f"[telegram {when} from {who}] {text}"
        # TEXT first, tab-separated, so a caller that needs the bare reply
        # (inbox-route.sh) reads it directly instead of parsing it back out
        # of `display` -- see the usage comment above (#152).
        print(f"{text}\t{display}")

    # Acknowledge only after the messages have been printed, and still inside
    # the lock: if this process dies mid-write, the messages are re-read next
    # call rather than lost, and no other caller can interleave an ack in
    # between this read and this write.
    if highest and not peek:
        tmp = offset_file + ".tmp"
        with open(tmp, "w") as handle:
            handle.write(str(highest + 1))
        os.replace(tmp, offset_file)
PY
