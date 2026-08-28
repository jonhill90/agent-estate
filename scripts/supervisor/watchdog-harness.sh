#!/bin/bash
# Harness detection for watchdog.sh (agent-supervisor#704 split): is the pane
# busy, and which harness/*.sh adapter owns it? Sourced only -- not meant to
# be run standalone. See watchdog.sh's own header for the split's shape and
# tests/supervisor/test_watchdog.sh for the behaviour this is verified
# against.
#
# Depends on $PANE (set by watchdog.sh before this is sourced) and reads
# $HERE to locate harness-registry.sh; defines HARNESS_REGISTRY_OK,
# harness_slot, pane_busy_state, harness_note.

# --- #215: which harness is in the pane, and is it busy? --------------------
#
# This script used to answer "is it busy?" with one Claude Code literal --
# `grep -q 'esc to interrupt'` -- in two places, with no harness parameter and
# no third outcome. On a pane that does not paint that exact string the check
# fell through to NOT BUSY, so a mid-turn lane read as a dead one and this
# script's whole purpose inverted: the watchdog acts on idle, so a false idle
# is an intervention against a working lane. Copilot's own busy footer is
# `◎ Working esc interrupt` -- `esc interrupt`, no "to" -- which is exactly the
# near miss a shared literal does not survive. Latent today (the pane watched
# by default is the Claude supervisor loop), live the moment the supervisor
# itself runs on another harness, which is the stated goal.
#
# WHY NOT adapter.py's `classify_capture`, which #215 points at. Because the
# test for an adapter is "is the implementation fit to be called?", not "is
# there a caller?" (agent-dotfiles#223). `classify_capture` matches quoted
# phrases anywhere in a 25-line window -- the wide-window anti-pattern #65
# fixed lanes.sh to avoid, where a lane merely QUOTING "Should I proceed?"
# reads back as blocked -- its own header says not to wire a new caller
# through it before it is rebuilt to lanes.sh's standard, and
# docs/supervisor-disposition.md §5 lists that rebuild as outstanding work.
# It also models only claude and codex; a copilot pane would make it RAISE,
# and a raising classifier inside a bash `if` is a non-zero exit, which is
# "not busy" again. Wiring it in here would have imported the defect.
#
# So: the same harness/*.sh adapters lanes.sh uses, via harness-registry.sh --
# already the estate's fit, tested, per-harness seam -- and a probe with THREE
# outcomes rather than two. `unknown` is the important one: an unrecognised
# harness, an unreadable capture, a harness whose adapter declines to model a
# busy shape, or no adapters at all all resolve to "cannot tell", and every
# caller below treats cannot-tell as busy. A false busy costs one skipped
# tick; a false idle costs a working lane its turn.
HARNESS_REGISTRY_OK=0
if [ -r "$HERE/harness-registry.sh" ]; then
  # shellcheck source=./harness-registry.sh
  . "$HERE/harness-registry.sh" && HARNESS_REGISTRY_OK=1
fi

# Which adapter owns $PANE. SUPERVISOR_HARNESS names it outright; otherwise it
# is inferred from the pane's own command, the same signal lanes.sh keys on.
# That inference is imperfect and the imperfection is filed: `pane_current_
# command` is `node` for EVERY Node-based harness (agent-dotfiles#216), so a
# second Node CLI in the supervisor pane would be handed copilot's chrome.
# It fails toward `unknown` rather than toward a false idle -- another
# harness's markers do not match this one's paint -- so #216 is a precision
# bug here, not a safety one, and is left to #216 rather than fixed in passing.
harness_slot() {
  local cmd
  [ "$HARNESS_REGISTRY_OK" = 1 ] || return 1
  if [ -n "${SUPERVISOR_HARNESS:-}" ]; then
    harness_index_for_name "$SUPERVISOR_HARNESS"
    return $?
  fi
  cmd=$(tmux display-message -p -t "$PANE" '#{pane_current_command}' 2>/dev/null) || return 1
  [ -n "$cmd" ] || return 1
  harness_index_for_command "$cmd"
}

# pane_busy_state <capture> -> prints busy | idle | unknown
#
# Reads only the harness's OWN bounded window (HARNESS_BUSY_TAIL non-empty
# lines: 1 for Claude and Copilot, whose busy marker IS the last line; 4 for
# Codex, whose marker sits above a footer that never changes) rather than
# sweeping the whole capture. That is the #65 discipline: the old probe
# grepped six scrollback lines plus the visible pane, so a pane that had
# merely PRINTED the phrase -- reviewing this very file -- read busy.
pane_busy_state() {
  local capture="$1" hidx lines busy_tail
  hidx=$(harness_slot) || { printf 'unknown\n'; return 0; }
  # An adapter that does not model a busy shape cannot answer this question.
  # Silence is not evidence of idleness -- see harness/copilot.sh's unset
  # blocked markers for the same posture applied to a different probe.
  [ -n "${H_BUSY_RE[$hidx]}" ] || { printf 'unknown\n'; return 0; }
  lines=$(grep -v '^[[:space:]]*$' <<<"$capture")
  # A capture that succeeded and returned nothing: a repainting pane, a pane
  # whose harness has printed nothing yet. The old probe scored this as "the
  # string is absent", i.e. idle, which is the fail-open direction.
  [ -n "$lines" ] || { printf 'unknown\n'; return 0; }
  busy_tail=$(tail -n "${H_BUSY_TAIL[$hidx]}" <<<"$lines")
  if grep -qE "${H_BUSY_RE[$hidx]}" <<<"$busy_tail"; then printf 'busy\n'; else printf 'idle\n'; fi
}

# Why the probe said what it said, for the status file's detail line -- so a
# `harness_unknown` tick names WHICH cannot-tell it hit rather than leaving a
# human to re-derive it.
harness_note() {
  local hidx
  [ "$HARNESS_REGISTRY_OK" = 1 ] || { printf 'no harness-registry.sh beside this watchdog'; return 0; }
  if ! hidx=$(harness_slot); then
    if [ -n "${SUPERVISOR_HARNESS:-}" ]; then
      printf 'no adapter under harness/ is named %s' "$SUPERVISOR_HARNESS"
    else
      printf 'no adapter under harness/ claims pane command %s' \
        "$(tmux display-message -p -t "$PANE" '#{pane_current_command}' 2>/dev/null || echo '<unreadable>')"
    fi
    return 0
  fi
  if [ -z "${H_BUSY_RE[$hidx]}" ]; then
    printf 'harness %s has no busy shape modelled' "${HARNESS_IDS[$hidx]}"
  else
    printf 'harness %s captured nothing to read' "${HARNESS_IDS[$hidx]}"
  fi
}
