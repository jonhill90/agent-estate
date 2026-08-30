#!/bin/bash
# Sourced-only, from dispatch.sh, right after dispatch-args.sh. Part of the
# agent-supervisor#716 split (see dispatch-rehome.sh's own header for the
# shape and precedent). Never run standalone.
#
# Everything that must be resolved or refused BEFORE any lane is touched, in
# the order dispatch.sh already ran it: the host-pressure gate, prior-attempt
# history carried into the brief, the quota gate, the `--reviews-pr` inference
# (agent-supervisor#70) and its `--pr`/`--adopt-pane` validation, and the
# window name / tmux session resolution (including auto-bootstrap). All of
# this reads $ISSUE_ARG/$SLUG/$BRIEF/$REPO/$REPO_PATH/$ISSUES resolved by
# dispatch-args.sh, and sets $SESSION/$WINDOW_NAME/$PREFIX/$NAME_PART/
# $PR_SCOPED for every file sourced after it.
# agent-supervisor#500 / corpus directive it-ef548e51e71daebe: a NEW dispatch
# is the actual resource-multiplying step in this estate (a new worktree, a
# new agent process) -- director-loop.sh's own tick only sends a nudge to an
# EXISTING pane, so this is the one place in the live shell/tmux control
# plane that can refuse before adding load, not after. Checked here, before
# claim.sh or worktree.sh run (both below), for the same "cheapest failure
# first" reason the brief-file check above already gives: a refused dispatch
# must leave the estate exactly as it found it, and claiming an issue or
# building a worktree it then has to unwind would be strictly worse than
# never starting. See host-pressure.sh's own header for the thresholds and
# why they live in a second bash implementation rather than shelling out to
# daemon/internal/pressure's Go binary.
if [ -x "$HERE/host-pressure.sh" ]; then
  HOST_PRESSURE_OUT=$("$HERE/host-pressure.sh"); HOST_PRESSURE_RC=$?
  if [ "$HOST_PRESSURE_RC" -ne 0 ]; then
    echo "dispatch: $HOST_PRESSURE_OUT -- NOT dispatching #$ISSUE_ARG" >&2
    exit 1
  fi
else
  echo "dispatch: host-pressure.sh missing or not executable at $HERE -- refusing to guess whether this host can take another dispatch" >&2
  exit 1
fi

# --- carry forward what the LAST agent on this issue already found ----------
# Measured 2026-08-20: 184 issues in this estate have been worked more than
# once -- #308 thirteen times, #284 ten. Every re-dispatch started from zero,
# because results were written and never read back. The tenth pass on #284
# concluded "investigation-only, worktree diff is empty" while an attempt three
# days earlier had already found a real defect in the same area and posted a
# reject verdict with a mutation check.
#
# So the brief is AUGMENTED, not replaced: a section naming the prior result
# files, newest first. It does not summarise them -- a paraphrase quietly
# becomes the record, and this estate already carries a corpus that is 30-65%
# agent-written for exactly that reason. The lane reads the files.
#
# Never fatal. rc=1 (fresh issue) and rc=3 (cannot see the results dir) both
# leave the brief untouched and say so; a dispatcher that refused to dispatch
# because it could not read history would be worse than one that dispatches
# blind, which is the status quo.
if [ -x "$HERE/prior-attempts.sh" ]; then
  _pa_issue="$(printf '%s' "$ISSUE_ARG" | cut -d, -f1 | tr -cd '0-9')"
  if [ -n "$_pa_issue" ]; then
    _pa_section="$("$HERE/prior-attempts.sh" --issue "$_pa_issue" --brief 2>/dev/null)"
    _pa_rc=$?
    case "$_pa_rc" in
      0)
        _pa_brief="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}/brief-with-history-$SLUG.md"
        { cat "$BRIEF"; printf '%s
' "$_pa_section"; } > "$_pa_brief"           && BRIEF="$_pa_brief"           && echo "dispatch: brief augmented with prior attempts on #$_pa_issue -- $_pa_brief"
        ;;
      1) : ;;  # genuinely fresh; nothing to carry
      3) echo "dispatch: could not read prior results -- dispatching without history (NOT the same as none)" >&2 ;;
    esac
  fi
fi

# --- -0.5. the quota gate. Nothing gets dispatched on a window we cannot see
# ----------------------------------------------------------------------------
# agent-supervisor#227: quota.sh -- the thing every tick is required to run
# before spending anything -- was untracked and absent from the deployed
# `live/` tree. A caller reading "not 1" as "proceed" turned a missing file
# (exit 127, bash's own "No such file or directory") into permission to
# spend, which is the exact inversion the gate exists to prevent and the
# mechanism behind the $80 -> $8 burn.
#
# `quota.sh check` defines exactly four exit codes: 0 SAFE, 1 WIND DOWN, 2
# UNAVAILABLE, 3 codexbar MISSING. This is the caller, and it enumerates what
# it accepts rather than testing what it refuses -- `case ... 0) ;; 1) ...;;
# *) ...;; esac`, never `[ "$rc" -eq 1 ]`. Only 0 proceeds. 1, 2, 3, 127, and
# anything this dispatcher has never seen all fail closed the same way:
# refuse to dispatch. QUOTA_GATE is overridable so tests can point it at a
# path that does not exist and watch this refuse.
QUOTA_GATE="${QUOTA_GATE:-$HERE/quota.sh}"
QUOTA_OUT=$("$QUOTA_GATE" check 2>&1)
QUOTA_RC=$?
case "$QUOTA_RC" in
  0)
    : # SAFE -- proceed
    ;;
  1)
    echo "dispatch: quota gate says WIND DOWN -- refusing to dispatch #$ISSUE_ARG" >&2
    sed 's/^/  /' <<<"$QUOTA_OUT" >&2
    echo "dispatch: this is a legitimate stop, not a failure -- quota-watch.sh brings the loop back" >&2
    exit 1
    ;;
  *)
    echo "dispatch: quota gate exited $QUOTA_RC ($QUOTA_GATE) -- UNKNOWN, never treated as safe" >&2
    sed 's/^/  /' <<<"$QUOTA_OUT" >&2
    exit 1
    ;;
esac

# --- infer --reviews-pr when the caller forgot it (agent-supervisor#70) ----
# agent-dotfiles#263's shape: a guard that "proceeds unchanged if the flag is
# omitted" is a guard that will be omitted. Measured on this estate: a
# self-review was dispatched three times in one day (PR #62 twice, #69 once)
# because `--reviews-pr` was forgotten every time, not because the guard
# below (step 0.5) failed once it ran.
#
# This is a SAFETY NET on top of the explicit flag, not a replacement for it.
# Skipped entirely when `--reviews-pr` was actually passed -- REVIEWS_PR is
# already non-empty then, and the caller's explicit answer is never
# second-guessed. loop-tick.md's instruction to pass the flag stands.
#
# Detection: a line -- in the issue's own title, checked first, then the
# brief -- containing both the word "review" and a PR reference, in one of
# three shapes (case-insensitive):
#   * "PR #<N>"                       -- "review PR #204"
#   * "PR <owner>/<repo>#<N>"         -- "review of PR jonhill90/agent-
#                                         supervisor#72" (agent-supervisor#72:
#                                         the Director's own review briefs
#                                         write it this way, every time)
#   * a github.com PR URL             -- ".../pull/<N>", with or without the
#                                         word "PR" nearby -- "/pull/" is
#                                         itself PR-specific (issues use
#                                         "/issues/"), so it needs no other
#                                         signal
# That is the shape every review issue and brief in this estate's own
# history already uses (see the #212/#35 fixtures in
# tests/supervisor/test_dispatch.sh and loop-tick.md's own dispatch
# instructions). No new convention is introduced; this only reads ones that
# already exist.
#
# DELIBERATELY NOT MATCHED: a bare "#<N>" with no "PR" word and no
# owner/repo before it. This estate's issues and briefs constantly say
# "closing issue #70" or "Fixes #240" right next to a PR reference on the
# same line ("review PR #500, no flag passed" is itself a title fixture
# below) -- a bare number would as often grab the issue being closed as the
# PR being reviewed, and inferring the WRONG PR is worse than inferring
# none (step 0.5's guard would refuse or exclude based on the wrong
# author). Likewise, `owner/repo#<N>` with no "PR" word is not matched: that
# exact shape is this repo's own convention for citing an ISSUE inline
# ("Fixes #240" close cousin), so matching it bare would conflate issue and
# PR references. The "PR " prefix is what disambiguates both.
#
# WRONG IN EACH DIRECTION, both survivable:
#   * FALSE POSITIVE (detected as a review when the dispatch is not one): the
#     worst outcome is step 0.5 below excluding one candidate lane it did not
#     need to, or refusing outright with "no free lane other than the
#     author" when that candidate was the only free one. Annoying, not
#     dangerous -- the message names exactly why, and the fix is to
#     re-dispatch (the issue's claim is released on refusal, same as every
#     other refusal path in this script).
#   * FALSE NEGATIVE (a real review not detected -- title and brief phrase it
#     differently than the shapes above): behaviour is exactly today's
#     status quo. The explicit flag remains the only guaranteed way to
#     trigger the guard; this block only catches the forgotten-flag case
#     when the issue happens to name the PR in one of the shapes above.
#
# agent-supervisor#101 changed NONE of the detection above. The false
# positive it reports is real -- "rebase it so it can be reviewed" next to
# "PR #93" is a rebase, not a review -- but every narrowing available reads
# the same prose the current pattern reads and so would drop real reviews
# this catches today, which is the dangerous direction. The escape is
# `--not-a-review` instead: an operator says "not a review" at the dispatch,
# where the intent actually lives, rather than rewording a brief until the
# tool stops matching it. Detection stays exactly as wide as it was; only the
# cost of being wrong changed, from "reword the brief" to "pass a flag".
INFER_PR_PATTERN='pr[[:space:]]*#[0-9]+|pr[[:space:]]*[a-z0-9_.-]+/[a-z0-9_.-]+#[0-9]+|github\.com/[a-z0-9_.-]+/[a-z0-9_.-]+/pull/[0-9]+'
REVIEWS_PR_INFERRED=""
if [ -z "$REVIEWS_PR" ] && [ -z "$NOT_A_REVIEW" ]; then
  INFER_GH_REPO_ARGS=()
  [ -n "$REPO" ] && INFER_GH_REPO_ARGS=(-R "$REPO")
  ISSUE_TITLE=$(gh issue view "$ISSUE" "${INFER_GH_REPO_ARGS[@]+"${INFER_GH_REPO_ARGS[@]}"}" --json title -q .title 2>/dev/null)
  INFERRED_PR=$(grep -iE 'review' <<<"$ISSUE_TITLE" | grep -ioE "$INFER_PR_PATTERN" | grep -oE '[0-9]+$' | head -1)
  INFERRED_FROM="issue #$ISSUE's title"
  if [ -z "$INFERRED_PR" ]; then
    INFERRED_PR=$(grep -iE 'review' "$BRIEF" | grep -ioE "$INFER_PR_PATTERN" | grep -oE '[0-9]+$' | head -1)
    INFERRED_FROM="the brief"
  fi
  if [ -n "$INFERRED_PR" ]; then
    REVIEWS_PR="$INFERRED_PR"
    REVIEWS_PR_INFERRED=1
    echo "dispatch: inferred --reviews-pr $REVIEWS_PR from $INFERRED_FROM (a line with 'review' and 'PR #$REVIEWS_PR') -- pass --reviews-pr explicitly to override" >&2
    echo "dispatch: if this dispatch is NOT a review, re-run it with --not-a-review (agent-supervisor#101) -- no need to reword the brief" >&2
  fi
fi

# agent-supervisor#159: PR_SCOPED is the PR this dispatch is FOR, whichever
# flag said so -- `--reviews-pr` (explicit or inferred above) implies it, and
# `--pr` says it directly for a dispatch that is not a review at all (a fix
# pass, most often). Computed AFTER inference, not before, so an inferred
# `--reviews-pr` is covered by this check too: naming two different PRs
# between `--pr` and `--reviews-pr` is refused rather than silently picking
# one, the same posture `--reviews-pr`/`--not-a-review` already takes above.
if [ -n "$PR" ] && [ -n "$REVIEWS_PR" ] && [ "$PR" != "$REVIEWS_PR" ]; then
  echo "dispatch: --pr $PR and --reviews-pr $REVIEWS_PR name different PRs -- refusing rather than picking one" >&2
  exit 2
fi
PR_SCOPED="${REVIEWS_PR:-$PR}"

# --- --adopt-pane validation (agent-supervisor#668) -------------------------
# Refused OUTRIGHT for the three shapes this mode does not yet speak, rather
# than silently degrading one of them: a review needs the author-exclusion
# bookkeeping (step 0.5, above), a PR-scoped follow-up needs its own source
# recording (step 6), and a multi-issue dispatch's window name is not simply
# `<issue>-<slug>` for the FIRST issue the way the rest of this mode assumes.
# None of the three is a hard technical wall; each is just untested and
# unasked-for by #668's own brief, so this stays additive rather than
# widening scope nobody requested. Checked here, before anything is claimed.
if [ -n "$ADOPT_PANE" ]; then
  if [ -n "$PR_SCOPED" ]; then
    echo "dispatch: --adopt-pane does not yet support a PR-scoped dispatch (--reviews-pr/--pr) -- use the ordinary tmux flow for #$ISSUE_ARG instead" >&2
    exit 2
  fi
  if [ "${#ISSUES[@]}" -gt 1 ]; then
    echo "dispatch: --adopt-pane does not yet support a multi-issue dispatch ($ISSUE_ARG) -- use the ordinary tmux flow instead" >&2
    exit 2
  fi
  # Accept either `@42` (what lanes.sh/tmux itself prints) or a bare `42`
  # (what an operator is more likely to type) -- normalized to the `@N` shape
  # every candidate_target below already carries, so the match in step 1 is a
  # plain string comparison rather than a second regex.
  case "$ADOPT_PANE" in
    @[0-9]*) ADOPT_PANE_ID="$ADOPT_PANE" ;;
    [0-9]*)  ADOPT_PANE_ID="@$ADOPT_PANE" ;;
    *)
      echo "dispatch: --adopt-pane '$ADOPT_PANE' is not a window id -- expected '@<N>' or '<N>' (agent-supervisor#668)" >&2
      exit 2
      ;;
  esac
  # This mode's whole point is staying on the named pane's own tmux window --
  # never routed to a freshly minted claude-print lane (step 1.5, below) and
  # never the pre-#171 tmux flow's OWN candidate search, which is free to
  # land on any free lane it likes. `--live-pane` already means exactly "keep
  # this dispatch on tmux/send-keys" (see that flag's own comment); implying
  # it here reuses that existing meaning instead of inventing a second one.
  LIVE_PANE=1
else
  ADOPT_PANE_ID=""
fi

# Window name: <prefix><issue>-<slug>, the convention loop-tick.md requires so
# Jon can read the tmux window list and know what the estate is doing. The
# prefix is the repo's initials when its name is hyphenated (agent-dotfiles ->
# ad, agent-evals -> ae) and the name itself when it is not (skills ->
# skills139-...), which is what the live session already looks like.
NAME_PART="${REPO##*/}"
[ -n "$NAME_PART" ] || NAME_PART="$(basename "$REPO_PATH")"

# agent-supervisor#111: one tmux session per repo, named for the repo NAME_PART
# just resolved -- a lane working in jonhill90/agent-tui runs in session
# agent-tui, not whatever repo the supervisor itself happens to live in.
# LANES_SESSION still overrides unconditionally (session_for_repo, #99).
# Resolved here rather than at the top of the script because NAME_PART is the
# earliest point REPO and REPO_PATH are both known, and SESSION is not read
# by anything above this line (first use is the lanes.sh call below).
SESSION="$(session_for_repo "$NAME_PART")"

if [[ "$NAME_PART" == *-* ]]; then
  PREFIX=$(tr '-' '\n' <<<"$NAME_PART" | cut -c1 | tr -d '\n')
else
  PREFIX="$NAME_PART"
fi
WINDOW_NAME="${PREFIX}${ISSUE}-${SLUG}"

# --- -1. the session itself must exist before any lane in it can be trusted -
# agent-supervisor#422: lanes.sh already refuses when $SESSION does not exist
# ("lanes: session '$SESSION' does not exist", exit 1) but every call site
# below wants only its stdout classification table and discards stderr
# (`2>/dev/null`) to get it -- so that message never reaches anyone. What
# reaches the caller instead is a LATER, generic "no free lane" refusal,
# worded identically to "every lane is busy" (#111 gives each repo its own
# session; a repo dispatch.sh has never bootstrapped a session for reads as
# silently, permanently full). Checked here, before step 0, so a missing
# session gets its own message and its own fix -- distinguishable from every
# other refusal below -- instead of falling through the candidate search and
# reading as ordinary busyness.
#
# `=name` is bootstrap-session.sh's own exact-match fix (#137): tmux
# prefix-matches `has-session -t foo` against an existing `foo-2`, so the
# same `=` prefix is required here or this probe could pass by matching a
# DIFFERENT session than the one this dispatch actually needs.
if ! tmux has-session -t "=$SESSION" 2>/dev/null; then
  BOOTSTRAP_LANES="${DISPATCH_BOOTSTRAP_LANES:-3}"
  echo "dispatch: session '$SESSION' does not exist -- #$ISSUE_ARG cannot be dispatched (agent-supervisor#422)" >&2
  if [ "${DISPATCH_NO_AUTO_BOOTSTRAP:-0}" = 1 ]; then
    echo "dispatch: DISPATCH_NO_AUTO_BOOTSTRAP=1 -- not auto-bootstrapping. Run by hand:" >&2
    echo "dispatch:   $HERE/bootstrap-session.sh --session $SESSION --lanes $BOOTSTRAP_LANES --cwd $REPO_PATH" >&2
    exit 2
  fi
  echo "dispatch: auto-bootstrapping session '$SESSION' ($BOOTSTRAP_LANES lanes, cwd $REPO_PATH)" >&2
  if "$HERE/bootstrap-session.sh" --session "$SESSION" --lanes "$BOOTSTRAP_LANES" --cwd "$REPO_PATH" >&2; then
    echo "dispatch: session '$SESSION' bootstrapped -- continuing dispatch" >&2
  else
    echo "dispatch: auto-bootstrap of session '$SESSION' FAILED -- run by hand:" >&2
    echo "dispatch:   $HERE/bootstrap-session.sh --session $SESSION --lanes $BOOTSTRAP_LANES --cwd $REPO_PATH" >&2
    exit 2
  fi
fi
