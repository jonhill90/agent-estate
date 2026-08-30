#!/bin/bash
# Sourced-only, from dispatch.sh, right after dispatch-rehome.sh. Part of the
# agent-supervisor#716 split (see dispatch-rehome.sh's own header for the
# shape and precedent). Never run standalone.
#
# Argument handling: the `--force`/`--pr`/`--reviews-pr`/`--live-pane`/
# `--not-a-review`/`--adopt-pane` flag loop, the positional
# <issue>/<slug>/<brief>/[repo]/[repo-path] arguments, the issue list, and the
# brief-file existence check -- everything dispatch.sh needs resolved before
# any gate, any claim, or any lane is even considered.
#
# `$HERE/dispatch.sh` (not `${BASH_SOURCE[0]}`) is used everywhere this file
# prints its usage block: `${BASH_SOURCE[0]}` inside a sourced file resolves
# to THIS file, which carries no `# Usage:` comment of its own -- the
# `$0`/`BASH_SOURCE` trap #716 was watched for. `dispatch.sh` is always the
# file holding that comment, and $HERE already names its directory.
# `--reviews-pr <PR>` is pulled out wherever it appears rather than bound to a
# fixed position: every other argument here is positional and some of them
# (repo, repo-path) are already optional, so a new optional flag is scanned
# out first and the remaining args keep their existing $1..$5 meaning
# untouched. This is deliberately NOT `DISPATCH_LANE` reborn under a new name
# -- see the lane-selection loop below -- it names which PR is under review,
# it never names which lane to use.
REVIEWS_PR=""
# agent-supervisor#650: set ONLY when `--reviews-pr` is passed explicitly on
# the command line, at the same point REVIEWS_PR itself is set below --
# never touched by the agent-supervisor#70 inference block further down.
# This is what step 3.2's collision downgrade keys on, not REVIEWS_PR: an
# operator passing the flag is asserting "this is a review" and accepting
# that a real overlap will not be re-checked; a title merely matching the
# inference pattern is not the same assertion (see that downgrade's own
# comment for the reproduced bypass this exists to close).
REVIEWS_PR_EXPLICIT=""
NOT_A_REVIEW=""
PR=""
# DISPATCH_LIVE_PANE=1 is `--live-pane` for every call this process makes,
# without a flag at every call site -- the same "test override, env var,
# same posture QUOTA_GATE/DISPATCH_SETTLE/DISPATCH_RESPAWN_SETTLE already
# take elsewhere in this file" shape, not a second, competing mechanism.
# EXISTS BECAUSE OF WHAT #171 MEASURED BUILDING THIS: pointing this file's
# own pre-existing test suites (test_dispatch.sh and siblings -- none of
# them stub a `claude` binary, because before #171 nothing in dispatch.sh's
# own flow ever ran one) at the new default invoked whatever real `claude`
# happened to be on this machine's PATH -- a live, billed subprocess call
# from inside a test, which is never acceptable. Those suites now export
# this for every case (see their own `run()`), so their hundreds of
# assertions keep testing the tmux flow they were written for; nothing
# about their coverage of #241/#212/#159/etc. changed. A real caller with
# no reason to force the old pane sets nothing and gets the new default.
LIVE_PANE="${DISPATCH_LIVE_PANE:-}"
# agent-supervisor#291: the pre-dispatch collision check's escape hatch, for
# a known and intended file overlap with an in-flight lane -- see step 3.2,
# below the worktree, and collision-check.sh's own header for what "overlap"
# means and why refusing is the default. Takes no value, same shape as
# --not-a-review.
COLLISION_FORCE=""
# agent-supervisor#668: the window id of an already-running, idle pane this
# dispatch should ADOPT instead of spawning a new harness process for -- see
# this flag's own usage comment above. Empty (the default) leaves every
# existing dispatch path -- claude-print, the tmux flow's own respawn -- byte
# for byte unchanged; this variable is read only inside the blocks that
# explicitly branch on it.
ADOPT_PANE=""
POSITIONAL=()
while [ $# -gt 0 ]; do
  case "$1" in
    --force)
      COLLISION_FORCE=1
      shift
      ;;
    --pr)
      # agent-supervisor#159. Same dangling-flag hazard and same fix as
      # `--reviews-pr` just below -- see that case's own comment.
      if [ $# -lt 2 ]; then
        echo "dispatch: --pr requires a PR number" >&2
        sed -n '/^# Usage:/,/^$/p' "$HERE/dispatch.sh" | sed 's/^# \{0,1\}//' >&2
        exit 1
      fi
      PR="$2"
      shift 2
      ;;
    --reviews-pr)
      # A `--reviews-pr` with no value after it (flag last, value forgotten)
      # must refuse rather than hang: `shift 2` with only 1 arg left ($#=1)
      # fails under `set -uo pipefail` (no `set -e` here), and a `case` loop
      # that never shifts on a failed shift spins at ~100% CPU forever --
      # indistinguishable from outside from a lane still working. Refusing
      # loudly here is also why this is a dedicated check rather than
      # `shift 2 || shift`: silently falling back to `shift 1` would make the
      # flag consume the next positional argument (e.g. the brief path) as
      # its value instead, which is its own defect, not a fix for this one.
      if [ $# -lt 2 ]; then
        echo "dispatch: --reviews-pr requires a PR number" >&2
        sed -n '/^# Usage:/,/^$/p' "$HERE/dispatch.sh" | sed 's/^# \{0,1\}//' >&2
        exit 1
      fi
      REVIEWS_PR="$2"
      REVIEWS_PR_EXPLICIT=1
      shift 2
      ;;
    --live-pane)
      # agent-supervisor#171. The opt-OUT: keep this dispatch on the
      # candidate lane's own tmux pane (the pre-#171 behaviour, unchanged)
      # instead of the new default below, which routes a fresh `claude`
      # dispatch over `claude-print` and leaves the candidate pane untouched.
      # Takes no value, same shape as --not-a-review.
      LIVE_PANE=1
      shift
      ;;
    --not-a-review)
      # agent-supervisor#101. Takes no value, so it has none of the dangling-
      # flag hazard above.
      NOT_A_REVIEW=1
      shift
      ;;
    --adopt-pane)
      # agent-supervisor#668. Same dangling-flag hazard and same fix as
      # --pr/--reviews-pr above -- see either's own comment.
      if [ $# -lt 2 ]; then
        echo "dispatch: --adopt-pane requires a window id" >&2
        sed -n '/^# Usage:/,/^$/p' "$HERE/dispatch.sh" | sed 's/^# \{0,1\}//' >&2
        exit 1
      fi
      ADOPT_PANE="$2"
      shift 2
      ;;
    *)
      POSITIONAL+=("$1")
      shift
      ;;
  esac
done
# bash 3.2 (macOS's real /bin/bash, no associative arrays -- see #213's
# `declare -A` removal in this same file) treats "${arr[@]}" on an EMPTY
# array as an unbound-variable error under `set -u`, even though modern bash
# treats it as zero words. POSITIONAL is empty on dispatch.sh's own
# zero-argument path (the usage-error branch just below), which every
# invocation with a typo hits, so the 3.2-safe idiom is required here, not
# optional: "${arr[@]+"${arr[@]}"}" expands to nothing when the array is
# empty (the `+` alternate-value test never triggers) and to the array's
# words otherwise.
set -- "${POSITIONAL[@]+"${POSITIONAL[@]}"}"

# agent-supervisor#101: the two flags are contradictory statements about the
# same dispatch -- "this reviews PR N" and "this is not a review". Neither is
# an inference this script may resolve in the caller's favour: honouring
# `--reviews-pr` would ignore an explicit "no", honouring `--not-a-review`
# would disarm the guard on an explicit "yes". Refuse before anything is
# claimed, which is the cheapest failure available and leaves the estate
# untouched.
if [ -n "$REVIEWS_PR" ] && [ -n "$NOT_A_REVIEW" ]; then
  echo "dispatch: --reviews-pr $REVIEWS_PR and --not-a-review contradict each other -- refusing rather than picking one" >&2
  echo "dispatch: pass --reviews-pr <PR> for a review, or --not-a-review for anything else, never both" >&2
  exit 2
fi

ISSUE_ARG="${1:-}"
SLUG="${2:-}"
BRIEF="${3:-}"
REPO="${4:-}"

if [ -z "$ISSUE_ARG" ] || [ -z "$SLUG" ] || [ -z "$BRIEF" ]; then
  sed -n '/^# Usage:/,/^$/p' "$HERE/dispatch.sh" | sed 's/^# \{0,1\}//' >&2
  exit 2
fi

# agent-supervisor#17 (secondary finding): [repo-path] defaults to $PWD, and
# [repo] given with [repo-path] omitted reads naturally as "target that repo"
# -- it is almost never an intent to build the worktree from whatever
# directory dispatch.sh happens to be run from. `${5+x}` (not `${5:-}`) is
# what distinguishes "never passed" from "passed as empty string": the
# former is the trap, the latter is an explicit (if odd) choice to use $PWD
# and is left alone. Silence was what made this a trap; it is refused now
# unless the caller opts in explicitly.
if [ -n "${5+x}" ]; then
  REPO_PATH="$5"
elif [ -n "$REPO" ] && [ -z "${DISPATCH_ALLOW_CWD_REPO_PATH:-}" ]; then
  echo "dispatch: [repo] '$REPO' was given but [repo-path] was not -- refusing to default to the working directory ($PWD)" >&2
  echo "dispatch: this is the trap agent-supervisor#17 is about: a cross-repo dispatch silently building its worktree from the wrong checkout" >&2
  echo "dispatch: pass [repo-path] explicitly, or set DISPATCH_ALLOW_CWD_REPO_PATH=1 to use \$PWD on purpose" >&2
  exit 2
else
  REPO_PATH="$PWD"
fi

# One brief, possibly several issues (agent-dotfiles#112): #109 and #110 came
# from the same review of the same PR and were dispatched to one lane, but
# dispatch.sh only ever claimed the issue it was given -- the rest sat open
# and looked free to the next dispatcher while a lane was actively on them.
# ISSUE (singular, first of the list) is what names the lane branch and the
# tmux window; a list changing the window name mid-estate would break
# `lanes.sh` and `claim.sh stale`, which both match on it.
IFS=',' read -r -a ISSUES <<< "$ISSUE_ARG"
ISSUE="${ISSUES[0]:-}"

# Checked before anything is claimed or created: a typo in the brief path is
# the cheapest failure available, and it must stay that way.
[ -f "$BRIEF" ] || { echo "dispatch: no brief file at $BRIEF" >&2; exit 1; }
BRIEF="$(cd "$(dirname "$BRIEF")" && pwd)/$(basename "$BRIEF")"
