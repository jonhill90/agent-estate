#!/bin/bash
# Send Jon a short message when an autonomous process needs a human.
#
# Channel order per Jon, 2026-08-11: Telegram FIRST (he chose it over iMessage
# because it is not tied to one Mac), iMessage as a local fallback, then
# Discord and Teams. Slack deferred. Only one working channel
# is required; the rest are portability. Tracked as jonhill90/skills#146.
#
# BOUNDARY: this is one-way HUMAN notification. It is not agent-to-agent
# transport. agent-dotfiles#16 forbids depending on a chat service to
# coordinate work, and that still stands — GitHub remains the queue, tmux
# remains the frontend. Nothing here becomes a dependency of the loop.
#
# Usage: AGENT_NOTIFY_CALLER=supervisor notify.sh "subject" "body"
# Config: AGENT_NOTIFY_IMESSAGE_TO   phone number or Apple ID to message
#         AGENT_NOTIFY_TELEGRAM_TOKEN / _CHAT_ID   (fallback, later)
#
# CALLER GATE (agent-dotfiles#52): only the supervisor/watchdog sender may
# message Jon. This script refuses to touch any channel unless
# AGENT_NOTIFY_CALLER=supervisor is set — `watchdog_notify.py` sets it on
# every call it makes. See that gate below for what this does and does not
# guarantee.
#
# Exit 0 only if a channel actually accepted the message. A send failure is
# logged loudly rather than swallowed — an unreachable channel that looks like
# "nothing to report" is the instrument-blindness failure this estate keeps
# hitting (see verify-the-instrument).

set -uo pipefail
STATE="$HOME/.local/state/agent-dotfiles-supervisor"
LOG="$STATE/notify.log"
SUBJECT="${1:-agent needs you}"
BODY="${2:-}"
# Credentials live in an untracked 0600 env file, never in this script, never
# in the repo, never in the LaunchAgent plist. NOTIFY_ENV overrides the path.
ENVFILE="${NOTIFY_ENV:-$STATE/notify.env}"
# shellcheck source=/dev/null
if [ -r "$ENVFILE" ]; then set -a; . "$ENVFILE"; set +a; fi

iso=$(date -u +%Y-%m-%dT%H:%M:%SZ)
log() { printf '%s %s\n' "$iso" "$*" >>"$LOG"; }

# --- caller gate (agent-dotfiles#52) ---------------------------------------
# Only the supervisor/watchdog sender may message Jon: five lanes each
# judging their own work urgent produces five notifications for one event.
# Nothing OS-level distinguishes a worker's shell from the supervisor's --
# same user, same $HOME, same credentials -- so this is a deliberate
# affordance, not a security boundary: a caller has to know to identify
# itself as "supervisor" to get through. That is enough to stop a worker
# from reaching Jon by routine or accident, which is the failure this
# exists to prevent; it is not enough to stop a worker that reads this
# script and decides to spoof it. `watchdog_notify.py` sets this on every
# invocation it makes.
if [ "${AGENT_NOTIFY_CALLER:-}" != "supervisor" ]; then
  log "REFUSED — caller not identified as supervisor (AGENT_NOTIFY_CALLER=${AGENT_NOTIFY_CALLER:-<unset>}): $SUBJECT${BODY:+ — $BODY}"
  echo "NOTIFY REFUSED: only the supervisor/watchdog sender may notify Jon (set AGENT_NOTIFY_CALLER=supervisor)" >&2
  exit 1
fi

msg="$SUBJECT"
[ -n "$BODY" ] && msg="$SUBJECT — $BODY"

# --- Telegram (PRIMARY) ---------------------------------------------------
# Jon chose Telegram over iMessage on 2026-08-11: it works from any machine
# and does not depend on macOS automation permissions. iMessage stays below
# as a Mac-only fallback since it was already written and costs nothing.
if [ -n "${AGENT_NOTIFY_TELEGRAM_TOKEN:-}" ] && [ -n "${AGENT_NOTIFY_TELEGRAM_CHAT_ID:-}" ]; then
  if curl -fsS -m 15 -X POST \
       "https://api.telegram.org/bot${AGENT_NOTIFY_TELEGRAM_TOKEN}/sendMessage" \
       -d "chat_id=${AGENT_NOTIFY_TELEGRAM_CHAT_ID}" \
       --data-urlencode "text=${msg}" >/dev/null 2>&1; then
    log "SENT telegram: $SUBJECT"
    echo "sent via telegram"; exit 0
  fi
  log "FAILED telegram — falling through"
fi

# --- iMessage (fallback, Mac-only) ----------------------------------------
to="${AGENT_NOTIFY_IMESSAGE_TO:-}"
if [ -n "$to" ]; then
  # `buddy` resolves an existing conversation; `participant` handles a fresh
  # one. Try buddy, fall back to creating the chat by handle.
  if osascript <<APPLESCRIPT >/dev/null 2>&1
tell application "Messages"
  set svc to 1st account whose service type = iMessage
  send "$msg" to participant "$to" of svc
end tell
APPLESCRIPT
  then
    log "SENT imessage to $to: $SUBJECT"
    echo "sent via imessage"; exit 0
  fi
  log "FAILED imessage to $to — falling through"
fi

# --- No channel worked ----------------------------------------------------
# Deliberately loud. The caller must be able to tell "nobody was told" from
# "told successfully", or escalation silently does nothing.
log "UNREACHABLE — no channel accepted: $SUBJECT${BODY:+ — $BODY}"
echo "NOTIFY FAILED: no channel configured or all channels failed" >&2
exit 1
