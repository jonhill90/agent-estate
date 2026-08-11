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
# Exit 0 with output = new messages. Exit 0 with no output = nothing new.
# Exit 1 = could not reach Telegram, which is NOT the same as "no messages"
# and must never be reported as silence.

set -uo pipefail
STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
OFFSET_FILE="${INBOX_OFFSET:-$STATE/.telegram-offset}"
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

offset=$(cat "$OFFSET_FILE" 2>/dev/null || echo 0)
url="https://api.telegram.org/bot${token}/getUpdates?timeout=0"
[ "$offset" -gt 0 ] 2>/dev/null && url="${url}&offset=${offset}"

body=$(curl -fsS -m 20 "$url" 2>/dev/null) || {
  echo "inbox: telegram unreachable" >&2
  exit 1
}

printf '%s' "$body" | OFFSET_FILE="$OFFSET_FILE" PEEK="$PEEK" python3 -c '
import json, os, sys

data = json.load(sys.stdin)
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
    print(f"[telegram {when} from {who}] {text}")

# Acknowledge only after the messages have been printed: if this process dies
# mid-write, the messages are re-read next tick rather than lost.
if highest and not os.environ.get("PEEK"):
    with open(os.environ["OFFSET_FILE"], "w") as handle:
        handle.write(str(highest + 1))
'
