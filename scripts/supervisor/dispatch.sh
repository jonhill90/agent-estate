#!/bin/bash
# Dispatch one issue to one lane: pick a free lane, claim the issue, CREATE
# THE LANE'S WORKTREE, then send the brief. One command, or nothing happens.
#
# WHY: agent-dotfiles#81. `worktree.sh` was built for #73 and nothing called
# it -- `grep -rn worktree.sh` found three code fences in loop-tick.md and a
# section of the supervisor README, and that was all. The tool fails closed
# when it is called; what was missing was anything that calls it. Enforcement
# was "the dispatcher reads the file and runs the command", which is the same
# mechanism whose failure produced #73: a lane had its branch switched out
# from under it in the shared checkout and lost four files of uncommitted
# work. The risk moved from the lanes to the dispatcher; it did not go away.
#
# The estate has now hit this shape three times: acp_transport.py (302 lines,
# tested, zero importers, #56), claim.sh (wired into the dispatch step by #74,
# the one that got it right), and worktree.sh (#81). A tool that fails closed
# when called, and that nothing calls, is a documentation rule with a binary
# attached. So the sequence a dispatcher used to perform by hand -- read
# lanes.sh, run claim.sh, run worktree.sh, rename the window, send-keys --
# lives here, where the worktree step cannot be the one that gets skipped.
#
# REFINEMENT (agent-dotfiles#222): the rule above has an opposite failure
# mode, not just the nothing-calls-it one. An abstraction can be present and
# CORRECTLY avoided. When callers route around a seam because its
# implementation is worse than the ad-hoc code it would replace, that is
# indistinguishable from outside from the nothing-calls-it defect above --
# and is its opposite. Wiring the caller in "fixes" it by importing the
# defect. The test for an adapter is "is the implementation fit to be
# called?", not "is there a caller?". When the answer is no, the avoidance
# must be recorded at the seam, not only in whichever caller dodged it. Live
# instance: adapter.classify_capture's header comment, avoided by this very
# script's dispatch path -- see that comment for the mechanics.
#
# EVERY FAILURE ABORTS THE DISPATCH. In particular a failed `worktree.sh new`
# is fatal: a lane with no worktree works in the shared checkout, and that is
# the original bug, not a degraded mode of operation. Whatever was already
# done -- the claim, the worktree -- is undone before exiting, so a failed
# dispatch leaves the estate exactly as it found it and the issue stays
# available to the next tick.
#
# Usage:
#   dispatch.sh <issue>[,<issue>...] <slug> <brief-file> [repo] [repo-path] [--reviews-pr <PR>] [--not-a-review] [--pr <PR>]
#
# <issue>      one issue number, or a comma-separated list (agent-dotfiles#112)
#              when one brief covers several -- e.g. `110,109`. Every issue in
#              the list is claimed; the lane still gets ONE worktree and ONE
#              brief, because it is doing one piece of work that happens to
#              close more than one issue.
# <slug>       short reason, e.g. `dispatch-worktree`; with <issue> it names
#              both the lane branch and the tmux window.
# <brief-file> the worker's complete brief. Sent by path, not pasted: a brief
#              large enough to be worth writing is too large for send-keys.
# [repo]       OWNER/NAME for the claim; omitted, gh resolves it from [repo-path].
# [repo-path]  the shared checkout to branch the worktree from; default $PWD.
#              [repo] given with [repo-path] omitted is refused (#17): almost
#              always the mistake is believing [repo] alone also selects the
#              checkout. Set DISPATCH_ALLOW_CWD_REPO_PATH=1 to use $PWD anyway.
#              After the worktree is built, its `origin` is compared against
#              [repo] and the dispatch is refused on mismatch (#17).
# --reviews-pr <PR>
#              this dispatch is a review of PR <PR>. dispatch.sh (#212, widened
#              by #190) then refuses any candidate lane that CONTRIBUTED to
#              that PR -- its authoring dispatch, and any later dispatch
#              (e.g. a fix pass) recorded against the same issue or the same
#              worktree -- fails closed if that contributor set cannot be
#              determined at all, and proceeds unchanged if the flag is
#              omitted -- see step 0.5.
#              agent-supervisor#70: when omitted, dispatch.sh tries to infer
#              it from the issue's title, then the brief -- a line naming
#              both "review" and "PR #<N>" -- so a forgotten flag is not
#              automatically a silent self-review. Passing the flag
#              explicitly always wins and is never second-guessed.
# --not-a-review
#              this dispatch is NOT a review, whatever its brief says.
#              agent-supervisor#101: the inference above reads prose, and
#              prose about a PR is not the same as a review OF that PR --
#              "rebase it so it can be reviewed" plus "PR #93" was enough to
#              infer a review and refuse a rebase on authorship grounds. This
#              is the escape, taken at the DISPATCH rather than by rewording
#              the brief: it turns the inference off for this one invocation
#              and changes nothing else. The guard itself is untouched --
#              `--reviews-pr` still guards, prose still infers when neither
#              flag is given. Passing both flags is refused rather than
#              resolved: the two say opposite things about the same dispatch
#              and guessing which one is meant is exactly the guessing this
#              guard exists to avoid.
# --pr <PR>
#              agent-supervisor#159: this dispatch's real scope is PR <PR>,
#              not the issue(s) named by <issue> -- a review of PR <PR>, a
#              fix pass on it after REQUEST CHANGES, or any other follow-up
#              work on it. Two things follow: step 2 does NOT call
#              `claim.sh take` on <issue> at all (the issue stays claimed by
#              whatever opened <PR> -- that is the whole point, see #159's
#              own "why it matters"), and this dispatch is recorded in the
#              ledger keyed by the PR (`source_kind='pull'`), not the issue,
#              so a second dispatcher can see the PR is already spoken for
#              (`cli.py pr-lane`) instead of minting a second task for it.
#              `<issue>` is still required and still names the worktree and
#              tmux window -- it is where this dispatch's WORK happens to
#              live, not what it claims.
#              `--reviews-pr <PR>` IMPLIES `--pr <PR>` (a review is one kind
#              of PR-scoped dispatch, the one that also runs the author
#              guard above) -- passing both is fine as long as they name the
#              SAME PR; naming two different PRs is refused, the same
#              "neither is an inference this script may resolve" posture
#              `--reviews-pr`/`--not-a-review` already takes.
# --live-pane
#              (DISPATCH_LIVE_PANE=1 in the environment is the same thing
#              for every call this process makes, not just one -- see that
#              variable's own comment where LIVE_PANE is initialized.)
#              agent-supervisor#171: keep this dispatch on tmux/`send-keys`,
#              the pane the candidate lane already is, even when the winning
#              candidate's harness is `claude`. Default (this flag omitted):
#              when step 1 below picks a FREE `claude` lane and this is a
#              plain, single-issue, non-PR-scoped dispatch, dispatch.sh
#              releases that candidate untouched and instead mints a brand
#              NEW `claude-print` lane for #<issue> (same mechanism
#              `dispatch-claude-print.sh` proved out for #171/#215) -- no
#              tmux pane, no send-keys, nothing left in an input box to
#              strand. `--live-pane` is the explicit opt-out for the roles
#              that genuinely need the candidate's persistent, watchable
#              pane: one that must be INTERRUPTED mid-turn, one that must
#              answer an INTERACTIVE PROMPT (a usage-limit dialog, a
#              permission request, a menu), or one WATCHED AND RESUMED BY A
#              HUMAN directly (`cli.py`'s own `adapter_for_harness` comment).
#              Measured against this script's own job (#255): an ordinary
#              `dispatch.sh <issue>` call is never any of the three -- it
#              claims an issue, hands a lane a brief, and collects a PR, the
#              same "dispatch-and-collect" shape as a review or a fix pass --
#              so there is no lane PROPERTY this script can read back out of
#              a pane to infer the three roles above; only the human
#              dispatching knows a given call is one of them, which is why
#              this is a caller decision, not something lanes.sh classifies.
#              A review (`--reviews-pr`), a PR-scoped follow-up (`--pr`) or a
#              multi-issue dispatch (`<issue>,<issue>...`) is left on the
#              pre-#171 tmux flow regardless of this flag for now --
#              `dispatch-claude-print.sh` does not yet speak any of those
#              three shapes, and silently dropping one would be worse than
#              routing it the old way; see agent-supervisor#171's own
#              tracked follow-up.
# --force
#              agent-supervisor#291: dispatch anyway when the pre-dispatch
#              collision check (step 3.2) finds #<issue>'s files overlap an
#              already in-flight lane's -- for a known and intended overlap.
#              Never silences an UNKNOWN verdict (there is nothing to force
#              past there; unknown already allows) and never suppresses the
#              log line naming what was overridden. See collision-check.sh's
#              own header for what "overlap" means.
#
# Exit 0 only when a lane has been sent a brief -- over tmux/send-keys, or
# (new, #171, default for a plain single-issue `claude` dispatch) over a
# freshly minted `claude-print` lane. Exit 1 on any refusal -- no free lane,
# an issue someone else already claimed, a worktree that could not be
# created, a send that failed, a `claude-print` register/assign that could
# not reach `claude` (this NEVER falls back to send-keys -- see
# `dispatch-claude-print.sh`'s own header), or a review whose only free lane
# wrote the PR under review.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./input-box.sh
. "$HERE/input-box.sh"
# shellcheck source=./send.sh
# agent-supervisor#178: the type-verify-retype-then-submit loop below used to
# live here, inline. It is the ORIGINAL of the shared primitive now in
# send.sh -- extracted, not reimplemented, so read that file for why each
# step exists; this file only supplies its own proof tokens and the ledger
# commit that has to happen between "landed" and "submitted".
. "$HERE/send.sh"
# shellcheck source=./harness-registry.sh
. "$HERE/harness-registry.sh"
# shellcheck source=./session-defaults.sh
. "$HERE/session-defaults.sh"
# agent-supervisor#166/#421: every site that puts a harness's launch command
# on a pane's PATH needs the same tmux guard bootstrap-session.sh/restore.sh
# already install ahead of it -- `dispatch.sh` respawns a lane's process far
# more often than either of those run (every ordinary dispatch/re-home), so
# leaving it unwired here left most of a lane's working lifetime unguarded.
# Best effort, same as the other two call sites: an install failure degrades
# to launching without the guard rather than refusing to dispatch at all.
# shellcheck source=./tmux-guard.sh
. "$HERE/tmux-guard.sh"
# agent-supervisor#111: SESSION is resolved from the target repo, not a
# global default -- see the assignment below NAME_PART, once REPO and
# REPO_PATH are both known. Nothing above that point touches tmux, so this
# placeholder only exists to document that SESSION is not usable yet.

dispatch_rehome_lane() {
  local target="$1" dir="$2" harness="${3:-}" hidx="" cmd="" launch_cmd=""
  [ -n "$target" ] || { echo "dispatch: --rehome-lane requires a tmux target" >&2; return 2; }
  [ -n "$dir" ] || dir="${HOME:-/tmp}"
  [ -d "$dir" ] || { echo "dispatch: re-home target directory does not exist: $dir" >&2; return 1; }

  if [ -n "$harness" ]; then
    hidx=$(harness_index_for_name "$harness") || hidx=""
  else
    cmd=$(tmux display-message -p -t "$target" '#{pane_current_command}' 2>/dev/null) || cmd=""
    [ -n "$cmd" ] && hidx=$(harness_index_for_command "$cmd") || hidx=""
  fi
  if [ -z "$hidx" ] || [ -z "${H_LAUNCH_CMD[$hidx]:-}" ]; then
    echo "dispatch: cannot re-home $target -- no launch command for harness '${harness:-${cmd:-unknown}}'" >&2
    return 1
  fi

  launch_cmd="${H_LAUNCH_CMD[$hidx]}"
  # agent-supervisor#236: the harness is the pane's PROCESS, handed to
  # respawn-pane as its own argv -- never typed into whatever the respawn
  # produces. The prior shape (respawn, sleep, then blind `send-keys
  # "$launch_cmd" Enter`) is the mechanism #236 reports: a lane was found
  # blocked on a menu offering to run a pasted launch command, because
  # nothing checked what was listening for those keystrokes a second later.
  # One tmux call now does what three did, so there is no window in which
  # the launch command exists as text and nothing is left to settle for --
  # H_SEND_LITERAL governed how `send-keys` parsed ITS OWN argument for tmux
  # key names, which does not apply to a shell command handed to
  # respawn-pane's own argv.
  #
  # agent-supervisor#166/#421: re-homing a lane respawns its process the same
  # way bootstrap-session.sh/restore.sh do, so it gets the same tmux guard on
  # PATH ahead of the real binary -- best effort, same as those two sites.
  local guard_bin=""
  guard_bin="$(install_tmux_guard 2>&2)" || guard_bin=""
  if [ -n "$guard_bin" ]; then
    launch_cmd="PATH=\"$guard_bin:\$PATH\" $launch_cmd"
  fi
  if ! tmux respawn-pane -k -t "$target" -c "$dir" "$launch_cmd" 2>/dev/null; then
    echo "dispatch: tmux respawn-pane failed while re-homing $target to $dir" >&2
    return 1
  fi
}

if [ "${1:-}" = "--rehome-lane" ]; then
  rehome_target="${2:-}"
  rehome_dir="${3:-${HOME:-/tmp}}"
  rehome_harness="${4:-}"
  dispatch_rehome_lane "$rehome_target" "$rehome_dir" "$rehome_harness"
  exit $?
fi

# `--reviews-pr <PR>` is pulled out wherever it appears rather than bound to a
# fixed position: every other argument here is positional and some of them
# (repo, repo-path) are already optional, so a new optional flag is scanned
# out first and the remaining args keep their existing $1..$5 meaning
# untouched. This is deliberately NOT `DISPATCH_LANE` reborn under a new name
# -- see the lane-selection loop below -- it names which PR is under review,
# it never names which lane to use.
REVIEWS_PR=""
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
        sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
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
        sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
        exit 1
      fi
      REVIEWS_PR="$2"
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
  sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
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

# agent-supervisor#500 / corpus directive it-ef548e51e71daebe: a NEW dispatch
# is the actual resource-multiplying step in this estate (a new worktree, a
# new agent process) -- director-loop.sh's own tick only sends a nudge to an
# EXISTING pane, so this is the one place in the live shell/tmux control
# plane that can refuse before adding load, not after. Checked here, before
# claim.sh or worktree.sh run (both below), for the same "cheapest failure
# first" reason the brief-file check above already gives: a refused dispatch
# must leave the estate exactly as it found it, and claiming an issue or
# building a worktree it then has to unwind would be strictly worse than
# never starting. See host-pressure.sh's own header for the thresholds and
# why they live in a second bash implementation rather than shelling out to
# daemon/internal/pressure's Go binary.
if [ -x "$HERE/host-pressure.sh" ]; then
  HOST_PRESSURE_OUT=$("$HERE/host-pressure.sh"); HOST_PRESSURE_RC=$?
  if [ "$HOST_PRESSURE_RC" -ne 0 ]; then
    echo "dispatch: $HOST_PRESSURE_OUT -- NOT dispatching #$ISSUE_ARG" >&2
    exit 1
  fi
else
  echo "dispatch: host-pressure.sh missing or not executable at $HERE -- refusing to guess whether this host can take another dispatch" >&2
  exit 1
fi

# --- carry forward what the LAST agent on this issue already found ----------
# Measured 2026-08-20: 184 issues in this estate have been worked more than
# once -- #308 thirteen times, #284 ten. Every re-dispatch started from zero,
# because results were written and never read back. The tenth pass on #284
# concluded "investigation-only, worktree diff is empty" while an attempt three
# days earlier had already found a real defect in the same area and posted a
# reject verdict with a mutation check.
#
# So the brief is AUGMENTED, not replaced: a section naming the prior result
# files, newest first. It does not summarise them -- a paraphrase quietly
# becomes the record, and this estate already carries a corpus that is 30-65%
# agent-written for exactly that reason. The lane reads the files.
#
# Never fatal. rc=1 (fresh issue) and rc=3 (cannot see the results dir) both
# leave the brief untouched and say so; a dispatcher that refused to dispatch
# because it could not read history would be worse than one that dispatches
# blind, which is the status quo.
if [ -x "$HERE/prior-attempts.sh" ]; then
  _pa_issue="$(printf '%s' "$ISSUE_ARG" | cut -d, -f1 | tr -cd '0-9')"
  if [ -n "$_pa_issue" ]; then
    _pa_section="$("$HERE/prior-attempts.sh" --issue "$_pa_issue" --brief 2>/dev/null)"
    _pa_rc=$?
    case "$_pa_rc" in
      0)
        _pa_brief="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}/brief-with-history-$SLUG.md"
        { cat "$BRIEF"; printf '%s
' "$_pa_section"; } > "$_pa_brief"           && BRIEF="$_pa_brief"           && echo "dispatch: brief augmented with prior attempts on #$_pa_issue -- $_pa_brief"
        ;;
      1) : ;;  # genuinely fresh; nothing to carry
      3) echo "dispatch: could not read prior results -- dispatching without history (NOT the same as none)" >&2 ;;
    esac
  fi
fi

# --- -0.5. the quota gate. Nothing gets dispatched on a window we cannot see
# ----------------------------------------------------------------------------
# agent-supervisor#227: quota.sh -- the thing every tick is required to run
# before spending anything -- was untracked and absent from the deployed
# `live/` tree. A caller reading "not 1" as "proceed" turned a missing file
# (exit 127, bash's own "No such file or directory") into permission to
# spend, which is the exact inversion the gate exists to prevent and the
# mechanism behind the $80 -> $8 burn.
#
# `quota.sh check` defines exactly four exit codes: 0 SAFE, 1 WIND DOWN, 2
# UNAVAILABLE, 3 codexbar MISSING. This is the caller, and it enumerates what
# it accepts rather than testing what it refuses -- `case ... 0) ;; 1) ...;;
# *) ...;; esac`, never `[ "$rc" -eq 1 ]`. Only 0 proceeds. 1, 2, 3, 127, and
# anything this dispatcher has never seen all fail closed the same way:
# refuse to dispatch. QUOTA_GATE is overridable so tests can point it at a
# path that does not exist and watch this refuse.
QUOTA_GATE="${QUOTA_GATE:-$HERE/quota.sh}"
QUOTA_OUT=$("$QUOTA_GATE" check 2>&1)
QUOTA_RC=$?
case "$QUOTA_RC" in
  0)
    : # SAFE -- proceed
    ;;
  1)
    echo "dispatch: quota gate says WIND DOWN -- refusing to dispatch #$ISSUE_ARG" >&2
    sed 's/^/  /' <<<"$QUOTA_OUT" >&2
    echo "dispatch: this is a legitimate stop, not a failure -- quota-watch.sh brings the loop back" >&2
    exit 1
    ;;
  *)
    echo "dispatch: quota gate exited $QUOTA_RC ($QUOTA_GATE) -- UNKNOWN, never treated as safe" >&2
    sed 's/^/  /' <<<"$QUOTA_OUT" >&2
    exit 1
    ;;
esac

# --- infer --reviews-pr when the caller forgot it (agent-supervisor#70) ----
# agent-dotfiles#263's shape: a guard that "proceeds unchanged if the flag is
# omitted" is a guard that will be omitted. Measured on this estate: a
# self-review was dispatched three times in one day (PR #62 twice, #69 once)
# because `--reviews-pr` was forgotten every time, not because the guard
# below (step 0.5) failed once it ran.
#
# This is a SAFETY NET on top of the explicit flag, not a replacement for it.
# Skipped entirely when `--reviews-pr` was actually passed -- REVIEWS_PR is
# already non-empty then, and the caller's explicit answer is never
# second-guessed. loop-tick.md's instruction to pass the flag stands.
#
# Detection: a line -- in the issue's own title, checked first, then the
# brief -- containing both the word "review" and a PR reference, in one of
# three shapes (case-insensitive):
#   * "PR #<N>"                       -- "review PR #204"
#   * "PR <owner>/<repo>#<N>"         -- "review of PR jonhill90/agent-
#                                         supervisor#72" (agent-supervisor#72:
#                                         the Director's own review briefs
#                                         write it this way, every time)
#   * a github.com PR URL             -- ".../pull/<N>", with or without the
#                                         word "PR" nearby -- "/pull/" is
#                                         itself PR-specific (issues use
#                                         "/issues/"), so it needs no other
#                                         signal
# That is the shape every review issue and brief in this estate's own
# history already uses (see the #212/#35 fixtures in
# tests/supervisor/test_dispatch.sh and loop-tick.md's own dispatch
# instructions). No new convention is introduced; this only reads ones that
# already exist.
#
# DELIBERATELY NOT MATCHED: a bare "#<N>" with no "PR" word and no
# owner/repo before it. This estate's issues and briefs constantly say
# "closing issue #70" or "Fixes #240" right next to a PR reference on the
# same line ("review PR #500, no flag passed" is itself a title fixture
# below) -- a bare number would as often grab the issue being closed as the
# PR being reviewed, and inferring the WRONG PR is worse than inferring
# none (step 0.5's guard would refuse or exclude based on the wrong
# author). Likewise, `owner/repo#<N>` with no "PR" word is not matched: that
# exact shape is this repo's own convention for citing an ISSUE inline
# ("Fixes #240" close cousin), so matching it bare would conflate issue and
# PR references. The "PR " prefix is what disambiguates both.
#
# WRONG IN EACH DIRECTION, both survivable:
#   * FALSE POSITIVE (detected as a review when the dispatch is not one): the
#     worst outcome is step 0.5 below excluding one candidate lane it did not
#     need to, or refusing outright with "no free lane other than the
#     author" when that candidate was the only free one. Annoying, not
#     dangerous -- the message names exactly why, and the fix is to
#     re-dispatch (the issue's claim is released on refusal, same as every
#     other refusal path in this script).
#   * FALSE NEGATIVE (a real review not detected -- title and brief phrase it
#     differently than the shapes above): behaviour is exactly today's
#     status quo. The explicit flag remains the only guaranteed way to
#     trigger the guard; this block only catches the forgotten-flag case
#     when the issue happens to name the PR in one of the shapes above.
#
# agent-supervisor#101 changed NONE of the detection above. The false
# positive it reports is real -- "rebase it so it can be reviewed" next to
# "PR #93" is a rebase, not a review -- but every narrowing available reads
# the same prose the current pattern reads and so would drop real reviews
# this catches today, which is the dangerous direction. The escape is
# `--not-a-review` instead: an operator says "not a review" at the dispatch,
# where the intent actually lives, rather than rewording a brief until the
# tool stops matching it. Detection stays exactly as wide as it was; only the
# cost of being wrong changed, from "reword the brief" to "pass a flag".
INFER_PR_PATTERN='pr[[:space:]]*#[0-9]+|pr[[:space:]]*[a-z0-9_.-]+/[a-z0-9_.-]+#[0-9]+|github\.com/[a-z0-9_.-]+/[a-z0-9_.-]+/pull/[0-9]+'
REVIEWS_PR_INFERRED=""
if [ -z "$REVIEWS_PR" ] && [ -z "$NOT_A_REVIEW" ]; then
  INFER_GH_REPO_ARGS=()
  [ -n "$REPO" ] && INFER_GH_REPO_ARGS=(-R "$REPO")
  ISSUE_TITLE=$(gh issue view "$ISSUE" "${INFER_GH_REPO_ARGS[@]+"${INFER_GH_REPO_ARGS[@]}"}" --json title -q .title 2>/dev/null)
  INFERRED_PR=$(grep -iE 'review' <<<"$ISSUE_TITLE" | grep -ioE "$INFER_PR_PATTERN" | grep -oE '[0-9]+$' | head -1)
  INFERRED_FROM="issue #$ISSUE's title"
  if [ -z "$INFERRED_PR" ]; then
    INFERRED_PR=$(grep -iE 'review' "$BRIEF" | grep -ioE "$INFER_PR_PATTERN" | grep -oE '[0-9]+$' | head -1)
    INFERRED_FROM="the brief"
  fi
  if [ -n "$INFERRED_PR" ]; then
    REVIEWS_PR="$INFERRED_PR"
    REVIEWS_PR_INFERRED=1
    echo "dispatch: inferred --reviews-pr $REVIEWS_PR from $INFERRED_FROM (a line with 'review' and 'PR #$REVIEWS_PR') -- pass --reviews-pr explicitly to override" >&2
    echo "dispatch: if this dispatch is NOT a review, re-run it with --not-a-review (agent-supervisor#101) -- no need to reword the brief" >&2
  fi
fi

# agent-supervisor#159: PR_SCOPED is the PR this dispatch is FOR, whichever
# flag said so -- `--reviews-pr` (explicit or inferred above) implies it, and
# `--pr` says it directly for a dispatch that is not a review at all (a fix
# pass, most often). Computed AFTER inference, not before, so an inferred
# `--reviews-pr` is covered by this check too: naming two different PRs
# between `--pr` and `--reviews-pr` is refused rather than silently picking
# one, the same posture `--reviews-pr`/`--not-a-review` already takes above.
if [ -n "$PR" ] && [ -n "$REVIEWS_PR" ] && [ "$PR" != "$REVIEWS_PR" ]; then
  echo "dispatch: --pr $PR and --reviews-pr $REVIEWS_PR name different PRs -- refusing rather than picking one" >&2
  exit 2
fi
PR_SCOPED="${REVIEWS_PR:-$PR}"

# Window name: <prefix><issue>-<slug>, the convention loop-tick.md requires so
# Jon can read the tmux window list and know what the estate is doing. The
# prefix is the repo's initials when its name is hyphenated (agent-dotfiles ->
# ad, agent-evals -> ae) and the name itself when it is not (skills ->
# skills139-...), which is what the live session already looks like.
NAME_PART="${REPO##*/}"
[ -n "$NAME_PART" ] || NAME_PART="$(basename "$REPO_PATH")"

# agent-supervisor#111: one tmux session per repo, named for the repo NAME_PART
# just resolved -- a lane working in jonhill90/agent-tui runs in session
# agent-tui, not whatever repo the supervisor itself happens to live in.
# LANES_SESSION still overrides unconditionally (session_for_repo, #99).
# Resolved here rather than at the top of the script because NAME_PART is the
# earliest point REPO and REPO_PATH are both known, and SESSION is not read
# by anything above this line (first use is the lanes.sh call below).
SESSION="$(session_for_repo "$NAME_PART")"

if [[ "$NAME_PART" == *-* ]]; then
  PREFIX=$(tr '-' '\n' <<<"$NAME_PART" | cut -c1 | tr -d '\n')
else
  PREFIX="$NAME_PART"
fi
WINDOW_NAME="${PREFIX}${ISSUE}-${SLUG}"

# --- -1. the session itself must exist before any lane in it can be trusted -
# agent-supervisor#422: lanes.sh already refuses when $SESSION does not exist
# ("lanes: session '$SESSION' does not exist", exit 1) but every call site
# below wants only its stdout classification table and discards stderr
# (`2>/dev/null`) to get it -- so that message never reaches anyone. What
# reaches the caller instead is a LATER, generic "no free lane" refusal,
# worded identically to "every lane is busy" (#111 gives each repo its own
# session; a repo dispatch.sh has never bootstrapped a session for reads as
# silently, permanently full). Checked here, before step 0, so a missing
# session gets its own message and its own fix -- distinguishable from every
# other refusal below -- instead of falling through the candidate search and
# reading as ordinary busyness.
#
# `=name` is bootstrap-session.sh's own exact-match fix (#137): tmux
# prefix-matches `has-session -t foo` against an existing `foo-2`, so the
# same `=` prefix is required here or this probe could pass by matching a
# DIFFERENT session than the one this dispatch actually needs.
if ! tmux has-session -t "=$SESSION" 2>/dev/null; then
  BOOTSTRAP_LANES="${DISPATCH_BOOTSTRAP_LANES:-3}"
  echo "dispatch: session '$SESSION' does not exist -- #$ISSUE_ARG cannot be dispatched (agent-supervisor#422)" >&2
  if [ "${DISPATCH_NO_AUTO_BOOTSTRAP:-0}" = 1 ]; then
    echo "dispatch: DISPATCH_NO_AUTO_BOOTSTRAP=1 -- not auto-bootstrapping. Run by hand:" >&2
    echo "dispatch:   $HERE/bootstrap-session.sh --session $SESSION --lanes $BOOTSTRAP_LANES --cwd $REPO_PATH" >&2
    exit 2
  fi
  echo "dispatch: auto-bootstrapping session '$SESSION' ($BOOTSTRAP_LANES lanes, cwd $REPO_PATH)" >&2
  if "$HERE/bootstrap-session.sh" --session "$SESSION" --lanes "$BOOTSTRAP_LANES" --cwd "$REPO_PATH" >&2; then
    echo "dispatch: session '$SESSION' bootstrapped -- continuing dispatch" >&2
  else
    echo "dispatch: auto-bootstrap of session '$SESSION' FAILED -- run by hand:" >&2
    echo "dispatch:   $HERE/bootstrap-session.sh --session $SESSION --lanes $BOOTSTRAP_LANES --cwd $REPO_PATH" >&2
    exit 2
  fi
fi

# --- 0. the ledger must be readable before any lane is trusted ------------
# agent-dotfiles#174. Everything below this line asks the LEDGER whether a
# lane is free, not the window name -- the whole point of the change. That
# only holds if the ledger itself can be read at all: an unreadable ledger
# answering nothing must mean "cannot tell what is free", never "assume
# everything is free". This is the inverse of #140's original ledger WRITE,
# which was made non-fatal precisely because nothing read it yet (see step 6
# below for where that reasoning still applies, and why).
#
# Checked once, up front, rather than folded into the per-candidate query in
# step 1: a broken ledger fails every one of those queries identically, and
# diffusing the same failure across a loop would report it as "no free lane"
# -- true, but not why, and indistinguishable from an estate that is
# genuinely full.
LEDGER_PYTHON="${DISPATCH_PYTHON:-python3}"
LEDGER_CLI="$HERE/cli.py"
# agent-supervisor#108: "are these two lane ids the same lane?" is answered by
# `core.lane_relation` through the ledger CLI, never by `=` here. Two callers
# ask it (this script's author-exclusion guard, and digest.sh's independence
# report) and they must not disagree; a bash string comparison in either one is
# how they came to disagree with the ledger in the first place.
#
# Anything that is not a positively parsed answer -- python missing, cli.py
# broken, JSON in a shape this cannot read -- prints `unknown`, and every
# caller treats `unknown` as "do not admit". Failing to run the check must
# never read as permission.
#
# agent-supervisor#292: `cli.py lane-relation` now ALSO widens a shape-check
# `unknown` through the ledger's registry (a claude-print/pi-rpc lane has no
# `<session>:<index>` to compare, so the shape check alone can never place it
# positive of anything -- see `core.lane_relation_from_rows`'s own comment),
# and on any answer that is still not `different` it names which POPULATION
# each side is in (`lane_population`/`other_population` in the JSON) so a
# refusal can say WHY, not just THAT. Both are stashed here as a side effect
# -- `LANE_REL_POPULATION_CANDIDATE`/`LANE_REL_POPULATION_OTHER` -- so the
# skip message below does not need a second round-trip to explain itself.
# Empty when the relation is `different` (never needed there) or the JSON
# carried no such field (an older cli.py; still safe, just less specific).
LANE_REL_POPULATION_CANDIDATE=""
LANE_REL_POPULATION_OTHER=""
lane_relation() {  # lane_relation <lane> <other> [lane-pane-id] [other-pane-id] -> same|different|unknown
  # agent-supervisor#235: the optional third argument is a LIVE pane id the
  # caller just measured off tmux for `$1` -- see the author-exclusion loop
  # below, which is the one caller that has a real tmux target to measure.
  # Reconciled through the ledger's `pane_id` registry INSTEAD OF the
  # `<session>:<index>` shape check, which trusts the window INDEX half of
  # `$1` and is exactly what `renumber-windows on` (Jon's tmux setting)
  # rewrites out from under a lane the instant a lower window closes -- see
  # `core.py`'s own comment on `cli.py lane-relation --lane-pane-id`.
  #
  # agent-supervisor#631: the optional FOURTH argument is `$2`'s own FROZEN
  # pane id -- a contributor task's `tasks.pane_id` snapshot
  # (`AUTHOR_PANE_IDS`, see the author-exclusion loop below) -- used INSTEAD
  # of re-resolving `$2` through the ledger's mutable `lanes` table by
  # string. That live lookup is exactly what a later, unrelated dispatch can
  # silently overwrite for a contributor's OLD lane string once
  # `renumber-windows on` hands it to a different pane: `$2` would then
  # answer for the NEW occupant, not the historical contributor this
  # comparison is actually about.
  local json rel lane_pane_id_args=() other_pane_id_args=()
  [ -z "${3:-}" ] || lane_pane_id_args=(--lane-pane-id "$3")
  [ -z "${4:-}" ] || other_pane_id_args=(--other-pane-id "$4")
  json=$("$LEDGER_PYTHON" "$LEDGER_CLI" lane-relation --lane "$1" --other "$2" \
    "${lane_pane_id_args[@]}" "${other_pane_id_args[@]}" 2>/dev/null) || json=""
  rel=$(sed -n 's/.*"relation":"\([a-z]*\)".*/\1/p' <<<"$json" | head -1)
  LANE_REL_POPULATION_CANDIDATE=$(sed -n 's/.*"lane_population":"\([a-zA-Z-]*\)".*/\1/p' <<<"$json" | head -1)
  LANE_REL_POPULATION_OTHER=$(sed -n 's/.*"other_population":"\([a-zA-Z-]*\)".*/\1/p' <<<"$json" | head -1)
  case "$rel" in
    same|different) printf '%s\n' "$rel" ;;
    *) printf 'unknown\n' ;;
  esac
}
if ! LEDGER_STATUS_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" status 2>&1); then
  echo "dispatch: the ledger is unreadable -- refusing to dispatch #$ISSUE_ARG" >&2
  echo "dispatch: cannot tell which lanes are free without it, so nothing is safe to pick" >&2
  sed 's/^/  /' <<<"$LEDGER_STATUS_OUT" >&2
  exit 1
fi

# --- 0.2 refuse to dispatch for a supervisor that does not hold the lease -
# agent-dotfiles#238. On 2026-08-12 a second, fully legitimate supervisor
# instance resumed in an ordinary tmux window (identity had only ever been
# INFERRED from a window index, never recorded) and dispatched the same five
# issues a first instance had claimed seconds earlier. `claim.sh`'s per-issue
# claim could not catch it: both instances authenticate as the same GitHub
# user, so an assignee one took reads as claimed to the other too, not as a
# collision signal. This is the ledger-recorded fact that closes the gap one
# level up, at the supervisor ROLE itself -- see `Ledger.take_supervisor_lease`.
#
# `SUPERVISOR_LEASE_OWNER_PID` names the process that is supposed to hold the
# lease -- defaults to `$PPID`, the process that invoked this script, which
# in production is the long-lived supervisor loop itself (loop-tick.md takes
# the lease with its own `$$` at tick start, before calling this script; see
# that file's lease gate). Overridable for a caller that is not its own
# direct parent (none, today).
#
# Fails CLOSED only against a genuine conflict -- a lease recorded for some
# OTHER pid -- never against the mere absence of one. A ledger that has never
# seen `take-supervisor-lease` (every existing test fixture, and any manual
# `dispatch.sh` run outside the loop) has no row to conflict with, so this
# proceeds silently rather than demanding every caller adopt lease tracking
# before it dispatches anything -- the loop is what negotiates the lease with
# the WHOLE estate; a lone `dispatch.sh` invocation reading no lease at all
# has nothing to conflict with and nothing to protect against. Per
# agent-dotfiles#199, stderr on a dispatch that is not failing stays clean --
# an absent or unreadable lease is not, by itself, a failure.
SUPERVISOR_LEASE_OWNER_PID="${SUPERVISOR_LEASE_OWNER_PID:-$PPID}"
if LEASE_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" supervisor-lease 2>&1); then
  if grep -qF '"held":true' <<<"$LEASE_OUT"; then
    LEASE_OWNER=$(sed -n 's/.*"owner":"\([^"]*\)".*/\1/p' <<<"$LEASE_OUT" | head -1)
    LEASE_PID="${LEASE_OWNER##*:}"
    if [ -n "$LEASE_PID" ] && [ "$LEASE_PID" != "$SUPERVISOR_LEASE_OWNER_PID" ]; then
      echo "dispatch: the supervisor lease is held by $LEASE_OWNER, not this process (expected pid $SUPERVISOR_LEASE_OWNER_PID) -- refusing to dispatch #$ISSUE_ARG" >&2
      echo "dispatch: a second supervisor instance must stand down, not dispatch; see agent-dotfiles#238" >&2
      exit 1
    fi
  fi
fi

# --- 0.5 clear claims whose dispatcher died where nothing could clean up ---
# agent-dotfiles#209. Step 1's claim is released on every abort path below and
# by the EXIT/TERM/INT trap installed with it -- but SIGKILL, an OOM kill and
# a host crash cannot be trapped by any shell, and a dispatcher lost that way
# leaves its placeholder behind holding a lane that nothing is working. The
# ledger reads that lane occupied forever, which is #102's exact shape
# (dispatch capacity silently falling to zero while lanes sit idle) reached
# through the mechanism that exists to prevent it.
#
# `reap-lane-claims` removes only claim placeholders whose recorded owner pid
# is provably gone on this host -- not a TTL, which could not tell a slow
# dispatch from a dead one and would reopen #184's race by expiring a live
# claim (see `Ledger.reap_stale_lane_claims`). So this cannot make a lane
# available that was not already unowned, which is #124/#126's ratchet.
#
# HERE rather than in a new daemon, and here rather than the watchdog: the
# dispatcher is the only thing that reads lane availability to act on it, so
# it is where a stranded claim actually costs something, and running the reap
# immediately before selection means capacity comes back on the very next
# dispatch attempt instead of on some sweep's schedule.
#
# NEVER FATAL. A reap that fails leaves exactly the state that existed before
# this block -- some lanes stranded -- and refusing to dispatch over it would
# turn a partial capacity loss into a total one.
if REAP_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" reap-lane-claims 2>&1); then
  if ! grep -qF '"count":0' <<<"$REAP_OUT"; then
    echo "dispatch: cleared stranded lane claim(s) whose dispatcher is gone: $REAP_OUT" >&2
  fi
else
  echo "dispatch: WARNING -- could not reap stranded lane claims; continuing" >&2
  sed 's/^/  /' <<<"$REAP_OUT" >&2
fi

# --- 0.5. a review must not land on the lane that wrote what it reviews ---
# agent-dotfiles#212. On 2026-08-12 a review of #204 was dispatched to lane
# 4, the same lane that had written the code under review (ad193/ad204),
# and its APPROVE had to be thrown away. This is that refusal, built the way
# #174 requires: BY LEDGER RECORD, never by window name -- for a lane the
# ledger already knows, `cli.py lane_free` answers from the ledger alone and
# the window name is never consulted (see step 1's own comment), so a name
# cannot be used to steer a review away from its author either.
#
# Only runs when the caller says this dispatch IS a review, via
# `--reviews-pr`. Ordinary (non-review) dispatches are unaffected -- there is
# no author to avoid.
#
# THE PRIMARY MAPPING (issue -> author lane) is `source_tasks.source_ref` (the
# issue number `record_dispatch` wrote it as, step 6 below) joined to
# `tasks.lane` by task id, with review tasks explicitly filtered out -- see
# `Ledger.get_author_task_for_issue`. Neither side comes from a branch name.
#
# THE FALLBACK MAPPING, used only when that lookup is silent, is #117's own
# fix: the WORKTREE `worktree.sh new` built for a dispatch (step 3 below) is
# recorded against that dispatch's task id at dispatch time (`--worktree`,
# step 6), and never touched again. That worktree is not renamed even when
# its branch is -- a lane routinely renames its checkout to a type-prefixed
# branch (`fix/`, `feat/`, ...) with a slug of its OWN choosing, unrelated
# to the dispatch slug the task id was minted from, so the branch on a PR is
# frequently NOT `<prefix>/<n>-<slug>` for the `$PREFIX`/`$SLUG` this
# dispatch would reconstruct (agent-supervisor#117: task
# `as101-reviewspr-inference` produced branch `fix/101-not-a-review-escape`
# -- the two slugs share no text at all). So this asks `git worktree list`
# which worktree currently has the PR's HEAD_REF checked out, then asks the
# ledger which task that worktree's PATH was recorded against -- a fact
# nobody has to reconstruct, because it was written once and never changes.
#
# A LEGACY fallback (step 3.1) still reconstructs a task id from the branch
# name, kept only for tasks dispatched before this column existed (they
# recorded no worktree path at all, so step 3 can never match them) -- see
# its own comment below for why it is trusted no further than before.
#
# FAILS CLOSED throughout: `gh` unreachable, a PR with no head branch, or
# every source below coming up silent -- every one of these means authorship
# cannot be determined, and this refuses the WHOLE dispatch rather than
# guess. A candidate lane is only ever excluded, never assumed innocent from
# missing data.
#
# agent-supervisor#35: a branch name is a label someone typed at dispatch
# time, not a record this system wrote -- the ledger row is. THE LEDGER IS
# ASKED FIRST NOW, keyed by the issue the PR closes, and the branch name is
# only ever consulted through a ledger record it points at, never trusted on
# its own -- first the worktree that produced it (#117), then, only for
# rows that predate that, the task id it implies by convention (#35).
# Measured on this repo's own merged history when #35 shipped: 3 of 11
# merged PRs, plus open PR #33, were unreviewable through the branch-only
# path -- see the #35 issue body for the count.
# agent-supervisor#190: this used to resolve a SINGLE `AUTHOR_LANE` -- the
# one task the ledger could name as having produced the PR's branch. Two
# live dispatches (this issue's own evidence) show why that is not enough:
# a FIX-PASS task dispatched against the same issue to address review
# findings (`as178-fix186`, fixing PR #186) is a second, later contributor
# to the same PR, sitting in the exact same `source_tasks` rows the single
# lookup below queries -- it was just discarded by the "narrow to one"
# step, and the lane that wrote it went on to approve its own fix.
#
# So this now builds a SET, `AUTHOR_LANES` (with `AUTHOR_TASKS` as its
# parallel "why" for messages) -- every lane the ledger can show contributed
# to this PR, not just the one it can name as "the" author. Plain indexed
# arrays, not `declare -A` (bash 3.2 has none -- see #199's own removal
# above), so membership is a linear scan (`author_lane_known`, below) and
# both are guarded with the same `"${arr[@]+"${arr[@]}"}"` bash-3.2-empty-
# array idiom `POSITIONAL` already established in this file.
# agent-supervisor#308 item 4: the resolution chain itself (issue, PR-task,
# PR-contributor, worktree, legacy branch) now lives in
# resolve-pr-contributors.sh, shared with mark-pr-external.sh's laundering
# gate -- see that file's header for why a second, drifted copy is exactly
# the defect #321's review measured in verdict-independence.sh.
# shellcheck source=./resolve-pr-contributors.sh
. "$HERE/resolve-pr-contributors.sh"
AUTHOR_LANES=()
AUTHOR_TASKS=()
AUTHOR_PANE_IDS=()
FALLBACK_TASK=""
# True (1) only when `contributor-issue-lanes` (or a fallback below) was
# consulted and answered with a NON-empty, known set. Distinguishes "no PR
# review requested, no set was ever needed" (both arrays legitimately empty,
# no refusal below) from "a review WAS requested and the ledger came back
# silent" (arrays empty for a reason, and step 4 below must refuse), which an
# empty-array check alone cannot tell apart.
CONTRIBUTORS_RESOLVED=""
if [ -n "$REVIEWS_PR" ]; then
  if ! resolve_pr_contributors "$REPO" "$REVIEWS_PR" "$REPO_PATH" "$PREFIX" "$LEDGER_PYTHON" "$LEDGER_CLI"; then
    exit 1
  fi

  # 3.2. agent-supervisor#308 item 3: "authored outside the lane system" as
  # a FIRST-CLASS, RECORDABLE state -- checked only when every real
  # resolution path above came back silent, and NEVER treated as "proceed
  # when authorship is unknown" (#190 forbids that flag outright; this is
  # not it). A PR marked here was a DECISION an operator made -- that no
  # lane wrote it -- so the contributor set is KNOWN-EMPTY, not unresolved:
  # every free lane is a valid independent reviewer, the safe case. This is
  # the #316/#301/#300 shape: a PR authored directly by a human or the
  # watchdog, closing no issue the ledger can name, whose branch fails the
  # legacy convention outright. See `Ledger.get_pr_external` / `mark-pr-external`.
  if [ -z "$CONTRIBUTORS_RESOLVED" ]; then
    EXTERNAL_JSON=$("$LEDGER_PYTHON" "$LEDGER_CLI" pr-external --repo "$REPO" --pr "$REVIEWS_PR" 2>&1) || EXTERNAL_JSON=""
    if grep -qF '"known":true' <<<"$EXTERNAL_JSON"; then
      CONTRIBUTORS_RESOLVED=1
      echo "dispatch: PR #$REVIEWS_PR is recorded as authored OUTSIDE the lane system (marked external) -- no lane contributors to exclude, every free lane is a valid independent reviewer" >&2
    fi
  fi

  # 4. Still silent -> refuse. Every source above answered "no record", not
  # "safe". agent-supervisor#190's fail-closed requirement: an unresolvable
  # contributor set must make this dispatch LESS likely to proceed, never
  # fall back to some narrower, single-author check that would admit a
  # candidate this wider set does not yet clear (#124/#126).
  if [ -z "$CONTRIBUTORS_RESOLVED" ]; then
    # Wording kept verbatim from before #190 ("could not determine PR
    # #N's author", "authorship unknown") -- both predate the widening and
    # every earlier caller, including tests, greps for those exact phrases.
    # The two describe the same fact before and after: nobody the ledger
    # will vouch for produced (or contributed to) this PR.
    echo "dispatch: could not determine PR #$REVIEWS_PR's author -- the ledger has no record by issue, by commit, or by branch '$HEAD_REF' (task ${FALLBACK_TASK:-none}) -- refusing (authorship unknown, failing closed)" >&2
    echo "dispatch: if this PR was genuinely authored outside the lane system (a human, or the watchdog), record that once with: $HERE/mark-pr-external.sh '$REPO' $REVIEWS_PR '<why>' '$REPO_PATH'" >&2
    echo "dispatch: NOTE -- use mark-pr-external.sh, not cli.py mark-pr-external directly; the CLI now refuses without --chain-verified, which only the wrapper's own exhaustive resolution chain earns (PR #331 review, finding 2)" >&2
    # agent-supervisor#101, third red-first item: on the inferred path these
    # are TWO separate findings arriving together -- "this looked like a
    # review" and "its contributors are unresolvable" -- and read as one
    # failure about authorship. An operator whose dispatch was never a
    # review has no authorship problem to fix; they have an inference to
    # switch off. Say which of the two is theirs.
    if [ -n "$REVIEWS_PR_INFERRED" ]; then
      echo "dispatch: NOTE -- --reviews-pr was never passed; PR #$REVIEWS_PR was INFERRED from $INFERRED_FROM" >&2
      echo "dispatch: two separate things are true here -- this dispatch LOOKED like a review, and that PR's contributors cannot be resolved" >&2
      echo "dispatch: if it is not a review at all (a rebase, a fix pass, a follow-up), re-run with --not-a-review and the authorship question does not arise" >&2
    fi
    exit 1
  fi
fi

# --- 0.6 a PR already claimed by a lane is refused, not double-dispatched -
# agent-supervisor#159 (issue comment, third occurrence measured the same
# night): two lanes worked #157's review and two lanes worked #149's fix
# pass, both times through the SAME task id with a "b" suffix -- something
# minted a second task for work that already had one instead of detecting
# the first. The shared cause: a ledger-bypassing tmux hand-off (the
# workaround for the issue-claim refusal this PR removes) leaves no row a
# second dispatcher can see. This is the detection that was missing --
# checked BEFORE step 1 picks a lane, so it costs nothing when it refuses:
# no lane claim taken yet, no worktree built.
#
# Only runs for a PR-scoped dispatch (`PR_SCOPED` set, see the flag block
# above) -- an ordinary issue-scoped dispatch has no PR to check yet, and
# `claim.sh` in step 2 below is exactly this same guarantee for that case,
# already proven. `cli.py pr-lane` asks `Ledger.get_open_task_for_pr`, which
# is OPEN-status only (unlike `issue-lane`): a completed or cancelled prior
# review of this PR must not block a fresh one.
#
# agent-supervisor#169: THIS CHECK ALONE IS A TOCTOU, and is not the whole
# guarantee -- a reviewer of this PR reproduced the exact "b"-suffixed
# collision above THROUGH it: it is a plain read, run once, before a lane is
# even picked, while the write that actually claims the PR
# (`record-dispatch --pr`, step 6, far below) does not happen until after
# lane selection, worktree creation and the brief itself -- a real,
# multi-second window, not the sub-second gap `claim.sh`'s own docstring
# accepts for the GitHub-assignee race. Two dispatchers can both pass this
# exact check before either one has recorded anything. What actually closes
# the race is `core.py`'s `one_open_pull_per_source_ref` trigger on
# `record-dispatch`'s write, at the bottom of this script -- this check
# stays because it is the FRIENDLY, EARLY refusal for the common case (no
# wasted worktree, no stray brief); it is not, by itself, load-bearing for
# correctness anymore.
if [ -n "$PR_SCOPED" ]; then
  PR_LANE_REPO_ARGS=()
  [ -n "$REPO" ] && PR_LANE_REPO_ARGS=(--repo "$REPO")
  PR_LANE_JSON=$("$LEDGER_PYTHON" "$LEDGER_CLI" pr-lane --pr "$PR_SCOPED" "${PR_LANE_REPO_ARGS[@]+"${PR_LANE_REPO_ARGS[@]}"}" 2>&1)
  if [ $? -ne 0 ]; then
    echo "dispatch: could not ask the ledger whether PR #$PR_SCOPED already has a lane -- refusing rather than risk a duplicate" >&2
    sed 's/^/  /' <<<"$PR_LANE_JSON" >&2
    exit 1
  fi
  if grep -qF '"known":true' <<<"$PR_LANE_JSON"; then
    PR_HOLDER_LANE=$(sed -n 's/.*"lane":"\([^"]*\)".*/\1/p' <<<"$PR_LANE_JSON")
    PR_HOLDER_TASK=$(sed -n 's/.*"task":"\([^"]*\)".*/\1/p' <<<"$PR_LANE_JSON")
    echo "dispatch: PR #$PR_SCOPED is already claimed by lane $PR_HOLDER_LANE (task $PR_HOLDER_TASK) -- not dispatching, pick different work" >&2
    exit 1
  fi
fi

# --- 1. a lane that is actually safe to dispatch to ------------------------
# `send-keys -t session:` with an empty index does not error; it targets the
# active window, which is usually the supervisor. Refuse an empty target
# rather than discover where the brief landed.
#
# TWO questions, and `lanes.sh --free` only answers the first. "Is an agent
# there and not mid-turn" it answers from pane content -- that stays exactly
# as it was; lanes.sh keeps classifying panes, this change is not about that.
# "Is this lane UNOWNED" is the ledger's question now, not the window name's:
# a lane that finished and was never renamed, and a lane paused on an
# approval prompt, both show no busy marker and are byte-identical to a
# genuinely idle one from pane content alone, and the ledger is what breaks
# that tie. On 2026-08-11 the supervisor took `--free | head -1` by hand, got
# another dispatcher's task-named lane, and `/clear`ed it; nothing was lost
# only because that lane had already shipped.
#
# The window NAME still matters for exactly one thing: MIGRATION. A lane the
# ledger has never heard of -- every lane alive before this landed, or one
# opened by hand -- is backfilled into the ledger as free the FIRST time it
# is seen named `free-N`, and never consulted by name again after that (see
# `cli.py lane_free`'s docstring). A lane the ledger already knows about, free
# or occupied, answers from the ledger alone regardless of what it is
# currently named -- that is the inversion #174 exists to prove: an occupied
# lane hand-renamed to `free-N` is still not offered.
#
# There is deliberately NO env-var override of this selection. `DISPATCH_LANE`
# used to be honoured verbatim -- no free check, no name check, no supervisor
# exclusion -- and `DISPATCH_LANE=t:1` put `/clear` plus a full brief into the
# supervisor's own pane at exit 0, which is the incident loop-tick.md records
# under "an empty tmux target hits the ACTIVE window", reached through a stray
# environment variable instead of an empty string. Nothing called it. An
# escape hatch around the only guard is not worth a caller it does not have.
#
# agent-dotfiles#184 (closing #188 finding 2's own gap): `lane-free` is a
# QUERY, not a claim -- see `cli.py lane_free`'s own docstring for the
# measured proof. Left alone, nothing re-checks between a candidate reading
# free and the first `send-keys` several steps below, and that window is not
# sub-second the way `claim.sh`'s is: it spans claim, worktree creation and
# the send itself. So a candidate reading free is now followed IMMEDIATELY
# by `claim-lane`, an atomic write-then-verify (see `Ledger.claim_lane`'s
# docstring): it inserts a placeholder occupying the lane, protected by the
# same `one_open_task_per_lane` unique index the rest of the ledger already
# relies on, and re-reads to confirm the placeholder it just wrote is still
# the one occupying the lane. Two dispatchers racing the SAME candidate are
# serialized by that call, not by this loop -- the loser's claim is refused,
# not merged, and it moves on to the next candidate instead of stopping.
# `record_dispatch` (step 6) still mints a fresh nonce and cancels whatever
# was outstanding for the lane on every call (measured, #183 round 3) -- but
# by the time it runs, "whatever was outstanding" is this dispatch's OWN
# claim, not a stranger's, because the claim already closed the window a
# stranger could have used.
CLAIM_TOKEN="$WINDOW_NAME"
# agent-dotfiles#209. Two guards, and they are not the same guard.
#
# CLAIM_LANE: nothing has been claimed yet, so there is nothing to release.
#
# CLAIM_COMMITTED: step 4.5 has marked the claim LIVE and the brief is going
# into a real pane, so this dispatch is no longer unwindable. Past that point
# releasing the claim would free a lane that is actively working --
# #102/#126's failure, caused by the cleanup rather than prevented by it. It
# matters because of the trap below, which fires on the SUCCESS path too: on a
# clean dispatch `record_dispatch`'s own `_register_lane_tx` has already
# cancelled this placeholder, so the release would be a harmless no-op -- but
# when `record_dispatch` FAILS (non-fatal by design, step 6) the placeholder is
# still the only thing holding the lane, and deleting it would hand a working
# lane to the next dispatcher.
#
# THIS FLAG IS A FAST PATH, NOT THE GUARANTEE (agent-dotfiles#209 round 2).
# Round 1 had only this flag, set ~70 lines after the brief was submitted, and
# a signal landing in between freed a working lane. The durable half is now
# step 4.5's `commit-lane-claim`, which moves the row to a status
# `release-lane-claim` is scoped away from -- so even a signal arriving
# between that ledger write and the assignment of this variable is safe, and
# so is a SIGKILL that never lets this shell run anything again.
release_lane_claim() {
  [ -n "${CLAIM_LANE:-}" ] || return 0
  [ -z "${CLAIM_COMMITTED:-}" ] || return 0
  "$LEDGER_PYTHON" "$LEDGER_CLI" release-lane-claim --lane "$CLAIM_LANE" --token "$CLAIM_TOKEN" >/dev/null 2>&1
}

# agent-supervisor#572: the GitHub issue claim (`claim.sh take`, step 2 below)
# is the same shape of held resource the lane claim above is -- and until now
# it had exactly the gap #209 closed for the lane claim: every INTENTIONAL
# refusal this script enumerates (CLAIM_FAILED, a failed worktree, every
# abort_send) already released it inline, but a `kill`, a timeout wrapper, a
# closed terminal or a crashed shell hit none of those lines, same as #209
# found for release_lane_claim. Measured against #571: the issue was left
# assigned by a run that never reached one of the explicit release_claim call
# sites, and every retry after that read "already claimed" -- indistinguishable
# from someone else genuinely working it.
#
# CLAIMED and this function are declared here, BEFORE the trap below, rather
# than at step 2 where the claim itself is taken -- a signal landing before
# step 2 must find `release_claim` already callable and `$CLAIMED` already
# empty (a no-op), not an undefined function. Step 2 (below) only ever
# APPENDS to $CLAIMED; it does not redeclare it.
#
# release_claim itself stays UNGATED -- unlike release_lane_claim, it is not
# only the trap's cleanup, it is also every abort_send's own explicit call,
# and abort_send is reached AFTER step 4.5 too (the swallowed-Enter and
# never-cleared-menu cases: the commit moved before the send on purpose,
# #209 round 2). Those refusals deliberately release the ISSUE claim even
# with CLAIM_COMMITTED set -- an unconfirmed lane stays HELD (nothing may
# free the LANE once the brief might be live), but the ISSUE goes back to
# the pool so another lane can pick it up while this one is investigated;
# "the claim is released when the brief never starts" (test_dispatch.sh)
# pins exactly this asymmetry. Gating release_claim itself on
# CLAIM_COMMITTED would have silently turned that release into a no-op.
#
# release_claim_on_signal, below, is the ONLY thing gated: the trap must not
# release a claim out from under a signal landing while the brief may
# genuinely be live and unconfirmed, the same #102 shape release_lane_claim
# already guards against -- but it has to be a SEPARATE function so the
# explicit abort_send call sites above keep their existing, deliberate
# behaviour unchanged.
CLAIMED=()
release_claim() {
  local failed=() i
  # Reverse of claim order; the order itself has no observable effect on
  # GitHub state, but unwinding newest-first mirrors how the failure was hit.
  for ((idx = ${#CLAIMED[@]} - 1; idx >= 0; idx--)); do
    i="${CLAIMED[idx]}"
    "$HERE/claim.sh" release "$i" "$REPO" >/dev/null 2>&1 || failed+=("$i")
  done
  if [ "${#failed[@]}" -gt 0 ]; then
    # Loud and unambiguous: a claim nobody can see is worse than no claim,
    # and a silently half-undone abort is exactly that -- issues in $failed
    # are still assigned even though this dispatch is telling its caller it
    # sent nothing.
    echo "dispatch: could not release the claim on #${failed[*]} -- release ${failed[*]} by hand" >&2
  fi
}
release_claim_on_signal() {
  [ -z "${CLAIM_COMMITTED:-}" ] || return 0
  release_claim
}

# The claim is a held resource, and every sibling script in this directory
# that holds one guards it with a trap: `advance-live.sh:296`,
# `would-revert.sh:138-140`, `watchdog.sh:428`, `inbox-poll.sh:200,215-217`.
# dispatch.sh had none. Its four inline `release_lane_claim` calls cover the
# four failures it ENUMERATES; a `kill`, a timeout wrapper, a closed terminal
# or a crashed shell are not among them and left the claim behind.
#
# EXIT alone is not enough, for the reason #187 measured on inbox-poll.sh: an
# untrapped SIGTERM reaches the EXIT trap only when bash happens to be waiting
# on a foreground child, and lands as an outright kill otherwise -- and this
# script spends most of its life in `sleep` and `tmux`, so both cases are
# routine. TERM and INT are therefore trapped explicitly.
#
# SIGKILL CANNOT BE TRAPPED BY ANY SHELL, and neither can a host crash. This
# trap does not cover them and does not claim to; step 0.5's reap is what
# covers what the trap cannot, and the two together are the whole of #209's
# cleanup. Neither alone is sufficient. `claim.sh audit`/`reap` (#359) is the
# same second half for the issue claim -- SIGKILL leaves it stale, not
# released, exactly like the lane claim.
#
# And neither is allowed past step 4.5 (agent-dotfiles#209 round 2). A SIGKILL
# AFTER the brief goes live leaves a claim the reap deliberately will not
# clear, so that one case ends at the documented manual recovery rather than
# at an automatic cleanup -- because the alternative is handing the next
# dispatcher a lane with a worker in it, and that is the loss this whole
# subsystem exists to prevent. release_claim_on_signal's own CLAIM_COMMITTED
# gate, above, is the same protection for the issue claim -- the trap below
# calls THAT, never the ungated release_claim directly, or every clean
# successful dispatch's own EXIT would try to release the claim it just
# delivered on.
#
# release_lane_claim and release_claim_on_signal are both idempotent (a
# scoped DELETE/no-CLAIMED-left that matches nothing the second time), so
# the TERM/INT handlers re-entering them via EXIT is a no-op.
trap 'release_claim_on_signal; release_lane_claim' EXIT
trap 'release_claim_on_signal; release_lane_claim; exit 143' TERM   # 128 + 15
trap 'release_claim_on_signal; release_lane_claim; exit 130' INT    # 128 + 2

# agent-dotfiles#199: NOT `declare -A`. macOS ships /bin/bash 3.2, which has
# no associative arrays -- `declare -A` is rejected there and prints
# straight to stderr on every dispatch, which reads like a broken guard on
# the command that decides where work goes. A plain (indexed) array works
# without it: bash auto-vivifies WINDOW_NAME_BY_INDEX on first assignment,
# and every subscript below is a tmux window index (numeric, from
# `lanes.sh`'s own window-index column), so each key keeps its own slot the
# same way an associative array would. This is only safe because the keys
# stay numeric -- a non-numeric key here would silently collapse to index 0
# instead of getting its own slot.
while IFS=$'\t' read -r idx wname; do
  [ -n "$idx" ] || continue
  WINDOW_NAME_BY_INDEX["$idx"]="$wname"
done < <("$HERE/lanes.sh" "$SESSION" 2>/dev/null | awk 'NR>1 && $1 ~ /^[0-9]+$/ {print $1"\t"$2}')

LANE=""
LANE_TARGET=""
CLAIM_LANE=""
LANE_HARNESS=""
AUTHOR_SKIPPED=""
EXCLUSION_LINES=""
SUGGEST_RECORD_COMPLETION=""
SUGGEST_RELEASE_CLAIM=""
json_field() {
  local key="$1" json="$2"
  sed -n "s/.*\"$key\":\\([^,}]*\\).*/\\1/p" <<<"$json" | head -1 | sed -E 's/^"//; s/"$//'
}
append_exclusion() {
  local line="$1"
  EXCLUSION_LINES="${EXCLUSION_LINES}${line}"$'\n'
}
claim_token_from_task() {
  local lane="$1" task="$2" prefix
  prefix="ledger-claim:${lane}:"
  case "$task" in
    "$prefix"*) printf '%s' "${task#"$prefix"}" ;;
    *) printf '' ;;
  esac
}
describe_excluded_lane() {
  local lane="$1" pane_state="$2" diag task status age_base age_minutes token line
  diag=$("$LEDGER_PYTHON" "$LEDGER_CLI" lane-diagnostic --lane "$lane" 2>/dev/null) || diag=""
  task=$(json_field task "$diag")
  status=$(json_field status "$diag")
  [ "$task" = null ] && task=""
  [ "$status" = null ] && status=""

  if [ -z "$diag" ]; then
    append_exclusion "dispatch:   $lane: pane state $pane_state; ledger diagnostic unavailable"
    return
  fi
  if [ -z "$task" ]; then
    if [ "$pane_state" = free ]; then
      append_exclusion "dispatch:   $lane: pane is idle, but no claim could be won"
    else
      append_exclusion "dispatch:   $lane: pane state $pane_state; no open ledger task"
    fi
    return
  fi

  token=$(claim_token_from_task "$lane" "$task")
  line="dispatch:   $lane: "
  if [ "$pane_state" = free ]; then
    line="${line}busy (task $task"
    [ -n "$status" ] && line="${line} $status"
    age_base=$(json_field delivered_at "$diag")
    [ "$age_base" = null ] || [ -n "$age_base" ] || age_base=$(json_field updated_at "$diag")
    if [[ "$age_base" =~ ^[0-9]+$ ]]; then
      age_minutes=$(( ($(date +%s) - age_base) / 60 ))
      [ "$age_minutes" -ge 0 ] && line="${line} ${age_minutes}m ago"
    fi
    line="${line}); pane idle"
    append_exclusion "$line"
    if [ "$status" = delivered ]; then
      SUGGEST_RECORD_COMPLETION="${lane}	${task}"
    fi
  else
    append_exclusion "${line}pane state $pane_state (task $task${status:+ $status})"
  fi

  if [ -n "$token" ] && [ "$status" = created ]; then
    SUGGEST_RELEASE_CLAIM="${lane}	${token}"
  fi
}
# TWO IDENTITIES PER CANDIDATE, AND THEY ANSWER DIFFERENT QUESTIONS (#241).
#
# `$candidate` is `session:<index>` -- the LANE, which is what the ledger
# keys on and what every operator recovery command below names. It is a slot
# number and it must stay one: a lane has to keep its identity across a
# window being closed and recreated, and a window id is destroyed by exactly
# that.
#
# `$candidate_target` is `session:@<id>` -- the TMUX TARGET, and the only
# thing any tmux call below is allowed to be given. tmux window INDICES are
# not stable on this server (`renumber-windows on`, measured in #241):
# closing any window shifts every higher index down by one. The gap between
# resolving a lane here and the final `send-keys Enter` spans a claim, a
# worktree creation and a rename -- "not sub-second the way `claim.sh`'s is",
# as the comment above already says of the ledger race #184 closed. The same
# gap lets an index silently come to mean another pane, and on 2026-08-12
# three briefs landed in windows other than the ones this script reported.
# A window id cannot move: tmux guarantees it for the window's lifetime and
# never reuses it.
while IFS=$'\t' read -r candidate candidate_target; do
  [ -n "$candidate" ] || continue
  # THE EMPTY-TARGET REFUSAL, EXTENDED TO THE NEW SHAPE (#241). `send-keys -t
  # session:` with an empty index does not error -- it targets the ACTIVE
  # window, which is usually the supervisor, and that is the incident
  # loop-tick.md records under "an empty tmux target hits the ACTIVE window".
  # `session:@` is empty in exactly the same way and must be refused exactly
  # as hard, so this is a POSITIVE check on the shape rather than a
  # non-emptiness one: a candidate whose target is not a real `@N` handle is
  # skipped, never guessed at and never fallen back to the index for. A
  # `lanes.sh` that stopped emitting the second column would then dispatch
  # nothing at all, which is the fail-closed direction.
  if [[ ! "$candidate_target" =~ :@[0-9]+$ ]]; then
    echo "dispatch: skipping candidate '$candidate' -- lanes.sh gave no usable window-id target ('${candidate_target:-}')" >&2
    continue
  fi
  idx="${candidate##*:}"
  wname="${WINDOW_NAME_BY_INDEX[$idx]:-}"
  # agent-dotfiles#212: excluded BEFORE the ledger's free/occupied query, not
  # inside it -- a candidate that contributed to the PR under review is
  # unsafe regardless of what `lane-free` would say, and this way the
  # exclusion is visible on its own rather than folded into that check's
  # result. An ordinary (non-review) dispatch never populates AUTHOR_LANES
  # and never reaches this branch.
  #
  # agent-supervisor#108: the comparison is `lane_relation`, not string
  # equality. A lane id embeds the session's NAME, and renaming the session
  # (done on 2026-08-14 to recover from #102) changed that name for every
  # window at once -- so a contributor row `agent-dotfiles:3` stopped
  # matching the very same window now called `agent-supervisor:3`, and this
  # guard silently admitted a contributor. Only a POSITIVE `different` --
  # both ids parse and their window indices differ -- lets a candidate
  # through; `same` and `unknown` both exclude, which is the same
  # fail-closed posture step 0.5 already takes when the contributor set
  # cannot be resolved at all.
  #
  # agent-supervisor#190: checked against EVERY lane in the set, not one --
  # `lane_relation` short-circuits on the first that is not positively
  # `different` (a match against ANY contributor is disqualifying), so a
  # candidate that made it through against the first ten contributors but
  # matches the eleventh is still excluded, not admitted by majority.
  if [ ${#AUTHOR_LANES[@]} -gt 0 ]; then
    # agent-supervisor#235: measured HERE, off `$candidate_target` -- the
    # window-id form `lanes.sh` already gave this candidate, immune to the
    # renumber that makes `$candidate`'s INDEX half untrustworthy -- so
    # `lane_relation` below can reconcile against the ledger's `pane_id`
    # registry instead of trusting that index against a contributor's. Empty
    # (tmux gone, target stale between `lanes.sh` and here) is passed through
    # unchanged: `lane_relation` already treats a missing pane id as "cannot
    # widen", falling back to the pre-#235 shape check, never to admission.
    candidate_pane_id=$(tmux display-message -p -t "$candidate_target" '#{pane_id}' 2>/dev/null) || candidate_pane_id=""
    MATCHED_CONTRIBUTOR_LANE=""
    MATCHED_CONTRIBUTOR_TASK=""
    for ai in "${!AUTHOR_LANES[@]}"; do
      al="${AUTHOR_LANES[$ai]}"
      # agent-supervisor#631: this contributor's frozen pane_id, when
      # `resolve_pr_contributors` recorded one -- so the comparison below is
      # against THIS task's own pane, never whatever `$al` currently
      # resolves to in the ledger's `lanes` table.
      al_pane_id="${AUTHOR_PANE_IDS[$ai]:-}"
      if [ "$(lane_relation "$candidate" "$al" "$candidate_pane_id" "$al_pane_id")" != different ]; then
        MATCHED_CONTRIBUTOR_LANE="$al"
        MATCHED_CONTRIBUTOR_TASK="${AUTHOR_TASKS[$ai]}"
        break
      fi
    done
    if [ -n "$MATCHED_CONTRIBUTOR_LANE" ]; then
      if [ "$candidate" = "$MATCHED_CONTRIBUTOR_LANE" ]; then
        echo "dispatch: skipping $candidate -- it contributed task $MATCHED_CONTRIBUTOR_TASK to PR #$REVIEWS_PR under review; a contributor does not review their own work" >&2
      elif [ -n "$LANE_REL_POPULATION_CANDIDATE" ] && [ -n "$LANE_REL_POPULATION_OTHER" ] && [ "$LANE_REL_POPULATION_CANDIDATE" != "$LANE_REL_POPULATION_OTHER" ]; then
        # agent-supervisor#292: the populations differ (one side has a tmux
        # window, the other does not), so the pre-#292 "a session rename
        # changes a lane's name" text would be actively wrong here -- there
        # was never a window on both sides to rename. The ledger's own
        # registry (pane_id) could not tell the two apart either, so this is
        # STILL a refusal, just an honest one about why.
        echo "dispatch: skipping $candidate ($LANE_REL_POPULATION_CANDIDATE) -- it cannot be told apart from contributor lane $MATCHED_CONTRIBUTOR_LANE ($LANE_REL_POPULATION_OTHER, task $MATCHED_CONTRIBUTOR_TASK, contributed to PR #$REVIEWS_PR under review); the ledger has no pane_id record proving these are different lanes" >&2
      else
        echo "dispatch: skipping $candidate -- it cannot be told apart from contributor lane $MATCHED_CONTRIBUTOR_LANE (task $MATCHED_CONTRIBUTOR_TASK, contributed to PR #$REVIEWS_PR under review); a session rename changes a lane's name, not which window it is" >&2
      fi
      AUTHOR_SKIPPED=1
      continue
    fi
  fi
  # #241: `--lane` stays the index (the ledger's slot identity) and `--target`
  # becomes the window id. Before this merge both arguments were `$candidate`,
  # so the ledger recorded an index as the thing to address the window with --
  # which is the defect, one seam later.
  CHECK=$("$LEDGER_PYTHON" "$LEDGER_CLI" lane-free --lane "$candidate" --target "$candidate_target" --window-name "$wname" 2>/dev/null) || continue
  if ! grep -qF '"free":true' <<<"$CHECK"; then
    if grep -qF '"known":false' <<<"$CHECK" && [[ ! "$wname" =~ ^free-[0-9]+$ ]]; then
      append_exclusion "dispatch:   $candidate: pane idle, but unknown to the ledger and window name '$wname' is not the free-N migration shape"
    else
      describe_excluded_lane "$candidate" free
    fi
    continue
  fi

  # Test-only instrumentation (agent-dotfiles#184): when set, run this
  # command with the candidate lane as $1 right after it reads free and
  # before this dispatch claims it -- exactly the gap a second dispatcher
  # would need to land a whole competing dispatch in to prove the race.
  # No caller sets this outside tests/supervisor/test_dispatch.sh.
  if [ -n "${DISPATCH_TEST_RACE_HOOK:-}" ]; then
    "$DISPATCH_TEST_RACE_HOOK" "$candidate" || true
  fi

  # CLAIM_LANE is set BEFORE the claim call, not after it (agent-dotfiles#209).
  # The placeholder row is written INSIDE that call, so assigning afterwards
  # left a real window: a TERM landing while the dispatcher waited on this
  # command substitution ran the trap with CLAIM_LANE still empty and the row
  # already committed -- a stranded claim on the one signal path the trap
  # exists to cover. Naming a lane this dispatch did not win costs nothing:
  # `release_lane_claim` is scoped to (lane, THIS dispatch's token,
  # status='created'), so it matches no row unless the claim really succeeded.
  #
  # `--owner-pid $$` is THIS script's pid, not the `cli.py` child's: the child
  # exits the moment the claim is written, so its pid would read dead
  # instantly and step 0.5's reap would clear a live dispatch's claim. `$$` is
  # the parent shell's pid even inside this command substitution.
  CLAIM_LANE="$candidate"
  CLAIM=$("$LEDGER_PYTHON" "$LEDGER_CLI" claim-lane --lane "$candidate" --token "$CLAIM_TOKEN" --owner-pid $$ 2>/dev/null) || { release_lane_claim; continue; }
  if grep -qF '"claimed":true' <<<"$CLAIM"; then
    LANE="$candidate"
    LANE_TARGET="$candidate_target"
    # agent-dotfiles#216: `lane-free` above already resolved this lane's
    # RECORDED harness (from its @hill90_lane_harness pane option, or the
    # ledger row if it was already known) -- carried forward to step 6 so
    # `record-dispatch` gets an explicit --harness instead of re-guessing one
    # from `#{pane_current_command}`, which cannot tell a Node harness like
    # copilot apart from any other. Empty is possible only if `lane-free`'s
    # own JSON shape ever changes underneath this grep; step 6's existing
    # fallback (HARNESS_BY_COMMAND) covers that, unchanged.
    LANE_HARNESS=$(grep -oE '"harness":"[a-z-]*"' <<<"$CHECK" | head -1 | sed -E 's/.*:"([a-z-]*)"/\1/')
    break
  fi
  claim_reason=$(json_field reason "$CLAIM")
  claim_holder=$(json_field holder "$CLAIM")
  [ "$claim_holder" = null ] && claim_holder=""
  if [ -n "$claim_holder" ]; then
    append_exclusion "dispatch:   $candidate: claim refused ($claim_reason; holder $claim_holder)"
  else
    append_exclusion "dispatch:   $candidate: claim refused ($claim_reason; no holder reported; token '$CLAIM_TOKEN' may already exist)"
  fi
  # Lost this candidate to another dispatcher: move on, exactly as before.
  # The release is a no-op in that case (the row is the winner's, not ours)
  # and only bites when the claim committed but its result did not come back
  # readable -- which would otherwise leak a claim only the reap could clear.
  release_lane_claim
done < <("$HERE/lanes.sh" --free "$SESSION" 2>/dev/null)

if [ -z "$LANE" ]; then
  if [ -n "$AUTHOR_SKIPPED" ]; then
    # AUTHOR_TASKS is guaranteed non-empty here: AUTHOR_SKIPPED is only ever
    # set inside the loop above, which only runs its exclusion branch when
    # AUTHOR_LANES (and so its parallel AUTHOR_TASKS) is non-empty.
    # Wording kept verbatim from before #190 ("no free lane other than the
    # author of PR", "an author never reviews their own PR") -- tests predating
    # the widening grep for those exact phrases, and they are still literally
    # true: every excluded candidate matched SOME lane in the contributor set.
    CONTRIBUTOR_TASKS_JOINED=$(IFS=,; echo "${AUTHOR_TASKS[*]}")
    echo "dispatch: no free lane other than the author of PR #$REVIEWS_PR (tasks $CONTRIBUTOR_TASKS_JOINED) -- not dispatching its review #$ISSUE_ARG" >&2
    echo "dispatch: an author never reviews their own PR, even when it is the only free lane" >&2
    if [ -n "$REVIEWS_PR_INFERRED" ]; then
      echo "dispatch: --reviews-pr was never passed; PR #$REVIEWS_PR was INFERRED from $INFERRED_FROM -- if this is not a review, re-run with --not-a-review (agent-supervisor#101)" >&2
    fi
  fi
  echo "dispatch: no free lane in session '$SESSION' -- not dispatching #$ISSUE_ARG" >&2
  echo "dispatch: the ledger must say a lane is free to be dispatchable --" >&2
  echo "dispatch: one it has never seen is backfilled only if named 'free-N'; one it knows is occupied stays occupied regardless of name" >&2
  echo "dispatch: a lane that read free just now may have already been claimed by another dispatcher" >&2

  LANE_ROWS_JSON=$("$HERE/lanes.sh" --json "$SESSION" 2>/dev/null || printf '[]')
  while IFS=$'\t' read -r diag_idx diag_state; do
    [ -n "$diag_idx" ] || continue
    # agent-dotfiles#239: this used to compare $diag_idx to
    # ${LANES_SUPERVISOR_WINDOW:-1} -- a window INDEX, unstable under
    # renumber-windows on, the exact defect this issue is about. lanes.sh is
    # the sole authority on which window is the supervisor's (session-defaults.sh's
    # id-based `supervisor_window_id`, #239's fix there) and already emits
    # `state=supervisor` for it; asking IT rather than re-deriving a second,
    # independently stale answer here is the fix, not a workaround.
    [ "$diag_state" = supervisor ] && continue
    [ "$diag_state" = free ] && continue
    describe_excluded_lane "$SESSION:$diag_idx" "$diag_state"
  done < <(printf '%s' "$LANE_ROWS_JSON" | "$LEDGER_PYTHON" -c 'import json,sys
for row in json.load(sys.stdin):
    print("{}\t{}".format(row.get("window", ""), row.get("state", "")))' 2>/dev/null)

  if [ -n "$EXCLUSION_LINES" ]; then
    echo "dispatch: lane exclusion diagnostics:" >&2
    printf '%s' "$EXCLUSION_LINES" >&2
  else
    echo "dispatch: lane exclusion diagnostics: no lane rows were readable from lanes.sh" >&2
  fi

  if [ -n "$SUGGEST_RECORD_COMPLETION" ]; then
    completion_lane="${SUGGEST_RECORD_COMPLETION%%	*}"
    completion_task="${SUGGEST_RECORD_COMPLETION#*	}"
    echo "dispatch: suggested recovery: inspect $completion_lane; if task $completion_task finished but never signalled, run:" >&2
    echo "dispatch:   $LEDGER_PYTHON $LEDGER_CLI record-completion --lane $completion_lane --note '<what finished>'" >&2
  elif [ -n "$SUGGEST_RELEASE_CLAIM" ]; then
    claim_lane="${SUGGEST_RELEASE_CLAIM%%	*}"
    claim_token="${SUGGEST_RELEASE_CLAIM#*	}"
    echo "dispatch: suggested recovery: $LEDGER_PYTHON $LEDGER_CLI release-lane-claim --lane $claim_lane --token $claim_token" >&2
  else
    echo "dispatch: no ledger surgery suggested; inspect or wait on panes whose state is not ready" >&2
  fi
  "$HERE/lanes.sh" "$SESSION" >&2
  exit 1
fi

# The refusal above is about there being no lane. This one is about not
# knowing WHERE the lane is, and it is the same guard the loop applies per
# candidate, restated once for the winner so that no path can reach a tmux
# call with an unusable target (#241). Nothing has been claimed on GitHub or
# created on disk yet, so refusing here is still free -- and the alternative
# is `send-keys -t session:` landing in the active window, which is the
# supervisor.
if [[ ! "$LANE_TARGET" =~ :@[0-9]+$ ]]; then
  echo "dispatch: lane $LANE has no usable tmux window-id target ('${LANE_TARGET:-}') -- not dispatching #$ISSUE_ARG" >&2
  echo "dispatch: an empty or index-shaped target is refused: an empty tmux target hits the ACTIVE window, which is the supervisor" >&2
  release_lane_claim
  exit 1
fi

# --- 1.5. flip the default (agent-supervisor#171): a fresh `claude` dispatch
# goes over `claude-print`, not this candidate's tmux pane ------------------
#
# THE ROLES THAT MUST KEEP A LIVE PANE, NAMED HERE, IN CODE (#171's own
# brief asks for this, not left in a comment elsewhere): a lane that must be
# INTERRUPTED mid-turn, a lane that must answer an INTERACTIVE PROMPT (a
# usage-limit dialog, a permission request, a menu), or a lane WATCHED AND
# RESUMED BY A HUMAN directly -- `cli.py`'s own `adapter_for_harness` comment
# names the same three. dispatch.sh's own job -- claim an issue, hand a lane
# a brief, collect a PR -- is never one of them (#255 measured this: "almost
# everything in this estate is dispatch-and-collect"), and there is no lane
# PROPERTY this script can read back out of a pane to auto-detect the three
# roles above -- only the human dispatching knows a given call is one of
# them. So `--live-pane` is a CALLER decision (this dispatch keeps the old
# tmux flow below, unchanged), never something inferred from the pane.
#
# WHY THE CANDIDATE IS RELEASED, NOT USED: `$LANE`/`$LANE_TARGET` are a real
# tmux pane from the fixed, standing pool -- one of the "existing lanes"
# #171's brief says not to touch. Routing THIS dispatch over `claude-print`
# instead means that pane is never respawned, never sent a keystroke, and
# never recorded against -- `release_lane_claim` (already wired to this
# script's EXIT trap) clears its ledger claim the moment this branch exits,
# leaving it exactly as free as it was before the loop above found it, for
# a later dispatch (one that passes --live-pane) to actually use.
#
# WHY GATED TO A PLAIN, SINGLE-ISSUE, NON-PR-SCOPED DISPATCH:
# `dispatch-claude-print.sh` (the mechanism this calls into) does not speak
# `--reviews-pr`'s author-exclusion bookkeeping, `--pr`'s PR-scoped source
# recording, or a comma-joined multi-issue list -- see its own usage
# comment. Falling through to the pre-#171 tmux flow for those three shapes
# is a known, tracked scope boundary (agent-supervisor#171), not a silent
# fallback: nothing has failed yet at this point, so this is a routing
# choice made BEFORE any commitment, the opposite of the forbidden kind of
# fallback (a REAL claude-print failure below never falls back to send-keys
# -- see the `exit $?` a few lines down and dispatch-claude-print.sh's own
# fail-closed header).
if [ "$LANE_HARNESS" = claude ] && [ -z "$LIVE_PANE" ] \
    && [ "${#ISSUES[@]}" -eq 1 ] && [ -z "$REVIEWS_PR" ] && [ -z "$PR" ]; then
  # `dispatch-claude-print.sh` requires <repo> non-empty (its own usage
  # error otherwise); dispatch.sh itself allows [repo] to be omitted and
  # left for `gh`/`claim.sh` to resolve from the working directory. Resolved
  # here the same way, from $REPO_PATH -- and if it cannot be resolved, this
  # falls through to the pre-#171 tmux flow rather than refusing the whole
  # dispatch over a routing decision, not a failure.
  CLAUDE_PRINT_REPO="$REPO"
  if [ -z "$CLAUDE_PRINT_REPO" ]; then
    CLAUDE_PRINT_REPO=$(cd "$REPO_PATH" 2>/dev/null && gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null) || CLAUDE_PRINT_REPO=""
  fi
  if [ -z "$CLAUDE_PRINT_REPO" ]; then
    echo "dispatch: claude lane $LANE selected, but [repo] could not be resolved for claude-print -- falling through to $LANE's own tmux pane for #$ISSUE_ARG (agent-supervisor#171)" >&2
  elif ! command -v claude >/dev/null 2>&1; then
    # FAIL CLOSED AND LOUDLY (#171's own guard): no `claude` binary on PATH
    # means the new default cannot run at all -- this refuses the dispatch
    # rather than silently falling back to $LANE's tmux pane, which would
    # make the ledger's absence of a claude-print row look like a choice
    # instead of a missing binary. `--live-pane` is the way to actually ask
    # for the tmux pane; a missing binary is not that ask.
    echo "dispatch: claude lane $LANE selected but no 'claude' binary on PATH -- refusing rather than falling back to send-keys (agent-supervisor#171); #$ISSUE_ARG was NOT dispatched" >&2
    exit 1
  else
    echo "dispatch: claude lane $LANE selected -- routing #$ISSUE_ARG over claude-print instead (agent-supervisor#171); $LANE stays free for --live-pane work" >&2
    "$HERE/dispatch-claude-print.sh" "$ISSUE_ARG" "$SLUG" "$BRIEF" "$CLAUDE_PRINT_REPO" "$REPO_PATH"
    exit $?
  fi
fi

# --- 2. the claim, before anything else is built --------------------------
# The repo slot is ALWAYS passed, even empty. claim.sh's interface is
# positional -- `take <issue> [repo] [lane]` -- so dropping an empty repo does
# not shorten the argument list, it SHIFTS the lane name into the repo slot.
# `dispatch.sh 95 claim-refuses-closed brief.md` with no repo argument ran
# `gh issue view 95 -R claim-refuses-closed`, which fails, and reported
# `claim: could not assign #95` for an open, unclaimed issue. Indistinguishable
# from a legitimate refusal, and it aborted the dispatch every time.
#
# CLAIMED holds only what actually got claimed, in claim order, so a failure
# partway through a multi-issue list (agent-dotfiles#112) unwinds exactly the
# issues this dispatch took and none it did not touch. Aborting the WHOLE
# dispatch when any one claim fails, rather than proceeding with a partial
# claim, matches the existing "every failure aborts" contract: a lane already
# dispatched to a partial claim would be actively working issues the estate
# cannot see as taken, which is the exact failure #112 was filed over.
#
# agent-supervisor#159: a PR-scoped dispatch (`PR_SCOPED` set) skips this
# loop entirely -- ITS ISSUE(S) STAY CLAIMED BY THE ORIGINAL WORK, on
# purpose. That claim is not this dispatch's to take: #159 was filed
# because `claim.sh take` correctly refuses here (the issue IS claimed,
# by the in-flight work that opened the PR under review/fix), and that
# correct refusal was what pushed dispatch to a ledger-invisible tmux
# hand-off. Step 0.6 above is this dispatch's real ownership check, against
# the PR, not the issue; `CLAIMED` stays empty so `release_claim` (declared
# earlier, alongside the trap that now also covers it -- #572) is already a
# correct no-op for this path with no special-casing needed there.
CLAIM_FAILED=""
if [ -n "$PR_SCOPED" ]; then
  echo "dispatch: PR-scoped dispatch (PR #$PR_SCOPED) -- issue(s) ${ISSUES[*]} left claimed by the original work, no GitHub assignee taken" >&2
else
  for i in "${ISSUES[@]}"; do
    # agent-supervisor#572, the same shape #209 round 2 found for CLAIM_LANE:
    # `$i` is appended to CLAIMED BEFORE `claim.sh take` runs, not after it
    # returns true. `claim.sh take` is a foreground child, so a TERM landing
    # while dispatch.sh is blocked in `wait()` on it is only delivered once
    # that child exits -- and bash runs the pending trap right then, before
    # this loop body gets to resume at `CLAIMED+=("$i")`. Appending first
    # means the trap's release_claim always finds the issue it just claimed,
    # even when the signal lands in that exact gap; popped back off below if
    # the claim never actually landed, so a failed/refused claim is never
    # handed to release_claim as something to unwind.
    CLAIMED+=("$i")
    if "$HERE/claim.sh" take "$i" "$REPO" "$WINDOW_NAME"; then
      :
    else
      unset 'CLAIMED[${#CLAIMED[@]}-1]'
      echo "dispatch: #$i is not available -- pick different work" >&2
      CLAIM_FAILED=1
      break
    fi
  done
fi

if [ -n "$CLAIM_FAILED" ]; then
  release_claim
  release_lane_claim
  exit 1
fi

# --- 3. the worktree. Not optional, not recoverable ------------------------
# worktree.sh prints the path on stdout and git's progress on stderr, so the
# two are captured separately: the path is consumed here, the diagnostics are
# only shown if it fails. Called ONCE -- a retry would leave an orphan.
WORKTREE_ERR=$(mktemp)
WORKTREE=$("$HERE/worktree.sh" new "${ISSUE}-${SLUG}" "$REPO_PATH" 2>"$WORKTREE_ERR")
rc=$?
if [ "$rc" -ne 0 ] || [ -z "$WORKTREE" ] || [ ! -d "$WORKTREE" ]; then
  echo "dispatch: worktree.sh new failed for #$ISSUE_ARG in $REPO_PATH -- NOT dispatching" >&2
  echo "dispatch: a lane with no worktree works in the shared checkout, which is #73" >&2
  sed 's/^/  /' "$WORKTREE_ERR" >&2
  rm -f "$WORKTREE_ERR"
  release_claim
  release_lane_claim
  exit 1
fi
rm -f "$WORKTREE_ERR"

# agent-supervisor#373: `worktree.sh new` just created branch `lane/<slug>`
# via `git worktree add -b` -- that call FAILS if the branch already existed,
# so reaching this line means THIS invocation, and no earlier one, created
# it (worktree.sh's `done` only ever removes the WORKTREE, never the branch
# -- see its own header -- which is what left `lane/<issue>-<slug>` behind on
# every refusal below until now). Recorded once, here, so every cleanup path
# past this point deletes exactly the branch this dispatch made and nothing
# a caller or an earlier lane already owned.
DISPATCH_BRANCH="lane/${ISSUE}-${SLUG}"
cleanup_dispatch_branch() {
  # Guarded by show-ref, same posture would-revert.sh's own scratch-branch
  # cleanup already takes: only delete a branch that still exists under the
  # exact name this dispatch created, never a branch a caller or a later
  # dispatch retargeted onto (#373 point 3 -- clean up only what THIS
  # invocation created).
  if git -C "$REPO_PATH" show-ref --verify --quiet "refs/heads/$DISPATCH_BRANCH"; then
    git -C "$REPO_PATH" branch -D "$DISPATCH_BRANCH" >/dev/null 2>&1 \
      || echo "dispatch: could not remove stray branch $DISPATCH_BRANCH -- remove it by hand: git -C $REPO_PATH branch -D $DISPATCH_BRANCH" >&2
  fi
}

# `abort_send` is defined here, right after the worktree exists, because step
# 3.5 below is now the first thing that can fail with both a claim and a
# worktree already committed -- the same shape every later failure in this
# script already has. Moved up from just after the rename (where it used to
# sit) with no change to its body.
abort_send() {
  echo "dispatch: $1" >&2
  cleanup_worktree=1
  lane_current_path=$(tmux display-message -p -t "$LANE_TARGET" '#{pane_current_path}' 2>/dev/null || true)
  case "$lane_current_path" in
    "$WORKTREE"|"$WORKTREE"/*)
      if dispatch_rehome_lane "$LANE_TARGET" "$REPO_PATH" "$LANE_HARNESS" >/dev/null 2>&1; then
        echo "dispatch: re-homed $LANE out of $WORKTREE before rollback cleanup" >&2
      else
        cleanup_worktree=0
        echo "dispatch: leaving worktree $WORKTREE in place because $LANE still appears to be inside it" >&2
        echo "dispatch: recover with: $0 --rehome-lane $LANE_TARGET '$REPO_PATH' ${LANE_HARNESS:-}" >&2
      fi
      ;;
  esac
  if [ "$cleanup_worktree" = 1 ]; then
    "$HERE/worktree.sh" done "$WORKTREE" >/dev/null 2>&1
    # agent-supervisor#373: only once the worktree itself is actually gone --
    # a branch still checked out by a worktree cannot be deleted anyway, and
    # the branch is left alone (like the worktree) in the "$LANE still
    # appears to be inside it" case above, since that is not a clean abort.
    cleanup_dispatch_branch
  fi
  release_claim
  release_lane_claim
  # agent-dotfiles#209 round 2. Past step 4.5 the lane claim is LIVE and
  # `release_lane_claim` above is a deliberate no-op, so this abort leaves the
  # lane held while releasing the issue and the worktree. That is the
  # fail-closed side of moving the commit point to the send, and an operator
  # must not have to infer it from a silent absence -- an abort that says
  # "NOT dispatched" while a lane stays occupied is exactly the kind of
  # mismatch between message and state this estate keeps filing bugs about.
  if [ -n "${CLAIM_COMMITTED:-}" ]; then
    echo "dispatch: $LANE STAYS HELD -- the brief may have gone live in it, and a lane wrongly freed is not recoverable." >&2
    echo "dispatch: CHECK THE PANE. If the lane finished but never signalled, write the real completion with:" >&2
    # This row is a claim, not a task ($WINDOW_NAME is not its id) -- --lane
    # resolves whichever row shape holds it, same as cancel-open-task below.
    echo "dispatch:   $LEDGER_PYTHON $LEDGER_CLI record-completion --lane $LANE --note <note>" >&2
    echo "dispatch: If nothing real ran and the live brief must be discarded, free it with:" >&2
    echo "dispatch:   $LEDGER_PYTHON $LEDGER_CLI cancel-open-task --lane $LANE" >&2
  fi
  exit 1
}

# --- 3.1 the worktree must actually BE $REPO (#17) --------------------------
# claim.sh (step 2, above) claimed the issue against $REPO. worktree.sh (step
# 3, above) built the worktree from $REPO_PATH. Those two arguments encode
# the same fact -- which repository this dispatch is for -- and until now
# nothing compared them, though both are known by this point. Skipped only
# when REPO was never given: `gh` then resolves the claim from REPO_PATH
# itself, so there is nothing for the two to disagree about.
if [ -n "$REPO" ]; then
  WORKTREE_ORIGIN=$(git -C "$WORKTREE" remote get-url origin 2>/dev/null)
  # Normalize `git@github.com:owner/name.git`, `https://github.com/owner/name`
  # and a bare `owner/name` down to the same OWNER/NAME shape $REPO is given
  # in -- the same awk this repo's watchdog.sh already uses to read its own
  # identity from `origin`.
  WORKTREE_REPO=$(sed 's/\.git$//' <<<"$WORKTREE_ORIGIN" | awk -F'[:/]' 'NF>=2{print $(NF-1)"/"$NF}')
  if [ -z "$WORKTREE_REPO" ] || [ "$WORKTREE_REPO" != "$REPO" ]; then
    abort_send "worktree $WORKTREE has origin '${WORKTREE_ORIGIN:-<unreadable>}' (repo '${WORKTREE_REPO:-unknown}'), not the claimed repo '$REPO' -- refusing rather than drop a lane claimed against one repository into a worktree of another (#17); #$ISSUE_ARG was NOT dispatched"
  fi
fi

# --- 3.2 the pre-dispatch collision check (agent-supervisor#291) -----------
# as#263 and as#266 independently wrote the same fix to the same file
# (scripts/supervisor/quota-watch.sh) -- one lane's work was entirely
# wasted, plus a review, plus a merge decision. This is the refusal that
# catches that shape before it repeats: does #$ISSUE_ARG's own file set (the
# brief, its branch, and the PR it is scoped to, if any -- see
# collision-check.sh's own header) overlap the file set an ALREADY in-flight
# lane is actually touching (measured by `git diff`, not guessed).
#
# HERE, not earlier: this is the first point a real worktree exists for
# `_files_changed_in_worktree` to read (signal 2, a resumed branch's own
# content) and past step 3.1 so the worktree is already confirmed to BE
# $REPO. `abort_send` (defined right after the worktree was built, above)
# is reused rather than reimplemented -- it already unwinds the claim, the
# lane claim, and the worktree in the right order, and already handles a
# lane whose pane cwd is somehow already inside $WORKTREE.
#
# UNKNOWN (no file signal at all) ALLOWS and says so -- most issues never
# name a file, and refusing on "I could not tell" would refuse nearly every
# dispatch; see collision-check.sh's header for why this is the one place
# the fail-closed posture inverts, deliberately. A real refusal is fatal:
# a duplicated PR costs far more than a refused dispatch, and `--force`
# exists for the case an operator genuinely intends the overlap.
COLLISION_OUT=$("$HERE/collision-check.sh" check \
  --issue "$ISSUE_ARG" --brief "$BRIEF" --worktree "$WORKTREE" \
  --repo-path "$REPO_PATH" --repo "$REPO" \
  --exclude-lane "$LANE" \
  ${PR_SCOPED:+--pr "$PR_SCOPED"} \
  ${COLLISION_FORCE:+--force} 2>&1)
COLLISION_RC=$?
if [ "$COLLISION_RC" -ne 0 ]; then
  # A refusal, same as every other guard's stderr above -- agent-dotfiles#199
  # only requires SILENCE on a SUCCESSFUL dispatch; this one is not one.
  sed 's/^/dispatch: collision-check: /' <<<"$COLLISION_OUT" >&2
  abort_send "#$ISSUE_ARG's files collide with an in-flight lane -- NOT dispatched. Re-run with --force if this overlap is known and intended (agent-supervisor#291)"
fi
# ALLOW (no-conflict, unknown, or forced) -- on stdout, not stderr:
# agent-dotfiles#199 requires stderr silent on a successful dispatch, and
# "say UNKNOWN, don't let it read as nothing" (the issue's own words) only
# requires this is SAID, not that it is said on stderr specifically.
sed 's/^/dispatch: collision-check: /' <<<"$COLLISION_OUT"

# WHAT IS TYPED INTO THE PANE STAYS SHORT, AND HERE IS THE MEASURED REASON.
#
# This message is typed into the lane's input box and then verified by reading
# the pane back. The box shows only its last few rows: past a certain length it
# scrolls INTERNALLY, the head disappears from the visible region, and
# `capture-pane` cannot see it however correct the delivery was.
#
# Measured against a real Claude Code TUI at 80x24 (throwaway tmux server, one
# probe per length, never a live lane):
#
#   ~450 chars -> head visible          ~500 chars -> HEAD LOST
#
# The first version of the deliverable contract lived in this string and took
# it to 610 characters. That failed verification 4 times out of 4 at 80x24 and
# passed 3/3 at 126x60 -- and `free-9` and `free-10` are 80x24, so it broke
# dispatch to real lanes while every stub test stayed green (#118 review).
#
# So the contract is NOT in this string. The message is back to 389 characters
# with representative paths, and `MESSAGE_BUDGET` below pins that this stays
# true: the paths are the bulk of it and they vary, so the margin is thin and
# it is enforced rather than remembered.
#
# Built and budget-checked here, BEFORE step 3.5 relaunches the harness: an
# over-budget message means this dispatch is refused regardless of anything
# below, and there is no reason to kill and relaunch a lane's harness only to
# then abort without ever typing into it.
MESSAGE="Read $BRIEF and do exactly what it says. That file is your complete brief. Do all of your work in the worktree at $WORKTREE -- it is yours, already branched; never work in the shared checkout at $REPO_PATH."

# The head of the message is what an internally-scrolling box hides first, so
# the length that matters is the whole string. 450 is the measured cliff at
# 80x24; 430 leaves a little room for a slow repaint eating a row.
MESSAGE_BUDGET="${DISPATCH_MESSAGE_BUDGET:-430}"
if [ "${#MESSAGE}" -gt "$MESSAGE_BUDGET" ]; then
  echo "dispatch: the brief message is ${#MESSAGE} chars, over the ${MESSAGE_BUDGET}-char budget for an 80x24 lane." >&2
  echo "  Past ~450 the input box scrolls the head out of view, capture-pane cannot see it," >&2
  echo "  and dispatch aborts even though the message arrived. Shorten it, or put the text in" >&2
  abort_send "the brief message is over the ${MESSAGE_BUDGET}-char budget -- #$ISSUE_ARG was NOT dispatched"
fi

# --- 3.5. put the lane IN its worktree, at the OS level (#15) --------------
#
# Measured on a live codex lane (#15): `lsof -a -p "$(tmux list-panes ... pane_pid)" -d cwd`
# resolved to the SHARED checkout, never the worktree just created above,
# even though the brief typed into the pane a few lines below names the
# worktree and tells the lane to work only there. The harness process
# occupying the pane had its cwd fixed the moment it was execed -- at lane
# creation (`bootstrap-session.sh`'s `new-window -c "$WORKDIR"`) or its last
# relaunch -- and nothing after that point can change a running process's
# cwd from outside it. Typing `cd $WORKTREE` into the pane does not touch
# that cwd: past this point in a lane's life the pane is not a shell, it is
# the harness's own chat input box, and text sent there is a PROMPT, read and
# acted on by the agent, never executed by a shell. A Claude lane usually
# gets away with it because it reasons in absolute paths; a codex lane does
# not, which is exactly how #15 was caught.
#
# So the fix is not a stronger sentence in the brief (#15 already had one,
# and it still failed -- the shape #73/#81/#263 keep closing). It is the same
# mechanism `restore.sh` already uses to put a restored lane in the right
# directory: `-c <dir>` on the tmux call that creates the pane's process,
# which sets the REAL, OS-level starting directory before anything runs in
# it. `restore.sh` gets that for free because it always creates a fresh
# window. `dispatch.sh` reuses an existing lane's window, so the equivalent
# here is `respawn-pane -k -c "$WORKTREE"`: kill whatever the harness left
# running (the pool only ever offers this step a FREE lane -- ledger and
# lanes.sh both said so above -- so there is no live conversation to lose,
# same as `restore.sh`'s own "no open task -> restore fresh" branch) and
# start the harness AS the pane's new process, its adapter's own
# `HARNESS_LAUNCH_CMD` given to the same tmux call as its argv (#236) --
# a real shell command, run as the pane's process directly, which is the one
# place in this script's lifetime a `cd`-shaped instruction is actually
# obeyed by something other than the agent choosing to obey it.
#
# Fails closed: a harness this dispatch cannot identify, or one whose
# adapter records no launch command, is refused rather than dispatched with
# an unverifiable cwd -- the exact failure mode #15 is about, produced on
# purpose instead of by accident.
HARNESS_HIDX=""
if [ -n "$LANE_HARNESS" ]; then
  HARNESS_HIDX=$(harness_index_for_name "$LANE_HARNESS") || HARNESS_HIDX=""
fi
if [ -z "$HARNESS_HIDX" ] || [ -z "${H_LAUNCH_CMD[$HARNESS_HIDX]:-}" ]; then
  abort_send "no launch command recorded for harness '${LANE_HARNESS:-unknown}' in $LANE -- cannot relaunch it in the worktree, so its cwd cannot be verified correct (#15); #$ISSUE_ARG was NOT dispatched"
fi
LAUNCH_CMD="${H_LAUNCH_CMD[$HARNESS_HIDX]}"

# agent-dotfiles#255: a harness whose adapter sets H_LAUNCH_TAKES_PROMPT
# (codex, see harness/codex.sh) does not treat the first message TYPED into
# a live pane as a real turn -- so for this harness's fresh-lane path there
# is no typed message at all. The short "Read $BRIEF ..." pointer built
# above (MESSAGE) is folded into LAUNCH_CMD itself, as the harness's own
# documented launch-time PROMPT argument, and step 4 below skips typing
# entirely for it -- see PROMPT_IN_LAUNCH.
#
# `printf %q` quotes MESSAGE for re-parsing by the shell respawn-pane hands
# LAUNCH_CMD to (it contains no embedded newline -- MESSAGE is built as one
# line above -- so this is a plain single-token quoting job, not the
# newline-as-Enter hazard send.sh's verified_type refuses).
PROMPT_IN_LAUNCH=0
if [ "${H_LAUNCH_TAKES_PROMPT[$HARNESS_HIDX]:-0}" = 1 ]; then
  PROMPT_IN_LAUNCH=1
  LAUNCH_CMD="$LAUNCH_CMD $(printf '%q' "$MESSAGE")"
fi

# agent-supervisor#236: LAUNCH_CMD is handed to respawn-pane as its own
# argv, so it becomes the pane's PROCESS directly -- it is never typed into
# whatever the respawn produces. The prior shape here was `respawn-pane -k`,
# sleep one second, then a blind `send-keys "$LAUNCH_CMD" Enter`: exactly the
# mechanism #236 reports, a lane found blocked on a Claude Code menu offering
# to run a pasted, unpinned launch command (the same shape #120/#135 already
# refuse to let this file's OWN launch literal be -- see harness/claude.sh),
# because nothing checked what was listening for those keystrokes a second
# after the respawn. One tmux call now does what three did. There is no
# window in which the launch command exists as text for something else to
# receive, so DISPATCH_RESPAWN_SETTLE -- the sleep that used to buy that
# window time to become a shell before it was typed into -- has nothing left
# to settle and is no longer read on this path. H_SEND_LITERAL is likewise
# moot here: it governed how `send-keys` parsed `$LAUNCH_CMD` for tmux key
# names, which does not apply to a shell command handed to respawn-pane's
# own argv.
#
# This still only runs against a lane `lanes.sh` and the ledger have already
# said is FREE (checked above, same as before #236) -- `respawn-pane -k`
# kills whatever is in the pane, and that hazard is unchanged by this fix;
# it is guarded by staying on the free-lane path, not by anything new here.
#
# agent-supervisor#166/#421: this is the routine, everyday respawn -- every
# ordinary dispatch of a free lane onto its next issue goes through here far
# more often than a lane's first boot or a post-crash restore, so it gets the
# same tmux guard on PATH ahead of the real binary those two sites already
# install. Best effort: an install failure degrades to dispatching without
# the guard rather than refusing the dispatch outright.
GUARD_BIN=""
GUARD_BIN="$(install_tmux_guard 2>&2)" || GUARD_BIN=""
if [ -n "$GUARD_BIN" ]; then
  LAUNCH_CMD="PATH=\"$GUARD_BIN:\$PATH\" $LAUNCH_CMD"
fi
if ! tmux respawn-pane -k -t "$LANE_TARGET" -c "$WORKTREE" "$LAUNCH_CMD" 2>/dev/null; then
  abort_send "tmux respawn-pane failed for $LANE -- could not put it in its worktree; #$ISSUE_ARG was NOT dispatched"
fi

# Give the harness time to actually start before anything else is typed at
# it -- a cold process start is slower than the UI repaint `/clear` waits out
# below, so this gets its own, longer default. Still load-bearing after
# #236: the harness's startup wall-clock is unchanged by how it was started,
# and `verified_preclear` just below still sends `/clear` + Enter before it
# has read the pane back even once -- this sleep is what keeps that send
# from landing on a splash screen instead of a ready input box.
sleep "${DISPATCH_LAUNCH_SETTLE:-3}"

# --- 3.6 accept a fresh harness's own one-time menu, if it has one --------
# agent-dotfiles#255. A cold codex process launched into a directory it has
# never seen (every worktree this dispatch just created, every time) opens
# on its own directory-trust menu, not the ordinary chat box the next steps
# expect -- see `send.sh`'s `verified_dismiss_menu` for the live-measured
# mechanics and why this is #255's actual root cause: the pane's first
# Enter goes to that menu's default selection, not to `/clear` or a brief,
# which is the same "first Enter lands somewhere else" shape #255 reported
# as a whole brief consumed as a session TITLE.
#
# `H_OPTION_ROW_RE`/`H_MENU_TAIL` are the SAME per-harness adapter values
# `lanes.sh` already keys its own menu-blocked reading on -- nothing new is
# defined here, and a harness whose adapter names no such menu (Claude,
# Copilot today) gets an empty regex, which returns success on the first
# read without sending anything at all. Fails closed: a menu still showing
# after every retry aborts the dispatch rather than typing a brief onto it.
if ! verified_dismiss_menu "$LANE_TARGET" "${H_OPTION_ROW_RE[$HARNESS_HIDX]:-}" "${H_MENU_TAIL[$HARNESS_HIDX]:-6}" \
     --settle "${DISPATCH_MENU_SETTLE:-2}" --retries "${DISPATCH_MENU_RETRIES:-5}"; then
  abort_send "a startup menu never cleared in $LANE -- #$ISSUE_ARG was NOT dispatched (check the pane by hand)"
fi

# --- 4. the lane is told what it is doing, then given the work ------------
if ! tmux rename-window -t "$LANE_TARGET" "$WINDOW_NAME" 2>/dev/null; then
  echo "dispatch: could not rename $LANE -- not dispatching #$ISSUE_ARG" >&2
  "$HERE/worktree.sh" done "$WORKTREE" >/dev/null 2>&1
  cleanup_dispatch_branch
  release_claim
  release_lane_claim
  exit 1
fi

# The standing deliverable contract (#117), written into the BRIEF rather than
# typed at the pane. A lane completed #112 correctly -- tests green,
# mutation-checked, committed -- and stopped, because the brief never said to
# push. It was right to be literal. From outside, a lane that finished without
# shipping is indistinguishable from one that did nothing: no PR, no comment,
# issue still claimed, and the work living only as an unpushed commit in a
# temporary worktree one cleanup away from being lost.
#
# Still structural, which is the whole point of #117: the DISPATCHER writes it
# on every dispatch, so it does not depend on whoever wrote the brief
# remembering -- the mechanism that failed in #114. It moved out of the typed
# message because that string has a hard length budget and this text does not
# fit in it; the brief file has no such limit and is the thing the lane is told
# to read.
#
# It also stops the message contradicting itself. Typed at the pane, the
# dispatcher said "that file is your COMPLETE brief" and then added an
# instruction that was not in it -- and for a read-only review brief, "push
# your branch and open a PR" contradicted the brief's own first line. In the
# file it sits with the rest of the instructions and defers to them.
CONTRACT_MARKER="<!-- dispatch:deliverable-contract -->"
if ! grep -qF "$CONTRACT_MARKER" "$BRIEF" 2>/dev/null; then
  cat >>"$BRIEF" <<EOF || abort_send "could not append the deliverable contract to $BRIEF -- #$ISSUE_ARG was NOT dispatched"

$CONTRACT_MARKER
## Delivering this work

Added by \`dispatch.sh\` on every dispatch, not by the brief's author.

Unless this brief says otherwise, when you are finished:
**push your branch and open a PR**.
If you produced no code -- a review, an investigation, an options paper --
**post your findings as a comment** on the issue or PR the brief names.

Do not stop with the work only in your worktree. From outside, a lane that
finished without shipping is indistinguishable from a lane that did nothing:
unshipped work looks exactly like no work, and the worktree is temporary.
EOF
fi

# agent-dotfiles#237: the instant BEFORE the `/clear` that starts this lane's
# new conversation. `harness_session.py` uses it to tell the transcript this
# dispatch created from every other transcript on the machine -- see that
# module for why "began after this moment" is one of the three tests it
# requires, and why nothing weaker resolves a lane in this estate.
DISPATCH_SEND_EPOCH=$(date +%s)

# `/clear` first: an author reviewing their own PR is not an independent
# reviewer, and a lane carrying the last task's context is not a fresh one.
#
# `C-u` immediately before it, agent-supervisor#178/#179: this is the FIRST
# thing dispatch.sh ever types into a lane it did not just create, and #179
# found exactly this box holding a leftover, unsubmitted prompt from
# something else entirely (`merge the PR`, sitting in the author lane's
# input box). Typing `/clear` on top of that appends to it rather than
# clearing anything -- `/clear` is a plain string to the pane, not a key
# tmux interprets -- and #178's diagnosis is that the Enter which follows
# then may not submit either.
#
# VERIFIED, not bare, as of agent-supervisor#193: `/clear`'s own Enter can be
# swallowed exactly the way #178 already found a brief's Enter can be, and
# that failure used to be invisible -- the retyped brief landed glued onto
# the unsubmitted "/clear", and the proof-token check below (an AND-of-
# substrings check with no notion of position) still read it as `landed`
# because its tokens were still true substrings of the corrupted line.
# `verified_preclear` (send.sh) confirms the screen actually came back
# BLANK -- the input box reads `empty`, not `text` or `unknown` -- before
# anything else is ever typed. This is still not `verified_type`/
# `verified_submit`: `/clear` blanks the whole screen, which the proof-token
# check was never built to read through (see that function's own header) --
# confirming the blank is the whole of what can be confirmed here.
#
# Defaults raised 2 -> 5 (settle) and 2 -> 6 (retries), agent-dotfiles#255,
# landed by #447: Jon reported three consecutive dispatches refused today
# with "`/clear` did not blank <lane>'s screen" at the old defaults, on real,
# loaded panes -- the guard firing correctly (#193's whole point), just
# against too small a budget to survive the load it was actually up against.
# Raising to these values, by hand, made all three succeed. The
# `Escape`-before-`C-u` fix just above (send.sh) targets the same failure by
# a different mechanism -- a real key instead of a bigger budget -- and may
# make some of this margin unnecessary; it is kept anyway because it costs
# latency only on the preclear path (2-5s x up to 6 retries, worst case, once
# per dispatch), and a lane wrongly refused here costs nothing a retry
# doesn't fix, while a `/clear` this guard incorrectly waved through is
# exactly #255's silent-success shape.
#
# agent-dotfiles#255: skipped entirely when PROMPT_IN_LAUNCH -- that harness's
# pane was just started fresh by respawn-pane above with the brief-pointer
# message already folded into its own argv, so there is no live conversation
# to clear and nothing typed yet for a corrupted `/clear` to glue onto.
if [ "$PROMPT_IN_LAUNCH" != 1 ]; then
  if ! verified_preclear "$LANE_TARGET" \
       --settle "${DISPATCH_SETTLE:-5}" --retries "${DISPATCH_PRECLEAR_RETRIES:-6}"; then
    if [ "$SEND_STATUS" = send_failed ]; then
      abort_send "send-keys to $LANE failed -- #$ISSUE_ARG was not dispatched"
    fi
    abort_send "/clear did not blank $LANE's screen -- #$ISSUE_ARG was NOT dispatched (check the pane by hand)"
  fi
fi

# Type, verify, THEN submit -- send.sh's verified_type, extracted from what
# used to be this loop. The verification is why the Enter is a separate call
# (verified_submit, below the ledger commit): what the pane actually shows
# is the only evidence that the keys landed.
#
# Check BOTH ENDS of the message plus the worktree path -- the proof tokens
# below. The head is what a dropped prefix eats first (observed live,
# 2026-08-11), and it is also the first thing an over-long message hides by
# scrolling -- so checking the head alone conflates "arrived and is visible"
# with "fits". The tail is the part that stays visible under scrolling, so it
# is the half that still reports honestly when the box is full; checking only
# the tail would pass a dropped prefix, which is the failure this loop exists
# for. Both, or neither is evidence.
#
# The tail token is the closing phrase plus $REPO_PATH, not the path alone:
# the harness prints the working directory in its own header, so the bare
# path matches ordinary pane furniture and would pass on a blank pane.
# send.sh strips spaces and newlines from both sides before matching, because
# a real pane wraps a long path across lines and indents the continuation.
#
# The head token is `--proof-head`, not `--proof`, as of agent-supervisor#193:
# the ORIGINAL `--proof "Read $BRIEF"` only checked that the string appeared
# somewhere on the pane, and "somewhere" is exactly what let a corrupted
# `/clearRead $BRIEF...` -- `/clear`'s own Enter swallowed, the retyped brief
# glued onto the unsubmitted line -- read as `landed`: `Read $BRIEF` was
# still a true substring, just not the start of anything. `--proof-head`
# anchors it to the START of the input box's own content instead (see
# send.sh's `_send_head_matches`), so this exact corruption now fails the
# check and falls into the C-u-and-retype loop below rather than shipping.
#
# `--preclear`, as of agent-supervisor#240: `verified_preclear` above already
# confirmed this box empty, but that confirmation covers the instant it was
# taken, not the instant `send-keys` below actually runs -- six lanes were
# measured holding live unsent text that `lanes.sh` had classified `free`,
# `busy` or `broken`, never `unsent`, which is exactly the shape of text
# landing in a gap no earlier check could see. `--proof-head` already
# detects a glued brief and the retry above already recovers it, but that
# recovery costs a whole extra type-and-check round trip every time, and it
# is not the only thing that could go wrong in that gap. A classification --
# `verified_preclear`'s included -- can be wrong; one more `C-u`, sent
# immediately before the keys that matter, cannot be.
# agent-dotfiles#255: skipped when PROMPT_IN_LAUNCH, same reason as the
# preclear above -- the message is already this harness's launch argv, never
# a live pane's typed input, so there is nothing here to type or verify.
if [ "$PROMPT_IN_LAUNCH" != 1 ]; then
  if ! verified_type "$LANE_TARGET" "$MESSAGE" \
       --settle "${DISPATCH_SETTLE:-1}" --retries 2 --preclear \
       --proof-head "Read $BRIEF" \
       --proof "$WORKTREE" \
       --proof "never work in the shared checkout at $REPO_PATH."; then
    if [ "$SEND_STATUS" = send_failed ]; then
      abort_send "send-keys to $LANE failed -- #$ISSUE_ARG was not dispatched"
    fi
    abort_send "the brief did not land intact in $LANE -- #$ISSUE_ARG was NOT dispatched (check the pane by hand)"
  fi
fi

# --- 4.5 THE POINT OF NO RETURN, AND IT IS THE SEND -----------------------
# agent-dotfiles#209 round 2. `CLAIM_COMMITTED` used to be set ~70 lines below
# this, after step 5's confirmation loop -- and step 5 costs up to
# DISPATCH_CONFIRM_TRIES x DISPATCH_SETTLE (10s by default) of wall clock. So
# for that whole window the lane was renamed and the brief was ABOUT to be
# live, while both cleanup paths still believed the dispatch was unwindable. A
# SIGTERM landing there ran the trap and deleted the claim, and
# `lane_available` answered True for a lane that was actively working -- #102's
# shape produced BY the cleanup, which step 6's own comment below says in its
# own words must not happen. Reproduced against the stubs, both directions, in
# tests/supervisor/test_dispatch.sh.
#
# So the commit happens HERE, before the Enter rather than after the
# confirmation, because "the brief is live" starts at the submit and not at
# the moment the bookkeeping finishes noticing.
#
# IT IS WRITTEN TO THE LEDGER, not just to this shell. `CLAIM_COMMITTED` below
# is only a fast path for the trap; it dies with this process, and the SIGKILL
# case is exactly the one where this process stops existing while the pane
# keeps working. `commit-lane-claim` moves the placeholder to a status both
# `release_lane_claim` and `reap-lane-claims` refuse to touch, so the
# protection survives a kill that no shell can trap. That ordering also means
# a signal arriving BETWEEN the ledger write and the assignment below is
# already safe: the trap's release matches no row.
#
# WHAT THE REORDERING COSTS, stated rather than discovered later. From here
# every failure leaves the lane HELD -- including a send that fails outright,
# and step 5 concluding the brief never left the input box. Those used to free
# the lane. That is deliberate and it is the fail-closed direction: a lane
# wrongly held costs capacity and is recovered by the documented command the
# "no free lane" refusal prints; a lane wrongly freed costs a running lane's
# work and is recovered by nothing. `lanes.sh` still shows such a lane
# `unsent`, so the cost is visible rather than silent.
#
# FATAL IF IT FAILS, and that is the same argument pointing the other way:
# nothing has gone into the pane yet, so refusing is still free, and sending a
# brief we could not first mark as live would leave the exact window this
# block exists to close.
COMMIT_OUT=$("$LEDGER_PYTHON" "$LEDGER_CLI" commit-lane-claim --lane "$LANE" --token "$CLAIM_TOKEN" 2>&1) \
  || COMMIT_OUT="${COMMIT_OUT:-commit-lane-claim failed to run}"
if ! grep -qF '"committed":true' <<<"$COMMIT_OUT"; then
  sed 's/^/  /' <<<"$COMMIT_OUT" >&2
  abort_send "could not mark $LANE's claim live before sending -- #$ISSUE_ARG was NOT dispatched (nothing was submitted)"
fi
CLAIM_COMMITTED=1

# --- 5. AND THE BRIEF ACTUALLY STARTED ------------------------------------
# #141. Everything above proves the brief was TYPED. Nothing proved it was
# SUBMITTED, and on 2026-08-11 two lanes sat for 40 minutes each holding a
# full brief in the input box because the Enter arrived while `/clear` was
# still repainting and was swallowed. The dispatcher printed
# `dispatch: #N -> lane` and walked away. This is the #81 and #130 shape
# again: the dispatcher's success message is not evidence of dispatch.
#
# What "started" means is measured, not assumed. The obvious check -- wait for
# the footer to show a running shape -- is racy: driving a real Claude Code
# pane through a short turn, `esc to interrupt` was gone from the footer
# within six seconds, so a fast first turn looks exactly like a brief that
# never ran. The input box emptying is the durable signal: it is true while
# the turn runs AND after it finishes, and it is false in precisely the
# failure this exists for.
#
# LATENCY: this loop adds ~DISPATCH_SETTLE (default 1s) to every dispatch,
# even one that lands instantly, because the first sleep runs before the
# first check -- and up to DISPATCH_CONFIRM_TRIES x DISPATCH_SETTLE (10s by
# default) to a slow-confirming one. That is the price of #141: it is what
# turns "the dispatcher printed success" into "the box actually went empty",
# so do not tune DISPATCH_CONFIRM_TRIES down to make dispatch feel faster
# without understanding that the loop is what makes an unsent brief
# detectable instead of silent.
# verified_submit sends the Enter itself -- this used to be a separate
# `tmux send-keys ... Enter` call right above step 5's comment block; moving
# it into send.sh changed nothing about WHEN it fires (still immediately
# after the ledger commit above, still fatal if the send-keys call itself
# errors), only where the code that fires it lives.
# agent-dotfiles#255: PROMPT_IN_LAUNCH has no Enter to send and no box to
# poll for empty -- the message left as this harness's launch argv, so
# `verified_submit`'s box-state check has nothing to read. `verified_launch_prompt`
# is its replacement for this path: it polls for that harness's OWN recorded
# failure signature (H_LAUNCH_PROMPT_FAILURE_RE -- codex's `Session renamed
# to`, see harness/codex.sh) instead of a box state, because there is no box
# state here for a title-eating quirk to leave behind -- only that
# signature, painted by the harness itself.
# `--blocked-re`/`--option-row-re` below, agent-dotfiles#255 round 2: LIVE
# reproduction, real codex, never a mock -- `respawn-pane -c $WORKTREE`
# always starts the harness in a worktree it has never seen, so codex's own
# directory-trust menu ("Press enter to continue") came up on THIS dispatch
# before the folded prompt was ever read, and nothing above would have
# caught it: the menu contains no `Session renamed to`, so an unanswered
# codex lane would have reported SEND_STATUS=submitted -- silent success,
# #255's exact shape, out of the very fix meant to close it. Wired from the
# same H_BLOCKED_MARKERS/H_OPTION_ROW_RE fields `lanes.sh` already reads to
# classify a lane `menu-blocked`.
if [ "$PROMPT_IN_LAUNCH" = 1 ]; then
  if ! verified_launch_prompt "$LANE_TARGET" \
       --tries "${DISPATCH_CONFIRM_TRIES:-10}" \
       --settle "${DISPATCH_SETTLE:-1}" \
       --failure-re "${H_LAUNCH_PROMPT_FAILURE_RE[$HARNESS_HIDX]:-}" \
       --blocked-re "${H_BLOCKED_MARKERS[$HARNESS_HIDX]:-}" \
       --option-row-re "${H_OPTION_ROW_RE[$HARNESS_HIDX]:-}"; then
    case "$SEND_STATUS" in
      stranded)
        abort_send "$LANE's harness did not accept the folded launch prompt as a turn (${H_LAUNCH_PROMPT_FAILURE_RE[$HARNESS_HIDX]:-} matched) -- #$ISSUE_ARG was NOT dispatched (check the pane by hand)" ;;
      blocked)
        abort_send "$LANE is still stuck on a menu/prompt after the folded launch (e.g. a first-sight directory-trust gate) -- the brief may be queued behind it, unconfirmed either way; #$ISSUE_ARG was NOT dispatched (answer the prompt by hand, then re-dispatch)" ;;
      unknown)
        echo "dispatch: WARNING -- $LANE's harness has H_LAUNCH_TAKES_PROMPT set but no H_LAUNCH_PROMPT_FAILURE_RE, so the folded launch prompt could not be confirmed either way" >&2
        echo "dispatch: #$ISSUE_ARG is claimed and the worktree exists; CHECK THE PANE BY HAND." >&2
        ;;
    esac
  fi
elif ! verified_submit "$LANE_TARGET" \
     --confirm-tries "${DISPATCH_CONFIRM_TRIES:-10}" \
     --confirm-settle "${DISPATCH_SETTLE:-1}"; then
  case "$SEND_STATUS" in
    send_failed)
      abort_send "could not submit the brief in $LANE -- #$ISSUE_ARG was not dispatched" ;;
    stranded)
      # Confirmed failure: the message is still sitting in the box. Unwind,
      # so the issue goes back to the pool rather than looking
      # claimed-and-running.
      #
      # The text is deliberately NOT cleared on the way out. C-u does not
      # reliably empty a multi-row box on a real pane, so "cleared" would be
      # another unverified claim -- and a lane left holding it is now
      # visible: `lanes.sh` reports it `unsent` with a count line, which is
      # the state #141 added for exactly this.
      abort_send "the brief was typed into $LANE but never submitted -- #$ISSUE_ARG was NOT dispatched (lanes.sh will show that lane 'unsent')" ;;
    unknown)
      # The box could not be identified at all -- another harness, or a pane
      # too short to show it. The brief may well be running, so unwinding
      # would release a claim out from under a working lane, which is its
      # own failure. Say so loudly instead of printing a clean success line.
      echo "dispatch: WARNING -- could not confirm the brief started in $LANE" >&2
      echo "dispatch: the input box was not readable (input_box_state: ${SEND_BOX_STATE:-none})." >&2
      echo "dispatch: #$ISSUE_ARG is claimed and the worktree exists; CHECK THE PANE BY HAND." >&2
      ;;
  esac
fi

# --- 5.5 THE PANE ACTUALLY SURVIVED, NOT JUST "TMUX ACCEPTED THE SEND" ----
# agent-supervisor#456, mined from Gastown's `VerifySurvived` (`internal/
# session/lifecycle.go`, `StartSession` step 12) via #453. Step 5 above
# proves the input box went EMPTY; it does not prove the process behind it
# is still THERE. Both read identically to `verified_submit`: a turn that
# just started empties the box, and so does a process that crashed and took
# the whole tmux window down with it -- codex eating a whole brief as a
# session title (agent-dotfiles#255) and a lane running for an hour inside a
# worktree that no longer existed are both this same shape, "success" was
# read off the send, not off the agent. Only checked when SEND_STATUS is
# still `submitted` -- true whichever of the two branches just above set it:
# `verified_submit`'s ordinary box-empty read, or `verified_launch_prompt`'s
# own no-failure-signature read for a `PROMPT_IN_LAUNCH` harness
# (agent-dotfiles#255's codex fold). Every other value either branch can
# leave behind (`stranded`/`send_failed`/`blocked` already aborted above,
# `unknown` already means "could not observe") is left alone here -- this
# step cannot improve on "could not observe" and must not run at all on a
# path that already aborted.
#
# Deliberately does NOT release the claim -- CLAIM_COMMITTED was set at step
# 4.5, before this pane was ever typed into, and step 4.5's own comment is
# unchanged by this: a lane wrongly freed here costs a running lane's work
# and is recovered by nothing, while a lane wrongly left HELD costs only
# capacity and is recovered by the command printed below. What a dead pane
# never gets is ACCEPTED (see `DISPATCH_CONFIRM_LANDED_ARGS` immediately
# below, which reads `DISPATCH_DIED`) -- and, in practice, no fresh
# task row at all: `LANE_META` (step 6, further down) reads the pane's own
# identity fields, which read BLANK for a target that no longer exists
# (measured, see `verified_survived`'s own header), and `record_dispatch`'s
# existing, pre-#456 guard already refuses to register a lane from blank
# fields -- falling into the existing `ledger_record_failed`/`mark_lane_held`
# path rather than a new one. Either way, dispatch.sh's own exit code goes
# non-zero (see `DISPATCH_DIED`, checked at the very end) so a caller does
# not read a clean "dispatch: #N -> lane" success line over a lane that is
# not running anything.
# `--settle` defaults to `$DISPATCH_SETTLE` itself (1s in production, the
# same knob step 5 above already tunes), not a fresh number -- measured
# 2026-08-21 against a real, throwaway isolated tmux socket: `kill-pane`
# takes a killed window out of `list-windows` synchronously (no propagation
# lag to wait out; see `verified_survived`'s own header in send.sh for the
# measurement), so this settle only has to cover a crash landing a beat
# AFTER the box drained, not tmux's own latency. Reusing `DISPATCH_SETTLE`
# also means `tests/supervisor/test_dispatch.sh`'s existing `DISPATCH_SETTLE=0`
# already keeps this step instant in the suite, the same way it already
# keeps steps 3-5 instant, without a second env var every test call site
# would otherwise need to remember to zero.
#
# ADDED LATENCY UNDER LOAD (agent-dotfiles#255, same live session that raised
# verified_preclear's own default 2s/2 -> 5s/6, and that Jon reports needed
# `DISPATCH_SETTLE=12-14`/`DISPATCH_PRECLEAR_RETRIES=14-16` by hand to clear
# reliably that day): this loop sleeps ONE `--settle` period before its FIRST
# check and returns the instant that check finds the pane alive -- it only
# burns a second retry's worth of sleep for a pane that is ACTUALLY not there
# yet. So a heavier `DISPATCH_SETTLE`, however high an operator has to raise
# it for `verified_preclear`'s box-CONTENT reads to stop false-refusing under
# load, costs this step only that same single settle period on the ordinary,
# healthy path -- not `settle x retries`. And because this check is
# existence-only (no content read at all, see the header above), a harness
# that is merely SLOW to render under load is not what this loop is
# watching for in the first place: the window still exists the whole time a
# slow-but-fine harness is starting, so raising DISPATCH_SETTLE for
# verified_preclear's sake does not make this step more likely to
# false-positive the way it would a content-matching check.
# Saved BEFORE calling verified_survived, which overwrites SEND_STATUS with
# its own vocabulary (survived/died) on return -- `DISPATCH_CONFIRM_LANDED_ARGS`
# just below still needs to know verified_submit's own answer, not
# verified_survived's, and reading `$SEND_STATUS` there unsaved would silently
# read the WRONG check's status by the time it runs.
DISPATCH_SUBMIT_STATUS="$SEND_STATUS"
if [ "$SEND_STATUS" = submitted ]; then
  if ! verified_survived "$LANE_TARGET" \
       --settle "${DISPATCH_SURVIVE_SETTLE:-${DISPATCH_SETTLE:-1}}" --retries "${DISPATCH_SURVIVE_RETRIES:-2}"; then
    DISPATCH_DIED=1
    echo "dispatch: WARNING -- $LANE's pane is GONE after the brief was submitted (#$ISSUE_ARG)" >&2
    echo "dispatch: the brief looked delivered (the input box went empty) but nothing survived to run it -- the process or its window died during startup." >&2
    echo "dispatch: $LANE STAYS HELD; this dispatch is NOT recorded as accepted (step 6 below may not even find pane identity left to record). CHECK THE PANE BY HAND." >&2
    echo "dispatch:   $LEDGER_PYTHON $LEDGER_CLI record-completion --lane $LANE --note <note>   # once you know what actually happened" >&2
  fi
fi

# agent-supervisor#193: the ledger's own record of "was this genuinely
# accepted" (`--confirm-landed`, step 6 below), set ONLY when
# `DISPATCH_SUBMIT_STATUS` reads `submitted` -- the box actually confirmed
# empty after a position-anchored, proof-checked send -- AND the pane
# survived to be re-observed running it (step 5.5, agent-supervisor#456,
# `DISPATCH_DIED` unset). `DISPATCH_SUBMIT_STATUS`, not `$SEND_STATUS`:
# verified_survived overwrote the latter with its own `survived`/`died`
# vocabulary above, so reading `$SEND_STATUS` here would silently compare
# against the WRONG check's answer. The `unknown` case above is explicitly
# NOT `submitted` either: the brief may well be running, but nothing here
# CONFIRMED it, and #193's whole point is that the reconciler must not take
# "the lane went quiet" as a stand-in for that confirmation -- #456 extends
# the same rule to "the lane went quiet because it is gone".
DISPATCH_CONFIRM_LANDED_ARGS=()
[ "$DISPATCH_SUBMIT_STATUS" = submitted ] && [ -z "${DISPATCH_DIED:-}" ] && DISPATCH_CONFIRM_LANDED_ARGS=(--confirm-landed)

# The dispatch was committed at step 4.5, not here. This comment used to
# introduce a `CLAIM_COMMITTED=1` on this line, and that placement was
# agent-dotfiles#209's blocking defect: everything it says about a brief being
# live in a real pane was already true ~70 lines and up to
# DISPATCH_CONFIRM_TRIES x DISPATCH_SETTLE earlier, at the `send-keys Enter`
# above. The reasoning was right and the position was wrong, so the position
# moved; do not move it back. What that reasoning still buys, unchanged, is
# the case below: a `record_dispatch` FAILURE is non-fatal by design and falls
# straight through to the exit, where the EXIT trap runs -- and the claim
# placeholder is then the only thing keeping a working lane out of the next
# dispatcher's hands. Step 4.5's ledger commit is what makes both the trap and
# the reap leave it alone.

# --- 6. record what was dispatched. BEST EFFORT, NEVER FATAL --------------
#
# agent-dotfiles#140, updated by agent-dotfiles#174. Every signal that a lane
# is busy used to be inferred from pane content, and inference is what
# produced the false-`free` bugs #102, #123 and #126. This writes the fact
# down instead.
#
# #140 made this write non-fatal because NOTHING READ IT YET -- a recording
# layer nothing depends on can be wrong without taking the estate down. That
# premise is gone: step 1 above now reads exactly this record to pick every
# future lane, and #174 was filed to make that inversion explicit rather than
# leave this comment asserting the opposite of what the code now does. Read
# what follows as "still non-fatal, for a DIFFERENT reason", not as the old
# reason left unexamined.
#
# THIS BLOCK STILL MUST NOT ABORT THE DISPATCH, AND THAT IS STILL THE
# OPPOSITE OF EVERY OTHER STEP ABOVE. The brief has already been typed,
# verified and submitted into a REAL, LIVE pane by the time this block runs --
# unwinding the claim and the worktree here would strand a worker that is
# actively working, which is strictly worse than a stale ledger row. So the
# failure mode this block accepts is not "the estate may dispatch to a lane
# that is actually busy" -- it is "this ONE lane stops being offered until
# reconciliation happens". That cost is bounded and visible (the LOUD message
# below, and `lanes.sh` still showing the window doing nothing); trading the
# live worker for it would not be. Do not "fix" it into an abort_send;
# tests/supervisor/test_dispatch.sh mutation-checks that removing this
# tolerance turns the suite red.
#
# agent-dotfiles#188 finding 1: step 1's fail-closed read does NOT rule out
# "actually busy" on its own here. `Ledger.record_dispatch` is one
# transaction, so a failure rolls back every one of its five writes -- and
# for a lane the ledger already had registered free (every lane after its
# first backfill, and every lane `lane-done.sh` has ever freed, which is
# ordinary steady state, not an edge case), rollback restores exactly that
# pre-existing FREE row, not UNKNOWN. A comment here used to claim the
# unrecorded lane reads UNKNOWN regardless; it does not, and #188 is the
# defect that shape produces (also #145, #170: a comment asserting a
# protection the code does not have). `cli.py`'s `record_dispatch` now closes
# that window itself -- on any failure it calls `Ledger.mark_lane_held`
# before re-raising, which writes a placeholder outstanding task for the
# lane so `lane_available` reads occupied, not whatever it read before the
# call. That write happens inside the Python process handling this exact
# failure, so it is not conditional on this bash block at all; what follows
# here is only the loud, human-facing report of what already happened.
#
# WHY IT RUNS LAST, after the final Enter and past every abort path: a record
# asserting work is in flight, left behind by a dispatch that then aborted, is
# worse than no record at all -- the point of the ledger is to be believed.
# Ordering is what guarantees that, not cleanup, so nothing above this line
# needs an unwind for it.
#
# That is also why step 5 (#141) sits ABOVE this block rather than below it.
# Step 5 can abort_send -- a brief that was typed but never submitted unwinds
# the claim and the worktree -- and a ledger record written before it would be
# exactly the "work is in flight" claim this paragraph rules out, asserted
# about a lane that is running nothing.
ledger_record_failed() {
  echo "dispatch: LEDGER RECORD FAILED for $WINDOW_NAME -- the dispatch STANDS, the record does not" >&2
  sed 's/^/  /' <<<"${1:-}" >&2
  echo "dispatch: the lane is working, and cli.py has marked it HELD (a placeholder occupied task) so it will not be offered again -- reconcile it by hand (cli.py register / record-dispatch) or let a later dispatch overwrite it" >&2
  return 0  # the ledger write is never fatal -- agent-dotfiles#140
}

# agent-supervisor#169: a PR-scoped record-dispatch failure is not an
# ordinary ledger write failure -- `PR-DUPLICATE:` (printed by `cli.py`'s
# `record_dispatch` when the write-time `one_open_pull_per_source_ref` index
# refuses a second open row for the same PR) means ANOTHER lane genuinely
# won the same PR, seconds ago, through the exact race step 0.6 cannot
# always catch. `ledger_record_failed` above is deliberately non-fatal
# because the brief is already live in the pane and nothing here can unsend
# it -- that stays true here too, the dispatch still stands and the lane is
# still marked HELD by the same call. What changes is the EXIT CODE: this
# refusal is loud AND non-zero, so a caller (a human, or a supervisor loop)
# is told this specific dispatch collided with a lane that actually holds
# the PR, rather than reading dispatch.sh's overall success alongside a
# buried warning.
ledger_record_pr_duplicate() {
  echo "dispatch: $1" >&2
  echo "dispatch: the lane is working, and cli.py has marked it HELD (a placeholder occupied task) so it will not be offered again -- reconcile it by hand (cli.py register / record-dispatch, or cli.py cancel-open-task --lane $LANE if this lane's own brief should be abandoned)" >&2
}

# One tmux call for the pane identity the ledger records. The recorder itself
# never talks to tmux: a durable record that cannot be written without a live
# tmux server is not the portability the ledger is for, and the caller here is
# already holding a tmux connection.
LANE_META=$(tmux display-message -p -t "$LANE_TARGET" \
  '#{pane_id}|#{pane_current_command}|#{pane_current_path}|#{socket_path}|#{session_created}|#{session_id}' 2>&1)
if [ -z "$LANE_META" ] || [[ "$LANE_META" != *"|"* ]]; then
  ledger_record_failed "could not read pane metadata for $LANE: $LANE_META"
else
  IFS='|' read -r PANE_ID PANE_CMD PANE_PATH SOCKET_PATH SESSION_CREATED SESSION_ID <<<"$LANE_META"
  # agent-dotfiles#237: the HARNESS conversation id, which is the only part of
  # this record that survives a tmux server loss -- `$SESSION_ID` above is
  # tmux's own `#{session_id}` and dies with the server. Resolved here, in the
  # same block as the rest of the pane identity, and under the same rule as
  # everything else in it: FAILING TO RESOLVE IS NOT A DISPATCH FAILURE. The
  # brief is already live in the pane; a lane recorded with no session id is
  # merely one `restore.sh` will report unrecoverable, which is the outcome
  # #237 asks for over a fresh agent wearing this lane's name.
  #
  # `--marker "$WORKTREE"`: unique to this dispatch (worktree.sh mints a fresh
  # path per dispatch), so the resolver can tell this lane's brand-new
  # transcript from every other one on the machine. See harness_session.py for
  # the two other tests it applies and for what was measured before settling
  # on these.
  HARNESS_SESSION_ID=""
  # agent-supervisor#172: the directory the harness process was actually
  # launched in -- `$PANE_PATH`, read in the same tmux call as everything
  # else above, at the SAME moment the session id is resolved. Never set
  # independently of `HARNESS_SESSION_ID`: `claude --resume` is scoped to
  # this directory, not to whatever `repo` (a worktree, rewritten on every
  # dispatch) happens to hold later, and pairing a real id with the wrong
  # directory is exactly the defect this issue exists to close. Left empty
  # alongside `HARNESS_SESSION_ID` whenever resolution fails, below.
  HARNESS_PROJECT_DIR=""
  if [ -n "$LANE_HARNESS" ]; then
    if HARNESS_SESSION_ID=$("${DISPATCH_PYTHON:-python3}" "$HERE/harness_session.py" \
        --harness "$LANE_HARNESS" \
        --marker "$WORKTREE" \
        --since "$DISPATCH_SEND_EPOCH" \
        --timeout "${DISPATCH_SESSION_TIMEOUT:-20}" 2>&1); then
      HARNESS_PROJECT_DIR="$PANE_PATH"
    else
      # stdout, not stderr (agent-dotfiles#199/#237): this is loud, not a
      # failure -- the brief already went out and the dispatch still
      # succeeds. #199 holds dispatch.sh's stderr clean on any successful
      # dispatch, on purpose, so the supervisor can treat stderr output as
      # "something is wrong" without also having to parse it for this
      # expected, non-fatal case. A resolver that can never find a real
      # harness process (this repo's own test stubs, or a lane whose
      # harness has no transcript path at all) hits this on every dispatch,
      # which is exactly the noise #199 exists to keep off stderr.
      echo "dispatch: no harness session id recorded for $WINDOW_NAME -- ${HARNESS_SESSION_ID:-no reason given}"
      echo "dispatch: the dispatch STANDS; this lane will read unrecoverable to restore.sh until its next dispatch"
      HARNESS_SESSION_ID=""
    fi
  fi
  # agent-supervisor#117: recorded CANONICAL, not as `worktree.sh` printed
  # it. `WORKTREE` is built from `$TMPDIR`/`WORKTREE_ROOT`, and on macOS
  # both `/tmp` and (the `/var/folders/...` `$TMPDIR` default) `/var` are
  # themselves symlinks into `/private` -- `git worktree list --porcelain`
  # (step 3's own lookup, above) reports the RESOLVED path, so recording
  # the unresolved one here would make every future lookup miss by
  # construction, silently, for every dispatch on such a host. Falls back
  # to the unresolved path only if `cd`+`pwd -P` cannot run (the worktree
  # already gone) rather than dropping the association entirely.
  WORKTREE_CANONICAL="$WORKTREE"
  WORKTREE_CANONICAL_RESOLVED=$(cd "$WORKTREE" 2>/dev/null && pwd -P) || true
  [ -n "$WORKTREE_CANONICAL_RESOLVED" ] && WORKTREE_CANONICAL="$WORKTREE_CANONICAL_RESOLVED"
  LEDGER_ARGS=(
    record-dispatch
    --lane "$LANE"
    --task "$WINDOW_NAME"
    --summary "#$ISSUE_ARG $SLUG; worktree=$WORKTREE; brief=$BRIEF"
    --pane-id "$PANE_ID"
    --pane-path "$PANE_PATH"
    --command "$PANE_CMD"
    --server-id "${SOCKET_PATH}:${SESSION_CREATED}"
    --session-id "$SESSION_ID"
    --harness-session-id "$HARNESS_SESSION_ID"
    --harness-project-dir "$HARNESS_PROJECT_DIR"
    --github "$REPO"
    # agent-supervisor#117: the worktree this dispatch just built, recorded
    # as its own column now (`Ledger.get_task_for_worktree`) instead of only
    # living inside --summary text, so a later --reviews-pr authorship check
    # can look it back up without reconstructing anything from a branch name.
    --worktree "$WORKTREE_CANONICAL"
    # bash 3.2-safe empty-array expansion -- see this file's own comment on
    # POSITIONAL, above, for why the bare "${arr[@]}" form is not.
    "${DISPATCH_CONFIRM_LANDED_ARGS[@]+"${DISPATCH_CONFIRM_LANDED_ARGS[@]}"}"
  )
  # agent-dotfiles#216: forward the harness `lane-free` already resolved
  # (step 1) instead of letting `record-dispatch` re-derive one from
  # $PANE_CMD via its own narrower HARNESS_BY_COMMAND fallback -- that
  # fallback cannot represent a Node harness at all, which is the bug this
  # closes. Omitted when step 1 never resolved one (LANE_HARNESS empty);
  # record-dispatch's fallback still applies then, unchanged.
  [ -z "$LANE_HARNESS" ] || LEDGER_ARGS+=(--harness "$LANE_HARNESS")
  for i in "${ISSUES[@]}"; do
    LEDGER_ARGS+=(--issue "$i")
  done
  # agent-supervisor#159: recorded as the `source_tasks` KEY for a PR-scoped
  # dispatch (`cli.py record_dispatch` writes `source_kind='pull'` when this
  # is present) -- see cli.py's own docstring. The `--issue` args above are
  # still sent; they land in evidence, not the source key, for this path.
  [ -z "$PR_SCOPED" ] || LEDGER_ARGS+=(--pr "$PR_SCOPED")
  if ! LEDGER_OUT=$("${DISPATCH_PYTHON:-python3}" "$HERE/cli.py" "${LEDGER_ARGS[@]}" 2>&1); then
    if grep -qF 'PR-DUPLICATE:' <<<"$LEDGER_OUT"; then
      PR_DUP_MSG=$(sed -n 's/^PR-DUPLICATE: //p' <<<"$LEDGER_OUT" | head -1)
      ledger_record_pr_duplicate "$PR_DUP_MSG"
      exit 1
    fi
    ledger_record_failed "$LEDGER_OUT"
  fi
fi

# agent-supervisor#456: a died pane never gets this block's own success-
# shaped lines -- step 5.5 already printed a loud, specific warning to
# stderr, and printing "dispatch: #N -> lane" on top of it here would read as
# a clean success to any caller that only checks stdout, exactly the
# mismatch #456 exists to close. The lane IS still claimed and the worktree
# IS still real (nothing above unwinds either, per step 4.5's own
# invariant) -- only "this looks like an ordinary successful dispatch" is
# withheld.
if [ -n "${DISPATCH_DIED:-}" ]; then
  echo "dispatch: #$ISSUE_ARG was claimed against $LANE ($WINDOW_NAME) but the pane DID NOT SURVIVE -- see the WARNING above" >&2
  exit 1
fi

# The target is printed as well as the lane (#241) because the caller's very
# next action is `lane-done.sh <window> <name> <channel>` (loop-tick.md), and
# that waiter blocks for as long as the work takes -- the longest-lived
# resolved target in the estate, and so the one most certain to be addressing
# a renumbered index by the time it fires. Give it the id, not the index.
echo "dispatch: #$ISSUE_ARG -> $LANE ($WINDOW_NAME)"
echo "  target:   $LANE_TARGET   # pass this to lane-done.sh, not the index"
echo "  worktree: $WORKTREE"
echo "  brief:    $BRIEF"
exit 0
