#!/bin/bash
# The harness adapter registry: load every harness/*.sh into parallel arrays
# and answer "which adapter, if any, owns this pane?".
#
# SOURCE this file (`. harness-registry.sh`); it defines variables and
# functions and runs nothing else. It has no shebang-worthy behaviour of its
# own -- the shebang above is for shellcheck and for the executable-bit
# convention this directory's scripts share.
#
# agent-dotfiles#201 gave every harness its own file under harness/ and made
# `lanes.sh` ask the adapter instead of naming a harness itself. This file is
# #215 taking the second step: the LOADER moved out of lanes.sh so a second
# consumer -- watchdog.sh, whose busy probe was still one Claude Code literal
# with no fail-closed branch -- can ask the same adapters rather than growing
# a second, divergent copy of them. Nothing about the contract changed in the
# move; the block below is lanes.sh's, verbatim apart from the variable names
# for this file's own directory.
#
# Sets, all parallel and keyed by POSITION:
#   HARNESS_IDS  H_COMMAND_RE  H_READY_RE  H_BUSY_RE  H_BUSY_TAIL
#   H_BLOCKED_MARKERS  H_OPTION_ROW_RE  H_MENU_ENTER_RE  H_MENU_TAIL
#   H_TEXT_PROMPT_RE
# and defines harness_index_for_command / harness_index_for_name.
#
# Parallel INDEXED arrays, not `declare -A`. Every caller in this directory
# invokes its script by its own shebang, which runs it under `/bin/bash` --
# macOS's own, stuck at 3.2.57 (no associative arrays) even when a newer
# `bash` sits on PATH. Checked live, not assumed: `/bin/bash -c 'declare -A
# x'` on this machine fails with "invalid option". Indexed arrays and
# `${!arr[@]}` both predate bash 3.2, so this loader matches the feature set
# its callers already rely on rather than introducing the one construct that
# would make every real invocation fail closed at line 1.

HARNESS_REGISTRY_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HARNESS_IDS=(); H_COMMAND_RE=(); H_READY_RE=(); H_BUSY_RE=(); H_BUSY_TAIL=()
H_BLOCKED_MARKERS=(); H_OPTION_ROW_RE=(); H_MENU_ENTER_RE=(); H_MENU_TAIL=(); H_TEXT_PROMPT_RE=()
# LANES_HARNESS_DIR is the name `lanes.sh` has always used and its tests still
# set (they point it at a MUTATED copy of the adapters to prove one adapter's
# breakage cannot move another harness's lane). Kept as an alias so that
# mutation coverage keeps working through this move.
HARNESS_DIR="${HARNESS_REGISTRY_DIR:-${LANES_HARNESS_DIR:-$HARNESS_REGISTRY_HERE/harness}}"
for _hf in "$HARNESS_DIR"/*.sh; do
  [ -e "$_hf" ] || continue
  unset HARNESS_NAME HARNESS_COMMAND_RE HARNESS_READY_RE HARNESS_BUSY_RE HARNESS_BUSY_TAIL \
        HARNESS_BLOCKED_MARKERS HARNESS_OPTION_ROW_RE HARNESS_MENU_ENTER_RE HARNESS_MENU_TAIL \
        HARNESS_TEXT_PROMPT_RE HARNESS_LAUNCH_CMD HARNESS_SEND_LITERAL
  # shellcheck disable=SC1090
  . "$_hf"
  : "${HARNESS_NAME:?$_hf did not set HARNESS_NAME}"
  : "${HARNESS_COMMAND_RE:?$_hf ($HARNESS_NAME) did not set HARNESS_COMMAND_RE}"
  : "${HARNESS_READY_RE:?$_hf ($HARNESS_NAME) did not set HARNESS_READY_RE}"
  HARNESS_IDS+=("$HARNESS_NAME")
  H_COMMAND_RE+=("$HARNESS_COMMAND_RE")
  H_READY_RE+=("$HARNESS_READY_RE")
  H_BUSY_RE+=("${HARNESS_BUSY_RE:-}")
  H_BUSY_TAIL+=("${HARNESS_BUSY_TAIL:-1}")
  H_BLOCKED_MARKERS+=("${HARNESS_BLOCKED_MARKERS:-}")
  H_OPTION_ROW_RE+=("${HARNESS_OPTION_ROW_RE:-}")
  H_MENU_ENTER_RE+=("${HARNESS_MENU_ENTER_RE:-}")
  H_MENU_TAIL+=("${HARNESS_MENU_TAIL:-6}")
  H_TEXT_PROMPT_RE+=("${HARNESS_TEXT_PROMPT_RE:-}")
done
unset _hf

# Which adapter, if any, claims a pane's own command -- prints that
# adapter's ARRAY INDEX (the arrays above are parallel, keyed by position,
# not by name). Empty output + a non-zero return means no adapter
# recognises it -- lanes.sh's own #126 posture (a whitelist, not a
# blacklist) applied one level up: an unrecognised COMMAND is `unknown`,
# same as an unrecognised STATUS LINE.
#
# The count guard is not decoration: an EMPTY adapter directory (a partial
# deploy, a bad HARNESS_DIR) leaves every array empty, and `${!arr[@]}` on an
# empty array under `set -u` is an unbound-variable error in bash 3.2 -- which
# would abort the caller rather than return "nothing recognises this". The
# whole point of this function is that not-recognised is an ANSWER.
harness_index_for_command() {
  local cmd="$1" i
  [ "${#HARNESS_IDS[@]}" -gt 0 ] || return 1
  for i in "${!HARNESS_IDS[@]}"; do
    if [[ "$cmd" =~ ${H_COMMAND_RE[$i]} ]]; then printf '%s\n' "$i"; return 0; fi
  done
  return 1
}

# The same lookup by adapter NAME, for a caller that has been told which
# harness it is talking to rather than having to infer it from a process name
# (watchdog.sh's SUPERVISOR_HARNESS). Worth having as its own entry point:
# process-name inference is exactly the seam agent-dotfiles#216 is open on --
# `pane_current_command` is `node` for every Node-based harness -- so a caller
# that can be told should not have to guess.
harness_index_for_name() {
  local want="$1" i
  [ "${#HARNESS_IDS[@]}" -gt 0 ] || return 1
  for i in "${!HARNESS_IDS[@]}"; do
    if [ "${HARNESS_IDS[$i]}" = "$want" ]; then printf '%s\n' "$i"; return 0; fi
  done
  return 1
}
