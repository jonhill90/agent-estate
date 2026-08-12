#!/bin/bash
# Deliver one inbound Telegram message to the lane it answers.
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
# the caller's log.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./input-box.sh
. "$HERE/input-box.sh"
SESSION="${2:-${LANES_SESSION:-agent-dotfiles}}"
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
    # `-l`: unlike dispatch.sh's own brief send, this text comes from an
    # external network service (agent-dotfiles#152) -- it is never safe to
    # assume it isn't a recognised key name. Without `-l`, a reply of
    # literally `C-c` or `Escape` fires that control action at the lane
    # instead of typing the four characters; `C-u` would silently wipe
    # whatever the lane had already typed. `-l` forces every byte to be
    # typed as literal text, key name or not, and Enter stays a deliberate
    # second call so a message can never smuggle in its own submission.
    if ! tmux send-keys -l -t "$LANE" "$MESSAGE" 2>/dev/null; then
      echo "inbox-route: send-keys to $LANE failed" >&2
      notify_jon "Telegram reply could not be delivered" \
        "$LANE was blocked and waiting but the send failed -- reply was: $MESSAGE"
      exit 1
    fi
    # #159: `delivered` must be backed by evidence from the pane, not by
    # `send-keys` returning 0 -- that was true right before the router typed
    # a reply into a menu and reported success. The first check confirms the
    # literal bytes actually landed in the input box before Enter is risked
    # at all; the second confirms Enter actually submitted them (#141's
    # failure shape: a repaint swallows the Enter and the reply sits there,
    # unsent, while the box still shows it).
    if [ "$(tmux capture-pane -pe -t "$LANE" 2>/dev/null | input_box_state)" != text ]; then
      echo "inbox-route: $LANE never showed the reply after typing it -- not sending Enter" >&2
      notify_jon "Telegram reply could not be confirmed" \
        "$LANE was blocked and waiting but the reply never appeared in its input box -- reply was: $MESSAGE"
      exit 1
    fi
    if ! tmux send-keys -t "$LANE" Enter 2>/dev/null; then
      echo "inbox-route: sending Enter to $LANE failed" >&2
      notify_jon "Telegram reply could not be delivered" \
        "$LANE was blocked and waiting but sending Enter failed -- reply was: $MESSAGE"
      exit 1
    fi
    if [ "$(tmux capture-pane -pe -t "$LANE" 2>/dev/null | input_box_state)" = empty ]; then
      echo "inbox-route: delivered to $LANE"
      exit 0
    fi
    echo "inbox-route: $LANE still shows the reply after Enter -- not confirmed delivered" >&2
    notify_jon "Telegram reply could not be confirmed" \
      "$LANE was blocked and waiting but Enter did not submit the reply -- it may still be sitting in the box -- reply was: $MESSAGE"
    exit 1
    ;;
  0)
    refuse_exit "Telegram message received, no lane is waiting" \
      "No blocked lane to route to -- got: $MESSAGE"
    ;;
  *)
    LIST="$(IFS=', '; echo "${BLOCKED[*]}")"
    refuse_exit "Telegram reply -- which lane?" \
      "${#BLOCKED[@]} lanes are blocked ($LIST) -- got: $MESSAGE -- reply naming the lane"
    ;;
esac
