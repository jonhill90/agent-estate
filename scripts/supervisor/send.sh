#!/bin/bash
# The one verified send primitive for typed text going into an agent's (or
# the Director/self's) tmux input box.
#
# WHY: agent-supervisor#178, diagnosed by Jon 2026-08-15. `Enter` does not
# submit text a PREVIOUS `send-keys` left sitting in the box -- `C-u` then
# retyping submits every time. A pane holding an unsubmitted prompt looks
# exactly like a lane that is thinking: no running turn, no error, the box
# just sits there. That is how #179's `merge the PR` prompt sat unsubmitted
# in the lane that authored PR #168 -- and only because it stranded. Fixing
# #178 removes the thing that saved us there, so the discipline below is
# load-bearing, not tidiness.
#
# `dispatch.sh` already had the right loop before this file existed --
# "type, verify against what the pane actually shows, THEN submit" -- and its
# own comments recorded two lessons a fresh implementation would lose. Both
# are extracted here rather than re-derived:
#
#   1. CHECK BOTH ENDS OF THE MESSAGE. A dropped prefix was observed live on
#      2026-08-11 (`Read ` eaten while the harness repainted); an over-long
#      message hides its head by scrolling. Head alone conflates "arrived"
#      with "fits"; tail alone passes a dropped prefix. Both, or neither is
#      evidence -- see `--proof` below.
#   2. SETTLE BEFORE TYPING. Typing immediately after a `/clear` (or any
#      repaint) loses leading characters. `--settle` is why the type call is
#      never the first thing this file does after a caller-side reset.
#
# A THIRD THING THIS FILE ADDS, because no single caller may own it: the
# PROOF that a message landed has to be something only the caller can name.
# `dispatch.sh` knows to look for `Read <brief>` and the worktree path; a
# watchdog nudge has no brief and nothing else in the pane it can promise.
# `--proof TOKEN` (repeatable) is that parameter -- given none, "landed"
# falls back to `input_box_state` reporting the box merely non-empty, which
# is weaker (confirms SOME text, not THIS text) but is the same evidence
# discipline `inbox-route.sh` and `director-route.sh` already used by hand
# before this file existed.
#
# FAIL CLOSED. Every non-zero return means "did not confirm submitted" --
# "cannot tell" must never read as "sent", the same one-way ratchet
# `input-box.sh`'s `unknown` answer and the lane-state code both already
# follow. A caller that ignores the return code is choosing that risk
# explicitly; nothing here ever reports success it cannot back with pane
# evidence.
#
# Sourced, not executed: functions only, no side effects at source time.

SEND_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./input-box.sh
. "$SEND_HERE/input-box.sh"

# Every function below sets these two on return, so a caller never has to
# parse a message to know what happened:
#   SEND_STATUS      one word: landed | not_landed | send_failed | submitted
#                     | stranded | unknown
#   SEND_BOX_STATE    the last `input_box_state` reading observed (text /
#                     empty / unknown), for a caller's own diagnostics --
#                     `dispatch.sh`'s existing WARNING line uses exactly this.

# _send_capture_matches <target> <token...>
#   True iff EVERY token appears (whitespace-stripped, literal match) in the
#   pane's full visible capture (whitespace-stripped). Empty token list is
#   vacuously true and is never how this is called -- see the two callers
#   below, which each supply their own fallback.
_send_capture_matches() {
  local target="$1"; shift
  local pane needle
  pane=$(tmux capture-pane -p -t "$target" 2>/dev/null | tr -d ' \n')
  for needle in "$@"; do
    needle="$(tr -d ' \n' <<<"$needle")"
    grep -qF -- "$needle" <<<"$pane" || return 1
  done
  return 0
}

# _send_head_matches <target> <token>
#   True iff the input box's OWN content -- not the whole pane, and not "the
#   token appears somewhere" -- STARTS WITH `token` (both whitespace-
#   stripped). agent-supervisor#193: `_send_capture_matches` above is an
#   AND-of-substrings check with no notion of position, and that is exactly
#   what let `/clearRead <brief>...` (a `/clear` whose Enter never submitted,
#   glued to the front of the actual brief by the retype that followed) read
#   as "landed" -- `Read <brief>` was still a true substring, just not the
#   start of anything. `input_box_text` (input-box.sh) is what makes "the
#   start of the box" answerable at all; an empty box body is never a match,
#   matching `input_box_text`'s own fail-closed contract for `unknown`.
_send_head_matches() {
  local target="$1" needle="$2" body
  body=$(tmux capture-pane -pe -t "$target" 2>/dev/null | input_box_text)
  needle="$(tr -d ' \n' <<<"$needle")"
  [ -n "$body" ] && [[ "$body" == "$needle"* ]]
}

# verified_type <target> <message> [--settle N] [--retries N] [--preclear]
#                                   [--literal] [--proof TOKEN]...
#
# Types `message` into `target`'s input box and confirms it landed BEFORE
# any Enter is ever risked. Never sends Enter itself -- see `verified_submit`.
#
#   --settle N     seconds to sleep after typing before checking (default 1)
#   --retries N    total type attempts; between a failed attempt and the
#                   next, `C-u` clears whatever partial text is sitting there
#                   and the message is retyped whole (default 1 -- no retry)
#   --preclear     send one `C-u` BEFORE the first attempt too. Needed by any
#                   caller that cannot promise the box started this send
#                   empty -- `watchdog.sh` and `director-route.sh`'s pane can
#                   hold leftover text from anywhere. `dispatch.sh` passes
#                   this too, as of agent-supervisor#240, even though it
#                   always runs a VERIFIED `/clear` immediately before: that
#                   verification covers the instant it was taken, not the
#                   instant this call's own `send-keys` actually runs, and
#                   six lanes were measured holding live unsent text whose
#                   dispatch had trusted exactly that gap to stay empty. The
#                   `C-u` this flag sends is one more tmux call either way;
#                   the difference is whether "nothing to clear" is an
#                   assumption or a thing this call confirms for itself.
#   --literal      pass `-l` to `send-keys` -- required whenever `message`
#                   might contain bytes tmux would otherwise read as a key
#                   name (`inbox-route.sh`'s reasoning: external text is
#                   never safe to assume isn't one). Does NOT cover an
#                   embedded newline -- see the multi-line note below; `-l`
#                   only changes whether tmux itself parses the argument as
#                   a key NAME, and a bare `\n` byte is not a key name, it is
#                   the same byte a terminal reads as Enter no matter who
#                   sent it or how.
#   --proof TOKEN  repeatable. ALL given tokens must appear in the pane
#                   capture for the send to count as landed. No `--proof` at
#                   all falls back to `input_box_state` reporting non-empty.
#   --proof-head TOKEN  a SINGLE token that must START the input box's own
#                   content (agent-supervisor#193) -- position-anchored,
#                   unlike `--proof`, which only checks whole-pane
#                   containment and so cannot tell "landed" from "landed
#                   with garbage glued in front" (see `_send_head_matches`).
#                   Combines with `--proof`: when both are given, EVERY
#                   `--proof` token must still appear (the tail, which
#                   survives scrolling) AND the head token must open the
#                   box (the head, which a dropped-or-prepended prefix hits
#                   first) -- `dispatch.sh`'s own comment on checking both
#                   ends applies here unchanged, just with the head half now
#                   actually anchored instead of merely present.
#
# MULTI-LINE MESSAGES ARE REFUSED, ALWAYS (agent-supervisor#186). `message`
# containing an embedded newline is rejected before any `tmux send-keys` is
# ever run, `--literal` or not: live-reproduced, `send-keys` (with or
# without `-l`) reads an embedded `\n` as an actual Enter keystroke mid-type,
# submitting whatever is in the box at that instant. That is a real Enter
# this function's own header promises it "never sends itself" -- `--literal`
# cannot make it safe because tmux's `-l` only governs how TMUX parses ITS
# OWN argument for key names like `Enter`/`C-c`; it says nothing about a raw
# `\n` byte, which every terminal on the other end reads as Enter regardless.
# Refusing is the fail-closed choice #178's whole file already commits to:
# "cannot tell" must never read as "sent", and a message this function
# cannot type without risking an unintended mid-type submission is exactly
# that case. A caller with a genuine need to send a multi-line message must
# split it into single-line `verified_type` calls of its own, joined by an
# explicit `verified_submit` where it actually wants Enter to fire.
#
# Return 0 (SEND_STATUS=landed), 2 (not_landed, retries exhausted), or
# 1 (send_failed -- either the `tmux send-keys` call itself errored, or
# `message` was refused for containing an embedded newline before any
# `tmux` call was made).
verified_type() {
  local target="$1" message="$2"; shift 2
  local settle=1 retries=1 preclear=0 literal=0 sent_ok=1
  local -a proof=()
  local proof_head=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --settle) settle="$2"; shift 2 ;;
      --retries) retries="$2"; shift 2 ;;
      --preclear) preclear=1; shift ;;
      --literal) literal=1; shift ;;
      --proof) proof+=("$2"); shift 2 ;;
      --proof-head) proof_head="$2"; shift 2 ;;
      *) echo "verified_type: unknown option $1" >&2; SEND_STATUS=send_failed; return 1 ;;
    esac
  done

  if [[ "$message" == *$'\n'* ]]; then
    echo "verified_type: message contains an embedded newline -- tmux reads a bare newline as Enter mid-type regardless of --literal; refusing rather than risk an unintended submission (agent-supervisor#186)" >&2
    SEND_STATUS=send_failed
    return 1
  fi

  if [ "$preclear" = 1 ]; then
    tmux send-keys -t "$target" C-u 2>/dev/null
  fi

  local attempt
  for ((attempt = 1; attempt <= retries; attempt++)); do
    if [ "$literal" = 1 ]; then
      sent_ok=1; tmux send-keys -l -t "$target" "$message" 2>/dev/null || sent_ok=0
    else
      sent_ok=1; tmux send-keys -t "$target" "$message" 2>/dev/null || sent_ok=0
    fi
    if [ "$sent_ok" != 1 ]; then
      SEND_STATUS=send_failed
      return 1
    fi
    sleep "$settle"

    if [ -n "$proof_head" ] || [ "${#proof[@]}" -gt 0 ]; then
      if { [ -z "$proof_head" ] || _send_head_matches "$target" "$proof_head"; } \
         && { [ "${#proof[@]}" -eq 0 ] || _send_capture_matches "$target" "${proof[@]}"; }; then
        SEND_STATUS=landed
        return 0
      fi
    else
      SEND_BOX_STATE=$(tmux capture-pane -pe -t "$target" 2>/dev/null | input_box_state)
      if [ "$SEND_BOX_STATE" = text ]; then
        SEND_STATUS=landed
        return 0
      fi
    fi

    if [ "$attempt" -lt "$retries" ]; then
      # Whatever landed partially (or nothing) is cleared before the retype
      # -- retyping ON TOP of a dropped prefix would just compound it.
      tmux send-keys -t "$target" C-u 2>/dev/null
      sleep "$settle"
    fi
  done
  SEND_STATUS=not_landed
  return 2
}

# verified_preclear <target> [--settle N] [--retries N]
#
# Sends `C-u` then `/clear` + Enter, and confirms the screen actually
# blanked -- the input box reads `empty`, not `text` or `unknown` -- before
# returning success. agent-supervisor#193: `/clear`'s own Enter can be
# swallowed exactly the way #178 already found a brief's Enter can be, and
# unlike a stranded brief this failure was INVISIBLE downstream -- the next
# `verified_type` call still found its proof tokens (as true substrings of
# "/clear" glued onto the front of the retyped brief) and reported `landed`.
# Confirming the blank HERE, before anything else is ever typed, is the
# fail-closed fix `dispatch.sh`'s own comment above this call promised and
# did not yet deliver: "abort the dispatch if it did not [clear], rather
# than typing over an unsubmitted line."
#
# This is deliberately its own primitive, not a `verified_type`/
# `verified_submit` pair: `/clear` blanks the WHOLE screen, which the
# `--proof` substring check was never built to read through (see
# `verified_type`'s header) -- the only thing checkable here is that the box
# came back empty, so that is the whole of what this function confirms.
#
#   --settle N   seconds to sleep after sending before checking (default 1)
#   --retries N  total attempts; each retry re-sends `C-u` then `/clear`
#                 Enter (default 1 -- no retry)
#
# Return 0 (SEND_STATUS=landed, box confirmed empty), 2 (not_landed -- the
# box still shows text, or could not be read at all, after every retry), or
# 1 (send_failed -- the `/clear` send-keys call itself errored).
verified_preclear() {
  local target="$1"; shift
  local settle=1 retries=1
  while [ $# -gt 0 ]; do
    case "$1" in
      --settle) settle="$2"; shift 2 ;;
      --retries) retries="$2"; shift 2 ;;
      *) echo "verified_preclear: unknown option $1" >&2; SEND_STATUS=send_failed; return 1 ;;
    esac
  done

  local attempt
  for ((attempt = 1; attempt <= retries; attempt++)); do
    tmux send-keys -t "$target" C-u 2>/dev/null
    if ! tmux send-keys -t "$target" "/clear" Enter 2>/dev/null; then
      SEND_STATUS=send_failed
      return 1
    fi
    sleep "$settle"
    SEND_BOX_STATE=$(tmux capture-pane -pe -t "$target" 2>/dev/null | input_box_state)
    if [ "$SEND_BOX_STATE" = empty ]; then
      SEND_STATUS=landed
      return 0
    fi
  done
  SEND_STATUS=not_landed
  return 2
}

# verified_submit <target> [--confirm-tries N] [--confirm-settle N]
#
# Sends Enter, then polls for the box going EMPTY -- the durable signal
# #141 established: true while a turn runs and after it finishes, false in
# exactly the failure this whole file exists for.
#
#   --confirm-tries N    how many times to poll (default 10)
#   --confirm-settle N   seconds between polls; the first poll also sleeps
#                         this long before its first check (default 1)
#
# Return 0 (SEND_STATUS=submitted), 1 (send_failed -- the Enter call itself
# errored), 3 (stranded -- the box still shows text; CONFIRMED, not
# assumed), or 4 (unknown -- the box could not be read at all, e.g. another
# harness or a pane too short to show it). 3 and 4 are both failure: #178's
# fail-closed rule is that "cannot tell" never degrades to "sent".
verified_submit() {
  local target="$1"; shift
  local confirm_tries=10 confirm_settle=1
  while [ $# -gt 0 ]; do
    case "$1" in
      --confirm-tries) confirm_tries="$2"; shift 2 ;;
      --confirm-settle) confirm_settle="$2"; shift 2 ;;
      *) echo "verified_submit: unknown option $1" >&2; SEND_STATUS=send_failed; return 1 ;;
    esac
  done

  if ! tmux send-keys -t "$target" Enter 2>/dev/null; then
    SEND_STATUS=send_failed
    return 1
  fi

  local attempt
  for ((attempt = 1; attempt <= confirm_tries; attempt++)); do
    sleep "$confirm_settle"
    SEND_BOX_STATE=$(tmux capture-pane -pe -t "$target" 2>/dev/null | input_box_state)
    if [ "$SEND_BOX_STATE" = empty ]; then
      SEND_STATUS=submitted
      return 0
    fi
  done

  if [ "$SEND_BOX_STATE" = text ]; then
    SEND_STATUS=stranded
    return 3
  fi
  SEND_STATUS=unknown
  return 4
}

# verified_send <target> <message> [every verified_type/verified_submit flag]
#
# Convenience wrapper for a caller with no work to do between "typed and
# confirmed landed" and "submitted" -- dispatch.sh has such work (the ledger
# commit at its point of no return) and calls the two functions above
# directly instead. Everyone else can call this.
#
# Returns whichever of verified_type/verified_submit's codes applies;
# verified_submit is never reached if verified_type did not land.
verified_send() {
  local target="$1" message="$2"; shift 2
  local -a type_args=() submit_args=()
  while [ $# -gt 0 ]; do
    case "$1" in
      --confirm-tries|--confirm-settle) submit_args+=("$1" "$2"); shift 2 ;;
      --proof|--proof-head) type_args+=("$1" "$2"); shift 2 ;;
      --preclear|--literal) type_args+=("$1"); shift ;;
      --settle|--retries) type_args+=("$1" "$2"); shift 2 ;;
      *) echo "verified_send: unknown option $1" >&2; SEND_STATUS=send_failed; return 1 ;;
    esac
  done
  verified_type "$target" "$message" "${type_args[@]+"${type_args[@]}"}" || return $?
  verified_submit "$target" "${submit_args[@]+"${submit_args[@]}"}"
}

# blind_send <target> <message> [--preclear-settle N] [--type-settle N] [--literal]
#
# NOT a verified send -- SEND_STATUS is always `unverified`, and the return
# code only ever reflects whether the `tmux send-keys` calls themselves
# errored, never whether the message landed or submitted. This exists for
# exactly one caller today, `watchdog.sh`'s self-nudge: the pane it nudges
# is tested against a stub built to model busy/idle CHROME (`⏵⏵ bypass
# permissions`, `esc to interrupt`), not an editable input buffer the way
# `dispatch.sh`'s stub is, and neither `input_box_state` nor a `--proof`
# substring check has anything to read there. `verified_type`/
# `verified_submit` would fail every call, always, for a reason that has
# nothing to do with whether the send actually worked.
#
# This is a NAMED, DELIBERATE gap, not a silent one: `watchdog.sh`'s nudge
# carried the same three unverified `tmux send-keys` calls before send.sh
# existed, with nothing in between confirming they landed. Moving them here
# does not add verification -- doing that for real needs the stub watchdog's
# own suite trusts to grow an input-buffer model the way `tmux-dispatch`
# already has, which is follow-up work, not this change -- but it does mean
# `grep -rn 'send-keys' scripts/` finds no caller that reaches `tmux`
# directly outside this one file, and it means the gap has ONE name
# (`blind_send`) instead of three anonymous call sites.
blind_send() {
  local target="$1" message="$2"; shift 2
  local preclear_settle=0 type_settle=0 literal=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --preclear-settle) preclear_settle="$2"; shift 2 ;;
      --type-settle) type_settle="$2"; shift 2 ;;
      --literal) literal=1; shift ;;
      *) echo "blind_send: unknown option $1" >&2; SEND_STATUS=send_failed; return 1 ;;
    esac
  done
  SEND_STATUS=unverified
  tmux send-keys -t "$target" C-u 2>/dev/null
  sleep "$preclear_settle"
  if [ "$literal" = 1 ]; then
    tmux send-keys -l -t "$target" "$message" 2>/dev/null
  else
    tmux send-keys -t "$target" "$message" 2>/dev/null
  fi
  sleep "$type_settle"
  tmux send-keys -t "$target" Enter 2>/dev/null
}
