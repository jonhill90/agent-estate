#!/bin/bash
# Dispatch one issue to one lane over `claude -p --output-format json`.
# `dispatch.sh`'s sibling, not its replacement -- same posture
# `dispatch-pi-rpc.sh` takes for `pi --mode rpc` (agent-supervisor#58/#160),
# built for agent-supervisor#171.
#
# WHY THIS SCRIPT EXISTS: #171/#215 measured that `pi --mode rpc` -- the
# transport #161 claimed to route one real lane onto -- is not exercisable
# with the credentials available to this estate. `pi --list-models` on this
# host lists exactly two providers, `github-copilot` and `openai-codex`; there
# is no `anthropic` provider configured (no ANTHROPIC_API_KEY, confirmed by
# `env` and a keychain lookup that found nothing) and pi's `--provider`
# defaults to `google`, which is also not configured. Both configured
# providers map onto the two harnesses #171's brief measured as exhausted
# (codex 100% weekly / zero credits, copilot 97.1%). So routing a lane
# through `pi`, over EITHER of its documented modes (`json` or `rpc`), would
# not be a real dispatch today -- it would fail against an exhausted quota,
# which is a worse outcome than never trying, because it would look like a
# transport defect rather than what it actually is. `claude -p` is the one
# non-keystroke surface this estate can actually exercise right now, because
# it draws on Claude's own capacity, not a wrapper's.
#
# WHY A SEPARATE SCRIPT, not a branch inside dispatch.sh: identical reasoning
# to `dispatch-pi-rpc.sh`'s own header -- `ClaudePrintAdapter.observe_lane` is
# a permanent no-op (there is no pane to poll), so there is no tmux pane for
# `lanes.sh` to classify as free, no window to rename, no input box to watch
# empty. Reusing `claim.sh` and `worktree.sh` unchanged, same as its sibling.
#
# WHY NOT REPLACE ANY STANDING `claude` LANE: `cli.py`'s own comment on
# `adapter_for_harness` is explicit -- "tmux stays the default transport for
# every existing lane (codex, claude) and is never replaced -- Jon requires
# the persistent, watchable terminals it gives him." This script never
# touches an existing lane; it registers a NEW one, opt-in, the same way
# `dispatch-pi-rpc.sh` adds a `pi-rpc` lane alongside `pi`'s send-keys default
# rather than converting one.
#
# FAIL CLOSED, EVERYWHERE, same shape as `dispatch-pi-rpc.sh`: `register`
# below performs a REAL `claude -p` handshake (`ClaudePrintAdapter.
# register_lane` calls `start_session` against a live subprocess) -- if
# `claude` is missing, crashes, or returns anything but a well-formed JSON
# result, this refuses and unwinds the claim and the worktree. `assign` then
# performs the REAL delivery: it starts a genuine `claude -p --resume
# <session>` turn and returns once that PROCESS HAS STARTED, not once it
# exits (agent-supervisor#278: `ClaudePrintAdapter.assign_task` calls
# `transport.run_detached`, a `subprocess.Popen(..., start_new_session=True)`
# that outlives this script). Stale as of an earlier revision of this
# comment, which described `assign` as blocking until `claude` exited --
# corrected agent-supervisor#692, whose whole defect was a dispatcher and a
# brief both still reasoning as though something waits around to resume a
# lane once its detached turn ends; nothing does. Nothing in this script
# ever falls back to `tmux send-keys` -- there is no tmux handling here at
# all to fall back to.
#
# Usage:
#   dispatch-claude-print.sh <issue> <slug> <brief-file> <repo> [repo-path] [--force] [--reviews-pr <n>] [--reviews-pr-explicit]
#
# Same argument meanings as `dispatch-pi-rpc.sh`.
#
# --force  agent-supervisor#291: dispatch anyway when the pre-dispatch
#          collision check (step 2.5) finds this issue's files overlap an
#          already in-flight lane's -- see dispatch.sh's own `--force` and
#          collision-check.sh's header for what "overlap" means. A guard on
#          one transport only is not a guard, so this script runs the exact
#          same check dispatch.sh does, at the same point in the flow (right
#          after the worktree exists).
#
# --reviews-pr <n>  agent-estate#838. NOT a general-purpose flag an operator
#          types by hand -- the one caller is dispatch-lane-select.sh's own
#          reroute, thrown ONLY when the tmux candidate loop just excluded
#          every free lane in the target session as a contributor to PR <n>
#          (agent-dotfiles#212's own AUTHOR_LANES exclusion) and no
#          non-author lane is left to hand the review to. `agent-dotfiles`
#          and `agent-tui` each run a supervisor plus exactly one worker
#          lane (#838's own measurement), so that refusal is not a rare edge
#          case there -- it is every review, every time. Routing the review
#          over `claude-print` instead sidesteps the problem structurally: a
#          `claude-print` lane's id is minted fresh per dispatch (`$LABEL`
#          below), so it can never equal a contributor's lane id, with no
#          author-exclusion bookkeeping needed here at all. Marks the
#          reconstructed task `source_kind=pull`, `source_ref=<n>` (not
#          `issue`) and `is_review=1` -- the same facts `record_dispatch`
#          records for the tmux flow's own `--reviews-pr` (see cli.py's
#          `record_dispatch` docstring on `pr`/`is_review`) -- so a second
#          dispatch against the same PR is still caught by `cli.py pr-lane`
#          (dispatch-guards.sh step 0.6) before it ever reaches here.
#
# --reviews-pr-explicit  agent-estate#838/agent-supervisor#650. Threaded
#          through from dispatch.sh's own `REVIEWS_PR_EXPLICIT` (set only
#          when the caller passed `--reviews-pr` on its OWN argv, never for
#          the #70 inference's guess) so step 2.5's collision downgrade below
#          carries the exact same restriction dispatch.sh's tmux flow
#          already enforces (see that flow's own comment on why the
#          downgrade must never key on inference alone -- #650 was a real
#          bypass caused by exactly that). Meaningless without `--reviews-pr`
#          also set; ignored if so.
#
# Exit 0 only once a real `claude -p` turn has exited and the task is
# recorded complete. Exit non-zero on any refusal -- no free `claude` binary,
# an issue already claimed, a worktree that could not be built, a register or
# assign that could not reach `claude` -- and the issue's claim is released
# so it is not stranded looking taken.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PYTHON="${DISPATCH_PYTHON:-python3}"
CLI="$HERE/cli.py"

# `--force`/`--reviews-pr`/`--reviews-pr-explicit` are pulled out wherever
# they appear, same as dispatch.sh's own flag-scanning loop, so the
# remaining args keep their positional meaning.
COLLISION_FORCE=""
REVIEWS_PR=""
REVIEWS_PR_EXPLICIT=""
POSITIONAL=()
while [ $# -gt 0 ]; do
  case "$1" in
    --force) COLLISION_FORCE=1; shift ;;
    --reviews-pr) REVIEWS_PR="${2:-}"; shift 2 ;;
    --reviews-pr-explicit) REVIEWS_PR_EXPLICIT=1; shift ;;
    *) POSITIONAL+=("$1"); shift ;;
  esac
done
set -- "${POSITIONAL[@]+"${POSITIONAL[@]}"}"

ISSUE="${1:-}"
SLUG="${2:-}"
BRIEF="${3:-}"
REPO="${4:-}"
REPO_PATH="${5:-$PWD}"

if [ -z "$ISSUE" ] || [ -z "$SLUG" ] || [ -z "$BRIEF" ] || [ -z "$REPO" ]; then
  echo "usage: dispatch-claude-print.sh <issue> <slug> <brief-file> <repo> [repo-path]" >&2
  exit 2
fi

# agent-estate#838: refuse a non-numeric --reviews-pr outright rather than
# let it flow into a GitHub URL / ledger source_ref that silently means
# something else -- same posture dispatch.sh's own flag parsing takes for
# every other id it accepts.
if [ -n "$REVIEWS_PR" ] && ! [[ "$REVIEWS_PR" =~ ^[0-9]+$ ]]; then
  echo "dispatch-claude-print: --reviews-pr '$REVIEWS_PR' is not a PR number" >&2
  exit 2
fi

[ -f "$BRIEF" ] || { echo "dispatch-claude-print: no brief file at $BRIEF" >&2; exit 1; }
BRIEF="$(cd "$(dirname "$BRIEF")" && pwd)/$(basename "$BRIEF")"

# Fail closed before anything else is touched: no `claude` on PATH means
# every step below would fail anyway, and this is the cheapest place to say
# so.
command -v claude >/dev/null 2>&1 || {
  echo "dispatch-claude-print: no 'claude' binary on PATH -- refusing rather than falling back to send-keys" >&2
  exit 1
}

# Same window-naming convention dispatch.sh/dispatch-pi-rpc.sh use.
NAME_PART="${REPO##*/}"
if [[ "$NAME_PART" == *-* ]]; then
  PREFIX=$(tr '-' '\n' <<<"$NAME_PART" | cut -c1 | tr -d '\n')
else
  PREFIX="$NAME_PART"
fi
LABEL="${PREFIX}${ISSUE}-${SLUG}"
LANE="$LABEL"
TASK_ID="$LABEL"

# agent-supervisor#572/#576: every INTENTIONAL failure below already
# releases this claim inline (each `abort` call, and the explicit
# `release_claim` calls it wraps), but a `kill`, a timeout wrapper, or a
# closed terminal hits none of those lines -- the same gap #209 closed for
# dispatch.sh's lane claim, and dispatch.sh's own trap now closes for its
# issue claim too. This script originally had no trap at all, and #576's
# first attempt at one still left a gap: it declared this block AFTER
# `claim.sh take` below, so a signal landing between that call succeeding
# and this block installing its trap still stranded the claim -- the same
# class of gap this fix exists to close, just shrunk to a few statements.
# Declared here, BEFORE the claim is even taken, so no window is open at
# all -- mirrors exactly how dispatch.sh's own fix orders it (declared
# hundreds of lines before its claim loop ever runs).
#
# release_claim (below, unchanged) is what every DETERMINED failure calls --
# every direct call to it in this script (steps 1-5) runs BEFORE step 6 ever
# starts a `claude -p` process at all, so there is nothing running yet and
# releasing is always correct there, exactly as before this fix.
#
# release_claim_on_signal is what the trap calls instead, gated on
# CLAIM_COMMITTED. That gate matters only around step 6: once `assign`
# starts the detached turn (agent-supervisor#278: it returns as soon as the
# process exists, not once the turn finishes), a signal reaching this shell
# does not prove the turn stopped too (a `kill` targeting only this pid, as
# opposed to its process group, orphans the child rather than killing it) --
# so this script cannot tell "nothing happened" from "work may still be
# running" the way it can for every step before it. CLAIM_COMMITTED is set
# right before that call, the same "point of no return" line dispatch.sh
# draws at its own step 4.5: past it, an ambiguous signal leaves the claim
# held rather than risk releasing one that is genuinely still being worked.
# SIGKILL
# cannot be trapped by any shell either way; that gap is `claim.sh
# audit`/`reap` (#359) to cover, same as dispatch.sh's own header says of its
# lane claim.
CLAIM_COMMITTED=""
# agent-supervisor#647: this call used to be UNGATED and its failure UNCHECKED
# -- exactly the same shape #572 fixed for a signal landing mid-dispatch, but
# for the ordinary case where `claim.sh release`'s own `gh api -X DELETE` call
# simply fails (rate limit, network blip). Before this fix that left the issue
# claimed with the refusal already printed and nothing to say the release
# itself did not land -- indistinguishable, from outside, from a healthy
# rollback. `dispatch.sh`'s own release_claim (its tmux flow) has reported
# this loudly since #209; this mirrors that wording so the same failure is
# never silent on either transport.
release_claim() {
  "$HERE/claim.sh" release "$ISSUE" "$REPO" >/dev/null 2>&1 \
    || echo "dispatch-claude-print: could not release the claim on #$ISSUE -- release it by hand: $HERE/claim.sh release $ISSUE $REPO" >&2
}
release_claim_on_signal() {
  [ -z "${CLAIM_COMMITTED:-}" ] || return 0
  release_claim
}
trap release_claim_on_signal EXIT
trap 'release_claim_on_signal; exit 143' TERM   # 128 + 15
trap 'release_claim_on_signal; exit 130' INT    # 128 + 2

# --- 0. host-pressure gate ---------------------------------------------------
# agent-supervisor#643: this script starts a new agent process (a `claude -p`
# subprocess, agent-supervisor#171) exactly like dispatch.sh's tmux flow
# does, but until now had NO host-pressure check of any kind -- measured
# directly (grep for "host-pressure" across scripts/supervisor turned up only
# dispatch.sh). host-pressure.sh's own bash gate is deliberately NOT reused
# here for consistency's sake alone; host_pressure.py (this dispatch's
# Python port of daemon/internal/pressure, see its own module docstring) is
# used instead, before claim.sh below, same "cheapest failure first" reason
# dispatch.sh's own comment gives: a refused dispatch must leave the estate
# exactly as it found it.
HOST_PRESSURE_OUT=$("$PYTHON" "$HERE/host_pressure.py"); HOST_PRESSURE_RC=$?
if [ "$HOST_PRESSURE_RC" -ne 0 ]; then
  echo "dispatch-claude-print: $HOST_PRESSURE_OUT -- NOT dispatching #$ISSUE" >&2
  exit 1
fi

# --- 1. claim the issue on GitHub, same tool dispatch.sh uses --------------
if ! "$HERE/claim.sh" take "$ISSUE" "$REPO" "$LANE"; then
  echo "dispatch-claude-print: could not claim #$ISSUE -- not dispatching" >&2
  exit 1
fi

# --- 2. give the lane its own worktree, same tool dispatch.sh uses --------
WORKTREE_ERR=$(mktemp)
WORKTREE=$("$HERE/worktree.sh" new "${ISSUE}-${SLUG}" "$REPO_PATH" 2>"$WORKTREE_ERR")
WORKTREE_RC=$?
if [ "$WORKTREE_RC" -ne 0 ] || [ -z "$WORKTREE" ]; then
  echo "dispatch-claude-print: worktree.sh new failed for #$ISSUE in $REPO_PATH -- NOT dispatching" >&2
  sed 's/^/  /' "$WORKTREE_ERR" >&2
  rm -f "$WORKTREE_ERR"
  release_claim
  exit 1
fi
rm -f "$WORKTREE_ERR"

abort() {
  echo "dispatch-claude-print: $1" >&2
  "$HERE/worktree.sh" done "$WORKTREE" >/dev/null 2>&1
  release_claim
  exit 1
}

# --- 2.5. the pre-dispatch collision check (agent-supervisor#291) ----------
# The same check dispatch.sh's tmux flow runs at the same point (right after
# the worktree exists) -- "a guard on one transport only is not a guard".
# See dispatch.sh's own step 3.2 comment and collision-check.sh's header for
# what "overlap" means, why UNKNOWN allows, and why a real collision is
# fatal. `--exclude-lane "$LANE"` is a no-op here in practice (this lane has
# no prior ledger row -- it does not exist until `register`/`reconstruct-task`
# below), kept only so this call reads identically to dispatch.sh's.
# `${REVIEWS_PR:+--pr "$REVIEWS_PR"}`: same as dispatch.sh's own
# `${PR_SCOPED:+--pr "$PR_SCOPED"}` -- scopes the diff collision-check.sh
# reads against the PR actually under review, not an unrelated one.
COLLISION_OUT=$("$HERE/collision-check.sh" check \
  --issue "$ISSUE" --brief "$BRIEF" --worktree "$WORKTREE" \
  --repo-path "$REPO_PATH" --repo "$REPO" \
  --exclude-lane "$LANE" \
  ${REVIEWS_PR:+--pr "$REVIEWS_PR"} \
  ${COLLISION_FORCE:+--force} 2>&1)
COLLISION_RC=$?
if [ "$COLLISION_RC" -ne 0 ]; then
  if [ -n "$REVIEWS_PR_EXPLICIT" ]; then
    # agent-supervisor#645/#650, mirrored from dispatch.sh's own tmux flow
    # (dispatch-worktree.sh): a `--reviews-pr` dispatch's deliverable is a PR
    # comment, never a commit, so it cannot produce the two-writers-one-file
    # collision #291 exists to catch -- downgraded to the informational line
    # below, same as an ALLOW. Keyed on REVIEWS_PR_EXPLICIT, never on the
    # mere presence of `$REVIEWS_PR`: #650 was a real bypass caused by
    # keying this on an INFERRED review, which let a genuine write dispatch
    # whose title happened to match agent-supervisor#70's inference pattern
    # skip this guard. This script has no inference of its own -- the only
    # way `$REVIEWS_PR` is ever set here is dispatch-lane-select.sh's own
    # reroute call, which always passes `--reviews-pr-explicit` alongside it
    # when (and only when) dispatch.sh's own `REVIEWS_PR_EXPLICIT` was set --
    # but the check stays explicit rather than implicit so a future direct
    # caller of this script cannot accidentally inherit the downgrade by
    # passing `--reviews-pr` alone.
    :
  else
    sed 's/^/dispatch-claude-print: collision-check: /' <<<"$COLLISION_OUT" >&2
    abort "#$ISSUE's files collide with an in-flight lane -- NOT dispatched. Re-run with --force if this overlap is known and intended (agent-supervisor#291)"
  fi
fi
# ALLOW (no-conflict, unknown, or forced), or a REFUSE downgraded to
# information for an EXPLICIT `--reviews-pr` dispatch (see above) -- on
# stdout, matching this script's own success-path convention (see the final
# echo block below).
sed 's/^/dispatch-claude-print: collision-check: /' <<<"$COLLISION_OUT"

# --- 3. the standing deliverable contract, same text dispatch.sh appends --
# agent-estate#793: $LANE is already known here (register/reconstruct-task
# above both use it) -- state it in the brief instead of telling the lane to
# derive it. A `claude-print` lane has NO pane at all ($TMUX_PANE unset), so
# the old bare-`tmux display-message` guidance was never even meaningful for
# this shape; `lane-whoami.sh`'s worktree-lane fallback already covers that
# case correctly, but a stated value needs no lookup at all.
CONTRACT_MARKER="<!-- dispatch:deliverable-contract -->"
if ! grep -qF "$CONTRACT_MARKER" "$BRIEF" 2>/dev/null; then
  cat >>"$BRIEF" <<EOF || abort "could not append the deliverable contract to $BRIEF -- #$ISSUE was NOT dispatched"

$CONTRACT_MARKER
## Delivering this work

Added by \`dispatch-claude-print.sh\` on every dispatch, not by the brief's author.

**Your lane id is \`$LANE\`.** Use this exact value for any \`Review-Lane:\` or
\`Lane:\` trailer this brief asks you to write -- it is stated here, not
something to derive. You have no tmux pane (\`\$TMUX_PANE\` is unset for a
\`claude-print\` lane), so do not attempt \`tmux display-message\` at all
(agent-estate#793); if you need to double-check this value,
\`scripts/supervisor/lane-whoami.sh\` is the one command that resolves it
correctly for a pane-less lane too.

Unless this brief says otherwise, when you are finished:
**push your branch and open a PR**.
If you produced no code -- a review, an investigation, an options paper --
**post your findings as a comment** on the issue or PR the brief names.

Do not stop with the work only in your worktree. From outside, a lane that
finished without shipping is indistinguishable from a lane that did nothing:
unshipped work looks exactly like no work, and the worktree is temporary.

**You are a single \`claude -p\` turn, not a long-lived process (agent-supervisor#692).**
Nothing in this estate resumes you: there is no watcher, no callback, no
"I will wait for X and continue" -- when this turn ends, the process exits and
no one calls \`claude -p --resume\` on it again. If a step in this brief would
normally run in the background and notify you later (a long sample, an hour of
monitoring, "run it detached and check back"), either run it to completion
synchronously inside this turn, or do not start it and say so in your result
instead -- never end your turn on the expectation that something will wake you
back up.
EOF
fi

# --- 4. register the lane -- a REAL claude -p handshake --------------------
# `ClaudePrintAdapter.register_lane` spawns `claude -p --session-id <uuid>`
# in $WORKTREE and requires a well-formed JSON result back. This is the
# fail-closed gate: a `claude` that cannot start, crashes, or never answers
# makes THIS call fail.
REGISTER_OUT=$("$PYTHON" "$CLI" register \
  --lane "$LANE" \
  --target "claude-print:$LANE" \
  --harness claude \
  --transport claude-print \
  --repo "$WORKTREE" 2>&1)
REGISTER_RC=$?
if [ "$REGISTER_RC" -ne 0 ]; then
  abort "register failed -- claude -p is not reachable in $WORKTREE, refusing rather than falling back to send-keys:
$REGISTER_OUT"
fi

# --- 5. record the work as a task, before any delivery is attempted -------
# agent-estate#838: a `--reviews-pr` reroute records a PULL-shaped row, keyed
# by the PR under review rather than the tracking issue -- the same shape
# `cli_dispatch_record.record_dispatch` builds for the tmux flow's own
# `--pr`/`--reviews-pr` (see that function's own docstring). This is what
# lets `cli.py pr-lane` (dispatch-guards.sh step 0.6) catch a SECOND dispatch
# against the same PR, and what `--is-review` records as the fact this
# reroute already knows instead of leaving it to be guessed back later from
# `$TASK_ID`/summary text (agent-supervisor#640's own reasoning). `$ISSUE` is
# still the tracking issue this dispatch was claimed against (`claim.sh
# take`, step 1 above) -- named in the summary and evidence below so a human
# reading the ledger can still see which issue this review traces back to,
# even though the SOURCE row itself is keyed on the PR.
if [ -n "$REVIEWS_PR" ]; then
  SOURCE_KIND_ARGS=(--source-kind pull --is-review)
  SOURCE_URL="https://github.com/$REPO/pull/$REVIEWS_PR"
  SOURCE_REF="$REVIEWS_PR"
  RECONSTRUCT_EVIDENCE=("claimed by dispatch-claude-print.sh for lane $LANE" "reviews PR $REVIEWS_PR" "issue: $ISSUE")
else
  SOURCE_KIND_ARGS=()
  SOURCE_URL="https://github.com/$REPO/issues/$ISSUE"
  SOURCE_REF="$ISSUE"
  RECONSTRUCT_EVIDENCE=("claimed by dispatch-claude-print.sh for lane $LANE")
fi
RECONSTRUCT_EVIDENCE_ARGS=()
for e in "${RECONSTRUCT_EVIDENCE[@]}"; do
  RECONSTRUCT_EVIDENCE_ARGS+=(--evidence "$e")
done
RECONSTRUCT_OUT=$("$PYTHON" "$CLI" reconstruct-task \
  --task "$TASK_ID" \
  "${SOURCE_KIND_ARGS[@]+"${SOURCE_KIND_ARGS[@]}"}" \
  --source-url "$SOURCE_URL" \
  --source-ref "$SOURCE_REF" \
  --summary "#$ISSUE $SLUG; worktree=$WORKTREE; brief=$BRIEF" \
  "${RECONSTRUCT_EVIDENCE_ARGS[@]}" 2>&1)
RECONSTRUCT_RC=$?
if [ "$RECONSTRUCT_RC" -ne 0 ]; then
  if [ -n "$REVIEWS_PR" ] && grep -qF 'source_tasks.source_ref' <<<"$RECONSTRUCT_OUT"; then
    # agent-supervisor#169's own race, reached through this reroute instead
    # of `record_dispatch`: dispatch-guards.sh step 0.6 already checked
    # `pr-lane` before dispatch-lane-select.sh ever reached this reroute,
    # but that check is a plain read, seconds before this write -- a second
    # dispatcher can still win the same PR in between. The
    # `one_open_pull_per_source_ref` trigger (core_ledger_schema.py) is what
    # actually closes it, atomically, the same guarantee `record_dispatch`
    # relies on for the tmux flow.
    abort "PR #$REVIEWS_PR already claimed by another lane (source_tasks.source_ref conflict) -- NOT dispatched:
$RECONSTRUCT_OUT"
  fi
  abort "reconstruct-task failed -- #$ISSUE was NOT dispatched:
$RECONSTRUCT_OUT"
fi

# --- 6. deliver -- a REAL claude -p turn, started detached ------------------
# CLAIM_COMMITTED set HERE, before the call, not after it returns -- exactly
# dispatch.sh's step 4.5 posture (agent-supervisor#572): a signal landing
# after `assign` returns is landing on a turn that is already running
# detached (agent-supervisor#278), and the claim must not be released out
# from under it just because this shell has moved past the call that
# started it.
CLAIM_COMMITTED=1
# `assign` routes to `ClaudePrintAdapter.assign_task` because the lane it
# just registered carries transport=claude-print: it re-spawns
# `claude -p --resume <session>` against $WORKTREE, sends this message as the
# prompt, and -- as of agent-supervisor#278 -- returns once that DETACHED
# process has started, not once it exits with a result; there is no tmux
# pane and no send-keys anywhere in this call. A crash before the spawn, or
# a malformed handoff, still raises and this script refuses exactly like
# every step above it; a crash IN the turn itself, after this call has
# already returned, is invisible here by construction (see the comment
# below this one, and agent-supervisor#692's fix in
# reconcile_lane_completions.py, for how that is now caught instead).
MESSAGE="Read $BRIEF and do exactly what it says. That file is your complete brief. Do all of your work in the worktree at $WORKTREE -- it is yours, already branched; never work in the shared checkout at $REPO_PATH."
ASSIGN_OUT=$("$PYTHON" "$CLI" assign \
  --lane "$LANE" \
  --task "$TASK_ID" \
  --summary "$MESSAGE" 2>&1)
ASSIGN_RC=$?
if [ "$ASSIGN_RC" -ne 0 ]; then
  abort "assign failed -- claude -p did not deliver #$ISSUE, refusing rather than falling back to send-keys:
$ASSIGN_OUT"
fi

# agent-supervisor#362: this line used to say "task $TASK_ID complete"
# unconditionally, derived from nothing. Every claude-print dispatch this
# window printed it while the task record carried `completed_at: null` --
# `ledger.complete()` had never been reached -- so a dispatch that delivered a
# brief and then produced no work read, to a human and to every consumer of
# this output, as a completed task. Three lanes (#313, #368 and #362 itself)
# were declared complete on this string having done nothing.
#
# The word "complete" must come from the field that records completion, and
# from nowhere else. This does NOT fix the underlying defect -- the transport
# still returns before the turn ends -- it stops the report from lying about
# it, which is the precondition for noticing the defect at all. Reporting and
# the transport fix are deliberately separate changes.
COMPLETED_AT=$(printf '%s' "$ASSIGN_OUT" | "$PYTHON" -c '
import json, re, sys
raw = sys.stdin.read()
match = re.search(r"\{.*\}", raw, re.S)
if not match:
    sys.exit(0)
try:
    print(json.loads(match.group()).get("completed_at") or "")
except (ValueError, AttributeError):
    pass
' 2>/dev/null)

if [ -n "$COMPLETED_AT" ]; then
  echo "dispatch-claude-print: #$ISSUE delivered to $LANE over claude-print, task $TASK_ID complete"
else
  echo "dispatch-claude-print: #$ISSUE DELIVERED to $LANE over claude-print, task $TASK_ID -- delivery only, NOT complete (completed_at is null; verify the artifact the brief asked for before believing this lane ran)"
fi
echo "  worktree: $WORKTREE"
echo "  brief:    $BRIEF"
echo "$ASSIGN_OUT"
exit 0
