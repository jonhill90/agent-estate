#!/bin/bash
# Give every lane task its own git worktree, so lanes and the Director stop
# sharing one working tree.
#
# WHY: agent-dotfiles#73. Every lane, the Director, and the supervisor
# operated in one checkout -- `~/source/repos/Personal/agent-dotfiles` -- with
# no coordination. Observed on 2026-08-11: a lane working #28 had its branch
# switched out from under it mid-task by another lane. Its uncommitted edits
# to four files were discarded, and its staged deletion of
# `.github/instructions/skill-authoring.instructions.md` was swept into an
# unrelated lane's commit (62393b1, shipped as PR #66 carrying a deletion its
# own message never mentions). That is the shared checkout destroying one
# agent's work and silently corrupting another's commit.
#
# The estate's own contract already says bounded issue work belongs in
# disposable worktrees. Writing that in loop-tick.md was not sufficient on its
# own -- the estate has already learned that a rule living in someone's memory
# gets skipped, which is why lanes.sh and claim.sh exist as tools rather than
# paragraphs. This is the same move: `new` hands a dispatch a ready worktree so
# there is nothing to remember, and `done`/`guard` refuse to discard anyone's
# uncommitted work, matching the safe-deletion contract -- a worktree with
# uncommitted changes is someone's unfinished work, not garbage.
#
# Usage:
#   worktree.sh new  <slug> [repo] [base]   create a worktree, print its path
#   worktree.sh done <path>                 remove a worktree; refuses if dirty
#   worktree.sh guard <repo>                exit 1 if <repo> itself is dirty
#   worktree.sh reap [--no-github] <path> [base]
#                                           single-target twin of `gc` below,
#                                            for a caller that already knows
#                                            exactly which one worktree it
#                                            means and must never risk
#                                            sweeping a sibling's tree as a
#                                            side effect of reaping this one
#                                            (agent-estate#804). Reuses the
#                                            EXACT SAME predicate chain `gc`
#                                            runs per-candidate -- not live
#                                            (`_gc_is_live`), branch content
#                                            already on [base] or a MERGED PR
#                                            on record (`branch_content_is_
#                                            on_base` / `_gc_pr_merged_from_
#                                            file`, skippable with
#                                            --no-github), then `safe_remove`
#                                            -- against exactly <path>,
#                                            without enumerating or touching
#                                            any other registered worktree.
#                                            Exit 0 = removed; exit 1 = a
#                                            guard refused (reason on
#                                            stderr); the worktree survives
#                                            either way this call errors.
#   worktree.sh gc [--dry-run] [--no-github] [repo] [base]
#                                           remove every worktree whose
#                                            branch's content is already on
#                                            [base] (or whose branch has a
#                                            MERGED PR on record, #682 --
#                                            skip that cross-check with
#                                            --no-github), whose tree is
#                                            clean, and that is not LIVE --
#                                            no tmux pane or process
#                                            references it, and it is older
#                                            than $WORKTREE_GC_MIN_AGE_SECONDS
#                                            (default 3600); --dry-run reports
#                                            what it would remove and changes
#                                            nothing. Scoped to
#                                            [repo]/.worktrees/ by default;
#                                            $WORKTREE_GC_EXTRA_ROOTS (space-
#                                            separated paths, unset by
#                                            default) opts in additional
#                                            roots for candidates matching
#                                            `new`'s own ad-<slug>-<pid>
#                                            naming (#682)
#
# [repo] defaults to the current directory; [base] defaults to origin/main.
# <slug> becomes branch `lane/<slug>` -- pass the issue and a short reason,
# e.g. `73-worktree-isolation`, so the branch name says what it is without
# opening a pane.
#
# WHY `gc` exists and what it deliberately does not do (agent-dotfiles#165):
# `new` has run on every dispatch since #81 and nothing ever calls `done` --
# `lane-done.sh` renames the tmux window and closes the ledger task but never
# touches the worktree it was given. 92 registered worktrees held 66 branches
# the night this was measured, and a held branch cannot be deleted: `gh pr
# merge --delete-branch` failed twice with "used by worktree at ...".
#
# The obvious fix, "lane-done.sh calls done", is wrong for two reasons a
# completion event cannot see. First, a finished lane's branch is normally
# pushed and in review -- a reviewer or a follow-up fix may still want that
# tree, so removing it at completion time is early, not automatic garbage.
# Second, `done` correctly refuses a dirty tree, so a lane that finished with
# uncommitted work would silently fail to clean and the leak would persist
# exactly where losing it matters, with nobody told.
#
# `gc` is a sweep, not a completion hook: it removes a worktree only once
# both are true -- [base] already contains the branch's content (see
# `branch_content_is_on_base` below; this was tip-ancestry until #169, which
# a squash merge never satisfies), its tree is clean (the same guard `done`
# already applies, reused rather than reimplemented), and it is not LIVE (see
# `_gc_is_live` below, agent-supervisor#478). Anything unmerged, dirty, or
# live is left alone and reported, not retried or forced.
#
# agent-supervisor#478: clean+merged is NOT the same question as "is anyone
# using this tree right now". Measured 2026-08-21, twice in one tick: a lane
# holding text typed-but-not-yet-submitted in its pane, or one that had just
# started, has written nothing to disk yet -- a perfectly clean tree -- and
# if its branch's content already landed on `base` (e.g. gc ran between a
# stalled dispatch and a retry that claimed the same slug), the old predicate
# matched exactly and reclaimed the worktree out from under the live pane.
# `lanes.sh` then reports the lane `broken`; the same class of bug destroyed
# a live lane's worktree mid-task on 2026-08-21, losing ~20 minutes of work
# on PR #489. `_gc_is_live` adds three liveness signals, ANDed as "keep if
# any one says maybe live" -- including "could not check": an uncollected
# worktree costs disk, a deleted one costs work, and blindness must never
# read as permission.
#
# What `gc` does NOT reach, measured on the live checkout 2026-08-11 and
# stated here so the next reader does not re-derive it: of 44 non-ancestor
# worktree-held branches, 24 have a MERGED PR, and the content predicate
# reaches 7 of those. The other 17 are merged *and then superseded* -- later
# commits on `main` edited the same files, so the scoped diff is non-empty
# and the branch's tip content no longer matches `main`'s. Nothing local can
# tell that apart from unmerged work; only the PR state can (#169 sketches
# a `gh pr view` path, deliberately not taken here because it must fail
# closed offline and that is a separate decision). Converging is not
# emptying: `gc` reaching 38 of 70 held trees and refusing the rest is the
# intended shape, not a shortfall to fix by loosening the predicate.
#
# `gc` removes worktrees, never branches. A wrong "already merged" verdict
# would free a tree, not delete a ref -- but it would still discard whatever
# was only in that tree, so every check above fails closed. This makes `gc` idempotent and safe to run repeatedly
# from wherever it ends up wired in -- deliberately NOT wired into the
# dispatch/lane-done pair or the Director tick by this change. This estate
# has shipped that exact shape wrong five times already (`acp_transport.py`,
# `claim.sh` before #74, `worktree.sh` itself before #81, and two more --
# see dispatch.sh's header): a tool that fails closed when called, that
# nothing calls. Wiring `gc` in is a separate decision for whoever owns the
# Director tick, not bundled into landing the tool.
set -uo pipefail

# `install_dispatch_origin_guard` (agent-supervisor#562) needs an absolute
# path to THIS repo's own `cli.py` -- not the target repo's, since `new`
# routinely hands out a worktree of a DIFFERENT repo (agent-dotfiles,
# agent-tui, skills) that carries no `scripts/supervisor/` tree of its own
# at all. Resolved once, here, the same way every other script in this
# directory resolves its own location.
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2; }

# Shared by `done` and `gc`: remove TARGET, refusing anything that would
# discard work. Never call this on a target that has not already been
# checked for uncommitted changes belonging to work in progress -- `gc`
# checks "merged" separately, before this runs.
# agent-supervisor#367: live/ was deleted by something that walked worktrees
# and removed one -- unconfirmed which caller, but this is the shape of the
# guard the estate already trusts for the same class of accident
# (tmux-isolation.sh's assert_isolated_tmux for kill-server, #247/#258): a
# destructive verb refuses its one unrecoverable target rather than trusting
# every caller to know not to pass it. Compared by realpath, not string
# equality -- $target can arrive with a trailing slash, a relative form, or
# (on macOS) an unresolved /var/... symlink of the same /private/var/...
# path `git worktree list` itself would report; string equality would miss
# all three and wave the removal through.
is_live_worktree() {
  local target="$1"
  local live="${SUPERVISOR_LIVE:-${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}/live}"
  local target_real live_real
  target_real=$(cd "$target" 2>/dev/null && pwd -P) || target_real="$target"
  live_real=$(cd "$live" 2>/dev/null && pwd -P) || live_real="$live"
  [ -n "$target_real" ] && [ "$target_real" = "$live_real" ]
}

safe_remove() {
  local target="$1"
  local dry="${2:-}"
  # The live worktree the watchdog LaunchAgent runs from is not a lane's
  # disposable tree, gc-eligible or not -- refuse unconditionally, before
  # any of the checks below that a clean, merged live/ would otherwise sail
  # through. See is_live_worktree's comment above.
  if is_live_worktree "$target"; then
    echo "worktree: $target is the live worktree (agent-supervisor#367) -- refusing to remove it, no matter how clean or merged it looks" >&2
    return 1
  fi
  # A worktree with uncommitted changes is someone's unfinished work, not
  # garbage -- refuse rather than guess, same as safe-deletion.
  local status
  status=$(git -C "$target" status --porcelain 2>&1)
  if [ -n "$status" ]; then
    echo "worktree: $target has uncommitted changes -- not removing" >&2
    echo "$status" >&2
    return 1
  fi
  # A clean `git status` says nothing about a detached HEAD: a lane can
  # `checkout --detach` and commit there, and the dirty-tree check above
  # never sees it. Removing the worktree at that point makes the commit
  # unreachable from any ref -- a dangling object, invisible and eventually
  # GC-eligible. Refuse unless some branch, local or remote, already
  # contains HEAD.
  if ! git -C "$target" symbolic-ref -q HEAD >/dev/null; then
    local containing
    containing=$(git -C "$target" for-each-ref refs/heads refs/remotes --contains HEAD --format='%(refname)' 2>/dev/null)
    if [ -z "$containing" ]; then
      echo "worktree: $target is on a detached HEAD at $(git -C "$target" rev-parse --short HEAD 2>/dev/null) with no branch containing it -- not removing (would lose the commit)" >&2
      return 1
    fi
  fi
  # --dry-run runs every refusal above and stops here: the answer a real run
  # would give, without the removal.
  [ -n "$dry" ] && return 0
  git -C "$target" worktree remove "$target" >&2 || return 1
  return 0
}

# agent-supervisor#478: is worktree $1 LIVE -- occupied by a tmux pane, a
# running process's cwd, or simply too young to trust either signal between
# tool calls? `gc`'s clean+merged predicate says nothing about this; see the
# header comment above for the incident this closes.
#
# Three independent checks, each one able to say "keep" on its own:
#   1. a tmux pane's #{pane_current_path} is inside it -- the direct signal.
#   2. a running process's ACTUAL CWD (via lsof, not its argv) is inside it
#      -- see _gc_process_refs's own comment for the 2026-08-22 miss this
#      replaced (a `ps -eo command` grep, blind to a process cwd'd into the
#      worktree with no path in its argv).
#   3. it is younger than $WORKTREE_GC_MIN_AGE_SECONDS -- neither signal
#      above is sufficient alone: a lane between tool calls holds no process
#      reference at that instant, and a pane can be momentarily elsewhere.
#      60 minutes (3600s) is what the supervisor's own sweep uses for the
#      same kind of staleness floor (SUPERVISOR_LANE_SWEEP_STALE_AFTER).
#
# Bias to keeping, always: when a signal cannot be read at all -- tmux not
# running, `ps` unreadable, the worktree's own mtime unreadable -- this
# returns "live" (keep), the same direction as every check above it. An
# uncollected worktree costs disk; a deleted one costs someone's work.
#
# Prints its reasoning to stderr (gc reports it as the skip reason) and
# returns 0 if the worktree must be kept, 1 if it is clear to move on to the
# existing clean/merged checks.
GC_MIN_AGE_SECONDS="${WORKTREE_GC_MIN_AGE_SECONDS:-3600}"

_gc_mtime() { stat -c %Y "$1" 2>/dev/null || stat -f %m "$1" 2>/dev/null; }

# agent-supervisor#25 (poller-recover.sh, quota-watch-recover.sh): lsof lives
# at /usr/sbin/lsof on macOS, and /usr/sbin is not on every caller's PATH
# (watchdog.sh's LaunchAgent PATH override is the measured case). Resolved
# the same way those two scripts already resolve it, so this is a third
# caller of one lookup, not a third slightly-different one.
_gc_lsof_bin() {
  if command -v lsof >/dev/null 2>&1; then printf '%s' lsof; return 0; fi
  if [ -x /usr/sbin/lsof ]; then printf '%s' /usr/sbin/lsof; return 0; fi
  return 1
}

# agent-supervisor#762: `cmd_gc` below fetches `tmux list-panes -a` ONCE for
# the whole sweep and caches it in `_GC_PANES`/`_GC_PANES_RC` before the
# per-worktree loop starts -- 157 registered worktrees means 157 avoidable
# subprocess spawns otherwise, one per candidate, for output that cannot
# have changed between them (a sweep is one point-in-time snapshot, not 157
# of them). `_GC_PANES_CACHED` distinguishes "no caller has populated the
# cache yet" from "populated, and it happens to be empty" (a real tmux
# server with zero panes is not the same fact as "tmux could not be asked"
# -- the latter must return 2 and bias to keep, the former legitimately
# means no pane occupies anything and this check must be free to say so).
# A caller outside `cmd_gc` (a test invoking `_gc_is_live`/
# `_gc_tmux_occupies` directly, as this file's own test suite does) leaves
# the cache unset and gets the original per-call `tmux list-panes`, so the
# batching is an optimisation `cmd_gc` opts into, not a contract every
# caller must satisfy first.
_GC_PANES_CACHED=""
_GC_PANES=""
_GC_PANES_RC=0

# 0 = a pane's cwd is inside $1; 1 = tmux answered and none matched;
# 2 = tmux itself could not be asked (no server, tmux missing, ...).
_gc_tmux_occupies() {
  local target_real="$1" panes pane pane_real
  if [ -n "$_GC_PANES_CACHED" ]; then
    [ "$_GC_PANES_RC" -eq 0 ] || return 2
    panes="$_GC_PANES"
  else
    panes=$(tmux list-panes -a -F '#{pane_current_path}' 2>/dev/null) || return 2
  fi
  while IFS= read -r pane; do
    [ -n "$pane" ] || continue
    pane_real=$(cd "$pane" 2>/dev/null && pwd -P) || continue
    case "$pane_real" in
      "$target_real"|"$target_real"/*) return 0 ;;
    esac
  done <<<"$panes"
  return 1
}

# 0 = some process's ACTUAL CWD is inside $1; 1 = lsof answered and none
# is; 2 = lsof itself could not be asked (no binary, or the query failed).
#
# This used to be `ps -eo command | grep -F "$target_real"` -- argv text,
# not cwd. Measured 2026-08-22: a live `claude` process sitting in
# agent-tui/.worktrees/mouse (launched with no path argument, `cd`'d into
# the worktree by its caller) was invisible to that grep --
# `ps -eo command | grep -o '/[^ ]*worktrees[^ ]*'` returned nothing for it
# -- and immediately visible to `lsof -a -d cwd`, which reports what a
# process's file descriptor table actually says its cwd is rather than
# what its argv happened to spell out. The failure mode is exactly #478's:
# a live occupant read as absent, because the check asked the wrong
# question about it. `_gc_tmux_occupies` already gets this right for tmux
# panes (`#{pane_current_path}`, not the pane's command line) -- this
# brings the bare-process signal to the same standard rather than leaving
# one of the two "keep if any one says maybe live" checks blind to a class
# of occupant the other one catches fine.
_gc_process_refs() {
  local target_real="$1" lsof_bin out line path
  lsof_bin=$(_gc_lsof_bin) || return 2
  out=$("$lsof_bin" -a -d cwd -Fn 2>/dev/null) || return 2
  [ -n "$out" ] || return 2
  while IFS= read -r line; do
    case "$line" in
      n*)
        path="${line#n}"
        case "$path" in
          "$target_real"|"$target_real"/*) return 0 ;;
        esac
        ;;
    esac
  done <<<"$out"
  return 1
}

_gc_is_live() {
  local p="$1" target_real mtime now age rc
  target_real=$(cd "$p" 2>/dev/null && pwd -P) || target_real="$p"

  _gc_tmux_occupies "$target_real"; rc=$?
  case $rc in
    0) echo "worktree: gc skipping $p -- a tmux pane's cwd is inside it (#478)" >&2; return 0 ;;
    2) echo "worktree: gc skipping $p -- could not query tmux panes; refusing to guess whether it is live (#478)" >&2; return 0 ;;
  esac

  _gc_process_refs "$target_real"; rc=$?
  case $rc in
    0) echo "worktree: gc skipping $p -- a running process's cwd is inside it (#478)" >&2; return 0 ;;
    2) echo "worktree: gc skipping $p -- could not query process cwd via lsof; refusing to guess whether it is live (#478)" >&2; return 0 ;;
  esac

  mtime=$(_gc_mtime "$p")
  if [ -z "$mtime" ]; then
    echo "worktree: gc skipping $p -- could not read its age; refusing to guess whether it is live (#478)" >&2
    return 0
  fi
  now=$(date +%s)
  age=$((now - mtime))
  if [ "$age" -lt "$GC_MIN_AGE_SECONDS" ]; then
    echo "worktree: gc skipping $p -- only ${age}s old, younger than the ${GC_MIN_AGE_SECONDS}s liveness floor (#478)" >&2
    return 0
  fi

  return 1
}

# Does <base> already contain branch <b>'s work? (agent-dotfiles#169)
#
# NOT ancestry. This repo squash-merges, and a squash merge writes one new
# commit on <base> with no parent link to the branch, so a fully-merged
# branch is never an ancestor of <base>. `gc`'s original predicate,
# `merge-base --is-ancestor`, therefore said "unmerged" about 24 of the 44
# non-ancestor worktree-held branches measured on 2026-08-11 -- permanently,
# and one more with every squash merge.
#
# The formulation is loop-tick.md's ("Which diff answers which question"),
# not a third invention: two-dot scoped to the paths the branch touched.
# Neither plain form works -- `base...b` still lists squashed work as
# outstanding, and `base..b` reports <base>'s own newer files as the
# branch's deletions once <base> drifts.
#
# Two traps, both of which fail in the "already merged" direction, which is
# the direction that deletes someone's work:
#   - The pathspec must be piped straight from `--name-only -z` into
#     `xargs -0`. Held in a variable first, command substitution strips the
#     NUL bytes and the whole list collapses into one nonexistent path that
#     matches nothing -- an empty diff, read as "merged". That is not
#     hypothetical: it happened while measuring this change, and inflated
#     the count from 12 to 41 before it was caught.
#   - Empty input to xargs is not portable: GNU runs the command once with
#     no pathspec (whole-tree diff), BSD/macOS does not run it at all (empty
#     output, read as "merged"). Decided here instead, before xargs sees it.
#
# Checked while auditing every "is this worktree unused" decision in this
# file (2026-08-22): nothing here, or anywhere else in scripts/supervisor,
# calls `git branch --merged` (`grep -rn -- "--merged" scripts/` is empty).
# It would be a near-no-op if it did -- confirmed live the same day:
# `git branch --merged origin/main | wc -l` against 360 local branches
# reports 3. Squash merges are why -- a squashed branch's tip is never an
# ancestor of `origin/main`, so `--merged` stays blind to almost everything
# this repo actually lands, which is exactly why this function diffs
# CONTENT instead. Left alone deliberately: rewriting it into a PR-state
# check (`gh pr view --json merged`) to make it "see" those branches would
# flip a near-no-op into a sweep over the ~357 branches it currently leaves
# alone, most with no configured remote tracking at all -- a much bigger,
# unrelated, and unreviewed blast radius than this fix's actual job (cwd
# liveness detection, above). Do that as its own deliberate, reviewed change
# if it's ever wanted -- not as a side effect of this one.
branch_content_is_on_base() {
  local repo="$1" b="$2" base="$3"
  # Ancestry still answers yes cheaply when it survives (a rebase-merged or
  # fast-forwarded branch); it is only insufficient, never wrong.
  git -C "$repo" merge-base --is-ancestor "refs/heads/$b" "$base" 2>/dev/null && return 0
  local mb
  mb=$(git -C "$repo" merge-base "$base" "refs/heads/$b" 2>/dev/null) || return 1
  [ -n "$mb" ] || return 1
  # No paths at all: the branch's tree is identical to the merge base, so it
  # carries commits with no net content. Fail closed rather than reason about
  # what an empty pathspec means to xargs.
  git -C "$repo" diff --quiet "$mb" "refs/heads/$b" 2>/dev/null
  case $? in
    0) return 1 ;;   # tree-identical to the merge base
    1) ;;            # differences exist -- the expected path
    *) return 1 ;;   # git failed; fail closed
  esac
  local out rc
  out=$(git -C "$repo" diff --name-only -z "$mb" "refs/heads/$b" \
        | xargs -0 git -C "$repo" diff --stat "$base..refs/heads/$b" -- 2>&1); rc=$?
  # Empty output *and* a clean exit. Anything git could not answer is not an
  # answer of "merged".
  [ "$rc" -eq 0 ] && [ -z "$out" ]
}

# agent-supervisor#682: `branch_content_is_on_base`'s own comment above
# documents its remaining gap by measurement, not guess -- of 44 non-
# ancestor worktree-held branches counted 2026-08-11, 24 had a MERGED PR
# and the scoped-diff test above reached only 7 of those. The other 17 are
# "merged, then superseded": the squash landed, but a LATER commit on
# <base> touched the same paths again, so the branch's own scoped diff
# against <base> is no longer empty even though nothing on the branch is
# unmerged work. Nothing local can tell that apart from a branch that was
# never merged at all -- only the branch's own PR record can, which is
# exactly the "second, deliberate, reviewed change" the comment above on
# `branch_content_is_on_base` left open rather than folding in as a side
# effect of an unrelated fix. This is that change, scoped to `gc`'s own
# worktree-removal predicate only -- NOT wired into `worktree.sh new`'s
# branch-reclaim check or anywhere else that deletes a REF rather than a
# disposable checkout directory. `gc` already documents why that asymmetry
# is safe: "gc removes worktrees, never branches. A wrong 'already merged'
# verdict would free a tree, not delete a ref."
#
# `_gc_fetch_merged_prs` fetches the MERGED-PR head-ref set once per `gc`
# run (never once per branch -- branch-sweep.sh already established this
# batch-then-grep shape for the identical `gh pr list` call, reused here
# rather than reinvented) and writes it to a temp file the caller manages;
# `_gc_pr_merged_from_file` is the per-branch lookup against it. Both
# no-op (return 1, "no second opinion available") when `gh` is missing,
# there is no readable GitHub remote, or the caller passed `--no-github`
# -- offline or unauthenticated must never read as "PR merged", only as
# "this check could not run", so `gc` falls back to the content-only
# answer it already had, never a looser one.
_gc_fetch_merged_prs() {
  local repo="$1" out_file="$2"
  command -v gh >/dev/null 2>&1 || return 1
  local slug
  slug=$(git -C "$repo" remote get-url origin 2>/dev/null \
         | sed -E 's#^(git@github\.com:|https://github\.com/)##; s#\.git$##')
  [ -n "$slug" ] || return 1
  gh repo view "$slug" >/dev/null 2>&1 || return 1
  # Same query shape as branch-sweep.sh's own GitHub cross-check
  # (--state all + headRefName,state, filtered to MERGED locally) --
  # reusing it rather than a second, differently-shaped `gh pr list` call
  # means one query pattern to trust, and lets this share
  # tests/supervisor/stubs-branch-sweep/gh's existing stub verbatim.
  local raw
  raw=$(gh pr list --repo "$slug" --state all --limit 4000 \
          --json headRefName,state -q '.[] | [.headRefName,.state] | @tsv' 2>/dev/null) || return 1
  awk -F'\t' '$2=="MERGED"{print $1}' <<<"$raw" | sort -u > "$out_file"
  return 0
}

_gc_pr_merged_from_file() {
  local merged_file="$1" b="$2"
  [ -n "$merged_file" ] && [ -f "$merged_file" ] || return 1
  grep -qxF "$b" "$merged_file"
}

# agent-supervisor#427: `refs/stash` is REPO-COMMON, not per-worktree -- every
# linked worktree of one repo (`git worktree add`'s whole model) shares one
# `.git` and therefore one stash LIFO stack. `git stash pop`/`apply` with no
# argument always resolves `stash@{0}`, the most recently pushed entry
# ACROSS EVERY WORKTREE, not "this worktree's own". Measured directly: push a
# stash in worktree A, `git stash pop` in a completely unrelated worktree B
# (clean tree, no conflict) applies A's WIP into B's working directory and
# reports success -- exactly what #427 observed (a lane's own `git stash`
# surfaced `lane/278-unblock-dispatch`'s stash instead of its own).
#
# `refs/worktree/*` refs ARE genuinely private per worktree (confirmed:
# `git update-ref refs/worktree/x ...` in one worktree is invisible via
# `git rev-parse` in another) -- but `git stash` is hardcoded to `refs/stash`
# and offers no way to point it at a different ref, so that mechanism cannot
# be borrowed to scope stash itself.
#
# Two interception strategies were tried and rejected, both for reasons that
# would silently defeat them in exactly the environment lanes actually run
# in:
#   - `git config --worktree alias.stash '!...'`: reads back fine via
#     `git config`, but is NOT consulted by git's own command dispatch (the
#     aliased verb still runs the real builtin) -- config.worktree is
#     honored for ordinary variables, not for overriding a builtin verb this
#     way. Measured, not assumed.
#   - Shadowing the `git` binary itself via a PATH-prepended wrapper: works
#     under a plain interactive shell, but a sandboxed agent's own command
#     execution can resolve `git` to a fixed real binary regardless of PATH
#     (measured in this environment) -- silently defeating the guard in the
#     one execution context (an autonomous coding agent's own shell calls)
#     that matters most, with no error to say so.
#
# The mechanism that DOES fire reliably, because git invokes it directly
# rather than via PATH lookup, is the `reference-transaction` hook (git
# 2.28+) -- it is called by git's own ref-transaction machinery for every ref
# write, and `core.hooksPath` set via `git config --worktree` (unlike
# `alias.*`) IS honored per worktree. Refusing any transaction that touches
# `refs/stash` closes the vulnerability for every worktree built after this
# change: if no lane can ever ADD to the shared stash, `stash pop`/`apply`
# has nothing foreign left to silently apply, ever again, without needing to
# distinguish "my own entry" from "someone else's".
#
# What this does NOT close, documented rather than silently assumed away: it
# cannot stop `stash pop`/`apply` from reading an entry that was already
# pushed by a worktree from BEFORE this fix landed (git applies the patch to
# the working tree as a merge -- that step is not a ref write and the hook
# never sees it; only the final `drop` is a ref write, and by then the
# working tree is already contaminated). The fix is preventive, not
# detective: it guarantees the pool stays empty going forward, it does not
# retroactively clean an already-populated one. `git -C "$REPO" stash clear`
# drains any pre-existing entries once, by hand, if this is rolled out onto
# a repo that already has some.
install_stash_guard() {
  local dest="$1" repo="$2"
  # extensions.worktreeConfig is repository-wide, not per-worktree -- set on
  # $repo (the common dir every worktree, including $dest, reads extensions
  # from), not $dest. Setting it anywhere else would be a no-op the first
  # time a worktree that predates this call reads it.
  git -C "$repo" config extensions.worktreeConfig true
  # Inside $dest's own git-dir (`.git/worktrees/<name>`, resolved via
  # `rev-parse --git-dir` rather than assumed), never inside the WORKING
  # tree: a hooks directory sitting in $dest itself is an untracked path
  # `git status` reports, which makes `safe_remove` (worktree.sh's own
  # `done`/`gc` dirty-check, shared with the safe-deletion contract this
  # whole file exists to keep) refuse to ever remove a worktree it just
  # created for having "uncommitted changes" that are nothing but this
  # guard's own installation. Measured, not assumed: the first version of
  # this fix put the hooks dir inside $dest and broke `gc removes a merged,
  # clean worktree` in test_worktree.sh, every single run.
  local git_dir hooks_dir
  git_dir=$(git -C "$dest" rev-parse --git-dir 2>/dev/null) || { echo "worktree: could not resolve $dest's git-dir" >&2; return 1; }
  case "$git_dir" in
    /*) : ;;
    *) git_dir="$dest/$git_dir" ;;
  esac
  hooks_dir="$git_dir/supervisor-stash-guard"
  mkdir -p "$hooks_dir"
  cat >"$hooks_dir/reference-transaction" <<'HOOK'
#!/bin/bash
# Installed by worktree.sh (agent-supervisor#427). Refuses any ref write
# touching refs/stash -- refs/stash is shared by every worktree of this
# repo, so a stash pushed here is visible, and poppable, from any of them.
[ "$1" = "prepared" ] || exit 0
blocked=0
while read -r _old _new ref; do
  if [ "$ref" = "refs/stash" ]; then
    blocked=1
  fi
done
if [ "$blocked" -eq 1 ]; then
  echo "worktree: git stash is refused in this worktree (agent-supervisor#427) -- refs/stash is shared by every worktree of this repo, so a stash pushed here could later be silently popped into (or silently pop in) a DIFFERENT lane's worktree. Commit your work in progress instead: git add -A && git commit -m wip --no-verify -- this worktree's branch is private, unlike the stash stack." >&2
  exit 1
fi
exit 0
HOOK
  chmod +x "$hooks_dir/reference-transaction"
  # Preserve any other hook this repo already ships (there are none as of
  # #427, but a future one added to $repo/.git/hooks should not silently
  # stop firing in every new worktree just because this only replaces the
  # DIRECTORY git looks hooks up in, not the hooks it had). reference-
  # transaction itself is deliberately excluded -- it is ours, not copied.
  local repo_hooks
  repo_hooks=$(git -C "$repo" rev-parse --git-path hooks 2>/dev/null) || repo_hooks=""
  if [ -n "$repo_hooks" ] && [ -d "$repo_hooks" ]; then
    local f name
    for f in "$repo_hooks"/*; do
      [ -e "$f" ] || continue
      name=$(basename "$f")
      case "$name" in
        *.sample|reference-transaction) continue ;;
      esac
      ln -sf "$f" "$hooks_dir/$name"
    done
  fi
  git -C "$dest" config --worktree core.hooksPath "$hooks_dir"
}

# agent-supervisor#562: twelve PRs across agent-supervisor and agent-tui
# ended one night reviewed, green, independently approved at head, and
# unmergeable -- every one refusing merge-pr.sh's existing independence
# gate with "PR author lane unresolved". Not one was defective; every one
# was started by writing a brief straight into a pane instead of dispatching
# through `dispatch.sh`, so the ledger never got a row for it. That gate
# already existed and already worked -- the defect is WHEN it fires: at
# merge time, after a reviewer has already spent a full pass on work nobody
# can vouch for. `#561`'s own standing rule ("all future work dispatches
# through dispatch.sh") was itself pane-authored while being written down,
# which is the concrete proof a restated rule does not bind -- see that
# issue and CLAUDE.md's own top section on why this needs a mechanism, not
# another sentence.
#
# WHERE THIS FIRES, and why not a CI check or an earlier point in
# merge-pr.sh: the actual dispatch record lives in this machine's own
# `ledger.sqlite3` (`~/.local/state/agent-dotfiles-supervisor`) -- nothing
# reachable from GitHub Actions' hosted runners can read it, so a required
# CI check (the pattern `fixpass-evidence.yml`/`ui-evidence.yml` already
# use) cannot answer this question at all, only guess from GitHub-visible
# signals a retroactive issue could fake. `merge-pr.sh` CAN read the
# ledger, but by the time anything reaches it a reviewer has already spent
# their pass -- exactly the cost this issue measures and wants moved
# earlier. The one place on this machine that sees EVERY push out of a
# lane's own worktree, before a PR exists for anyone to review, is a
# `pre-push` hook -- and `install_stash_guard` above already proved hooks
# installed here are not something a lane can forget to wire up, because
# `new` (this function's own caller) installs them on every worktree it
# hands out, whether or not that worktree's own dispatch went through
# `dispatch.sh`.
#
# WHAT IT CHECKS: `cli.py worktree-lane --path <dest> --include-reviews` --
# the exact self-lookup CLAUDE.md's invariant 10 already documents as the
# correct way for a lane to ask "does the ledger know this worktree",
# keyed on the worktree's own PATH rather than its branch name or task id.
# A worktree `dispatch.sh` created writes this row at DISPATCH time (before
# the first commit, let alone the first push) via `record-dispatch
# --worktree` -- so a legitimately dispatched task, including a fix-pass
# dispatched against a pre-existing issue (`--pr`), already has a known
# row the moment this hook runs, no matter how many pushes follow. A
# worktree nobody ever dispatched -- this function's own `new` caller was
# invoked by hand, or the work started as a pane-typed brief and was only
# LATER stuffed into a worktree -- never gets that row, because nothing
# ever called `record-dispatch` against ITS path. Filing a retroactive
# issue after the fact does not help: the row this hook reads is keyed on
# the worktree's path, not on whether some issue exists somewhere that a
# PR could later be pointed at -- see CLAUDE.md's own "the dispatcher must
# commit to scope before the outcome is known" line (#557/#561), which is
# exactly what a path-keyed, dispatch-time row proves and a same-day
# retroactive issue cannot fake.
#
# THE ESCAPE HATCH, and why it needs no code of its own: a genuinely
# human-authored PR (`#472`, `#495` -- real commits under Jon's own
# identity) is never pushed out of a lane worktree at all; CLAUDE.md
# records both as carrying "no worktree ever holding either branch." This
# hook is installed inside `$dest`'s own `.git/worktrees/.../` hooks
# directory (`core.hooksPath`, scoped to that one worktree by
# `install_stash_guard`'s own extensions.worktreeConfig above) -- it is
# physically absent from Jon's own regular checkout, so a human's own push
# never runs it. Nothing here has to recognise "this is a human" as a
# case; the human's push structurally never reaches this code, the same
# shape `mark-pr-external.sh`'s own `$TMUX_PANE` check already uses to
# tell an estate participant from an outside actor without an argument
# that could be spoofed.
#
# FAILS CLOSED, and says which failure it hit -- `cli.py` itself failing
# (a wedged lock, a missing state dir, a broken interpreter) is refused
# with a message naming that as unreadable ledger state, never silently
# treated as "no record" (which is a different, positive finding). A
# caller cannot tell "the ledger says no" apart from "the ledger could not
# be asked" by exit code alone (both refuse the push) -- but can by the
# printed reason, which is the fail-closed-but-not-mute-about-it posture
# the rest of this file already holds (`resolve-pr-contributors.sh`'s own
# header makes the identical distinction for its own callers).
#
# What this does NOT catch: `git push --no-verify` skips every local hook,
# same residual `merge-pr.sh`'s own header already documents for `gh pr
# merge` run by hand -- convention enforced by a mechanism, not a platform
# block that cannot be bypassed at all. And a worktree whose branch is
# later PUSHED FROM A DIFFERENT WORKTREE (its own `.git` dir copied or its
# ref pushed by tooling outside this hook's reach) is not something a
# per-worktree hook can see -- documented rather than silently assumed
# closed.
install_dispatch_origin_guard() {
  local dest="$1"
  local git_dir hooks_dir
  git_dir=$(git -C "$dest" rev-parse --git-dir 2>/dev/null) || { echo "worktree: could not resolve $dest's git-dir" >&2; return 1; }
  case "$git_dir" in
    /*) : ;;
    *) git_dir="$dest/$git_dir" ;;
  esac
  # Same hooks_dir install_stash_guard already pointed core.hooksPath at --
  # a second hook NAME (git only ever runs one script per hook name, but
  # `pre-push` and `reference-transaction` are different names, so both
  # live in the one directory without colliding).
  hooks_dir="$git_dir/supervisor-stash-guard"
  mkdir -p "$hooks_dir"
  cat >"$hooks_dir/pre-push" <<HOOK
#!/bin/bash
# Installed by worktree.sh (agent-supervisor#562). See install_dispatch_origin_guard's
# own comment in that file for the full reasoning -- this is the mechanism half.
#
# agent-supervisor#624: DEST is derived here, at push time, via \`git
# rev-parse --show-toplevel\` -- never baked in as a literal string the way
# this hook used to embed \$dest directly. Git invokes a hook with its own
# GIT_DIR/work-tree already resolved to the worktree the push is actually
# coming from, so this reads the SAME worktree the old baked literal named,
# just re-derived instead of frozen at install time. That matters because
# \`cli.py worktree-lane\` compares this against whatever spelling
# \`tasks.worktree_path\` holds; baking one spelling in here meant a hook
# installed while one path form was in fashion could never match a row
# written in the other, no matter how the comparison itself was fixed. This
# still relies on \`worktree-lane\` normalizing both sides (Ledger.
# get_task_for_worktree) -- re-deriving DEST here removes the SECOND
# possible source of a spelling mismatch, not the only one.
LEDGER_PYTHON="\${AGENT_PYTHON_BIN:-python3}"
DEST=\$(git rev-parse --show-toplevel 2>/dev/null)
if [ -z "\$DEST" ]; then
  echo "worktree: pre-push refused -- could not resolve this worktree's own path to check it against the dispatch ledger (agent-supervisor#624)." >&2
  exit 1
fi
OUT=\$("\$LEDGER_PYTHON" "$HERE/cli.py" worktree-lane --path "\$DEST" --include-reviews 2>&1)
RC=\$?
if [ "\$RC" -ne 0 ]; then
  echo "worktree: pre-push refused -- could not read the dispatch ledger to check this worktree's origin (ambiguous, NOT the same as 'determined undispatched'; agent-supervisor#562):" >&2
  echo "\$OUT" | sed 's/^/  /' >&2
  exit 1
fi
if grep -qF '"known":true' <<<"\$OUT"; then
  exit 0
fi
echo "worktree: pre-push refused -- \$DEST has no dispatch record in the ledger (agent-supervisor#562)." >&2
echo "worktree: this push is coming from a lane worktree the ledger never saw dispatch.sh register --" >&2
echo "worktree: dispatch this work through dispatch.sh against a real issue before pushing it." >&2
echo "worktree: genuinely human-authored work does not use a lane worktree at all -- push it from your own clone instead." >&2
exit 1
HOOK
  chmod +x "$hooks_dir/pre-push"
  git -C "$dest" config --worktree core.hooksPath "$hooks_dir"
}

CMD="${1:-}"
case "$CMD" in

new)
  SLUG="${2:-}"
  [ -n "$SLUG" ] || usage
  REPO="${3:-$PWD}"
  BASE="${4:-origin/main}"
  BRANCH="lane/$SLUG"
  ROOT="${WORKTREE_ROOT:-${TMPDIR:-/tmp}}"
  DEST="$ROOT/ad-$SLUG-$$"
  if [ -e "$DEST" ]; then
    echo "worktree: $DEST already exists" >&2
    exit 1
  fi
  # RECLAIM AN ABANDONED LANE BRANCH, but only when it is provably abandoned.
  #
  # WHY. A dispatch that fails AFTER `worktree add` created the branch but
  # BEFORE the lane starts -- a claim refusal, a quota refusal, a killed
  # dispatcher -- leaves `lane/<slug>` behind with no worktree attached. Every
  # later dispatch of that same slug then dies on:
  #
  #     fatal: a branch named 'lane/427-fix427' already exists
  #
  # and the issue becomes permanently undispatchable under its natural slug.
  # Measured 2026-08-20: 285 such branches had accumulated across the four
  # repos -- agent-supervisor alone held 165 -- and #427, #263 and #284 each
  # failed to dispatch on exactly this.
  #
  # WHAT MAKES IT SAFE TO DELETE, and it is the whole of the design:
  #
  #   - NO WORKTREE HOLDS IT. A branch checked out in a live worktree is a
  #     lane that is working right now. `git worktree list --porcelain` is the
  #     authority; if the branch appears there, we refuse and exit, exactly as
  #     before. This is the case #73 exists to protect and it is untouched.
  #   - NO UNIQUE COMMITS. `git cherry <base> <branch>` lists commits on the
  #     branch that are not on base. If it prints anything, this branch holds
  #     work nobody merged, and deleting it destroys that work. We refuse.
  #     Note this is deliberately STRICTER than `git branch -d`'s
  #     merged-ancestry test: a squash-merged branch is not an ancestor of
  #     main, so `-d` would refuse it, while cherry correctly sees its patches
  #     already upstream. We want the content question, not the ancestry one
  #     (agent-dotfiles#169 established the same distinction for `gc`).
  #
  # So the only branch this removes is one with no worktree and no commits of
  # its own -- an empty placeholder from a dispatch that never started. If
  # either check cannot be evaluated, we do NOT delete: an unreadable state is
  # not an abandoned one.
  if git -C "$REPO" show-ref --verify --quiet "refs/heads/$BRANCH"; then
    held=$(git -C "$REPO" worktree list --porcelain 2>/dev/null \
             | awk -v b="refs/heads/$BRANCH" '$1=="branch" && $2==b {print "held"}' | head -1)
    if [ -n "$held" ]; then
      echo "worktree: $BRANCH is checked out in a live worktree -- refusing to reuse it (#73)" >&2
      exit 1
    fi
    if ! unique=$(git -C "$REPO" cherry "$BASE" "$BRANCH" 2>/dev/null); then
      echo "worktree: could not compare $BRANCH against $BASE -- refusing to reuse a branch whose state is unreadable" >&2
      exit 1
    fi
    if [ -n "${unique// }" ]; then
      echo "worktree: $BRANCH has commits not on $BASE -- refusing to delete unmerged work" >&2
      echo "$unique" | head -5 >&2
      exit 1
    fi
    git -C "$REPO" branch -D "$BRANCH" >/dev/null 2>&1 \
      && echo "worktree: reclaimed abandoned branch $BRANCH (no worktree, no commits beyond $BASE)" >&2
  fi
  # REFUSE TO ADD A NEW LANE WHILE THE SHARED REPO IS CARRYING AN UNCLAIMED
  # STASH (agent-supervisor#427).
  #
  # `git worktree add` gives every lane its own working directory, but NOT
  # its own stash: `refs/stash` lives in the repo's common `.git` dir, which
  # every worktree of `$REPO` shares -- proven directly (not inferred) on
  # 2026-08-20:
  #
  #   $ git -C base stash push -m x        # from worktree A
  #   $ git -C wt-b stash list             # a DIFFERENT worktree, same repo
  #   stash@{0}: On lane-a: x
  #
  # #427 was filed on exactly this: a lane found `bootstrap-session.sh`
  # edits for #411 and a `git stash`/`git stash pop` briefly surfaced a
  # stash belonging to `lane/278-unblock-dispatch` -- inside a worktree that
  # had nothing to do with either. No script in this repo runs `git stash`
  # (checked: only comments mention the word), so the leak is not something
  # our own tooling did wrong; it is what happens the moment more than one
  # concurrent lane's *agent* touches `git stash` in a repo whose worktrees
  # share one `.git`. There is no git config that scopes `refs/stash` to a
  # single worktree (unlike `HEAD`, `refs/bisect/*` or `refs/worktree/*`,
  # which genuinely are per-worktree) -- the only place this can be caught
  # is before a SECOND lane starts sharing the danger, which is here.
  #
  # A pre-existing stash at dispatch time means somebody's uncommitted work
  # is sitting in the one namespace every worktree of this repo can see and
  # mutate. Dispatching into that state hands the new lane a live landmine:
  # its own future `git stash`/`git stash pop` (or another concurrent
  # lane's) can silently apply, lose, or leak that entry. Refuse rather than
  # let it ride, same posture as `safe_remove` refusing to discard
  # uncommitted work above -- a stash is somebody's unfinished work too.
  if stash_top=$(git -C "$REPO" rev-parse --verify --quiet refs/stash 2>/dev/null) && [ -n "$stash_top" ]; then
    echo "worktree: $REPO has an unclaimed git stash -- refusing to dispatch a new lane into it (agent-supervisor#427)" >&2
    git -C "$REPO" stash list >&2
    echo "worktree: refs/stash is shared by every worktree of this repo; resolve it first (git -C \"$REPO\" stash pop / drop) before dispatching another lane" >&2
    exit 1
  fi
  # git worktree add already writes its progress to stderr; leave stdout
  # clean so a caller can capture exactly the path from the line below.
  git -C "$REPO" worktree add -b "$BRANCH" "$DEST" "$BASE" 1>&2 || exit 1
  install_stash_guard "$DEST" "$REPO" 1>&2 || {
    echo "worktree: could not install the stash guard on $DEST (agent-supervisor#427) -- NOT handing out an unguarded worktree" >&2
    git -C "$REPO" worktree remove --force "$DEST" >/dev/null 2>&1
    exit 1
  }
  install_dispatch_origin_guard "$DEST" 1>&2 || {
    echo "worktree: could not install the dispatch-origin guard on $DEST (agent-supervisor#562) -- NOT handing out an unguarded worktree" >&2
    git -C "$REPO" worktree remove --force "$DEST" >/dev/null 2>&1
    exit 1
  }
  echo "$DEST"
  exit 0 ;;

done)
  TARGET="${2:-}"
  [ -n "$TARGET" ] || usage
  [ -d "$TARGET" ] || { echo "worktree: $TARGET does not exist" >&2; exit 1; }
  safe_remove "$TARGET" || exit 1
  exit 0 ;;

reap)
  shift
  USE_GH=1
  while :; do
    case "${1:-}" in
      --no-github) USE_GH=""; shift ;;
      *) break ;;
    esac
  done
  TARGET="${1:-}"
  [ -n "$TARGET" ] || usage
  BASE="${2:-origin/main}"
  [ -d "$TARGET" ] || { echo "worktree: $TARGET does not exist" >&2; exit 1; }

  # Single-target twin of `gc`'s own per-candidate predicate chain below,
  # reusing exactly the same functions gc already calls rather than a
  # second, differently-shaped implementation of the same checks
  # (agent-estate#804 -- "reuse worktree.sh's existing guards, do not
  # reimplement them"). This exists because a completion-time caller
  # (`reconcile_lane_completions.py`) already knows precisely which
  # worktree the task it just terminated owns; pointing `gc`'s own sweep at
  # a shared root via WORKTREE_GC_EXTRA_ROOTS to reach it would also bring
  # every SIBLING lane worktree under that root into scope for the same
  # call, which is a materially bigger blast radius than "reap this one".
  #
  # $TARGET doubles as its own "$repo" argument to every reused function
  # below (`_gc_is_live`, `branch_content_is_on_base`, `_gc_fetch_merged_
  # prs`): a linked worktree shares its refs, objects and remote config
  # with the repo that created it, so `git -C "$TARGET" ...` against
  # `refs/heads/<branch>`, `$BASE`, or `remote.origin.url` all answer
  # identically to running the same command against the main checkout --
  # there is nothing here that needs a second, separate repo path.
  if _gc_is_live "$TARGET"; then
    exit 1
  fi

  # gc's own per-candidate loop skips a branchless (detached/bare) entry
  # outright, before ever asking whether its content is merged -- same
  # posture here: a target `reap` cannot name a branch for is left alone,
  # never guessed at. `safe_remove`'s own detached-HEAD-with-unreachable-
  # commit guard (below) exists for a worktree that WAS on a branch and
  # was deliberately detached mid-task; this earlier check is for one that
  # answers "no branch" outright.
  BRANCH=$(git -C "$TARGET" symbolic-ref -q --short HEAD) || BRANCH=""
  if [ -z "$BRANCH" ]; then
    echo "worktree: $TARGET has no branch (detached HEAD) -- refusing without a branch to check against $BASE" >&2
    exit 1
  fi

  GH_MERGED_FILE=""
  if [ -n "$USE_GH" ]; then
    GH_MERGED_FILE=$(mktemp)
    if ! _gc_fetch_merged_prs "$TARGET" "$GH_MERGED_FILE"; then
      rm -f "$GH_MERGED_FILE"
      GH_MERGED_FILE=""
      echo "worktree: reap proceeding without the GitHub merged-PR cross-check for $TARGET (no gh, no readable remote, or gh pr list failed) -- a squash-merged-then-superseded branch will not be reached this call (agent-supervisor#682)" >&2
    fi
  fi
  trap '[ -n "$GH_MERGED_FILE" ] && rm -f "$GH_MERGED_FILE"' EXIT

  if branch_content_is_on_base "$TARGET" "$BRANCH" "$BASE"; then
    WHY="its content is already on $BASE"
  elif _gc_pr_merged_from_file "$GH_MERGED_FILE" "$BRANCH"; then
    WHY="its branch has a MERGED PR on GitHub even though $BASE has since diverged from its scoped diff (agent-supervisor#682)"
  else
    echo "worktree: $TARGET -- $BASE does not already contain branch '$BRANCH', and no MERGED PR is on record for it; refusing to remove unmerged work" >&2
    exit 1
  fi

  safe_remove "$TARGET" || exit 1
  echo "worktree: reaped $TARGET (branch '$BRANCH' -- $WHY)" >&2
  exit 0 ;;

gc)
  shift
  DRY=""
  USE_GH=1
  while :; do
    case "${1:-}" in
      --dry-run) DRY=1; shift ;;
      # agent-supervisor#682: skip the `gh pr list` cross-check (offline,
      # unauthenticated, or a test fixture with no real GitHub remote) --
      # same flag name and meaning as branch-sweep.sh's own `--no-github`.
      --no-github) USE_GH=""; shift ;;
      *) break ;;
    esac
  done
  REPO="${1:-$PWD}"
  BASE="${2:-origin/main}"
  # Fetched once per run, never once per candidate -- see
  # `_gc_fetch_merged_prs`'s own comment for why this batches the same way
  # branch-sweep.sh's identical `gh pr list` call already does. Empty
  # string means "no second opinion this run" (gh missing, no GitHub
  # remote, `gh pr list` itself failed, or --no-github) -- every caller of
  # `_gc_pr_merged_from_file` already treats that as "check unavailable",
  # never as "merged".
  GH_MERGED_FILE=""
  if [ -n "$USE_GH" ]; then
    GH_MERGED_FILE=$(mktemp)
    if ! _gc_fetch_merged_prs "$REPO" "$GH_MERGED_FILE"; then
      rm -f "$GH_MERGED_FILE"
      GH_MERGED_FILE=""
      echo "worktree: gc proceeding without the GitHub merged-PR cross-check for $REPO (no gh, no readable remote, or gh pr list failed) -- squash-merged-then-superseded worktrees will not be reached this run (agent-supervisor#682)" >&2
    fi
  fi
  trap '[ -n "$GH_MERGED_FILE" ] && rm -f "$GH_MERGED_FILE"' EXIT
  # The worktree gc is running from is nobody's garbage -- it is the caller's
  # own tree. `git worktree remove` refuses it anyway; refusing here says so
  # in gc's own words rather than as a git error.
  SELF=$(git -C "$PWD" rev-parse --show-toplevel 2>/dev/null || true)
  # Scope every candidate to $REPO/.worktrees/ before any liveness/age/dirty/
  # merged check runs (agent-supervisor#527 follow-up). `git worktree list`
  # answers for every worktree git knows about for this repo, wherever on
  # disk it was registered -- a temp dir, or an unrelated state directory
  # another loop uses for its own operational memory. Measured on the live
  # estate: two registered worktrees sat outside any repo's .worktrees/ tree,
  # one under a macOS temp dir, one under ~/.local/state/estate-loop holding
  # a DIFFERENT loop's own check.log/briefs/owner.md -- a live sweep reaching
  # that one would delete state, not disposable code. Scoped to .worktrees/
  # -- sweeping outside it (temp dirs, other loops' state directories) is a
  # separate decision, not made here. Resolved with `cd ... && pwd -P`, same
  # as `_gc_is_live`'s `target_real`, so a symlinked path (e.g. macOS's
  # /var -> /private/var) compares equal rather than failing the scope check
  # for a candidate that is genuinely inside .worktrees/.
  WORKTREES_ROOT_REAL=$(cd "$REPO/.worktrees" 2>/dev/null && pwd -P) || WORKTREES_ROOT_REAL=""
  # agent-supervisor#682: `.worktrees/` is not where most of this estate's
  # lane worktrees actually live. `new` (above) hands every dispatch a
  # worktree under `${WORKTREE_ROOT:-${TMPDIR:-/tmp}}/ad-<slug>-<pid>` --
  # NOT `$REPO/.worktrees/` -- so the scope filter above, left as an open
  # question by #530 ("whether the two excluded path classes should ever
  # be swept under some future design is explicitly left open, not decided
  # here"), was quietly excluding the majority of this repo's own
  # dispatched lane worktrees from `gc` entirely: measured against the
  # live estate 2026-08-28, 120 of 130 registered non-main worktrees sat
  # outside `.worktrees/`, and every one of them was skipped for that
  # reason alone -- before liveness, age, or content were even checked.
  #
  # `WORKTREE_GC_EXTRA_ROOTS` (space-separated real or resolvable paths,
  # unset by default -- no existing caller or test sets it, so default
  # behavior is byte-for-byte unchanged) lets a caller opt a directory
  # into scope. Even inside an opted-in root, a candidate must ALSO match
  # `new`'s own naming shape (`ad-*-<digits>`, `$DEST`'s literal pattern
  # above) before it is considered -- #530's own rejected example
  # (`~/.local/state/estate-loop/review-524`, a DIFFERENT loop's state
  # directory that happens to sit in a plausible root) is a name this
  # pattern does not match, so pointing `WORKTREE_GC_EXTRA_ROOTS` at a
  # broad temp root does not also sweep it in. This is a whitelist, not a
  # widened default -- CLAUDE.md invariant 6 ("unknown means not offered,
  # not broken").
  EXTRA_ROOTS_REAL=()
  for _extra_root in ${WORKTREE_GC_EXTRA_ROOTS:-}; do
    _er_real=$(cd "$_extra_root" 2>/dev/null && pwd -P) || continue
    EXTRA_ROOTS_REAL+=("$_er_real")
  done
  # Parse `worktree list --porcelain`: records are blank-line separated,
  # each starting with `worktree <path>`, optionally followed by a
  # `branch refs/heads/<name>` line (absent for detached/bare entries).
  # The first record is always the main worktree (REPO itself) -- never a
  # gc candidate, so it is dropped before the loop below.
  path="" branch="" first=1
  paths=() branches=()
  while IFS= read -r line; do
    case "$line" in
      worktree\ *) path="${line#worktree }"; branch="" ;;
      branch\ refs/heads/*) branch="${line#branch refs/heads/}" ;;
      "")
        if [ -n "$path" ]; then
          if [ "$first" -eq 1 ]; then
            first=0
          else
            paths+=("$path")
            branches+=("$branch")
          fi
        fi
        path="" ;;
    esac
  done < <(git -C "$REPO" worktree list --porcelain)
  if [ -n "$path" ] && [ "$first" -eq 0 ]; then
    paths+=("$path")
    branches+=("$branch")
  fi

  # agent-supervisor#762: one `tmux list-panes -a` for the whole sweep, not
  # one per candidate -- see `_gc_tmux_occupies`'s own comment on
  # `_GC_PANES_CACHED` for why this is safe (a sweep is one point-in-time
  # snapshot; querying tmux again per candidate cannot see anything the
  # first query didn't, it can only make the sweep slower and give a later
  # candidate a DIFFERENT snapshot than an earlier one saw).
  _GC_PANES=$(tmux list-panes -a -F '#{pane_current_path}' 2>/dev/null); _GC_PANES_RC=$?
  _GC_PANES_CACHED=1

  removed=0 skipped=0
  for i in "${!paths[@]}"; do
    p="${paths[$i]}"
    b="${branches[$i]}"
    if [ -z "$b" ]; then
      echo "worktree: gc skipping $p -- no branch (detached or bare)" >&2
      skipped=$((skipped + 1))
      continue
    fi
    if [ ! -d "$p" ]; then
      echo "worktree: gc skipping $p -- registered but missing on disk (run 'git worktree prune')" >&2
      skipped=$((skipped + 1))
      continue
    fi
    # Scope filter, before any liveness/age/dirty/merged check -- see the
    # comment on WORKTREES_ROOT_REAL above for why this exists and what it
    # deliberately leaves undecided. An empty WORKTREES_ROOT_REAL (no
    # .worktrees/ under $REPO at all) must never be treated as "matches
    # everything" -- an unquoted empty pattern with a trailing /* glob is
    # just "/*", which would match any absolute path. Nothing is in scope
    # when the root itself does not exist.
    p_real=$(cd "$p" 2>/dev/null && pwd -P) || p_real="$p"
    in_scope=1
    if [ -z "$WORKTREES_ROOT_REAL" ]; then
      in_scope=0
    else
      case "$p_real" in
        "$WORKTREES_ROOT_REAL"|"$WORKTREES_ROOT_REAL"/*) : ;;
        *) in_scope=0 ;;
      esac
    fi
    # Opted-in extra roots (#682, see WORKTREE_GC_EXTRA_ROOTS's own comment
    # above): only a candidate whose basename matches `new`'s own
    # ad-<slug>-<pid> naming AND sits under one of the opted-in roots is
    # brought into scope this way -- everything else stays excluded by the
    # .worktrees/-only default, unchanged.
    if [ "$in_scope" -eq 0 ] && [ "${#EXTRA_ROOTS_REAL[@]}" -gt 0 ]; then
      case "$(basename "$p_real")" in
        ad-*-[0-9]*)
          for _er in "${EXTRA_ROOTS_REAL[@]}"; do
            case "$p_real" in
              "$_er"|"$_er"/*) in_scope=1; break ;;
            esac
          done
          ;;
      esac
    fi
    if [ "$in_scope" -eq 0 ]; then
      echo "worktree: gc skipping $p -- outside $REPO/.worktrees/ and not opted into scope via WORKTREE_GC_EXTRA_ROOTS (sweeping outside .worktrees/ by default is a separate decision, not made here)" >&2
      skipped=$((skipped + 1))
      continue
    fi
    if [ -n "$SELF" ] && [ "$p" = "$SELF" ]; then
      echo "worktree: gc skipping $p -- this is the worktree gc is running in" >&2
      skipped=$((skipped + 1))
      continue
    fi
    if _gc_is_live "$p"; then
      skipped=$((skipped + 1))
      continue
    fi
    why=""
    if branch_content_is_on_base "$REPO" "$b" "$BASE"; then
      why="its content is already on $BASE"
    elif _gc_pr_merged_from_file "$GH_MERGED_FILE" "$b"; then
      # agent-supervisor#682: content check said no (base has moved past
      # what the branch touched -- "merged, then superseded"), but a
      # MERGED PR for this exact branch name is on record. The worktree
      # holds nothing unmerged; its content landed and was later built on
      # top of, which the scoped-diff test alone cannot distinguish from
      # unmerged work.
      why="its branch has a MERGED PR on GitHub even though $BASE has since diverged from its scoped diff (agent-supervisor#682)"
    else
      echo "worktree: gc skipping $p -- $BASE does not already contain branch '$b', and no MERGED PR is on record for it" >&2
      skipped=$((skipped + 1))
      continue
    fi
    if safe_remove "$p" "$DRY"; then
      if [ -n "$DRY" ]; then
        echo "worktree: gc would remove $p (branch '$b' -- $why)" >&2
      else
        echo "worktree: gc removed $p (branch '$b' -- $why)" >&2
      fi
      removed=$((removed + 1))
    else
      skipped=$((skipped + 1))
    fi
  done
  if [ -n "$DRY" ]; then
    echo "worktree: gc dry run done -- would remove $removed, skipped $skipped (nothing changed)" >&2
  else
    echo "worktree: gc done -- removed $removed, skipped $skipped" >&2
  fi
  exit 0 ;;

guard)
  REPO="${2:-$PWD}"
  # The Director branching on top of a lane's half-finished edits is the bug
  # this whole tool exists to prevent -- for the Director's own use of the
  # shared checkout, not just lane dispatch. Call this before doing anything
  # in the shared checkout that assumes it is clean.
  status=$(git -C "$REPO" status --porcelain 2>&1)
  if [ -n "$status" ]; then
    echo "worktree: $REPO has uncommitted changes -- that is someone's live work, not a base to branch on. Use worktree.sh new instead of working in the shared checkout." >&2
    echo "$status" >&2
    exit 1
  fi
  exit 0 ;;

*) usage ;;
esac
