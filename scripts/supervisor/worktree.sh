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
#   worktree.sh gc [--dry-run] [repo] [base]
#                                           remove every worktree whose
#                                            branch's content is already on
#                                            [base], whose tree is clean, and
#                                            that is not LIVE -- no tmux pane
#                                            or process references it, and it
#                                            is older than
#                                            $WORKTREE_GC_MIN_AGE_SECONDS
#                                            (default 3600); --dry-run reports
#                                            what it would remove and changes
#                                            nothing
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

# agent-supervisor#478: is worktree $1 LIVE -- occupied by a tmux pane,
# referenced by a running process, or simply too young to trust either signal
# between tool calls? `gc`'s clean+merged predicate says nothing about this;
# see the header comment above for the incident this closes.
#
# Three independent checks, each one able to say "keep" on its own:
#   1. a tmux pane's #{pane_current_path} is inside it -- the direct signal.
#   2. a running process's command line references it.
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

# 0 = a pane's cwd is inside $1; 1 = tmux answered and none matched;
# 2 = tmux itself could not be asked (no server, tmux missing, ...).
_gc_tmux_occupies() {
  local target_real="$1" panes pane pane_real
  panes=$(tmux list-panes -a -F '#{pane_current_path}' 2>/dev/null) || return 2
  while IFS= read -r pane; do
    [ -n "$pane" ] || continue
    pane_real=$(cd "$pane" 2>/dev/null && pwd -P) || continue
    case "$pane_real" in
      "$target_real"|"$target_real"/*) return 0 ;;
    esac
  done <<<"$panes"
  return 1
}

# 0 = some process's command line references $1; 1 = ps answered and none
# did; 2 = ps itself could not be asked.
_gc_process_refs() {
  local target_real="$1" out
  out=$(ps -eo command 2>/dev/null) || return 2
  [ -n "$out" ] || return 2
  grep -F -q "$target_real" <<<"$out" && return 0
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
    0) echo "worktree: gc skipping $p -- a running process references it (#478)" >&2; return 0 ;;
    2) echo "worktree: gc skipping $p -- could not query running processes; refusing to guess whether it is live (#478)" >&2; return 0 ;;
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
  echo "$DEST"
  exit 0 ;;

done)
  TARGET="${2:-}"
  [ -n "$TARGET" ] || usage
  [ -d "$TARGET" ] || { echo "worktree: $TARGET does not exist" >&2; exit 1; }
  safe_remove "$TARGET" || exit 1
  exit 0 ;;

gc)
  shift
  DRY=""
  if [ "${1:-}" = "--dry-run" ]; then DRY=1; shift; fi
  REPO="${1:-$PWD}"
  BASE="${2:-origin/main}"
  # The worktree gc is running from is nobody's garbage -- it is the caller's
  # own tree. `git worktree remove` refuses it anyway; refusing here says so
  # in gc's own words rather than as a git error.
  SELF=$(git -C "$PWD" rev-parse --show-toplevel 2>/dev/null || true)
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
    if [ -n "$SELF" ] && [ "$p" = "$SELF" ]; then
      echo "worktree: gc skipping $p -- this is the worktree gc is running in" >&2
      skipped=$((skipped + 1))
      continue
    fi
    if _gc_is_live "$p"; then
      skipped=$((skipped + 1))
      continue
    fi
    if ! branch_content_is_on_base "$REPO" "$b" "$BASE"; then
      echo "worktree: gc skipping $p -- $BASE does not already contain branch '$b'" >&2
      skipped=$((skipped + 1))
      continue
    fi
    if safe_remove "$p" "$DRY"; then
      if [ -n "$DRY" ]; then
        echo "worktree: gc would remove $p (branch '$b' -- its content is already on $BASE)" >&2
      else
        echo "worktree: gc removed $p (branch '$b' -- its content is already on $BASE)" >&2
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
