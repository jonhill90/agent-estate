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

# _send_head_matches <target> <token> <box_prompt> <box_close>
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
#
#   `box_prompt`/`box_close`, agent-estate#446: this harness's own
#   `H_INPUT_BOX_PROMPT`/`H_INPUT_BOX_CLOSE` (harness-registry.sh), already
#   RESOLVED to concrete values by the caller -- see `verified_type`'s own
#   header for how `--box-prompt`/`--box-close` (or their Claude-shaped
#   defaults, when omitted) are resolved once per call rather than here.
#   Both REQUIRED, always, even when empty: `input_box_text` itself is what
#   tells "omitted" (Claude's own default) from "explicitly empty" (a
#   harness with no measured box shape) apart, and it can only do that if
#   this function never collapses the two by leaving an argument off.
_send_head_matches() {
  local target="$1" needle="$2" box_prompt="$3" box_close="$4" body
  body=$(tmux capture-pane -pe -t "$target" 2>/dev/null | input_box_text "$box_prompt" "$box_close")
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
#   --box-prompt VALUE  agent-estate#446. This harness's own input-box
#                   marker (`H_INPUT_BOX_PROMPT`, harness-registry.sh) --
#                   threaded to `input_box_state`/`input_box_text`
#                   (input-box.sh) wherever this function reads the box
#                   itself: the `--proof-head` check above, and the no-proof
#                   fallback below. OMITTED (the default -- every caller
#                   before #446) means Claude's own marker, unchanged from
#                   what was hardcoded here pre-#446. Given as an EMPTY
#                   string (what a harness with no measured box shape --
#                   `harness/copilot.sh` today -- resolves to) means "no
#                   known box for this harness", and the box reads
#                   `unknown`/`""` rather than silently reusing Claude's own
#                   shape -- see input-box.sh's header, section 4, for why
#                   this distinction needs its own flag rather than folding
#                   into `--proof-head`.
#   --box-close VALUE   this harness's `H_INPUT_BOX_CLOSE` -- `rule`
#                   (Claude, the default) or `blank` (Codex). Only
#                   meaningful alongside `--box-prompt`; see input-box.sh.
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
  local box_prompt="" box_close="" box_given=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --settle) settle="$2"; shift 2 ;;
      --retries) retries="$2"; shift 2 ;;
      --preclear) preclear=1; shift ;;
      --literal) literal=1; shift ;;
      --proof) proof+=("$2"); shift 2 ;;
      --proof-head) proof_head="$2"; shift 2 ;;
      --box-prompt) box_prompt="$2"; box_given=1; shift 2 ;;
      --box-close) box_close="$2"; box_given=1; shift 2 ;;
      *) echo "verified_type: unknown option $1" >&2; SEND_STATUS=send_failed; return 1 ;;
    esac
  done
  # agent-estate#446: resolved ONCE, to concrete values, whether or not the
  # caller gave `--box-prompt`/`--box-close` -- omitted means Claude's own
  # marker/close-mode (INPUT_BOX_PROMPT, "rule"), unchanged from what was
  # hardcoded pre-#446; given (even as an explicit empty string, a harness
  # with no measured box shape) is used exactly as given. See input-box.sh's
  # header, section 4, for why "omitted" and "explicitly empty" must stay
  # distinguishable this far down rather than collapsing to one default.
  if [ "$box_given" != 1 ]; then
    box_prompt="$INPUT_BOX_PROMPT"
    box_close="rule"
  fi

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
      if { [ -z "$proof_head" ] || _send_head_matches "$target" "$proof_head" "$box_prompt" "$box_close"; } \
         && { [ "${#proof[@]}" -eq 0 ] || _send_capture_matches "$target" "${proof[@]}"; }; then
        SEND_STATUS=landed
        return 0
      fi
    else
      SEND_BOX_STATE=$(tmux capture-pane -pe -t "$target" 2>/dev/null | input_box_state "$box_prompt" "$box_close")
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
# Sends `Escape` then `C-u` then `/clear` + Enter, and confirms the screen
# actually blanked -- the input box reads `empty`, not `text` or `unknown` --
# before returning success. agent-supervisor#193: `/clear`'s own Enter can be
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
# `Escape` before `C-u`, agent-dotfiles#255: Jon's own recovery from three
# consecutive real refusals on 2026-08-21 was manual `Escape` then `C-u`
# then `Enter`, not `C-u` alone -- `C-u` only clears an editable input box,
# and does nothing to a pane stuck showing a menu or an in-progress turn,
# which is exactly the shape a leftover state can take. Sending it here
# costs nothing on the ordinary path: a no-op on an already-empty box, and
# (measured live against a real, never-before-trusted codex pane) a no-op
# on codex's own directory-trust menu too -- see harness/codex.sh's note on
# that menu for why a fresh dispatch's very first pane content is often it.
#
#   --settle N   seconds to sleep after sending before checking (default 1)
#   --retries N  total attempts; each retry re-sends `Escape`, `C-u`, then
#                 `/clear` Enter (default 1 -- no retry)
#   --box-prompt VALUE / --box-close VALUE   agent-estate#446, same contract
#                 as `verified_type`'s own flags of the same name -- see
#                 that function's header. Omitted means Claude's own marker
#                 and `rule` close mode, unchanged from pre-#446.
#
# `Escape` before `C-u`, agent-dotfiles#255: reported directly by Jon,
# 2026-08-20/21 -- under loaded panes, the default settle/retry budget
# ("DISPATCH_SETTLE=2 DISPATCH_PRECLEAR_RETRIES=2") was refused three
# dispatches running with "`/clear` did not blank <lane>'s screen", and a
# manual `Escape` then `C-u` then `Enter` cleared the state reliably where
# repeated bare `C-u`/`/clear` retries had not. `C-u` alone only clears an
# ORDINARY line already sitting in the box; `Escape` first cancels whatever
# else the pane might be mid-way through (a stuck key sequence, a partial
# escape code from a repaint under load) that `C-u` was never built to
# reach. Both are real KEYS tmux interprets, not strings sent to the pane
# the way `/clear` itself is -- see the header above this function's own
# caller in dispatch.sh for why that distinction is the whole of #255's
# root cause.
#
# Return 0 (SEND_STATUS=landed, box confirmed empty), 2 (not_landed -- the
# box still shows text, or could not be read at all, after every retry), or
# 1 (send_failed -- the `/clear` send-keys call itself errored).
verified_preclear() {
  local target="$1"; shift
  local settle=1 retries=1
  local box_prompt="" box_close="" box_given=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --settle) settle="$2"; shift 2 ;;
      --retries) retries="$2"; shift 2 ;;
      --box-prompt) box_prompt="$2"; box_given=1; shift 2 ;;
      --box-close) box_close="$2"; box_given=1; shift 2 ;;
      *) echo "verified_preclear: unknown option $1" >&2; SEND_STATUS=send_failed; return 1 ;;
    esac
  done
  if [ "$box_given" != 1 ]; then
    box_prompt="$INPUT_BOX_PROMPT"
    box_close="rule"
  fi

  local attempt
  for ((attempt = 1; attempt <= retries; attempt++)); do
    tmux send-keys -t "$target" Escape 2>/dev/null
    tmux send-keys -t "$target" C-u 2>/dev/null
    if ! tmux send-keys -t "$target" "/clear" Enter 2>/dev/null; then
      SEND_STATUS=send_failed
      return 1
    fi
    sleep "$settle"
    SEND_BOX_STATE=$(tmux capture-pane -pe -t "$target" 2>/dev/null | input_box_state "$box_prompt" "$box_close")
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
#   --box-prompt VALUE / --box-close VALUE   agent-estate#446, same contract
#                 as `verified_type`'s own flags of the same name.
#
# Return 0 (SEND_STATUS=submitted), 1 (send_failed -- the Enter call itself
# errored), 3 (stranded -- the box still shows text; CONFIRMED, not
# assumed), or 4 (unknown -- the box could not be read at all, e.g. another
# harness or a pane too short to show it). 3 and 4 are both failure: #178's
# fail-closed rule is that "cannot tell" never degrades to "sent".
verified_submit() {
  local target="$1"; shift
  local confirm_tries=10 confirm_settle=1
  local box_prompt="" box_close="" box_given=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --confirm-tries) confirm_tries="$2"; shift 2 ;;
      --confirm-settle) confirm_settle="$2"; shift 2 ;;
      --box-prompt) box_prompt="$2"; box_given=1; shift 2 ;;
      --box-close) box_close="$2"; box_given=1; shift 2 ;;
      *) echo "verified_submit: unknown option $1" >&2; SEND_STATUS=send_failed; return 1 ;;
    esac
  done
  if [ "$box_given" != 1 ]; then
    box_prompt="$INPUT_BOX_PROMPT"
    box_close="rule"
  fi

  if ! tmux send-keys -t "$target" Enter 2>/dev/null; then
    SEND_STATUS=send_failed
    return 1
  fi

  local attempt
  for ((attempt = 1; attempt <= confirm_tries; attempt++)); do
    sleep "$confirm_settle"
    SEND_BOX_STATE=$(tmux capture-pane -pe -t "$target" 2>/dev/null | input_box_state "$box_prompt" "$box_close")
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

# verified_launch_prompt <target> [--settle N] [--tries N] [--failure-re REGEX]
#                                  [--blocked-re REGEX] [--option-row-re REGEX]
#
# agent-dotfiles#255. A harness whose adapter sets H_LAUNCH_TAKES_PROMPT
# (harness-registry.sh; codex today) never has its brief-pointer message
# typed into a live pane at all -- `dispatch.sh` folds it into that
# harness's own LAUNCH_CMD, as its documented launch-time PROMPT argument,
# because that harness's fresh-lane path does not treat the first message
# TYPED into a live pane as a real turn (codex: it is consumed as the
# session's auto-generated title instead -- see harness/codex.sh). There is
# therefore no input box for `verified_type`/`verified_submit` to poll: no
# text was ever typed, so "the box went empty" proves nothing here.
#
# What this path DOES have is a harness-specific FAILURE signature: the
# shape that harness paints when the folded prompt was NOT accepted as a
# turn (codex's `Session renamed to`). This polls for that signature's
# ABSENCE across the same settle/retry budget verified_submit uses, rather
# than a box state -- there is no box state to read here, only whatever the
# harness itself painted.
#
# `--blocked-re`/`--option-row-re`, agent-dotfiles#255 round 2: reproduced
# LIVE, against a real codex lane, never a mock. `respawn-pane -c $WORKTREE`
# (dispatch.sh, step 3.5) always starts the harness in a WORKTREE it has
# never seen before, and codex's own directory-trust gate --
# "Do not trust the contents of this directory? ... Press enter to continue"
# -- comes up on that first sight regardless of how the prompt reaches it.
# A folded launch prompt SURVIVES that gate once a human answers it (also
# verified live: the exact same message that was queued behind the menu ran
# as a real turn the moment "1. Yes, continue" was accepted) -- but nothing
# answers it in an unattended dispatch, and the failure-re check above is
# blind to this: the trust menu contains no "Session renamed to", so a lane
# stuck there for the whole settle/retry budget reported SEND_STATUS=submitted
# -- dispatch: #N -> lane, exit 0 -- while the pane sat on a menu nobody had
# answered. That is #255's exact silent-success shape, produced by the fix
# meant to close it. These two harness-recorded markers (H_BLOCKED_MARKERS,
# H_OPTION_ROW_RE -- the same fields `lanes.sh` already uses to classify a
# lane `menu-blocked`) are checked against the LAST poll only: a menu seen
# ONCE and then answered is not a failure (the trust prompt during the
# settle window, in the reproduction above, cleared before the loop's last
# check); a menu still sitting there when the budget runs out is the one
# this function was never able to see a real turn behind.
#
#   --settle N     seconds between polls (default 1); the first poll also
#                   sleeps this long before its first check
#   --tries N      how many times to poll (default 10)
#   --failure-re REGEX   this harness's own recorded failure signature
#                   (H_LAUNCH_PROMPT_FAILURE_RE). Empty means no such
#                   signature is known for this harness -- fail closed
#                   (`unknown`) rather than report a success this file has
#                   no evidence for, same posture `verified_submit` already
#                   takes for an unreadable box.
#   --blocked-re REGEX     this harness's H_BLOCKED_MARKERS, checked against
#                   the whole final pane capture. Empty (no marker recorded)
#                   is never checked -- same posture as --failure-re.
#   --option-row-re REGEX  this harness's H_OPTION_ROW_RE, checked the same
#                   way. Either given and matching the LAST poll is a block.
#
# Return 0 (SEND_STATUS=submitted, no failure or still-blocked signature seen),
# 2 (SEND_STATUS=stranded, the failure signature appeared -- the folded
# prompt was not accepted as a turn), 3 (SEND_STATUS=blocked, the harness is
# still sitting on a menu/prompt after the whole budget -- the folded prompt
# may be queued behind it, unconfirmed either way), or 4 (SEND_STATUS=unknown
# -- no failure-re was given to check at all).
verified_launch_prompt() {
  local target="$1"; shift
  local settle=1 tries=10 failure_re="" blocked_re="" option_row_re=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --settle) settle="$2"; shift 2 ;;
      --tries) tries="$2"; shift 2 ;;
      --failure-re) failure_re="$2"; shift 2 ;;
      --blocked-re) blocked_re="$2"; shift 2 ;;
      --option-row-re) option_row_re="$2"; shift 2 ;;
      *) echo "verified_launch_prompt: unknown option $1" >&2; SEND_STATUS=send_failed; return 1 ;;
    esac
  done

  if [ -z "$failure_re" ]; then
    SEND_STATUS=unknown
    return 4
  fi

  local attempt pane
  for ((attempt = 1; attempt <= tries; attempt++)); do
    sleep "$settle"
    pane=$(tmux capture-pane -p -t "$target" 2>/dev/null)
    if grep -qE "$failure_re" <<<"$pane"; then
      SEND_STATUS=stranded
      return 2
    fi
  done

  if { [ -n "$blocked_re" ] && grep -qE "$blocked_re" <<<"$pane"; } \
     || { [ -n "$option_row_re" ] && grep -qE "$option_row_re" <<<"$pane"; }; then
    SEND_STATUS=blocked
    return 3
  fi

  SEND_STATUS=submitted
  return 0
}

# verified_survived <target> [--settle N] [--retries N]
#
# agent-supervisor#456, Gastown's `VerifySurvived` (`internal/session/
# lifecycle.go`, `StartSession` step 12). Everything above this function
# proves the brief was TYPED and the input box went EMPTY -- `verified_submit`
# returning SEND_STATUS=submitted. Neither proves the pane is still there a
# moment later: an input box reads empty the instant a turn STARTS and it
# also reads empty (because there is no box left to read at all) the instant
# the whole process dies and tmux takes the window down with it. #456's own
# property: a ledger row must not read live until the pane has been
# RE-OBSERVED running the agent -- not "tmux accepted the send".
#
# tmux does NOT error on a target that no longer exists. MEASURED live
# (2026-08-21, throwaway isolated socket, a killed pane's window gone from
# `list-windows`): `tmux display-message -p -t <dead-window-id> 'FMT'`
# returns exit 0 and every `#{...}` substitution BLANK -- never a non-zero
# exit code. A caller trusting `$?` here would never see this fail; this
# checks the substituted FIELDS for emptiness instead, the same discipline
# dispatch.sh's own `LANE_META` check already applies one block down.
#
# Deliberately does NOT match `#{pane_current_command}` against the
# harness's own H_COMMAND_RE. A live, healthy turn can shell out to a tool
# (git, ls, a test runner) within the very settle window this sleeps
# through, and misreading a WORKING lane's first tool call as a dead one is
# exactly the "refuses every dispatch under load" flakiness #456's brief
# warned against manufacturing. Existence and `#{pane_dead}` are what
# Gastown's own `HasSession` checks too -- a session/window boolean, not a
# process-identity match -- this mirrors that scope, not a wider one.
#
#   --settle N    seconds to sleep before each check (default 2 here; the
#                 only caller, dispatch.sh, passes its own DISPATCH_SETTLE
#                 instead -- see that call site for the measurement behind
#                 the number)
#   --retries N   total checks (default 2)
#
# Return 0 (SEND_STATUS=survived) as soon as one check finds the pane
# alive. Return 5 (SEND_STATUS=died) if every retry finds it gone.
verified_survived() {
  local target="$1"; shift
  local settle=2 retries=2
  while [ $# -gt 0 ]; do
    case "$1" in
      --settle) settle="$2"; shift 2 ;;
      --retries) retries="$2"; shift 2 ;;
      *) echo "verified_survived: unknown option $1" >&2; SEND_STATUS=send_failed; return 1 ;;
    esac
  done

  local attempt cmd dead
  for ((attempt = 1; attempt <= retries; attempt++)); do
    sleep "$settle"
    IFS='|' read -r cmd dead < <(tmux display-message -p -t "$target" '#{pane_current_command}|#{pane_dead}' 2>/dev/null)
    if [ -n "$cmd" ] && [ "$dead" != "1" ]; then
      SEND_STATUS=survived
      return 0
    fi
  done
  SEND_STATUS=died
  return 5
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
      # agent-estate#446: BOTH stages read the box, so this flag goes to
      # both -- verified_type's own reads (--proof-head, the no-proof
      # fallback) and verified_submit's own poll-for-empty.
      --box-prompt|--box-close) type_args+=("$1" "$2"); submit_args+=("$1" "$2"); shift 2 ;;
      *) echo "verified_send: unknown option $1" >&2; SEND_STATUS=send_failed; return 1 ;;
    esac
  done
  if [ ${#type_args[@]} -gt 0 ]; then
    verified_type "$target" "$message" "${type_args[@]}" || return $?
  else
    verified_type "$target" "$message" || return $?
  fi
  if [ ${#submit_args[@]} -gt 0 ]; then
    verified_submit "$target" "${submit_args[@]}"
  else
    verified_submit "$target"
  fi
}

# verified_dismiss_menu <target> <option_row_re> [menu_tail] [--settle N]
#                        [--retries N]
#
# agent-dotfiles#255's actual root cause, reproduced live: a fresh codex
# process, launched into a directory codex has never been given before
# (every `dispatch.sh` worktree, every time -- `worktree.sh new` never
# reuses a path), opens on its OWN one-time menu:
#
#   › 1. Yes, continue
#     2. No, quit
#
#     Press enter to continue
#
# not on the ordinary chat box `verified_preclear`/`verified_type` expect.
# Measured 2026-08-20/21 against real codex 0.148.0 in a throwaway tmux
# socket, a directory it had never seen: `/clear` (dispatch.sh's own first
# send) typed onto this menu does nothing -- none of its characters are `1`,
# `2` or Enter -- and the Enter that follows accepts the DEFAULT option
# ("1. Yes, continue"), not a clear. The screen then repaints to a genuinely
# empty, ready chat box, so `verified_preclear` reads `empty` and reports
# success -- but that success was luck: the pane's very first Enter went to
# a menu selection, not a submit, and a slower render (a colder process, a
# loaded machine) can easily put `/clear`'s Enter a beat ahead of the menu
# even existing yet, landing it nowhere. #255's original shape -- the whole
# brief consumed as a session TITLE rather than a turn -- is the same first-
# Enter-goes-somewhere-else failure with a different landing spot.
#
# So this is a discrete step BEFORE `verified_preclear`, not a change to it:
# confirm no such menu is showing (or accept it, if it is) using the SAME
# harness-adapter regex `lanes.sh` already keys its own menu-blocked reading
# on -- `H_OPTION_ROW_RE`, sourced from `harness/<name>.sh` via
# `harness-registry.sh`. Generic across harnesses by construction: called
# with an empty or non-matching `option_row_re` (Claude, Copilot, any lane
# whose adapter defines no such menu), this returns success on the very
# first pane read and sends nothing at all.
#
#   option_row_re  a harness's `H_OPTION_ROW_RE`. Empty means "this harness
#                   has no such menu" -- always succeeds without touching
#                   the pane.
#   menu_tail      lines from the end of the capture to search, matching
#                   `lanes.sh`'s own bounded menu read (`H_MENU_TAIL`,
#                   default 6) -- the #65 discipline: search the visible
#                   pane, never full scrollback. Blank lines are stripped
#                   BEFORE the tail is taken, the same as `lanes.sh`'s own
#                   `pane_lines` -- caught live against a real codex pane
#                   sized 100x40: the menu prints near the TOP of the pane,
#                   so a plain `tail -n 6` of the raw capture returns six
#                   trailing BLANK rows below it and never sees the menu at
#                   all. `grep -v` for whitespace-only lines first is what
#                   makes "last N lines" mean "last N lines of content".
#   --settle N     seconds to sleep after accepting before re-checking
#                   (default 2 -- a menu's own repaint, not a text box's)
#   --retries N    total ACCEPT attempts, i.e. Enters sent (default 5 -- a
#                   cold process start plus this menu's own render can be
#                   slower than a text box's ordinary repaint; see
#                   `dispatch.sh`'s `DISPATCH_LAUNCH_SETTLE` note)
#
# Return 0 (SEND_STATUS=landed) whether a menu was ever present: "nothing to
# dismiss" and "dismissed it" are the same success to a caller about to type
# into this pane next. Return 2 (SEND_STATUS=not_landed) only if the menu is
# STILL showing after every retry -- typing a brief onto an unresolved menu
# is exactly the failure this exists to refuse.
verified_dismiss_menu() {
  local target="$1" option_row_re="$2" menu_tail="${3:-6}"; shift 3
  local settle=2 retries=5
  while [ $# -gt 0 ]; do
    case "$1" in
      --settle) settle="$2"; shift 2 ;;
      --retries) retries="$2"; shift 2 ;;
      *) echo "verified_dismiss_menu: unknown option $1" >&2; SEND_STATUS=send_failed; return 1 ;;
    esac
  done

  if [ -z "$option_row_re" ]; then
    SEND_STATUS=landed
    return 0
  fi

  local attempt pane_tail
  for ((attempt = 1; attempt <= retries; attempt++)); do
    pane_tail=$(tmux capture-pane -p -t "$target" 2>/dev/null | grep -v '^[[:space:]]*$' | tail -n "$menu_tail")
    if ! grep -qE "$option_row_re" <<<"$pane_tail"; then
      SEND_STATUS=landed
      return 0
    fi
    tmux send-keys -t "$target" Enter 2>/dev/null
    sleep "$settle"
  done

  pane_tail=$(tmux capture-pane -p -t "$target" 2>/dev/null | grep -v '^[[:space:]]*$' | tail -n "$menu_tail")
  if ! grep -qE "$option_row_re" <<<"$pane_tail"; then
    SEND_STATUS=landed
    return 0
  fi
  SEND_STATUS=not_landed
  return 2
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
