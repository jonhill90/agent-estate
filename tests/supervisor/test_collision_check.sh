#!/bin/bash
# collision-check.sh -- the pre-dispatch collision check (agent-supervisor#291).
#
# THE MOTIVATING CASE, REPRODUCED HERE: as#263 and as#266, two lanes that
# independently wrote the same fix to scripts/supervisor/quota-watch.sh, one
# at 10 files/+689 lines, the other at 3 files/+243 -- one of those PRs was
# entirely wasted work. Case 1 below is that shape: an in-flight lane already
# has uncommitted changes to a file, and a fresh dispatch whose brief names
# that same file must be refused. Case 2 is the case that stops this from
# becoming a concurrency cap: two genuinely disjoint dispatches both succeed.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$HERE/../../scripts/supervisor/collision-check.sh"
CORE_DIR="$HERE/../../scripts/supervisor"
pass=0; fail=0

# A stubbed `gh` on PATH -- agent-supervisor#432's own review found this suite
# hitting REAL gh calls against whatever repo the process's cwd resolved to,
# so its outcome depended on which PRs happened to be open live. GH_BIN holds
# only a `gh` symlink to the stub; the rest of PATH is untouched so git,
# python3 etc. still resolve normally. Default: no open PRs, so cases 1-5
# (written before the open-PR-holder half existed) see the same "nothing
# there" result they always did.
GH_BIN=$(mktemp -d)
ln -s "$HERE/stubs/gh-collision-check" "$GH_BIN/gh"
export PATH="$GH_BIN:$PATH"
export GH_STUB_PR_LIST_JSON="[]"

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "collision-check.sh"

D=$(mktemp -d)
git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/base" 2>/dev/null
REPO="$D/base"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name Test
git -C "$REPO" checkout -q -b main
mkdir -p "$REPO/scripts/supervisor"
echo original > "$REPO/scripts/supervisor/quota-watch.sh"
echo original > "$REPO/scripts/supervisor/other.sh"
git -C "$REPO" add -A
git -C "$REPO" commit -q -m initial
git -C "$REPO" push -q -u origin main

# An in-flight lane, built with the SAME primitives a real dispatch uses
# (register_lane -> reconstruct_task -> assign), not a raw SQL row -- so this
# fixture only exercises shapes the real ledger API can actually produce.
register_lane() {
  local state="$1" task="$2" lane="$3" worktree="$4"
  PYTHONPATH="$CORE_DIR" python3 -c "
import core
l = core.Ledger('$state')
l.register_lane(lane='$lane', pane_id='%1', nonce='n1', harness='claude',
  repo='$REPO', server_id='s1', session_id='sess1', command='claude')
l.reconstruct_task(task_id='$task', source_kind='issue', source_url='https://x',
  source_ref='1', summary='test', source_state='OPEN', status='created',
  evidence=['test'], status_marker=None)
l.assign(task_id='$task', lane='$lane', pane_nonce='n1', summary='test', worktree_path='$worktree')
"
}

# --- 1. RED: an in-flight lane's uncommitted change collides with the new
#        dispatch's brief -- must be refused, naming lane and file ----------
STATE1=$(mktemp -d "$D/state1.XXXXXX")
LANE_A_WT="$D/laneA"
git -C "$REPO" worktree add -q -b lane/263-quota-fix "$LANE_A_WT" main
echo "lane A's fix" >> "$LANE_A_WT/scripts/supervisor/quota-watch.sh"
register_lane "$STATE1" "as263-quota-fix" "agent-supervisor:3" "$LANE_A_WT"

CAND_WT="$D/laneB"
git -C "$REPO" worktree add -q -b lane/266-quota-fix "$CAND_WT" main
echo 'Fix `scripts/supervisor/quota-watch.sh` -- it double counts.' > "$D/brief-266.md"

out=$(AGENT_SUPERVISOR_STATE_DIR="$STATE1" DISPATCH_PYTHON=python3 \
  "$CHECK" check --issue 266 --brief "$D/brief-266.md" --worktree "$CAND_WT" \
  --repo-path "$REPO" --exclude-lane "agent-supervisor:4" 2>&1)
rc=$?
want_exit "a collision with an in-flight lane's file is refused" "$rc" 1 "$out"
want_contains "...naming the colliding lane" "agent-supervisor:3" "$out"
want_contains "...naming the colliding file" "scripts/supervisor/quota-watch.sh" "$out"

# --- 2. GREEN: two genuinely disjoint dispatches both succeed -- the case
#        that stops this from becoming a concurrency cap --------------------
echo 'Fix `scripts/supervisor/other.sh` instead -- unrelated file.' > "$D/brief-disjoint.md"
out2=$(AGENT_SUPERVISOR_STATE_DIR="$STATE1" DISPATCH_PYTHON=python3 \
  "$CHECK" check --issue 267 --brief "$D/brief-disjoint.md" --worktree "$CAND_WT" \
  --repo-path "$REPO" --exclude-lane "agent-supervisor:4" 2>&1)
rc2=$?
want_exit "a disjoint dispatch (different file) is allowed" "$rc2" 0 "$out2"
want_contains "...says no-conflict" "no-conflict" "$out2"

# --- 3. --force overrides a real collision and says so -----------------------
out3=$(AGENT_SUPERVISOR_STATE_DIR="$STATE1" DISPATCH_PYTHON=python3 \
  "$CHECK" check --issue 266 --brief "$D/brief-266.md" --worktree "$CAND_WT" \
  --repo-path "$REPO" --exclude-lane "agent-supervisor:4" --force 2>&1)
rc3=$?
want_exit "--force allows a known collision" "$rc3" 0 "$out3"
want_contains "...records that it was forced" "forced" "$out3"
want_contains "...still names what was overridden" "agent-supervisor:3" "$out3"

# --- 4. no signal at all -> UNKNOWN, ALLOW, and it says so, not silence ------
echo "just do the thing, no file named here" > "$D/brief-vague.md"
CAND_WT2="$D/laneC"
git -C "$REPO" worktree add -q -b lane/268-vague "$CAND_WT2" main
out4=$(AGENT_SUPERVISOR_STATE_DIR="$STATE1" DISPATCH_PYTHON=python3 \
  "$CHECK" check --issue 268 --brief "$D/brief-vague.md" --worktree "$CAND_WT2" \
  --repo-path "$REPO" --exclude-lane "agent-supervisor:5" 2>&1)
rc4=$?
want_exit "no determinable file set allows the dispatch" "$rc4" 0 "$out4"
want_contains "...says UNKNOWN explicitly, never silence" "unknown" "$out4"

# --- 5. an in-flight lane whose worktree is gone is skipped, not fatal ------
STATE2=$(mktemp -d "$D/state2.XXXXXX")
register_lane "$STATE2" "as999-ghost" "agent-supervisor:9" "$D/does-not-exist"
CAND_WT3="$D/laneD"
git -C "$REPO" worktree add -q -b lane/269-ghost "$CAND_WT3" main
echo 'Touch `scripts/supervisor/quota-watch.sh` too.' > "$D/brief-ghost.md"
out5=$(AGENT_SUPERVISOR_STATE_DIR="$STATE2" DISPATCH_PYTHON=python3 \
  "$CHECK" check --issue 269 --brief "$D/brief-ghost.md" --worktree "$CAND_WT3" \
  --repo-path "$REPO" --exclude-lane "agent-supervisor:6" 2>&1)
rc5=$?
want_exit "an unreadable in-flight worktree is skipped rather than failing the whole check" "$rc5" 0 "$out5"

# --- 6. self-exclusion on the open-PR holder side, both directions ---------
#        (the M1/M3 evidence from #432's PR description was a manual one-off
#        run, never committed -- these are that evidence, committed.)
STATE3=$(mktemp -d "$D/state3.XXXXXX")
echo 'Touch `scripts/supervisor/quota-watch.sh` too.' > "$D/brief-selfexcl.md"
CAND_WT4="$D/laneE"
git -C "$REPO" worktree add -q -b lane/501-selfexcl "$CAND_WT4" main

export GH_STUB_PR_LIST_JSON='[{"number":501,"files":[{"path":"scripts/supervisor/quota-watch.sh"}]}]'
out6a=$(AGENT_SUPERVISOR_STATE_DIR="$STATE3" DISPATCH_PYTHON=python3 \
  "$CHECK" check --issue 501 --brief "$D/brief-selfexcl.md" --worktree "$CAND_WT4" \
  --repo-path "$REPO" --repo acme/widgets --exclude-lane "agent-supervisor:x" --pr 501 2>&1)
rc6a=$?
want_exit "reviewing PR 501 is not blocked by PR 501's own diff" "$rc6a" 0 "$out6a"
want_contains "...says no-conflict" "no-conflict" "$out6a"

out6b=$(AGENT_SUPERVISOR_STATE_DIR="$STATE3" DISPATCH_PYTHON=python3 \
  "$CHECK" check --issue 502 --brief "$D/brief-selfexcl.md" --worktree "$CAND_WT4" \
  --repo-path "$REPO" --repo acme/widgets --exclude-lane "agent-supervisor:x" --pr 777 2>&1)
rc6b=$?
want_exit "...but IS still blocked by a DIFFERENT open PR touching the same file" "$rc6b" 1 "$out6b"
want_contains "...naming that PR as the holder" "PR#501" "$out6b"
export GH_STUB_PR_LIST_JSON="[]"

# --- 7. a gh outage must be distinguishable from a clean check -------------
STATE4=$(mktemp -d "$D/state4.XXXXXX")
echo 'Touch `scripts/supervisor/other.sh` too.' > "$D/brief-outage.md"
CAND_WT5="$D/laneF"
git -C "$REPO" worktree add -q -b lane/601-outage "$CAND_WT5" main

out7a=$(AGENT_SUPERVISOR_STATE_DIR="$STATE4" DISPATCH_PYTHON=python3 GH_STUB_FAIL=1 \
  "$CHECK" check --issue 601 --brief "$D/brief-outage.md" --worktree "$CAND_WT5" \
  --repo-path "$REPO" --repo acme/widgets --exclude-lane "agent-supervisor:x" 2>&1)
rc7a=$?
want_exit "a gh outage still allows the dispatch (best-effort posture preserved)" "$rc7a" 0 "$out7a"
want_contains "...but says the PR check did not run" "SKIPPED" "$out7a"

out7b=$(AGENT_SUPERVISOR_STATE_DIR="$STATE4" DISPATCH_PYTHON=python3 \
  "$CHECK" check --issue 602 --brief "$D/brief-outage.md" --worktree "$CAND_WT5" \
  --repo-path "$REPO" --repo acme/widgets --exclude-lane "agent-supervisor:x" 2>&1)
rc7b=$?
want_exit "a real clean check (gh working, no matches) also allows" "$rc7b" 0 "$out7b"
want_missing "...and carries no skip marker -- blindness is distinguishable from cleanliness" "SKIPPED" "$out7b"

# --- 8. repo scoping: derived from --repo-path's origin when --repo is
#        omitted, never left to fall through to the caller's ambient cwd;
#        and when no repo can be identified at all, gh is never invoked ------
GH_LOG="$D/gh.log"
git -C "$REPO" remote set-url origin git@github.com:acme/widgets.git

STATE5=$(mktemp -d "$D/state5.XXXXXX")
echo 'Touch `scripts/supervisor/other.sh` too.' > "$D/brief-scoped.md"
CAND_WT6="$D/laneG"
git -C "$REPO" worktree add -q -b lane/701-scoped "$CAND_WT6" main

rm -f "$GH_LOG"
out8a=$(AGENT_SUPERVISOR_STATE_DIR="$STATE5" DISPATCH_PYTHON=python3 GH_STUB_LOG="$GH_LOG" \
  "$CHECK" check --issue 701 --brief "$D/brief-scoped.md" --worktree "$CAND_WT6" \
  --repo-path "$REPO" --exclude-lane "agent-supervisor:x" 2>&1)
rc8a=$?
want_exit "--repo omitted still allows a disjoint dispatch" "$rc8a" 0 "$out8a"
want_contains "...and gh was invoked scoped to --repo-path's own origin, not left unscoped to ambient cwd" \
  "-R acme/widgets" "$(cat "$GH_LOG" 2>/dev/null)"

# MUTATION-CHECK, direction 2: the SAME derived (not explicit) repo must
# still catch a genuine overlap -- deriving the repo cannot become a second
# way to go blind.
export GH_STUB_PR_LIST_JSON='[{"number":55,"files":[{"path":"scripts/supervisor/other.sh"}]}]'
out8b=$(AGENT_SUPERVISOR_STATE_DIR="$STATE5" DISPATCH_PYTHON=python3 GH_STUB_LOG="$GH_LOG" \
  "$CHECK" check --issue 702 --brief "$D/brief-scoped.md" --worktree "$CAND_WT6" \
  --repo-path "$REPO" --exclude-lane "agent-supervisor:x" 2>&1)
rc8b=$?
want_exit "...and a genuine overlap reached through that same derived repo still refuses" "$rc8b" 1 "$out8b"
want_contains "...naming the holder PR" "PR#55" "$out8b"
export GH_STUB_PR_LIST_JSON="[]"

# No --repo AND --repo-path has no origin remote at all: must SKIP explicitly
# (fail open, same posture as every other unknown here) and never invoke gh
# unscoped -- the exact fallthrough #432's review reproduced live.
NOREMOTE="$D/noremote"
mkdir -p "$NOREMOTE"
git -C "$NOREMOTE" init -q -b main
git -C "$NOREMOTE" config user.email test@example.com
git -C "$NOREMOTE" config user.name Test
echo x > "$NOREMOTE/f.txt"; git -C "$NOREMOTE" add -A; git -C "$NOREMOTE" commit -q -m x
echo 'Touch `f.txt` too.' > "$D/brief-noremote.md"

rm -f "$GH_LOG"
out8c=$(AGENT_SUPERVISOR_STATE_DIR="$STATE5" DISPATCH_PYTHON=python3 GH_STUB_LOG="$GH_LOG" \
  "$CHECK" check --issue 703 --brief "$D/brief-noremote.md" --worktree "$NOREMOTE" \
  --repo-path "$NOREMOTE" --exclude-lane "agent-supervisor:x" 2>&1)
rc8c=$?
want_exit "no repo identifiable at all still allows (fail open)" "$rc8c" 0 "$out8c"
want_contains "...but says so explicitly, never silence" "SKIPPED" "$out8c"
if [ -s "$GH_LOG" ]; then
  bad "...and gh is never invoked when the repo cannot be identified" "$(cat "$GH_LOG")"
else
  ok "...and gh is never invoked when the repo cannot be identified"
fi

rm -rf "$D" "$GH_BIN"

echo
echo "collision-check.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
