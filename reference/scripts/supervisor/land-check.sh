#!/bin/bash
# agent-estate#868: a squash-merge-safe "did this land" instrument for
# worktree.sh's sweep candidate surface. NOT wired into worktree.sh's reap
# path by this change -- #868 asks for the technique validated by hand
# against real candidates first; wiring it into what the sweep is willing
# to delete is a separate, higher-stakes change with its own review.
#
# THE PROBLEM (agent-supervisor#554 sub-decision 4, `director-reclaim.md`):
# under squash-merge a landed branch's pre-squash commits never return to
# "not ahead of origin/main" -- the ancestry check the sweep already uses
# (`branch_content_is_on_base` in worktree.sh) never clears for them, and
# the two other cheap instruments are both wrong for the same reason:
#   - `git diff base..branch` / `base...branch` report the SAME real
#     content differences for any branch cut before base moved on, whether
#     the branch landed or not -- three-dot still diffs against the
#     merge-base, it does not answer "is branch's content already there".
#   - `git cherry` matches patch-ids, which squash-merging does not
#     preserve, so it reports every squash-merged commit as "not in
#     upstream" identically to one that was never merged at all.
#   - `git merge-base --is-ancestor` is a THIRD broken instrument, not a
#     workaround for the first two: a squash-merged branch's commits are
#     never ancestors of the squash commit either (the squash commit is a
#     new, unrelated commit), so "NOT-ANCESTOR" means nothing here and must
#     not be read as "unrescued". Verified by hand against `refs/rescued/*`
#     while building this script -- see the header of the reverse-apply
#     candidate in agent-estate#868 for the full account.
#
# THE CANDIDATE TECHNIQUE: diff the branch against its own merge-base with
# base, then attempt to REVERSE-APPLY that diff against base's current
# tree. A clean reverse-apply means every line the branch added is already
# present in base, byte for byte, independent of commit history -- the
# standard technique for detecting squash-merged content. Implemented here
# against a SCRATCH INDEX (`GIT_INDEX_FILE` pointed at a tempfile, seeded
# with `git read-tree $base`) so this never touches the caller's actual
# index or working tree, and is safe to run from any checkout including a
# dirty one.
#
# THE ASYMMETRIC FAILURE DIRECTION THIS MUST HOLD (agent-estate#868's own
# brief): this instrument only WIDENS what can be positively confirmed
# "landed" -- it must never make the sweep more willing to guess. A false
# "landed" would delete work that never merged (irreversible); a false
# "not landed" only leaves a worktree around (costs disk). So every branch
# below defaults to the SAFE side:
#   - clean reverse-apply                       -> landed        (exit 0)
#   - reverse-apply fails to apply cleanly       -> not-landed    (exit 1)
#   - the check itself could not be run (missing
#     merge-base, missing branch, git failure)   -> unknown       (exit 2)
# "not-landed" and "unknown" both refuse a sweep permission to delete;
# only "landed" grants it. A branch genuinely landed-then-superseded (a
# LATER commit on base touched the same lines the branch touched) will
# report "not-landed" here, same as a branch that never merged at all --
# that is this instrument's own known gap, and it is on the safe side
# (worktree.sh's existing `_gc_pr_merged_from_file` cross-check, added by
# agent-supervisor#682, is the only local answer for that case; it is not
# reimplemented here).
#
# Usage:
#   land-check.sh <repo> <branch-or-ref> [base]
#     <repo>          a path to a git checkout (or any of its linked
#                     worktrees -- they share refs/objects, so any one
#                     answers identically)
#     <branch-or-ref> a local branch name (refs/heads/<name> is tried
#                     first) or, if that does not resolve, any ref-ish
#                     git itself understands (refs/rescued/<name>, a tag,
#                     a raw sha) -- this is what lets the same instrument
#                     be pointed at agent-estate#554's `refs/rescued/*`,
#                     which are not worktree branches at all
#     [base]          default origin/main
#
# Prints one line to stdout: "landed", "not-landed", or "unknown", followed
# by a short reason. Exit code mirrors the verdict: 0 landed, 1 not-landed,
# 2 unknown (same "2 means could not measure, refused not guessed"
# convention as host-pressure.sh / host_pressure.py in this directory).
set -uo pipefail

usage() { echo "usage: land-check.sh <repo> <branch-or-ref> [base]" >&2; exit 2; }

REPO="${1:-}"
BRANCH="${2:-}"
BASE="${3:-origin/main}"
[ -n "$REPO" ] && [ -n "$BRANCH" ] || usage
[ -d "$REPO" ] || { echo "unknown: $REPO does not exist"; exit 2; }

# Prefer refs/heads/<name> (the normal worktree-branch case); fall back to
# the literal argument for anything else git itself resolves (refs/rescued/
# <name>, a tag, a raw sha). Whichever resolves is what every message below
# names, so the reader can tell which one was actually checked.
if git -C "$REPO" rev-parse --verify --quiet "refs/heads/$BRANCH" >/dev/null 2>&1; then
  REF="refs/heads/$BRANCH"
elif git -C "$REPO" rev-parse --verify --quiet "$BRANCH" >/dev/null 2>&1; then
  REF="$BRANCH"
else
  echo "unknown: neither refs/heads/$BRANCH nor '$BRANCH' resolves in $REPO"
  exit 2
fi
git -C "$REPO" rev-parse --verify --quiet "$BASE" >/dev/null 2>&1 || {
  echo "unknown: base '$BASE' does not resolve in $REPO"
  exit 2
}

# Ancestry is the cheap fast path when it survives (a rebase-merged or
# fast-forwarded branch never needed the reverse-apply trick at all) --
# same posture as `branch_content_is_on_base` in worktree.sh. A squash
# merge will NOT satisfy this; that is the whole reason this script exists,
# not a bug in this check.
if git -C "$REPO" merge-base --is-ancestor "$REF" "$BASE" 2>/dev/null; then
  echo "landed: $REF is an ancestor of $BASE (plain/fast-forward merge, reverse-apply not needed)"
  exit 0
fi

MB=$(git -C "$REPO" merge-base "$BASE" "$REF" 2>/dev/null) || {
  echo "unknown: no merge-base between $BASE and $REF (unrelated histories)"
  exit 2
}
[ -n "$MB" ] || { echo "unknown: empty merge-base result for $REF vs $BASE"; exit 2; }

# An empty diff against the merge-base means the branch carries no net
# content of its own (e.g. a merge commit with nothing new) -- there is
# nothing to reverse-apply, and nothing for base to have "landed". Report
# this honestly rather than reading trivial success as a real answer.
DIFF=$(git -C "$REPO" diff "$MB" "$REF" 2>&1)
DIFF_RC=$?
if [ "$DIFF_RC" -ne 0 ]; then
  echo "unknown: git diff $MB..$REF failed (rc=$DIFF_RC)"
  exit 2
fi
if [ -z "$DIFF" ]; then
  echo "not-landed: $REF is tree-identical to its own merge-base with $BASE -- carries no content to have landed"
  exit 1
fi

# Reverse-apply against a SCRATCH index seeded from $BASE's tree -- never
# the caller's real index or working tree. `--check` alone reports success/
# failure without writing anything even to the scratch index.
TMP_INDEX=$(mktemp)
trap 'rm -f "$TMP_INDEX"' EXIT

if ! GIT_INDEX_FILE="$TMP_INDEX" git -C "$REPO" read-tree "$BASE" 2>/dev/null; then
  echo "unknown: could not seed a scratch index from $BASE's tree"
  exit 2
fi

APPLY_OUT=$(GIT_INDEX_FILE="$TMP_INDEX" git -C "$REPO" apply --check -R --cached - <<<"$DIFF" 2>&1)
APPLY_RC=$?

if [ "$APPLY_RC" -eq 0 ]; then
  echo "landed: reverse-apply of $REF's diff against $BASE's own merge-base ($MB) applies cleanly to $BASE -- every changed line is already present in $BASE, byte for byte"
  exit 0
fi

echo "not-landed: reverse-apply against $BASE failed (git apply --check -R exit $APPLY_RC) -- $BASE does not contain $REF's content byte-for-byte (either never merged, or merged-then-superseded by a later commit touching the same lines; this instrument cannot tell those apart, and both fail closed as not-landed)"
printf '%s\n' "$APPLY_OUT" | sed 's/^/  /'
exit 1
