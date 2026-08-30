#!/bin/bash
# agent-estate#822 ("#805 half B" -- #805 itself did half A, the completion-
# time reaper for a task's own registered worktree). This is the reaper for
# the $TMPDIR/ad-<slug>-<pid> directories neither `worktree.sh gc` nor #805's
# `lane_worktree_reap.py` can even see: both of those only ever consider a
# worktree that some repo's own `git worktree list` still enumerates.
# Measured live 2026-08-29: 427 total `$TMPDIR/ad-*/` dirs, ~1.8G; 251 of
# those do not appear in ANY of the estate's own repos' `git worktree list`;
# 223 of the 251 still carry a `.git` entry; 238 were modified within the
# last day (oldest is 2 days old).
#
# WHY THIS IS SAFE TO REAP WITHOUT REGISTRATION, and why it calls
# `worktree.sh reap` rather than re-deriving a second guard chain
# (agent-estate#804's own rule, restated in #805: "reuse the guards, do not
# reimplement them"): every check `reap` makes -- liveness (`_gc_is_live`:
# a tmux pane's cwd, a running process's actual cwd via `lsof`, and a
# too-young-to-trust-either-signal age floor), a dirty tree, a detached HEAD
# with no branch containing it, and branch content already on [base] or
# carrying a MERGED PR on record -- resolves through the CANDIDATE'S OWN
# `.git` pointer via `git -C <path>`. None of it asks the owning repo's own
# `git worktree list` anything. A directory that enumeration cannot see for
# whatever reason (a pruned admin entry, a path-resolution mismatch, a
# completion-time reaper that never ran against it) is not thereby
# ungovernable -- `reap`'s predicate chain answers exactly the same question
# about it that it would about a registered one, using only what the
# worktree's own `.git` file (or the lack of one) can prove.
#
# A candidate whose `.git` pointer is fully broken -- the repo it once linked
# to no longer has an admin entry for it, so git can no longer resolve
# ANYTHING about it -- is not a git repository at all from this candidate's
# own point of view, and is refused, fail-closed, the same direction every
# guard in this file already leans. This sweep does not try to second-guess
# that outcome: a directory that answers "not a git repository" is reported
# KEPT, never assumed dead just because it looks abandoned.
#
# agent-estate#843 ("#837 finding 2"): this ONE verdict -- "not a git
# repository" -- is cached per candidate path, checked before the rest of
# the guard chain runs for that candidate. It is the only one of the six
# refusal classes a live 179s run grouped (54 unmerged, 21 not-a-git-repo,
# 19 uncommitted, 10 cwd-inside-it, 10 detached HEAD, 10 younger than the
# age floor) that is safe to remember: a directory that is not a git repo
# does not spontaneously become one without deliberate action, unlike the
# other five, which are each transient by definition (a PR merges, a
# process exits, a worktree ages past the floor, a detached HEAD's commit
# lands on `origin/main`) or by construction. Caching any of those risks
# `#825`'s failure shape -- a stale verdict blocking a worktree that has
# since become reapable -- so NONE of the other five is cached here; every
# one of them still runs `reap`'s real guard chain fresh, every sweep, same
# as before this change.
#
# agent-estate#846: that 179s run was `cli.py reconcile-lane-completions`
# sweeping REGISTERED worktrees through `lane_worktree_reap.py` --
# a different candidate set and a different call path from THIS file's own
# sweep (unregistered `$TMPDIR/ad-*/` dirs, above). `orphan-worktree-reap.sh`
# is never invoked by `reconcile_lane_completions.py`/`lane_worktree_reap.py`
# and is not wired into any cron/launchd/watchdog schedule -- this cache
# speeds up THIS script's own manual, standalone runs only. A follow-up
# profile of the actual 179s sweep (agent-estate#846) found its cost
# concentrates in the `unmerged`/`uncommitted` classes via a `gh pr list`
# network call made once per branch-bearing candidate, not in the
# not-a-git-repository class this cache addresses -- so this change does not
# reduce that sweep's cost, and was never wired to. See
# `_nongit_identity`/`_nongit_cache_hit`/
# `_nongit_cache_put` below for the cache itself, and its own comment for
# why the entry self-invalidates rather than trusting a path string alone.
#
# Report mode first, matching `poller-leak-cleanup.sh`'s own established
# pattern in this codebase: the default run only lists every candidate and
# why it qualifies (or why a same-shaped directory does not), and changes
# nothing. Only `--reap` acts, and even then only through this exact
# `worktree.sh reap` call, re-run fresh per candidate rather than trusted
# from the report pass -- state can change between the two (a new tmux pane
# attaches, a new commit lands), and the guard chain must answer for the
# world as it is at the moment of removal, not as it was moments earlier.
# The one exception is the cached "not a git repository" verdict above,
# which by construction cannot have changed to "live"/"dirty"/"unmerged" in
# the interim -- there is no guard chain result to have gone stale.
#
# Usage:
#   orphan-worktree-reap.sh [--reap] [--no-github] [--root <dir>] [--base <ref>]
#     --reap        actually reap every candidate found (default: report only)
#     --no-github   skip reap's own MERGED-PR cross-check (offline/test use,
#                   same flag name and meaning as gc's/reap's own --no-github)
#     --root <dir>  scan <dir> instead of ${WORKTREE_ROOT:-${TMPDIR:-/tmp}}
#                   (worktree.sh new's own DEST base)
#     --base <ref>  passed through to `worktree.sh reap` as [base] (default
#                   origin/main, same default `reap`/`gc` already use)
#
# ORPHAN_REAP_NONGIT_CACHE overrides the "not a git repository" cache file
# path (default: `${SUPERVISOR_STATE:-~/.local/state/agent-dotfiles-
# supervisor}/orphan-reap-nongit-cache.tsv`) -- tests point this at a throwaway
# path so a real run's cache is never touched.
#
# Exit 0: ran to completion (report or reap), whether or not any candidate
#         was found/reaped -- "0 candidates" is a real, valid answer, never
#         conflated with a failure to look. Exit 2: usage error, or this
#         script could not resolve a repository list at all (see
#         SUPERVISOR_GC_REPOS below) -- refusing to guess which directories
#         are already registered elsewhere is safer than guessing wrong in
#         either direction. Exit 1: --reap ran and at least one candidate
#         that cleared the report pass was refused on the real pass (state
#         changed between the two, or `reap` itself errored) -- the
#         directories themselves are untouched either way; this exit code
#         only says the run's own bookkeeping saw a refusal worth a
#         non-zero exit for whatever calls this on a schedule.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKTREE_SH="$HERE/worktree.sh"

# agent-estate#843: `<device>:<inode>[:<birth-epoch>]` -- the same identity
# test the kernel itself uses for "is this the same file" (recreating a
# path always gets a fresh inode; a birth time, where the platform can
# report one, is extra insurance against the astronomically rare inode
# reuse case). Prints nothing (and the caller must treat that as "cannot
# identify, do not cache") if neither `stat` flavor answers -- GNU coreutils
# and BSD/macOS spell the same query differently, and there is no third
# guess to fall back to.
_nongit_identity() {
  local p="$1" dev_inode birth
  dev_inode=$(stat -f '%d:%i' "$p" 2>/dev/null) || dev_inode=$(stat -c '%d:%i' "$p" 2>/dev/null) || return 1
  birth=$(stat -f '%B' "$p" 2>/dev/null)
  if [ -z "$birth" ]; then
    birth=$(stat -c '%W' "$p" 2>/dev/null)
    [ "$birth" = "0" ] && birth=""
  fi
  printf '%s:%s' "$dev_inode" "${birth:-0}"
}

# 0 = the cache has a "not a git repository" entry for exactly this path AND
# exactly this identity (fail closed on everything else: file missing or
# unreadable, no entry for the path, an entry whose identity has since
# changed -- a removed-and-recreated directory -- or two conflicting entries
# for the same path, which can only mean the cache itself is corrupt/
# ambiguous and must never be trusted). 1 otherwise, meaning "run the real
# check".
_nongit_cache_hit() {
  local path="$1" identity="$2" cache="$3"
  [ -n "$identity" ] || return 1
  [ -r "$cache" ] || return 1
  local cpath cid found=""
  while IFS=$'\t' read -r cpath cid; do
    [ -n "$cpath" ] || continue
    if [ "$cpath" = "$path" ]; then
      if [ -n "$found" ] && [ "$found" != "$cid" ]; then
        return 1  # ambiguous entry -- do not trust either
      fi
      found="$cid"
    fi
  done < "$cache" 2>/dev/null
  [ -n "$found" ] && [ "$found" = "$identity" ]
}

# Records ONLY the "not a git repository" verdict, keyed on path, replacing
# any prior entry for the same path (a `mv`-based rewrite via `awk`, never an
# in-place append, so a path that was cached under an old identity does not
# accumulate a second, conflicting line the next time this same path is
# checked). Best-effort: a cache directory that cannot be created, or a
# rename that fails, leaves the cache exactly as it was -- the next sweep
# just re-runs the real check for this path again, which is correct, not a
# bug.
_nongit_cache_put() {
  local path="$1" identity="$2" cache="$3"
  [ -n "$identity" ] || return 0
  local dir; dir=$(dirname "$cache")
  mkdir -p "$dir" 2>/dev/null || return 1
  local tmp="$cache.tmp.$$"
  awk -F'\t' -v p="$path" 'BEGIN{OFS="\t"} $1 != p {print}' "$cache" 2>/dev/null > "$tmp"
  printf '%s\t%s\n' "$path" "$identity" >> "$tmp"
  mv "$tmp" "$cache" 2>/dev/null || rm -f "$tmp"
}

NONGIT_CACHE="${ORPHAN_REAP_NONGIT_CACHE:-${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}/orphan-reap-nongit-cache.tsv}"

REAP=0
USE_GH_FLAG=""
ROOT="${WORKTREE_ROOT:-${TMPDIR:-/tmp}}"
BASE="origin/main"
while [ $# -gt 0 ]; do
  case "$1" in
    --reap) REAP=1; shift ;;
    --no-github) USE_GH_FLAG="--no-github"; shift ;;
    --root) ROOT="${2:?--root requires a value}"; shift 2 ;;
    --base) BASE="${2:?--base requires a value}"; shift 2 ;;
    -h|--help)
      sed -n '/^# Usage:/,/^$/p' "$0" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *)
      echo "orphan-worktree-reap: unknown argument: $1" >&2
      exit 2 ;;
  esac
done

if [ ! -e "$WORKTREE_SH" ]; then
  echo "orphan-worktree-reap: worktree.sh is missing beside this script -- cannot reuse its guard chain" >&2
  exit 2
fi

# --- which repos this estate knows about, to exclude what's already
# REGISTERED (already this-estate's `gc` / #805's completion-time reaper's
# job, not this sweep's -- see the header comment above for why this sweep
# exists only for the population those two cannot see at all).
#
# SUPERVISOR_GC_REPOS (colon-separated) overrides for tests, same shape
# `watchdog-checks.sh`'s own worktree-gc sweep already uses -- production
# reads cli.py's own DEFAULT_REPOSITORIES table rather than a second
# hardcoded path list.
repos_raw=""
if [ -n "${SUPERVISOR_GC_REPOS:-}" ]; then
  repos_raw="$SUPERVISOR_GC_REPOS"
elif [ -e "$HERE/cli.py" ]; then
  repos_raw=$("${SUPERVISOR_PYTHON:-python3}" -c '
import sys
sys.path.insert(0, "'"$HERE"'")
try:
    import cli
except Exception:
    sys.exit(1)
print(":".join(r["path"] for r in cli.DEFAULT_REPOSITORIES))
' 2>/dev/null)
fi
if [ -z "$repos_raw" ]; then
  echo "orphan-worktree-reap: could not resolve a repository list (cli.py missing or unimportable, and SUPERVISOR_GC_REPOS unset) -- refusing to guess which worktrees are already registered elsewhere" >&2
  exit 2
fi

# Every path `git worktree list --porcelain` reports for a real repo on this
# machine, resolved with `cd ... && pwd -P` so a symlinked spelling (macOS's
# /var -> /private/var) compares equal to however a candidate directory's own
# resolution spells the same path -- the same reasoning `worktree.sh gc`'s
# own WORKTREES_ROOT_REAL comparison already documents.
declare -A REGISTERED
saved_ifs="$IFS"; IFS=':'
for repo in $repos_raw; do
  IFS="$saved_ifs"
  [ -n "$repo" ] || continue
  [ -d "$repo" ] || continue
  git -C "$repo" rev-parse --show-toplevel >/dev/null 2>&1 || continue
  while IFS= read -r line; do
    case "$line" in
      worktree\ *)
        wpath="${line#worktree }"
        wreal=$(cd "$wpath" 2>/dev/null && pwd -P) || wreal="$wpath"
        REGISTERED["$wreal"]=1
        ;;
    esac
  done < <(git -C "$repo" worktree list --porcelain 2>/dev/null)
  IFS=':'
done
IFS="$saved_ifs"

# --- enumerate: only directories `worktree.sh new` itself could have
# produced ($ROOT/ad-<slug>-<pid>), never every directory that merely starts
# with "ad-" by coincidence -- the identical naming-shape whitelist `gc`'s
# own WORKTREE_GC_EXTRA_ROOTS opt-in already applies (CLAUDE.md invariant 6:
# unknown means not offered, not swept in).
shopt -s nullglob
candidates_all=("$ROOT"/ad-*-[0-9]*)
shopt -u nullglob

if [ "${#candidates_all[@]}" -eq 0 ]; then
  echo "orphan-worktree-reap: 0 ad-*-<pid> director(ies) under $ROOT -- nothing to scan"
  exit 0
fi

total=${#candidates_all[@]}
registered_count=0
kept_count=0
to_reap=()

nongit_cached_count=0

for dir in "${candidates_all[@]}"; do
  [ -d "$dir" ] || continue
  dreal=$(cd "$dir" 2>/dev/null && pwd -P) || dreal="$dir"
  if [ -n "${REGISTERED[$dreal]:-}" ]; then
    registered_count=$((registered_count + 1))
    continue
  fi

  # agent-estate#843: the one cached class, checked before the rest of the
  # guard chain runs for this candidate. `identity` is empty when neither
  # `stat` flavor could answer (an unreadable/vanished directory) -- both
  # cache functions above already treat that as "cannot cache/cannot hit",
  # so this falls straight through to the real check below, same as a cold
  # cache would.
  identity=$(_nongit_identity "$dreal") || identity=""
  if _nongit_cache_hit "$dreal" "$identity" "$NONGIT_CACHE"; then
    kept_count=$((kept_count + 1))
    nongit_cached_count=$((nongit_cached_count + 1))
    echo "orphan-worktree-reap: KEPT $dir -- not a git repository (cached, agent-estate#843)"
    continue
  fi

  # A directory `git` itself cannot resolve as a work tree at all is not a
  # candidate for `reap`'s guard chain -- there is no branch, no status, no
  # HEAD to ask about, only a fatal error from every git subcommand that
  # would try. `rev-parse --is-inside-work-tree` is the cheap, read-only way
  # to ask that question without running (and mis-surfacing the failure of)
  # the branch-resolution step `reap` itself would otherwise hit first. This
  # is the ONLY one of the six refusal classes cached -- see the header
  # comment's agent-estate#843 note for why the other five are not.
  if ! git -C "$dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    kept_count=$((kept_count + 1))
    echo "orphan-worktree-reap: KEPT $dir -- not a git repository"
    _nongit_cache_put "$dreal" "$identity" "$NONGIT_CACHE"
    continue
  fi

  # Report pass: run `reap`'s own guard chain in --dry-run, changing
  # nothing. Its own stdout/stderr already say exactly why (live, dirty,
  # unmerged, detached-unreachable, or clear) -- surfaced verbatim rather
  # than re-derived, so this sweep's report can never drift from what a
  # real --reap call against the same directory would actually decide.
  out=$(bash "$WORKTREE_SH" reap --dry-run $USE_GH_FLAG "$dir" "$BASE" 2>&1)
  rc=$?
  reason=$(printf '%s\n' "$out" | tail -1)
  if [ "$rc" -eq 0 ]; then
    to_reap+=("$dir")
    echo "orphan-worktree-reap: CANDIDATE $dir -- $reason"
  else
    kept_count=$((kept_count + 1))
    echo "orphan-worktree-reap: KEPT $dir -- $reason"
  fi
done

echo "orphan-worktree-reap: $total ad-*-<pid> dir(s) scanned, $registered_count already registered elsewhere (left to worktree.sh gc / the completion-time reaper), ${#to_reap[@]} candidate(s), $kept_count kept ($nongit_cached_count via the not-a-git-repository cache, agent-estate#843)"

if [ "$REAP" -ne 1 ]; then
  if [ "${#to_reap[@]}" -gt 0 ]; then
    echo "orphan-worktree-reap: report only (pass --reap to actually remove the candidate(s) above); nothing was removed"
  fi
  exit 0
fi

worst=0
for dir in "${to_reap[@]}"; do
  out=$(bash "$WORKTREE_SH" reap $USE_GH_FLAG "$dir" "$BASE" 2>&1)
  rc=$?
  reason=$(printf '%s\n' "$out" | tail -1)
  if [ "$rc" -eq 0 ]; then
    echo "orphan-worktree-reap: REAPED $dir -- $reason"
  else
    echo "orphan-worktree-reap: refused to reap $dir on the real pass after it passed --dry-run -- something about it changed between the two calls (a new tmux pane, a new commit); left untouched -- $reason" >&2
    worst=1
  fi
done

exit "$worst"
