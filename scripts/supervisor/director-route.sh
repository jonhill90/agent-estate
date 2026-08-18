#!/bin/bash
# Deliver an inbound Telegram message to the Director. Always.
#
# agent-dotfiles#193. Before this, `inbox-route.sh` routed a Telegram reply
# to whichever worker lane happened to be blocked, and refused-and-notified
# Jon when none was. Both halves were wrong: Jon's own words --
# "telegram should always chat with director or orchestrator" -- and
# `docs/hierarchy-naming-57.md`'s own stated intent ("Director -- the one
# long-lived session that talks to Jon") say the Director is the recipient,
# not a lane guessed from blocked-state. This script is that default path.
# Lane routing does not survive as a ROUTER responsibility (recommendation
# (b) in #193's issue body, "the safest reading"); relaying a reply to a
# specific lane is something the Director itself can still do, deliberately,
# with `inbox-route.sh` -- kept for exactly that, see its own header.
#
# THE HAZARD IS NOT THE ONE #159 SOLVED FOR LANES. #159/#161 are about typing
# free text into a MENU. That still applies here, but it is not what makes
# this script different from a simple `inbox-route.sh` copy pointed at a
# fixed pane. Research for #193 established that the Director's pane IS the
# same physical pane `watchdog.sh` restarts as "the supervisor loop" --
# `SUPERVISOR_PANE`, default `agent-dotfiles:1.1` -- a self-scheduling
# `/loop`. `loop-tick.md`'s own header names the hazard: "A plain message
# sent to your pane REPLACES the loop prompt, so the next turn is an
# ordinary turn and nothing re-arms." Measured there (#85): 27 raw messages
# into that pane, zero `ScheduleWakeup` calls, the loop silently dead. A
# naive `send-keys` of Jon's raw text into an idle Director pane reproduces
# that exact failure -- even a *correct*, helpful reply to Jon would still
# kill the loop, because the injected turn has no reason to re-arm it.
#
# THE FIX REUSES TWO MECHANISMS THIS ESTATE ALREADY TRUSTS, RATHER THAN
# INVENTING A THIRD:
#
#   1. `director-inbox.sh post` -- already exactly the durable, locked,
#      never-silently-dropped queue this needs, just previously framed as
#      Director-to-its-own-next-tick. `loop-tick.md`'s tick already drains
#      it FIRST, before anything else, on every iteration regardless of who
#      triggered that iteration. Posting here is a #57-style extension of an
#      existing mechanism, not a second one -- Jon's message can never be
#      lost once this call succeeds, independent of whether live delivery
#      below succeeds, races, or the tmux server is down.
#   2. The exact `/loop` nudge `watchdog.sh` already sends to restart this
#      same pane, gated by the exact busy-check discipline it already uses
#      (recheck immediately before sending, to close the race window a
#      stale earlier check leaves open). That prompt is proven safe against
#      this hazard in production every time the watchdog restarts the loop:
#      it tells the pane to run `loop-tick.md` exactly, whose own first step
#      is draining the inbox this script just posted to, and whose own
#      instruction is "never call stop, always re-arm." Reusing it here
#      turns an already-scheduled-but-idle pane into an EARLY tick rather
#      than waiting out however long its own next `ScheduleWakeup` is --
#      which is what actually answers Jon's complaint ("I know you did not
#      reply right away") without reintroducing the tick-latency #142 was
#      built to remove for lanes.
#
# So every message takes the SAME two steps, in this order:
#
#   queue (always)   -> director-inbox.sh post. If this fails, the message
#                       really could be lost; that is reported to Jon via
#                       notify.sh (AGENT_NOTIFY_CALLER=supervisor -- this is
#                       the routing plumbing reporting on itself, not a
#                       Director-authored reply) as a last resort, same
#                       shape as inbox-route.sh's own notify_jon fallback.
#   nudge (if safe)   -> only when the pane's last status line is
#                       unambiguously idle (READY_RE, mirrored from
#                       lanes.sh) AND its input box holds no real unsent
#                       text (input_box_state != text, #141's fix, reused
#                       rather than re-derived) AND an immediate re-check
#                       right before sending still reads idle. Anything
#                       else -- busy, a menu/dialog, `unknown`, the session
#                       or pane not existing -- leaves the message safely
#                       queued and does not touch the pane. This is the
#                       same #126 posture as `lanes.sh`'s `free`: only
#                       positive evidence earns a send, absence of evidence
#                       never does.
#
# Usage: director-route.sh "<message text>" [session]
#        director-route.sh --flush [session]
#
# `--flush` takes no message: it is the retry half. `inbox-poll.sh` calls it
# once per drain iteration (roughly every `INBOX_POLL_TIMEOUT` seconds,
# ~25s by default) REGARDLESS of whether a new Telegram message arrived, so
# a message queued while the Director was busy gets nudged the moment the
# pane frees up, not only when Jon happens to send a second message. It
# skips the queue step entirely -- there is nothing new to post -- and runs
# only the nudge half.
#
# agent-supervisor#34: the nudge half depends on the pane going idle, and a
# pane that is mid-tick or waiting on a tool almost all the time can go hours
# without that ever being true -- measured at 9 messages, 12 hours, nothing
# downstream able to tell "queued" from "lost". Every call here (post or
# `--flush`) also runs `director-inbox.sh escalate`, which does not depend on
# the pane at all: once a message has been queued past
# `DIRECTOR_INBOX_STALE_SECONDS` (default 1800s) it pages Jon directly, once,
# regardless of whether the nudge below ever succeeds. See that block's own
# comment for why this cannot re-page every ~25s.
#
# Exit 0: queued, and the live nudge was sent and confirmed delivered
#         (same pane-evidence discipline as inbox-route.sh: the box must
#         show the prompt before Enter, and be empty after).
# Exit 2: queued successfully, but the pane was not safely idle -- the
#         message is NOT lost, it is sitting in director-inbox.sh's box for
#         the Director's own next tick (whenever that is) or a later
#         `--flush` to pick up. This is the expected, common outcome for a
#         mid-turn Director and is deliberately NOT reported to Jon --
#         unlike the old lane-routing "nobody is listening" refusal, there
#         is always exactly one recipient here, so a normal queue is not
#         news.
# Exit 1: the durable queue write itself failed. The one case this script
#         treats as a real loss risk; notify.sh is tried as a last resort
#         and the message text is always also printed to stderr so it is
#         never lost from the caller's own log, matching every other
#         failure path in this directory.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./input-box.sh
. "$HERE/input-box.sh"
# shellcheck source=./send.sh
. "$HERE/send.sh"
# shellcheck source=./session-defaults.sh
. "$HERE/session-defaults.sh"

FLUSH=""
if [ "${1:-}" = "--flush" ]; then
  FLUSH=1
  shift
  SESSION="${1:-$(lanes_session_or_default)}"
  MESSAGE=""
else
  MESSAGE="${1:-}"
  SESSION="${2:-$(lanes_session_or_default)}"
  if [ -z "$MESSAGE" ]; then
    echo "director-route: usage: director-route.sh \"<message text>\" [session] | director-route.sh --flush [session]" >&2
    exit 2
  fi
fi

# The Director's pane. Deliberately the SAME variable name and default
# `watchdog.sh` uses for `SUPERVISOR_PANE` -- #193's research confirmed they
# are, today, the same physical pane (see header). Overriding one without
# the other would silently split a target that is not actually split yet.
PANE="${SUPERVISOR_PANE:-${SESSION}:1.1}"

# Window-index form, for the plain (non-`-e`) status-line capture below --
# `tmux capture-pane -t` accepts either `session:window` or
# `session:window.pane`; the trailing `.pane` is kept for parity with
# `watchdog.sh` but is not required for a read.
# THE IDLE MATCHER COMES FROM THE HARNESS, never from a private copy here.
#
# This line used to read `READY_RE='^❯ [^←]*$|← 1 agent$'` and that made
# `idle()` return false on a genuinely idle pane, because Claude Code now
# renders its footer as `← for agents` with no count. Measured against a real
# idle lane rather than argued:
#
#   last line: `  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents`
#   old READY_RE -> NO MATCH      (so idle() is false, always)
#   fixed RE     -> MATCH
#
# `idle()` gates every delivery, so a matcher that never matches meant
# director-route could NEVER deliver a message to the Director. Jon's replies
# from Telegram queued behind it and the log filled with "flush -- pane not
# idle, nothing sent" while the pane sat plainly idle. Two of his instructions
# were found stranded in the input box, unread, hours old.
#
# This is the THIRD recurrence of one bug: #314 and #324 each fixed the footer
# shape in `harness/claude.sh`, and this private copy was missed both times.
# So it is not copied again -- it is sourced. A second copy of a value that
# tracks someone else's UI will always drift; the only fix that holds is having
# one.
if [ -z "${HARNESS_READY_RE:-}" ] && [ -r "$HERE/harness/claude.sh" ]; then
  # shellcheck disable=SC1091
  . "$HERE/harness/claude.sh" 2>/dev/null || true
fi
READY_RE="${HARNESS_READY_RE:-^❯ [^←]*\$|← [0-9]+ agents?\$|← for agents}"

notify_jon() {  # notify_jon <subject> <body>
  AGENT_NOTIFY_CALLER=supervisor "$HERE/notify.sh" "$1" "$2"
}

if [ -n "$MESSAGE" ]; then
  if ! "$HERE/director-inbox.sh" post "Telegram from Jon: $MESSAGE" >/dev/null; then
    echo "director-route: could not queue for the Director -- message was: $MESSAGE" >&2
    if notify_jon "Telegram message could not be queued for the Director" \
         "director-inbox.sh post failed -- message was: $MESSAGE"; then
      exit 1
    fi
    echo "director-route: could not reach Jon either -- message was: $MESSAGE" >&2
    exit 1
  fi
fi

# --- escalate on age, unconditionally, before the idle check below ---------
# agent-supervisor#34. Everything below this point can exit 2 (busy, on a
# menu, unsent text in the box, a lost race) without the message being lost
# -- it stays queued for the Director's own next tick. What #34 measured is
# that "queued" and "delivered" then become indistinguishable from outside:
# nine messages sat queued and un-escalated for twelve hours because the pane
# was never once caught idle, and nothing else was watching the clock.
#
# This runs on EVERY call -- a fresh post and a bare `--flush` alike -- and
# before the idle check, so a pane that is never idle cannot suppress it the
# way it suppresses the nudge.
#
# agent-supervisor#42 review: the row used to be marked escalated the instant
# it crossed the threshold, before notify_jon ran at all -- so a failed page
# (unreachable channel, no creds) left the row permanently marked escalated
# anyway, and the escalate-detect step's own not-already-escalated filter
# then suppressed every later retry. That recreated the exact silent-loss
# shape #34 exists to fix, one layer up: the escalation path failed once and
# then suppressed itself forever. `director-inbox.sh escalate` is now
# detect-only; the row is committed to `escalated: true` (via
# `escalate-commit`) ONLY after notify_jon has actually returned success. A
# failed notify leaves the row exactly as it was -- pending, not escalated --
# so the next `--flush` (~25s later) detects it as stale again and retries,
# the same debounce shape inbox.sh's own FAIL_THRESHOLD notification uses,
# but now honest about what "escalated" means.
STALE_THRESHOLD="${DIRECTOR_INBOX_STALE_SECONDS:-1800}"
if escalated=$("$HERE/director-inbox.sh" escalate "$STALE_THRESHOLD" 2>/dev/null) && [ -n "$escalated" ]; then
  if notify_jon "Director inbox has undelivered message(s)" \
       "pending >= ${STALE_THRESHOLD}s with the Director's pane never caught idle -- $escalated"; then
    # Bash 3.2 (macOS) has no readarray/mapfile -- build the argv array by
    # hand rather than reaching for either.
    escalated_ats=()
    while IFS= read -r ts; do
      escalated_ats+=("$ts")
    done < <(grep -oE '^\[director [^]]+\]' <<<"$escalated" | sed -E 's/^\[director (.+)\]$/\1/')
    "$HERE/director-inbox.sh" escalate-commit "${escalated_ats[@]}" >/dev/null
  else
    echo "director-route: escalation notify failed -- message(s) stay queued, will retry" >&2
  fi
fi

# --- is it safe to nudge the pane right now? --------------------------
idle() {
  local last box
  last=$(tmux capture-pane -p -t "$PANE" 2>/dev/null | grep -v '^[[:space:]]*$' | tail -1)
  [ -n "$last" ] || return 1
  grep -q 'esc to interrupt' <<<"$last" && return 1
  grep -qE "$READY_RE" <<<"$last" || return 1
  box=$(tmux capture-pane -pe -t "$PANE" 2>/dev/null | input_box_state)
  [ "$box" != text ]
}

if ! idle; then
  if [ -n "$FLUSH" ]; then
    echo "director-route: flush -- pane not idle, nothing sent"
  else
    echo "director-route: queued for the Director -- pane not idle, not nudging (was: $MESSAGE)"
  fi
  exit 2
fi

# Re-check immediately before sending -- the same race `watchdog.sh` closes
# this way: the earlier check is stale by however long capture-pane and the
# box read above took, and the pane can have started a turn inside that
# window. Losing this race sends the nudge into a busy pane, where it
# queues as inert plain text instead of being read as a prompt.
if ! idle; then
  echo "director-route: pane became busy during the idle recheck -- not nudging"
  exit 2
fi

# The exact nudge `watchdog.sh` sends to restart this same pane -- see this
# file's header for why reusing it, verbatim, is what makes this safe. `C-u`
# first clears any ghost artifact left in the box, matching watchdog.sh's
# own sequence. Same `SUPERVISOR_TICK` override watchdog.sh honours, so a
# test or an alternate deployment pointing one at a different tick file
# points both.
TICK="${SUPERVISOR_TICK:-$HERE/loop-tick.md}"
NUDGE="/loop Supervisor tick. Follow $TICK exactly. Dispatch to idle worker lanes rather than implementing yourself. Never call stop, always re-arm."

# agent-supervisor#178: type, verify, THEN submit, via the shared primitive
# in send.sh -- extracted from dispatch.sh's own loop. `--preclear` is this
# call's `C-u` (this pane can hold leftover text from anywhere, unlike
# dispatch.sh's freshly `/clear`'d lane); no `--proof` given falls back to
# `input_box_state` merely reporting non-empty, the same evidence discipline
# this file already used by hand before send.sh existed (#159/#161:
# `delivered` must mean the pane's own box showed the text before Enter was
# risked, not that send-keys returned 0). `--settle 0`/`--confirm-settle 0`
# and `--retries 1`/`--confirm-tries 1` reproduce this file's own prior
# behaviour exactly: one attempt each way, no injected delay, no retype.
if ! verified_type "$PANE" "$NUDGE" --preclear --literal --settle 0 --retries 1; then
  if [ "$SEND_STATUS" = send_failed ]; then
    echo "director-route: send-keys to $PANE failed -- message stays queued" >&2
  else
    echo "director-route: $PANE never showed the nudge after typing it -- not sending Enter, message stays queued" >&2
  fi
  exit 2
fi

if ! verified_submit "$PANE" --confirm-tries 1 --confirm-settle 0; then
  case "$SEND_STATUS" in
    send_failed) echo "director-route: sending Enter to $PANE failed -- message stays queued" >&2 ;;
    *) echo "director-route: $PANE still shows the nudge after Enter -- not confirmed, message stays queued" >&2 ;;
  esac
  exit 2
fi

echo "director-route: nudged $PANE -- Director will drain the inbox this tick"
exit 0
