# agent-supervisor#716: dispatch.sh (2753 lines) was split into dispatch.sh
# plus 7 sourced-only siblings (dispatch-rehome.sh, dispatch-args.sh,
# dispatch-preflight.sh, dispatch-guards.sh, dispatch-lane-select.sh,
# dispatch-worktree.sh, dispatch-send.sh, dispatch-record.sh). A mutation-test
# marker that used to live inside dispatch.sh's own text now lives in
# whichever sibling the code moved into -- see #713's watchdog.sh split for
# the same class of retarget ("left unpatched, they would have silently
# mutated dead text").
#
# Rather than have every test_dispatch* mutation block hardcode which sibling
# file it now targets (fragile: the next split reshuffle breaks it again the
# same way), this helper SEARCHES for the marker across the whole sandboxed
# scripts/supervisor tree and patches whichever single file actually contains
# it -- failing loudly (not silently) if the marker is in zero files or in
# more than one.
import glob
import os
import shutil

# The exact split set (dispatch.sh plus its 7 sourced-only siblings), NOT a
# `dispatch*.sh` wildcard: dispatch-claude-print.sh and dispatch-pi-rpc.sh
# are separate, pre-existing sibling SCRIPTS (not part of this split) that
# happen to share dispatch.sh's own bash-3.2-safe idioms -- a wildcard glob
# matched both `set -- "${POSITIONAL[@]+...}"` in dispatch-args.sh AND an
# identical line in dispatch-claude-print.sh, and "found in 2 files" is
# exactly the ambiguity patch() exists to refuse rather than guess through.
SPLIT_FILES = (
    "dispatch.sh",
    "dispatch-rehome.sh",
    "dispatch-args.sh",
    "dispatch-preflight.sh",
    "dispatch-guards.sh",
    "dispatch-lane-select.sh",
    "dispatch-worktree.sh",
    "dispatch-send.sh",
    "dispatch-record.sh",
)


def sandbox(dispatch_path, target_dir):
    """Copy dispatch_path's whole directory (every sibling it sources, plus
    every sibling THOSE source) into target_dir, fresh. Returns the path to
    the sandboxed dispatch.sh -- the entry point every test should run
    instead of $DISPATCH once a mutation is applied.

    Copying the WHOLE directory, not just dispatch.sh, is what makes
    dispatch.sh's own `HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"`
    keep working unmodified in the sandbox: HERE resolves to target_dir by
    construction, the same way it resolves to the real scripts/supervisor
    directory in production, and every source line inside dispatch.sh and
    its siblings finds its neighbour right there -- no HERE override needed.
    """
    src_dir = os.path.dirname(os.path.abspath(dispatch_path))
    if os.path.exists(target_dir):
        shutil.rmtree(target_dir)
    shutil.copytree(src_dir, target_dir)
    return os.path.join(target_dir, "dispatch.sh")


def patch(target_dir, marker, mutated, count=1, glob_pattern=None):
    """Replace the first `count` occurrences of `marker` with `mutated` in
    whichever single file under target_dir (matching glob_pattern, default
    SPLIT_FILES) contains it. Raises AssertionError -- same failure shape the
    pre-split tests already relied on -- if the marker is missing, or
    ambiguous across more than one file, or its count does not match what
    the caller expects.
    """
    if glob_pattern is None:
        candidates = [
            os.path.join(target_dir, name)
            for name in SPLIT_FILES
            if os.path.exists(os.path.join(target_dir, name))
        ]
    else:
        candidates = sorted(glob.glob(os.path.join(target_dir, glob_pattern)))
    hits = []
    for f in candidates:
        text = open(f).read()
        if marker in text:
            hits.append((f, text))
    if len(hits) == 0:
        raise AssertionError(
            "marker not found in any of %r -- script shape changed: %.80r"
            % (candidates, marker)
        )
    if len(hits) > 1:
        raise AssertionError(
            "marker found in %d files (want exactly 1, ambiguous): %r"
            % (len(hits), [f for f, _ in hits])
        )
    f, text = hits[0]
    if text.count(marker) != count:
        raise AssertionError(
            "marker found %d time(s) in %s, want %d -- not unique" % (text.count(marker), f, count)
        )
    text = text.replace(marker, mutated, 1)
    open(f, "w").write(text)
    return f


def read_owning_file(target_dir, marker, glob_pattern=None):
    """Same search as patch(), without writing -- for callers that only need
    to assert a marker's presence/absence (e.g. confirming a cut case arm is
    gone) rather than replace it.
    """
    if glob_pattern is None:
        candidates = [
            os.path.join(target_dir, name)
            for name in SPLIT_FILES
            if os.path.exists(os.path.join(target_dir, name))
        ]
    else:
        candidates = sorted(glob.glob(os.path.join(target_dir, glob_pattern)))
    hits = [f for f in candidates if marker in open(f).read()]
    return hits
