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
#              `dispatch-claude-print.sh` does not speak `--pr` or a
#              multi-issue dispatch, and silently dropping either would be
#              worse than routing it the old way; see agent-supervisor#171's
#              own tracked follow-up. A review IS now an exception to "left
#              on the tmux flow" in exactly one circumstance
#              (agent-estate#838, dispatch-lane-select.sh): when the tmux
#              candidate loop excludes every free lane in the target session
#              as a PR contributor (agent-dotfiles#212) and none is left to
#              hand the review to, this reroutes over `claude-print` instead
#              of refusing -- see that reroute's own comment for why a
#              freshly-minted `claude-print` lane needs none of
#              `--reviews-pr`'s author-exclusion bookkeeping to stay
#              independent. An ordinary review with a genuinely free
#              non-author lane is UNAFFECTED and still runs the tmux flow
#              exactly as before.
# --force
#              agent-supervisor#291: dispatch anyway when the pre-dispatch
#              collision check (step 3.2) finds #<issue>'s files overlap an
#              already in-flight lane's -- for a known and intended overlap.
#              Never silences an UNKNOWN verdict (there is nothing to force
#              past there; unknown already allows) and never suppresses the
#              log line naming what was overridden. See collision-check.sh's
#              own header for what "overlap" means.
# --adopt-pane <window-id>
#              agent-supervisor#668: dispatch to an ALREADY-RUNNING, idle pane
#              (a window `lanes.sh` reports free, addressed by its stable
#              `#{window_id}` -- e.g. `@42`, the `@`-prefix optional on the
#              command line) instead of spawning a new harness process for
#              it. Every other step runs exactly as an ordinary dispatch does
#              -- claim, worktree, rename, ledger record (step 6) -- so
#              authorship resolves afterward and `merge-pr.sh` can see who
#              wrote the PR. The one thing this mode skips is step 3.5's
#              `respawn-pane -k`: the pane's existing process is handed the
#              brief directly (the ordinary type-verify-submit send, step 4-5
#              below), never killed and relaunched. That is the whole point
#              -- zero new processes against a host's process-count ceiling.
#              Implies `--live-pane` (this dispatch stays on the named
#              window's own pane; it is never routed to a freshly minted
#              `claude-print` lane). Refuses, the same way an ordinary
#              dispatch refuses, if the named window is not free per
#              `lanes.sh`/the ledger by the time step 1 runs. Not yet wired
#              for a review (`--reviews-pr`), a PR-scoped follow-up (`--pr`)
#              or a multi-issue dispatch -- refused outright with those, same
#              posture as `--reviews-pr`/`--not-a-review`'s own contradiction
#              check above, rather than silently degrading one of them.
#              WHY THE PANE'S OS-LEVEL CWD IS NOT REPOINTED: #15's fix (step
#              3.5, below) sets a pane's real starting directory the only way
#              a running process's cwd can be changed from outside it --
#              killing and restarting it with `-c`. This mode deliberately
#              does not do that (see above), so an adopted pane keeps
#              whatever OS-level cwd its last dispatch gave it; the brief
#              still names the worktree explicitly (the same typed message
#              every tmux dispatch sends) and this mode is for a harness that
#              reasons in absolute paths, the same carve-out #15's own
#              comment already makes for Claude.
#
# Exit 0 only when a lane has been sent a brief -- over tmux/send-keys, or
# (new, #171, default for a plain single-issue `claude` dispatch; also #838,
# a review rerouted around a scarce session) over a freshly minted
# `claude-print` lane. Exit 1 on any refusal -- no free lane, an issue
# someone else already claimed, a worktree that could not be created, a send
# that failed, a `claude-print` register/assign that could not reach
# `claude` (this NEVER falls back to send-keys -- see
# `dispatch-claude-print.sh`'s own header), or a review whose only free lane
# wrote the PR under review AND could not be rerouted (`--live-pane` was
# requested, `[repo]` could not be resolved, or no `claude` binary is on
# PATH -- see dispatch-lane-select.sh's own reroute comment for the exact
# conditions).

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

# agent-supervisor#716: dispatch.sh was 2753 lines, 163 files reference it,
# and it starts every lane in the estate including the one that would split
# it -- the most delicate file in the split-by-responsibility sequence
# (watchdog.sh #713, core.py #708, sync.py agent-dotfiles#336). Behaviour-
# preserving: every line below is unchanged code, moved -- no flag, no output
# text, no ordering changed. Sourced (not called) so every global variable,
# every helper function, and this file's own `set -uo pipefail` stay visible
# across every one of them exactly as if they were still inline here -- the
# same mechanism `input-box.sh`/`send.sh`/etc. above already rely on, only
# applied to this script's OWN body instead of its siblings'.
#
# SOURCING ORDER IS EXECUTION ORDER, on purpose: dispatch.sh's original
# top-to-bottom flow is preserved verbatim, one file per named step-group
# rather than reordered into "setup" vs "logic" -- a step later in this list
# may read a variable or call a function a step earlier in this list defined,
# and never the reverse at DEFINITION time. (Two functions are the deliberate
# exception, by design, not by accident: `abort_send`, defined in
# dispatch-worktree.sh, calls `release_claim`/`release_lane_claim`, defined
# in dispatch-lane-select.sh just before it -- and every later file calls
# `abort_send` itself. All three resolve at CALL time, long after every file
# below has been sourced, so the backward name reference is never live.)
#
#   dispatch-rehome.sh       dispatch_rehome_lane() + the --rehome-lane entry
#                             point, which must run and exit before anything
#                             else below does anything (unaffected by --force
#                             et al.)
#   dispatch-args.sh          the flag loop and the positional
#                             issue/slug/brief/repo/repo-path arguments
#   dispatch-preflight.sh      host-pressure, prior-attempts, the quota gate,
#                             --reviews-pr inference, and window/session
#                             resolution
#   dispatch-guards.sh         the ledger-readable check, the supervisor
#                             lease, the stale-claim reap, and PR-authorship /
#                             already-claimed guards
#   dispatch-lane-select.sh    step 1 (pick a safe lane) and step 1.5 (the
#                             claude-print default route)
#   dispatch-worktree.sh       steps 2/3/3.1/3.2 (claim, worktree, collision
#                             check) and the message-budget check
#   dispatch-send.sh           steps 3.5 through 5.5 (respawn into the
#                             worktree, type, submit, confirm survival)
#   dispatch-record.sh         step 6 (record-dispatch) and the final exit
#
# shellcheck source=./dispatch-rehome.sh
. "$HERE/dispatch-rehome.sh"
# shellcheck source=./dispatch-args.sh
. "$HERE/dispatch-args.sh"
# shellcheck source=./dispatch-preflight.sh
. "$HERE/dispatch-preflight.sh"
# shellcheck source=./dispatch-guards.sh
. "$HERE/dispatch-guards.sh"
# shellcheck source=./dispatch-lane-select.sh
. "$HERE/dispatch-lane-select.sh"
# shellcheck source=./dispatch-worktree.sh
. "$HERE/dispatch-worktree.sh"
# shellcheck source=./dispatch-send.sh
. "$HERE/dispatch-send.sh"
# shellcheck source=./dispatch-record.sh
. "$HERE/dispatch-record.sh"
