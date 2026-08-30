#!/bin/bash
# Deliver one inbound Telegram message to the lane it answers.
#
# agent-dotfiles#193: NOT the automatic inbound path anymore. `inbox-poll.sh`
# calls `director-route.sh` for every message now -- the Director is the
# default recipient, and relaying a specific reply to a specific lane is
# something the Director itself decides to do, not something the router
# does automatically. This script is kept, unautomated, for exactly that:
# the Director (or a human) can still invoke it directly when a reply is
# clearly meant for a lane it already knows is waiting. Everything below —
# the menu-vs-text distinction, the evidenced delivery, the exit-code
# contract — is unchanged and still correct for that deliberate use.
#
# agent-dotfiles#142. The common case this exists for: a lane is mid-turn and
# hit an interactive prompt -- a permission approval, a disambiguating
# question -- and stalled. `lanes.sh` already detects exactly that shape as
# `blocked` (#123/#124). Jon replies on his phone; this is what gets that
# reply into the pane that is actually waiting on it, instead of it sitting
# in the offset file until the next Director tick reads `inbox.sh`.
#
# This deliberately does NOT keep a separate routing table mapping messages
# to lanes. `blocked` is the routing table -- it is recomputed fresh from
# tmux on every call, so it can never point at a lane that has since finished,
# been redispatched, or renamed. A stale table would misroute; asking tmux
# again cannot.
#
# Three cases, and only the first is unambiguous:
#
#   exactly one lane blocked  -> that lane is presumed to be who asked. If it
#                                 is waiting on a MENU, the reply is not typed
#                                 into it -- see below. If it is waiting on
#                                 TEXT, send the reply there.
#   zero lanes blocked        -> nobody is waiting. Sending nowhere would be
#                                 silent; guessing a target would be worse.
#                                 Tell Jon so, through notify.sh, so he knows
#                                 the message arrived and was not silently
#                                 dropped -- just that nothing was listening.
#   more than one blocked     -> routing would be a guess, and a reply
#                                 delivered to the wrong lane is worse than
#                                 one that pauses to ask. List the candidates
#                                 and ask Jon which one, through notify.sh.
#
# agent-dotfiles#159. `lanes.sh --blocked` splits into `menu` and `text`
# lanes (see lanes.sh's MENU_ENTER_RE comment). This matters because most
# blocked lanes in this estate are menus -- folder trust, `/model`, a bash
# tool-permission approval, `/theme` -- and typing free text into a menu does
# not answer it: the characters are consumed as navigation, and the Enter
# that follows commits whatever option happens to be highlighted, not
# whatever Jon meant. Proven live: a routed reply changed a lane's `/theme`
# setting instead of being read.
#
# So a menu-blocked lane never receives the raw reply. It gets refused, and
# Jon is sent the menu text instead, so he can answer with something that
# actually means a choice (`1`, `2`, `Esc`) rather than a sentence a selector
# cannot read. The alternative -- deliver and hope -- is the bug this issue
# is about.
#
# A text-blocked lane is delivered to exactly as before, EXCEPT that
# `delivered` is no longer just what `send-keys` returning 0 means. It is
# backed by two checks against the pane's own input box (input-box.sh, #141):
# the literal text must actually appear there before Enter is sent, and the
# box must actually be empty after -- the two ways this used to lie were
# claiming success when nothing landed at all, and claiming success when
# Enter was swallowed by a repaint and the reply sat there unsent.
#
# #164: two things worth being honest about rather than implying coverage.
# First, `text-blocked` now requires positive evidence of its own (see
# lanes.sh's TEXT_PROMPT_RE) instead of being reached by absence of menu
# evidence -- no real free-text-blocked prompt has ever been captured in
# this estate, so as measured today this branch is reached only by the
# modelled fixture, not by anything seen live. Second, the pane-evidence
# check two paragraphs up keys on `input_box_state`, which anchors on Claude
# Code's MAIN input box (`❯` + no-break space, input-box.sh). A genuinely
# text-blocked dialog is by definition not sitting at the main prompt and
# may never paint that box at all -- in which case this refuses with "never
# showed the reply" and exits 1. Safe (it does not lie), but it means the
# whole text branch, evidence check included, is modelled end to end and
# untested against a real pane.
#
# Usage: inbox-route.sh "<message text>" [session]
#
# Exit 0 once the message has been delivered to a lane and confirmed by the
# pane's own input box. Exit 2 if nothing was delivered but Jon was told why
# (a menu refusal, zero lanes waiting, or more than one candidate) -- #164:
# this used to share exit 0 with a real delivery, and inbox-poll.sh logged
# both as `ROUTED`, which is a message the log records as routed when it was
# deliberately not. Exit 1 if neither delivery nor notification happened --
# the message text is printed to stderr in that case so it is not lost from
# the caller's log. Exit 3 is exit 2's batched sibling (#186): zero lanes
# waiting, nothing delivered, but Jon was deliberately NOT told, because
# INBOX_ROUTE_BATCH is set and the caller owns a single summary notice for
# the whole batch instead. Only reachable with that env var set.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./input-box.sh
. "$HERE/input-box.sh"
# shellcheck source=./send.sh
. "$HERE/send.sh"
# shellcheck source=./session-defaults.sh
. "$HERE/session-defaults.sh"
SESSION="${2:-$(lanes_session_or_default)}"
MESSAGE="${1:-}"

if [ -z "$MESSAGE" ]; then
  echo "inbox-route: usage: inbox-route.sh \"<message text>\" [session]" >&2
  exit 2
fi

notify_jon() {  # notify_jon <subject> <body>
  if ! AGENT_NOTIFY_CALLER=supervisor "$HERE/notify.sh" "$1" "$2"; then
    echo "inbox-route: could not reach Jon either -- message was: $MESSAGE" >&2
    return 1
  fi
}

# #164: a refusal is not a delivery, and the exit code must say so -- the
# poller logged both as `ROUTED`, which recorded a message as routed when it
# was deliberately not. Exit 2 for "nothing was typed anywhere, but Jon was
# told why"; exit 1 (the pre-existing "neither happened" code) if notify.sh
# itself failed, same as every other failure path below.
refuse_exit() {  # refuse_exit <subject> <body>
  if notify_jon "$1" "$2"; then exit 2; else exit 1; fi
}

BLOCKED=(); KIND=()
while IFS=$'\t' read -r candidate kind; do
  [ -n "$candidate" ] || continue
  BLOCKED+=("$candidate"); KIND+=("$kind")
done < <("$HERE/lanes.sh" --blocked "$SESSION" 2>/dev/null)

case "${#BLOCKED[@]}" in
  1)
    LANE="${BLOCKED[0]}"
    if [ "${KIND[0]}" = menu ]; then
      # Refuse -- see the header comment. Jon gets the menu text itself so he
      # can answer with a key that means something, rather than a reply
      # this lane cannot read as an answer.
      MENU_TEXT=$(tmux capture-pane -p -t "$LANE" 2>/dev/null | grep -v '^[[:space:]]*$' | tail -6)
      echo "inbox-route: $LANE is menu-blocked, refusing to type the reply into it" >&2
      refuse_exit "Telegram reply -- $LANE is on a menu, not free text" \
        "$LANE is waiting on a selection, not typed text -- your reply (\"$MESSAGE\") was NOT sent, to avoid it landing as navigation keys. What it shows: $MENU_TEXT"
    fi
    # agent-supervisor#178: type, verify, THEN submit, via the shared
    # primitive in send.sh. `--literal` is agent-dotfiles#152's `-l`: unlike
    # dispatch.sh's own brief send, this text comes from an external network
    # service and is never safe to assume it isn't a recognised key name --
    # without it, a reply of literally `C-c` or `Escape` fires that control
    # action at the lane instead of typing the four characters. No
    # `--preclear`: `C-u` would silently wipe whatever the lane had already
    # typed, which is exactly the menu-blocked case handled above this
    # block. `--settle 0`/`--confirm-settle 0` and `--retries 1`/
    # `--confirm-tries 1` reproduce this file's own prior behaviour exactly
    # -- one attempt each way, no injected delay, no retype.
    #
    # #159/#141's evidence discipline is unchanged: the first check confirms
    # the literal bytes actually landed in the input box before Enter is
    # risked at all; the second confirms Enter actually submitted them (a
    # repaint swallowing the Enter leaves the reply sitting there, unsent,
    # while the box still shows it).
    if ! verified_type "$LANE" "$MESSAGE" --literal --settle 0 --retries 1; then
      if [ "$SEND_STATUS" = send_failed ]; then
        echo "inbox-route: send-keys to $LANE failed" >&2
        notify_jon "Telegram reply could not be delivered" \
          "$LANE was blocked and waiting but the send failed -- reply was: $MESSAGE"
      else
        echo "inbox-route: $LANE never showed the reply after typing it -- not sending Enter" >&2
        notify_jon "Telegram reply could not be confirmed" \
          "$LANE was blocked and waiting but the reply never appeared in its input box -- reply was: $MESSAGE"
      fi
      exit 1
    fi
    if ! verified_submit "$LANE" --confirm-tries 1 --confirm-settle 0; then
      if [ "$SEND_STATUS" = send_failed ]; then
        echo "inbox-route: sending Enter to $LANE failed" >&2
        notify_jon "Telegram reply could not be delivered" \
          "$LANE was blocked and waiting but sending Enter failed -- reply was: $MESSAGE"
      else
        echo "inbox-route: $LANE still shows the reply after Enter -- not confirmed delivered" >&2
        notify_jon "Telegram reply could not be confirmed" \
          "$LANE was blocked and waiting but Enter did not submit the reply -- it may still be sitting in the box -- reply was: $MESSAGE"
      fi
      exit 1
    fi
    echo "inbox-route: delivered to $LANE"
    exit 0
    ;;
  0)
    # agent-dotfiles#186: inbox-poll.sh drains a batch from one getUpdates
    # call and routes each message in turn. If every message in that batch
    # hit this branch and each called notify_jon itself, a burst of N
    # messages paged Jon N times in a row -- observed live as 23 messages
    # producing 21 identical pings in ~21 seconds. INBOX_ROUTE_BATCH is set
    # by inbox-poll.sh's drain loop (never by a standalone caller) to say
    # "you are one message in a batch -- stay quiet and let the batch owner
    # notify once." This is opt-in, not the default, so calling this script
    # directly -- as the standalone test suite and any other caller does --
    # keeps the original per-message notify. Exit 3 is distinct from exit 2
    # ("refused, already notified") precisely so the caller can tell the two
    # apart: 2 means Jon already knows, 3 means the caller still owes him a
    # summary.
    if [ -n "${INBOX_ROUTE_BATCH:-}" ]; then
      echo "inbox-route: no lane waiting, deferring notice to the caller's batch summary -- got: $MESSAGE" >&2
      exit 3
    fi
    refuse_exit "Telegram message received, no lane is waiting" \
      "No blocked lane to route to -- got: $MESSAGE"
    ;;
  *)
    LIST="$(IFS=', '; echo "${BLOCKED[*]}")"
    refuse_exit "Telegram reply -- which lane?" \
      "${#BLOCKED[@]} lanes are blocked ($LIST) -- got: $MESSAGE -- reply naming the lane"
    ;;
esac
