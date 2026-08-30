#!/bin/bash
# agent-estate#822: `orphan-worktree-reap.sh` is the reaper for the
# $TMPDIR/ad-<slug>-<pid> worktree directories that `git worktree list`
# does not enumerate for any repo this estate knows about -- exactly the
# population `worktree.sh gc` and #805's completion-time reaper can never
# reach, because both only ever consider a worktree some repo's own
# registration list still names.
#
# This file proves two things `test_worktree_reap.sh` does not, because that
# file only ever calls `worktree.sh reap` directly against a path it already
# knows the answer for:
#
#   1. `orphan-worktree-reap.sh` correctly scopes itself to the UNREGISTERED
#      population -- a worktree some known repo's `git worktree list` DOES
#      still enumerate is left completely alone, even when it would
#      otherwise clear every one of `reap`'s own guards (clean, merged, not
#      live). This is the registration-scope filter's own job, and #822's
#      verify step ("re-run after --reap: confirm registered worktrees are
#      completely untouched") is exactly this claim.
#   2. Report mode changes nothing; `--reap` reaps only what report mode
#      already named a candidate, reusing `worktree.sh reap`'s own guard
#      chain (never a second, differently-shaped implementation of it) --
#      so a live/dirty/unmerged unregistered worktree survives `--reap`
#      exactly as it would survive a direct `reap` call.
#
# `test_worktree_reap.sh` already exhaustively covers every individual guard
# (live pane, dirty tree, unmerged/unpushed branch, detached HEAD) at the
# `worktree.sh reap` layer -- this file does not re-litigate those; it
# covers one representative case of each direction plus the registration
# scope this layer adds on top.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WT="$HERE/../../scripts/supervisor/worktree.sh"
ORPHAN="$HERE/../../scripts/supervisor/orphan-worktree-reap.sh"
source "$HERE/../../scripts/supervisor/tmux-isolation.sh"
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
require_dest() {
  if [ -z "${2:-}" ] || [ ! -d "${2:-}" ]; then
    echo "  FATAL $1: worktree.sh new gave no usable path ('${2:-}'); aborting rather than letting git -C '' run here" >&2
    exit 2
  fi
}

echo "orphan-worktree-reap.sh"

D=$(mktemp -d)

# A throwaway, isolated tmux server for the liveness case -- never the
# operator's own attached session (CLAUDE.md invariant 4).
RT="$D/tmux-rt"
mkdir -p "$RT"
rtmux() { env -u TMUX TMUX_TMPDIR="$RT" tmux -f /dev/null "$@"; }
cleanup_rt() { unset TMUX; export TMUX_TMPDIR="$RT"; assert_isolated_tmux && tmux -f /dev/null kill-server 2>/dev/null; }
ANCHOR="wt822-anchor-$$"
if ! rtmux new-session -d -s "$ANCHOR" -c "$D" 2>/dev/null; then
  echo "  FATAL: could not start a throwaway tmux server under \$RT -- the live case cannot be tested for real" >&2
  exit 2
fi

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo"
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo one > "$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -q -m "initial"
git -C "$REPO" push -q -u origin main
git -C "$REPO" remote set-head origin main >/dev/null 2>&1 || true
# The bare origin's OWN HEAD symref still points at the default "master" it
# was `init`ed with, which never gained a branch (only "main" did) --
# `remote set-head` above only fixed $REPO's own local remote-tracking HEAD,
# not the bare repo's. A second, independent clone (below) resolves the
# BARE repo's HEAD directly and would otherwise see "refs/heads/master",
# get "remote HEAD refers to nonexistent ref", and check out no branch at
# all. Fixed at the source so every future clone of this origin behaves.
git -C "$D/origin.git" symbolic-ref HEAD refs/heads/main

# A SECOND, independent clone of the same origin. `git worktree add` always
# registers a new worktree with whichever repo's own `.git` created it --
# there is no way to create one this file's own registration scan cannot
# see by simply not registering it anywhere. The real 251 measured live are
# unregistered against the estate's own KNOWN, canonical repo checkouts
# while still carrying a fully valid `.git` pointer -- the faithful way to
# reproduce that here is a worktree registered against a DIFFERENT repo
# instance than the one this test's scan is told about, not a worktree with
# no registration anywhere. `REPO` below is the one named in
# SUPERVISOR_GC_REPOS (what the sweep is told to scan); `REPO_UNKNOWN` is
# not, so anything created against it resolves fully via its own `.git` but
# is invisible to the sweep's registration check -- exactly #822's shape.
git clone -q "$D/origin.git" "$D/repo-unknown"
REPO_UNKNOWN="$D/repo-unknown"
git -C "$REPO_UNKNOWN" config user.email test@example.com
git -C "$REPO_UNKNOWN" config user.name "Test"

# `install_dispatch_origin_guard`'s pre-push hook checks the ledger for a
# dispatch record -- irrelevant to this file's own concern, so stub it the
# same way test_worktree.sh / test_worktree_reap.sh already do.
mkdir -p "$D/bin"
cat >"$D/bin/allow-python3" <<'STUB'
#!/bin/bash
echo '{"known":true,"lane":"stub:0","path":"stub","task":"stub"}'
STUB
chmod +x "$D/bin/allow-python3"
export AGENT_PYTHON_BIN="$D/bin/allow-python3"

# Same #478 age-floor override test_worktree_reap.sh already documents:
# every fixture here is seconds old by construction.
export WORKTREE_GC_MIN_AGE_SECONDS=0

# `worktree.sh new`'s own DEST base -- deliberately OUTSIDE $REPO, matching
# production ($TMPDIR/ad-<slug>-<pid>, never $REPO/.worktrees/). This is
# also the exact shape `gc`'s own default scope filter excludes and #822
# exists to reach.
WORKTREE_ROOT="$D/tmproot"
mkdir -p "$WORKTREE_ROOT"
export WORKTREE_ROOT

# `orphan-worktree-reap.sh`'s registration scan is pointed at $REPO only --
# never $REPO_UNKNOWN. Everything created against $REPO_UNKNOWN below is
# therefore genuinely unregistered from this sweep's own point of view,
# while still fully git-resolvable through its own `.git` pointer -- #822's
# exact shape, and a materially more faithful fixture than deleting an
# admin entry by hand: it proves the sweep reaches a worktree via its OWN
# `.git` pointer, never via the scanned repo's own list.
export SUPERVISOR_GC_REPOS="$REPO"

# --- Case 1: unregistered, clean, merged -> a candidate, and --reap removes it
out=$(bash "$WT" new 822-clean "$REPO_UNKNOWN" origin/main 2>/dev/null); rc=$?
want_exit "setup: new (clean/merged case) exits 0" "$rc" 0 "$out"
CLEAN_DEST="$out"
require_dest "new (clean/merged case)" "$CLEAN_DEST"
echo "merged change" > "$CLEAN_DEST/clean.txt"
git -C "$CLEAN_DEST" add clean.txt
git -C "$CLEAN_DEST" -c user.email=test@example.com -c user.name=Test commit -q -m "merged work"
git -C "$CLEAN_DEST" push -q origin lane/822-clean
git -C "$REPO_UNKNOWN" fetch -q origin
git -C "$REPO_UNKNOWN" merge -q --no-edit origin/lane/822-clean
git -C "$REPO_UNKNOWN" push -q origin main
git -C "$CLEAN_DEST" fetch -q origin

# --- Case 2: unregistered, unmerged/unpushed -> must survive --reap
out=$(bash "$WT" new 822-unmerged "$REPO_UNKNOWN" origin/main 2>/dev/null); rc=$?
want_exit "setup: new (unmerged case) exits 0" "$rc" 0 "$out"
UNMERGED_DEST="$out"
require_dest "new (unmerged case)" "$UNMERGED_DEST"
echo "unmerged, unpushed change" > "$UNMERGED_DEST/unmerged.txt"
git -C "$UNMERGED_DEST" add unmerged.txt
git -C "$UNMERGED_DEST" -c user.email=test@example.com -c user.name=Test commit -q -m "unmerged, unpushed work"

# --- Case 3: unregistered, merged branch but a LIVE tmux pane's cwd inside
# it -> must survive --reap (the "never reap a live one" direction #822's
# own brief calls out by name).
out=$(bash "$WT" new 822-live "$REPO_UNKNOWN" origin/main 2>/dev/null); rc=$?
want_exit "setup: new (live case) exits 0" "$rc" 0 "$out"
LIVE_DEST="$out"
require_dest "new (live case)" "$LIVE_DEST"
echo "live-but-merged work" > "$LIVE_DEST/live.txt"
git -C "$LIVE_DEST" add live.txt
git -C "$LIVE_DEST" -c user.email=test@example.com -c user.name=Test commit -q -m "live-but-merged work"
git -C "$LIVE_DEST" push -q origin lane/822-live
git -C "$REPO_UNKNOWN" fetch -q origin
git -C "$REPO_UNKNOWN" merge -q --no-edit origin/lane/822-live
git -C "$REPO_UNKNOWN" push -q origin main
git -C "$LIVE_DEST" fetch -q origin
LIVE_SESSION="wt822-live-$$"
rtmux new-session -d -s "$LIVE_SESSION" -c "$LIVE_DEST"

# --- Case 4: REGISTERED against $REPO (the repo this sweep is told to
# scan), clean and merged -- would clear every one of `reap`'s own guards,
# but must be left ENTIRELY alone by this sweep because it is not this
# sweep's population to touch (#822's own verify step: "the registered
# worktrees are completely untouched").
out=$(bash "$WT" new 822-registered "$REPO" origin/main 2>/dev/null); rc=$?
want_exit "setup: new (registered case) exits 0" "$rc" 0 "$out"
REGISTERED_DEST="$out"
require_dest "new (registered case)" "$REGISTERED_DEST"
echo "registered, merged work" > "$REGISTERED_DEST/registered.txt"
git -C "$REGISTERED_DEST" add registered.txt
git -C "$REGISTERED_DEST" -c user.email=test@example.com -c user.name=Test commit -q -m "registered, merged work"
git -C "$REGISTERED_DEST" push -q origin lane/822-registered
git -C "$REPO" fetch -q origin
# $REPO's own local `main` can be behind origin/main by now -- cases 1 and 3
# above already pushed their own merges to origin/main from $REPO_UNKNOWN,
# a sibling clone $REPO never fetched into its local branch. Fast-forward
# first, or this merge+push is a stale non-fast-forward push that origin
# rejects.
git -C "$REPO" merge -q --ff-only origin/main
git -C "$REPO" merge -q --no-edit origin/lane/822-registered
git -C "$REPO" push -q origin main
git -C "$REGISTERED_DEST" fetch -q origin

# --- Report mode: changes nothing -------------------------------------------
report_out=$(env -u TMUX TMUX_TMPDIR="$RT" bash "$ORPHAN" --no-github --root "$WORKTREE_ROOT" 2>&1); report_rc=$?
want_exit "report mode exits 0" "$report_rc" 0 "$report_out"
for d in "$CLEAN_DEST" "$UNMERGED_DEST" "$LIVE_DEST" "$REGISTERED_DEST"; do
  if [ -d "$d" ]; then :; else bad "report mode removed nothing" "$d is gone after a report-only run"; fi
done
ok "report mode removed nothing (all four fixtures still present)"
if grep -qF "CANDIDATE $CLEAN_DEST" <<<"$report_out"; then ok "report names the clean/merged worktree a candidate"; else bad "report names the clean/merged worktree a candidate" "$report_out"; fi
if grep -qF "KEPT $UNMERGED_DEST" <<<"$report_out"; then ok "report keeps the unmerged worktree"; else bad "report keeps the unmerged worktree" "$report_out"; fi
if grep -qF "KEPT $LIVE_DEST" <<<"$report_out"; then ok "report keeps the live worktree"; else bad "report keeps the live worktree" "$report_out"; fi
if grep -qF "$REGISTERED_DEST" <<<"$report_out"; then bad "report never mentions the registered worktree at all" "$report_out"; else ok "report never mentions the registered worktree at all (out of scope, not merely kept)"; fi

reap_out=$(env -u TMUX TMUX_TMPDIR="$RT" bash "$ORPHAN" --reap --no-github --root "$WORKTREE_ROOT" 2>&1); reap_rc=$?
want_exit "--reap exits 0" "$reap_rc" 0 "$reap_out"

if [ -d "$CLEAN_DEST" ]; then bad "--reap removes the unregistered clean/merged worktree" "$CLEAN_DEST still present: $reap_out"; else ok "--reap removes the unregistered clean/merged worktree"; fi
if [ -d "$UNMERGED_DEST" ]; then ok "--reap leaves the unregistered unmerged worktree in place"; else bad "--reap leaves the unregistered unmerged worktree in place" "removed despite unmerged, unpushed work: $reap_out"; fi
if [ -d "$LIVE_DEST" ]; then ok "--reap leaves the unregistered live worktree in place"; else bad "--reap leaves the unregistered live worktree in place" "removed despite a live pane inside it: $reap_out"; fi
if [ -d "$REGISTERED_DEST" ]; then ok "--reap leaves the REGISTERED worktree completely untouched"; else bad "--reap leaves the REGISTERED worktree completely untouched" "removed despite being registered against \$REPO: $reap_out"; fi

rtmux kill-session -t "$LIVE_SESSION" 2>/dev/null

# --- Mutation, the direction that matters most for this layer specifically:
# a patched copy of orphan-worktree-reap.sh whose registration lookup always
# says "not registered" must reap the REGISTERED worktree too -- proving
# case 4's survival above is actually caused by the scope filter, not by
# some unrelated accident (e.g. it happening to also be unmerged).
# The mutant's own HERE resolves to its own directory (matching every other
# script in this tree) and looks for worktree.sh beside itself -- symlinked
# in here rather than copied so the mutant runs the REAL worktree.sh's own
# guard chain, isolating this mutation to the registration-scope check alone.
MUT_DIR="$D/mutbin"
mkdir -p "$MUT_DIR"
ln -sf "$WT" "$MUT_DIR/worktree.sh"
MUT_NEVER_REGISTERED="$MUT_DIR/orphan-worktree-reap.sh"
mut_rc=0
python3 - "$ORPHAN" "$MUT_NEVER_REGISTERED" <<'PY' || mut_rc=$?
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = 'if [ -n "${REGISTERED[$dreal]:-}" ]; then'
replacement = 'if false; then'
assert text.count(marker) == 1, "registration-check line not unique -- script shape changed"
open(dst, "w").write(text.replace(marker, replacement, 1))
PY
if [ "$mut_rc" -ne 0 ]; then
  bad "setup: patched a copy of orphan-worktree-reap.sh whose registration check never fires" "could not patch $ORPHAN (exit $mut_rc)"
else
  ok "setup: patched a copy of orphan-worktree-reap.sh whose registration check never fires"
  chmod +x "$MUT_NEVER_REGISTERED"
  mut_out=$(env -u TMUX TMUX_TMPDIR="$RT" bash "$MUT_NEVER_REGISTERED" --reap --no-github --root "$WORKTREE_ROOT" 2>&1)
  if [ ! -d "$REGISTERED_DEST" ]; then
    ok "mutation confirmed: disabling the registration-scope filter reaps a registered worktree (case 4's own survival assertion would now be RED)"
  else
    bad "mutation confirmed: disabling the registration-scope filter reaps a registered worktree" "expected $REGISTERED_DEST to be removed by the mutant, it survived: $mut_out"
  fi
fi

cleanup_rt
echo "orphan-worktree-reap.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
