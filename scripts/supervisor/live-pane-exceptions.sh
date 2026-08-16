#!/bin/bash
# The named exceptions to the send-keys retirement (agent-supervisor#284).
#
# WHY THIS FILE EXISTS: #284's brief asks that any lane kept on a live tmux
# pane on purpose be named "in code, not prose" -- not left as an
# unenumerated feeling that some lane "probably still needs" send-keys.
# dispatch.sh's own #171 comment already names the three roles that
# genuinely require a live, watchable pane: a lane that must be
# INTERRUPTED mid-turn, a lane that must answer an INTERACTIVE PROMPT (a
# usage-limit dialog, a permission request, a menu), or a lane WATCHED AND
# RESUMED BY A HUMAN directly. This file is where a lane earns a permanent
# place in that set -- one line, one lane, one reason.
#
# Measured at #284 close (2026-08-16): ZERO lanes in the live estate
# qualify under any of the three roles above. Every "send-keys" lane found
# was ordinary dispatch-and-collect work or a `--reviews-pr` review that
# fell through to tmux because dispatch.sh's #274 default does not yet
# cover the review path (a documented dispatch.sh scope boundary, not a
# per-lane exception -- see dispatch.sh's own #171 comment block). That
# boundary is #274/#171's to close, not this file's to paper over by
# listing every review lane here "just in case."
#
# LIST_LANE_EXCEPTIONS -- each entry is "<session>:<window-name>|<reason>".
# Empty on purpose: retire on sight, no lane is exempt right now. Add a
# line here ONLY when a lane's reason is one of the three roles above, and
# say which one.
LANE_EXCEPTIONS=(
  # "session:window-name|reason"   <- format, no entries yet
)

# is_exception_lane <session> <window-name>
# Prints the reason and returns 0 if the lane is a named exception;
# returns 1 (silent) otherwise so a retirement sweep can treat "not found"
# as "safe to retire" without a separate lookup table to keep in sync.
is_exception_lane() {
  local target="$1:$2"
  local entry
  for entry in "${LANE_EXCEPTIONS[@]}"; do
    local key="${entry%%|*}"
    if [ "$key" = "$target" ]; then
      echo "${entry#*|}"
      return 0
    fi
  done
  return 1
}
