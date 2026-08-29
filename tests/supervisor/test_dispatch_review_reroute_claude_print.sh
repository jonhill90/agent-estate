#!/bin/bash
# agent-estate#838: `agent-dotfiles` and `agent-tui` each run a supervisor
# plus exactly one worker lane -- so a review dispatched into that session
# always finds the author lane is the only free candidate, and until this
# fix that was a plain refusal (agent-dotfiles#212/agent-supervisor#190's own
# AUTHOR_LANES exclusion, already correct on its own terms) that cost an
# operator a hand-typed `LANES_SESSION=<bigger session>` workaround every
# time (#838's own issue text). This is the REAL case #838 asks to be
# constructed: a PR authored by the only worker lane in a single-lane
# session, then a review of it dispatched into that same session.
#
# THE FIX (dispatch-lane-select.sh, right where the candidate loop's
# AUTHOR_SKIPPED refusal used to be the only outcome): when every free lane
# in the session was excluded as a PR contributor and none is left, reroute
# the review over `claude-print` (dispatch-claude-print.sh, agent-supervisor
# #171's own mechanism) instead of refusing outright. A `claude-print` lane's
# id is minted fresh per dispatch, so it can never equal a contributor's lane
# id -- no author-exclusion bookkeeping needed on that side at all. The
# refusal stays the exact backstop it always was: `--live-pane`, a missing
# `claude` binary, or an unresolvable `[repo]` all still refuse, unchanged.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISPATCH="$HERE/../../scripts/supervisor/dispatch.sh"
export QUOTA_GATE="$HERE/stubs/quota-safe"
export SUPERVISOR_MAX_LOAD_PER_CORE=0
export SUPERVISOR_MIN_FREE_MEM_GB=0
export SUPERVISOR_MAX_AGENT_SESSIONS=0
pass=0; fail=0

ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; sed 's/^/       /' <<<"${2:-}"; fail=$((fail+1)); }
want_exit()     { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected exit $3, got $2: ${4:-}"; fi }
want_contains() { if grep -qF -- "$2" <<<"$3"; then ok "$1"; else bad "$1" "want '$2' in: $3"; fi }
want_missing()  { if grep -qF -- "$2" <<<"$3"; then bad "$1" "unwanted '$2' in: $3"; else ok "$1"; fi }

echo "dispatch.sh -- a single-worker-lane session reroutes a self-review over claude-print instead of refusing (agent-estate#838)"

D=$(mktemp -d); mkdir -p "$D/bin" "$D/roots"
cp "$HERE/stubs/gh-claim" "$D/bin/gh"
cp "$HERE/stubs/tmux-dispatch" "$D/bin/tmux"
cp "$HERE/stubs/claude" "$D/bin/claude"
export SUPERVISOR_STATE="$D/pa-state"
mkdir -p "$SUPERVISOR_STATE/results"
: > "$SUPERVISOR_STATE/results/zzz-unrelated-to-any-test-issue.md"
export CLAUDE_PRINT_LOG="$D/claude-print.log"

git init -q --bare "$D/origin.git"
git clone -q "$D/origin.git" "$D/repo" 2>/dev/null
REPO="$D/repo"
git -C "$REPO" config user.email test@example.com
git -C "$REPO" config user.name "Test"
git -C "$REPO" checkout -q -b main
echo one > "$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -q -m "initial"
git -C "$REPO" push -q -u origin main
git -C "$REPO" remote set-url origin "git@github.com:acme/agent-dotfiles.git"

echo "do the guard-bypass fix" > "$D/brief.md"

run() {
  : > "$D/tmux.log"
  rm -rf "$D/panes"; mkdir -p "$D/panes"
  PATH="$D/bin:$PATH" GH_ISSUES="$D/issues" GH_PRS="$D/prs" \
    LANES_FIXTURE="$D/lanes" LANES_SESSION="${RUN_SESSION:-t}" TMUX_LOG="$D/tmux.log" \
    TMUX_PANES="$D/panes" DISPATCH_SETTLE=0 \
    DISPATCH_LIVE_PANE="${DISPATCH_LIVE_PANE:-}" \
    AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:-$D/state}" \
    STUB_PANE_PATH="$REPO" \
    DISPATCH_CONFIRM_TRIES=2 DISPATCH_SESSION_TIMEOUT=0 \
    WORKTREE_ROOT="$D/roots" bash "${DISPATCH_SCRIPT:-$DISPATCH}" "$@" 2>&1
}
ledger() { AGENT_SUPERVISOR_STATE_DIR="${LEDGER_STATE:-$D/state}" python3 "$HERE/../../scripts/supervisor/cli.py" "$@"; }
tmuxlog()   { cat "$D/tmux.log"; }
claudelog() { cat "$CLAUDE_PRINT_LOG" 2>/dev/null; }

# --- the real case: a supervisor plus exactly ONE worker lane, agent-
# dotfiles/agent-tui's own shape (#838's own `list-windows` measurement) ----
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
cat > "$D/issues" <<'FIX'
100|| guard-bypass fix
101|| review PR #200
FIX
: > "$D/prs"

# The author dispatch runs with `--live-pane` -- the shape that actually
# occupies the session's one tmux lane (t:3), the same as a worker Jon is
# watching interactively. Without this, agent-supervisor#171's own
# claude-print default would already route the AUTHOR dispatch off t:3
# entirely, leaving it untouched and the review harmless regardless of this
# fix -- #838's real defect only bites when the author's own work actually
# occupied the session's one lane.
out=$(DISPATCH_LIVE_PANE=1 run 100 author100 "$D/brief.md" acme/agent-dotfiles "$REPO" --live-pane); rc=$?
want_exit "setup: the author dispatch lands on t:3 over tmux" "$rc" 0 "$out"
want_contains "setup: and t:3 is the target" "-t t:@103" "$(tmuxlog)"
ledger record-completion --task ad100-author100 --note done >/dev/null
printf '200|Fixes #100|lane/100-author100\n' >> "$D/prs"

# t:3 is free again, is the ONLY worker lane in the session, and IS PR
# #200's author -- exactly #838's own measured shape.
out=$(run 101 rev200 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 200); rc=$?
want_exit "the review is dispatched, not refused" "$rc" 0 "$out"
want_contains "the author is still named and excluded from the tmux candidate loop" "skipping t:3" "$out"
want_contains "and the refusal is explicitly rerouted, not silently swallowed" "rerouting the review over claude-print instead of refusing" "$out"
want_contains "dispatch-claude-print.sh actually ran the delivery" "DELIVERED to ad101-rev200 over claude-print" "$out"
log=$(tmuxlog)
if [ -z "$log" ]; then
  ok "t:3 -- the author's own lane -- was never touched (tmux.log is empty)"
else
  bad "t:3 -- the author's own lane -- was never touched (tmux.log is empty)" "$log"
fi
want_contains "the review's actual prompt was delivered over claude-print instead" "ad101-rev200" "$(claudelog)"

# The ledger's own record of the rerouted task must still carry PR #200 as
# its source (source_kind=pull, is_review) -- not merely "delivered
# somehow" -- so a second dispatch against the same PR is still caught by
# the existing 0.6 (`pr-lane`) guard, unweakened by this fix.
SOURCE_JSON=$(python3 - "$HERE/../../scripts/supervisor" "${LEDGER_STATE:-$D/state}" <<'PY'
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
row = Ledger(sys.argv[2]).get_source_task("ad101-rev200")
print(row)
PY
)
want_contains "the rerouted task is recorded PR-scoped (source_kind=pull, PR #200)" "'source_kind': 'pull'" "$SOURCE_JSON"
want_contains "and marked as a review, not guessed back from task-name text later" "'is_review': 1" "$SOURCE_JSON"

printf '203|| review PR #200 again\n' >> "$D/issues"
out=$(run 103 rev200-again "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 200); rc=$?
want_exit "a second dispatch against the SAME PR is still refused (0.6, pr-lane, unweakened)" "$rc" 1 "$out"
want_contains "and names the lane that already holds it" "already claimed by lane ad101-rev200" "$out"

# --- a --live-pane review is UNCHANGED: still refused, never rerouted -----
# (this brief's own hard constraint: pane work that genuinely needs a pane
# must still get one).
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
printf '104|| second guard-bypass fix\n' >> "$D/issues"
printf '105|| review PR #201\n' >> "$D/issues"
out=$(DISPATCH_LIVE_PANE=1 run 104 author104 "$D/brief.md" acme/agent-dotfiles "$REPO" --live-pane); rc=$?
want_exit "setup: a second author dispatch lands on t:3" "$rc" 0 "$out"
ledger record-completion --task ad104-author104 --note done >/dev/null
printf '201|Fixes #104|lane/104-author104\n' >> "$D/prs"

out=$(DISPATCH_LIVE_PANE=1 run 105 rev201 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 201 --live-pane); rc=$?
want_exit "a --live-pane review is still refused outright, never rerouted" "$rc" 1 "$out"
want_missing "no claude-print delivery was attempted for the --live-pane case" "DELIVERED to ad105-rev201 over claude-print" "$out"

# --- a genuinely independent, multi-lane session is UNCHANGED -------------
# Two free lanes: the tmux candidate loop finds a non-author lane on its own
# and this fix's reroute code is never reached at all -- same assertions
# test_dispatch_review_independence.sh already pins for this exact scenario,
# repeated here so this file alone proves both directions.
cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
4|free-4|claude.exe|❯ ready|1|0
FIX
printf '106|| third guard-bypass fix\n' >> "$D/issues"
printf '107|| review PR #202\n' >> "$D/issues"
out=$(DISPATCH_LIVE_PANE=1 run 106 author106 "$D/brief.md" acme/agent-dotfiles "$REPO" --live-pane); rc=$?
want_exit "setup: a third author dispatch lands on t:3" "$rc" 0 "$out"
ledger record-completion --task ad106-author106 --note done >/dev/null
printf '202|Fixes #106|lane/106-author106\n' >> "$D/prs"

out=$(run 107 rev202 "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 202); rc=$?
want_exit "a multi-lane session's review still dispatches" "$rc" 0 "$out"
want_contains "the author is skipped exactly as before" "skipping t:3" "$out"
want_missing "and this fix's reroute is never reached -- a real non-author lane was found" "rerouting the review over claude-print" "$out"
log=$(tmuxlog)
want_contains "the review lands on the OTHER free lane over tmux, t:4" "send-keys -t t:@104" "$log"
want_missing "never on the author's own lane" "send-keys -t t:@103 " "$log"

# --- MUTATION: revert this fix and confirm the SAME real case goes red ----
# Read at `merge-base HEAD origin/main`, never literal HEAD -- this fix is
# committed on this very branch, so HEAD already carries it (see
# test_dispatch_review_independence.sh's own #234/#725 comment for the full
# reasoning and the same origin/main-resolution fallback, reused verbatim
# here).
MUTATED_838="$D/dispatch-lane-select-pre838.sh"
patch_rc=0
python3 - "$HERE/../../scripts/supervisor/dispatch-lane-select.sh" "$MUTATED_838" <<'PY' || patch_rc=$?
import os
import subprocess
import sys

src = sys.argv[1]
dst = sys.argv[2]
repo_dir = os.path.dirname(os.path.abspath(src))


def git(*args):
    return subprocess.run(["git", "-C", repo_dir, *args], capture_output=True, text=True)


def resolves(ref):
    return git("rev-parse", "--verify", "-q", ref).returncode == 0


target = next((ref for ref in ("origin/main", "main") if resolves(ref)), None)

if target is None:
    fetch = git("fetch", "-q", "origin", "main:refs/remotes/origin/main")
    if fetch.returncode == 0 and resolves("origin/main"):
        target = "origin/main"
    else:
        print(
            "SKIP: no origin/main ref, and fetching one failed -- "
            f"{fetch.stderr.strip() or 'no route to the remote'}",
            file=sys.stderr,
        )
        sys.exit(3)

mb = git("merge-base", "HEAD", target)
if mb.returncode != 0 and git("rev-parse", "--is-shallow-repository").stdout.strip() == "true":
    git("fetch", "-q", "--unshallow", "origin")
    mb = git("merge-base", "HEAD", target)

if mb.returncode != 0:
    print(
        f"SKIP: git merge-base HEAD {target} failed even after fetch/unshallow: {mb.stderr.strip()}",
        file=sys.stderr,
    )
    sys.exit(3)

base_ref = mb.stdout.strip()
text = subprocess.run(
    ["git", "-C", repo_dir, "show", f"{base_ref}:scripts/supervisor/dispatch-lane-select.sh"],
    capture_output=True, text=True,
)
if text.returncode != 0:
    print(f"SKIP: could not read pre-#838 dispatch-lane-select.sh at {base_ref}: {text.stderr.strip()}", file=sys.stderr)
    sys.exit(3)
content = text.stdout
if "agent-estate#838" in content:
    print("SKIP: the merge-base already carries the #838 fix -- no pre-#838 baseline exists in this history to mutate", file=sys.stderr)
    sys.exit(3)
open(dst, "w").write(content)
PY
if [ "$patch_rc" -eq 3 ]; then
  echo "  SKIP agent-estate#838 mutation check: pre-#838 baseline could not be resolved (see stderr above) -- UNVERIFIED, not a pass"
elif [ "$patch_rc" -ne 0 ]; then
  bad "setup: fetched the pre-#838 dispatch-lane-select.sh from git HEAD" "could not fetch/patch (exit $patch_rc)"
else
  ok "setup: fetched the pre-#838 dispatch-lane-select.sh from git HEAD"
  # A full copy of scripts/supervisor/, with ONLY dispatch-lane-select.sh
  # replaced by the pre-#838 mutant -- same technique
  # test_dispatch_review_independence.sh's own #190 mutation uses (see that
  # file's `make_mutant_scripts_dir` comment), so this never touches the
  # real checked-out file: a crash partway through this section leaves
  # nothing to restore.
  MUTANT_DIR=$(mktemp -d "$D/mutant.XXXXXX")
  cp -R "$HERE/../../scripts/supervisor/." "$MUTANT_DIR/"
  rm -rf "$MUTANT_DIR/__pycache__"
  chmod +x "$MUTANT_DIR"/*.sh
  cp "$MUTATED_838" "$MUTANT_DIR/dispatch-lane-select.sh"
  cat > "$D/lanes" <<'FIX'
1|arch|claude.exe|❯ ready|1|0
3|free-3|claude.exe|❯ ready|1|0
FIX
  printf '108|| fourth guard-bypass fix\n' >> "$D/issues"
  printf '109|| review PR #203, against the pre-#838 guard\n' >> "$D/issues"
  out=$(DISPATCH_SCRIPT="$MUTANT_DIR/dispatch.sh" DISPATCH_LIVE_PANE=1 \
        run 108 author108 "$D/brief.md" acme/agent-dotfiles "$REPO" --live-pane); rc=$?
  ledger record-completion --task ad108-author108 --note done >/dev/null
  printf '203|Fixes #108|lane/108-author108\n' >> "$D/prs"
  out=$(DISPATCH_SCRIPT="$MUTANT_DIR/dispatch.sh" \
        run 109 rev203-mutant "$D/brief.md" acme/agent-dotfiles "$REPO" --reviews-pr 203); rc=$?
  want_exit "mutation confirmed: the pre-#838 guard refuses outright (it never rerouted)" "$rc" 1 "$out"
  want_missing "mutation confirmed: no claude-print reroute was attempted (the assertions above would now be red)" \
    "rerouting the review over claude-print" "$out"
fi

rm -rf "$D"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
