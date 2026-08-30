#!/bin/bash
# The one path the estate uses to post a verdict/comment to GitHub, so the
# class of bug in agent-supervisor#170 has exactly one place to be fixed.
#
# THE BUG THIS EXISTS TO CLOSE (#170): a caller passes an ARGUMENT-SHAPED
# STRING to an interface that takes a LITERAL value, the interface has no
# opinion about its own arguments, and it delivers the argument verbatim as
# content -- while reporting success. Two measured instances the same
# night: `notify.sh --body-file /dev/stdin` against a positional interface
# (17 Telegram pages whose body was literally "/dev/stdin"), and
# `gh pr comment --body @/tmp/...verdict.md` on PR #167 -- `--body` takes a
# literal string, so both comments' entire body was a file path. A reviewer
# believed it had approved a PR; the PR showed a path, and the real verdict
# existed only in scrollback.
#
# TWO INDEPENDENT DEFENSES, because either one alone reproduced an instance:
#
# (a) REFUSE ARGUMENT-SHAPED CONTENT AT THE BOUNDARY, before anything is
#     posted. A body that -- once trimmed -- is exactly one whitespace-free
#     token beginning with "@", "--", or "/", AND names a path that exists
#     on disk, is almost certainly a mistake: a caller who meant to write
#     "read this file" and instead wrote the path itself. This is
#     `notify.sh`'s flag-shaped-subject guard, generalised to the body and
#     to the "@file" form specifically, because that is the shape #167 hit.
#
#     WIDENED (finding 2, PR #176 review) to two more confirmed-slipping-
#     through shapes, both still argument-shaped content delivered as
#     content while the tool would report success -- #170's exact class,
#     just not the single-token-plus-real-path shape above:
#
#       - a bare TWO-token "FLAG PATH" body, e.g. "--body-file /etc/hosts":
#         the same #167 mistake, just glued by a space instead of "@".
#         Refused only when token 1 is long-option-shaped ("--word") AND
#         token 2 names a real path -- two-word prose that happens to
#         start with a flag-shaped word but continues into an unrelated
#         second word is not this shape and is left alone.
#       - a single BARE LONG-FLAG token that names no file at all, e.g.
#         "--reviews-pr" (a real flag this repo's own dispatch.sh uses --
#         see this repo's CLAUDE.md Conventions section -- but no file by
#         that name exists on disk, so the path-exists check above missed
#         it). The path-exists requirement is dropped for this one shape:
#         a verdict/comment body that IS, in its entirety, a bare
#         long-option token is not a realistic real comment, so the
#         narrower false-positive risk (a genuine one-word body that
#         happens to look like a flag) is accepted deliberately.
#
#     NOT widened, on purpose: a body with three or more tokens, or a
#     two-token body whose second token is not an existing path, is left
#     alone even if the first token is flag-shaped -- see the "still
#     allowed" cases in test_post_verdict.sh. Bare single-dash short
#     options ("-x") are also out of scope; #170's two measured instances
#     were both long-form ("--body-file", "--body"), and short options are
#     far more likely to appear inside real prose ("-1 to that idea").
#
# (c) VALIDATE THE Review-Lane: / Verdict: TRAILER PAIR AGAINST THE LEDGER,
#     before anything is posted -- agent-supervisor#187/#188. The
#     `Review-Lane:` trailer is the only thing that makes a verdict
#     attributable to a reviewer (`verdict._parse_review_lane`), and it used
#     to be hand-typed with nothing checking it at post time. Three
#     instances measured within 24h, all false refusals at the merge gate
#     hours later: a task slug instead of a lane id (unresolvable), and
#     TWICE the reviewing agent named the supervisor's own window instead of
#     its own lane. #188 found this script itself unwired -- nothing calls
#     it -- so the check could not have caught any of them even if it
#     existed. Both are fixed together here: this script is now the thing a
#     reviewer actually runs, and it now refuses before posting rather than
#     letting the merge gate discover the mistake later, when a retry costs
#     a whole extra dispatch instead of one.
#
#     Two independent checks, because a pairing mistake and a bad lane value
#     are different defects with different fixes:
#
#       - PAIRING: a `Verdict:` line with no `Review-Lane:` line, or a
#         `Review-Lane:` line with no `Verdict:` line, is refused outright.
#         A verdict nobody can attribute, or a lane stamp attributing
#         nothing, is always a mistake worth catching immediately rather
#         than at the merge gate.
#       - RESOLUTION: when both are present, the `Review-Lane:` value (via
#         `verdict._parse_review_lane`, the SAME parser the merge gate
#         uses -- no second implementation of "is this a lane" per
#         agent-supervisor#108's own lesson) must resolve to a lane this
#         ledger has REGISTERED (`core.Ledger.get_lane`) and must NOT be the
#         supervisor's own window (`core.LANE_ID_RE`'s index component
#         compared against the same `LANES_SUPERVISOR_WINDOW` convention
#         `lanes.sh` already uses, default window 1) -- the exact two
#         measured false-refusal shapes above.
#
#     A body with neither line (an ordinary comment posted through this
#     script for unrelated reasons -- see `--issue`) is untouched by either
#     check.
#
# (b) READ BACK WHAT WAS POSTED AND COMPARE, and only THEN report success.
#     "gh returned 0" is not evidence -- it is this repository's own
#     measured-versus-inferred rule, and BOTH instances above returned 0
#     while delivering garbage. `gh pr comment` / `gh issue comment` return
#     the URL of the comment they created; this script re-fetches that
#     exact comment by id from the API (never "the last comment", which
#     could race another poster) and compares its body byte-for-byte
#     against what was sent. A mismatch is reported loudly and non-zero,
#     even though the post itself already happened -- there is no way to
#     "un-post" a wrong comment, only to make sure the caller finds out.
#
# Usage:
#   post-verdict.sh <repo> <number> [--issue] [--expect-verdict]
#
# <repo> is owner/name. <number> is the PR (default) or issue (--issue)
# number. The body is read from STDIN, never from an argument -- this
# script has no --body / --body-file flag of its own to confuse, on
# purpose, the same lesson notify.sh's positional interface encodes.
#
# --expect-verdict (agent-estate#719): tells this script that the body it
# is about to post MUST carry a complete Verdict:/Review-Lane:/Reviewed-SHA:
# trailer, and to refuse (exit 10) with an exact statement of the missing
# shape if it does not. Opt-in per call, never inferred from the body's own
# wording -- a review-dispatch or fix-pass-reply brief should pass it; a
# caller posting an ordinary comment (status updates, `--issue` notes) omits
# it and keeps today's behaviour unchanged.
#
#   printf '%s\n' "$VERDICT_TEXT" | post-verdict.sh jonhill90/agent-supervisor 167
#
# Exit 0   posted, read back, and the read-back matched what was sent.
# Exit 1   `gh` failed to create the comment; nothing was posted.
# Exit 2   usage error.
# Exit 3   refused -- the body is argument-shaped content naming a real path.
# Exit 4   posted, but the comment id could not be recovered from `gh`'s own
#          output, so the read-back in (b) cannot happen. Fails closed:
#          unverifiable is treated as unproven, never as success.
# Exit 5   posted, but the read-back fetch itself failed.
# Exit 6   posted, but the read-back body did NOT match what was sent --
#          the exact failure this script exists to catch.
# Exit 7   refused -- a Verdict: line with no Review-Lane: line, or a
#          Review-Lane: line with no Verdict: line (#187/#232's pairing
#          mistake).
# Exit 8   refused -- the Review-Lane: value does not resolve to a lane this
#          ledger has registered, resolves to the supervisor's own window
#          (#187's two measured false-refusal shapes), or resolves to a lane
#          whose registration the live tmux server contradicts (#520).
# Exit 9   refused -- the body contains a `Verdict:`-shaped line that is not
#          the first line of a complete, unbroken Verdict:/Review-Lane:/
#          Reviewed-SHA: block (agent-supervisor#595's write-time mirror: a
#          bare label an agent is ABOUT TO POST is much more likely to be a
#          half-finished draft or a poisoning shape than a deliberate quote
#          of somebody else's trailer -- see `verdict._unblocked_verdict_labels`
#          for the exact check and why it does not give this the same
#          inline-code pass `_scan_verdict_lines` gives a comment being READ).
# Exit 10  refused -- `--expect-verdict` was passed but the body has NO
#          complete Verdict:/Review-Lane:/Reviewed-SHA: block at all
#          (agent-estate#719 item 1: a review that ends in prose --
#          "...Recommend APPROVE." -- with no trailer whatsoever). This is
#          opt-in per caller, not inferred from the body's own prose: a
#          brief dispatching a review or a fix-pass reply that must end in a
#          verdict should pass this flag so the refusal happens HERE, with
#          an exact statement of what to add, instead of failing silently
#          through as an "ordinary comment" and only surfacing later as an
#          unexplained stuck PR at the merge gate. A caller that does not
#          pass it keeps today's behaviour: a body with neither trailer
#          line is still an ordinary comment, untouched by any check here.
#
# IF YOU ARE WRITING BRIEF TEXT FOR A REVIEW OR FIX-PASS DISPATCH (#412):
# the class of bug #187 measured was never a committed script calling `gh
# pr comment` -- it was a reviewing agent hand-typing the verdict trailers
# because its OWN BRIEF told it to post that way. This script existing does
# nothing for that class unless the brief's closing instruction actually
# names it. Tell the lane to run:
#
#   printf '%s\n' "$BODY" | post-verdict.sh <repo> <N>
#
# never a raw `gh pr comment`/`gh issue comment`. `gh-comment-gate.sh`
# cannot catch a drift back to raw posting in brief text -- see its own
# docstring -- so this is enforced by writing the brief correctly, not by
# CI. `scripts/supervisor/loop-tick.md`'s review-dispatch section is the
# in-repo doc that generates that text; keep it pointed here.
set -uo pipefail

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2; }

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Same env var and default `quota.sh`/`sessions.sh` already use -- `cli.py`
# resolves this itself for every OTHER caller, but this script constructs
# `core.Ledger` directly (the same "no cli.py subcommand fits a single-field
# read" precedent as `verdict-independence.sh`'s `_lane_own_pane_id`), so it
# needs the same default in hand.
STATE="${AGENT_SUPERVISOR_STATE_DIR:-$HOME/.local/state/agent-dotfiles-supervisor}"
LEDGER_PYTHON="${POST_VERDICT_PYTHON:-python3}"
# The supervisor's own window index within its session -- `lanes.sh`'s own
# convention (`SUPERVISOR_WINDOW`), same env var, so the two never drift.
SUPERVISOR_WINDOW="${LANES_SUPERVISOR_WINDOW:-1}"

GH="${AGENT_GH_BIN:-gh}"

# --- args ---------------------------------------------------------------
# Flag-shaped repo/number is refused up front, same spirit as notify.sh's
# guard on $1: this script's own interface is positional, so anything
# flag-shaped in the two required slots is already a caller confused about
# which tool they are calling.
case "${1:-}" in --*) usage ;; esac
REPO="${1:-}"
NUMBER="${2:-}"
[ -n "$REPO" ] && [ -n "$NUMBER" ] || usage
case "$NUMBER" in
  ''|*[!0-9]*) echo "post-verdict.sh: <number> must be numeric, got '$NUMBER'" >&2; usage ;;
esac
shift 2 || true

KIND="pr"
EXPECT_VERDICT=""
while [ $# -gt 0 ]; do
  case "$1" in
    --issue) KIND="issue"; shift ;;
    --expect-verdict) EXPECT_VERDICT=1; shift ;;
    *) echo "post-verdict.sh: unrecognised argument '$1'" >&2; usage ;;
  esac
done

# --- read the body from stdin, never from an argument --------------------
BODY="$(cat)"

# --- (a) refuse argument-shaped content, before anything is posted -------
# Trim leading/trailing whitespace only -- an interior "@" or "--" (e.g. an
# email address, or "flags --like-this" inside real prose) is untouched;
# this guard exists for a body that IS the token(s), not one that merely
# contains a substring shaped like one.
trimmed="$(printf '%s' "$BODY" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"

# Token count and the first two tokens, whitespace-split (spaces, tabs, AND
# newlines -- same as the old *[[:space:]]* glob test this replaces, so a
# multi-line body is still never mistaken for a one- or two-token one).
# Scoped to a function so this does not clobber the script's own $1/$2 --
# bash gives a function its own positional parameters.
split_trimmed() {
  set -f
  # shellcheck disable=SC2086  # word-splitting is the point
  set -- $1
  set +f
  tok_count=$#
  tok1="${1:-}"
  tok2="${2:-}"
}
split_trimmed "$trimmed"

if [ -z "$trimmed" ]; then
  : # empty -- not this shape
elif [ "$tok_count" -eq 1 ]; then
  case "$tok1" in
    @*|--*|/*)
      candidate="$tok1"
      case "$candidate" in @*) candidate="${candidate#@}" ;; esac
      is_bare_flag=""
      case "$tok1" in --[A-Za-z]*) is_bare_flag=1 ;; esac
      if { [ -n "$candidate" ] && [ -e "$candidate" ]; } || [ -n "$is_bare_flag" ]; then
        echo "post-verdict.sh: refusing to post -- body is exactly the argument-shaped token '$trimmed'" >&2
        echo "post-verdict.sh: this is the #170 shape (a body that is really a file path or a bare flag, not real content) -- pass the CONTENT on stdin, not a reference to it" >&2
        exit 3
      fi
      ;;
  esac
elif [ "$tok_count" -eq 2 ]; then
  case "$tok1" in
    --[A-Za-z]*)
      if [ -e "$tok2" ]; then
        echo "post-verdict.sh: refusing to post -- body is exactly the two-token pair '$trimmed', which looks like a flag-plus-path invocation pasted as content instead of being run" >&2
        echo "post-verdict.sh: this is the #170 shape widened (finding 2, PR #176 review) -- pass the CONTENT on stdin, not a command line" >&2
        exit 3
      fi
      ;;
  esac
fi

# --- (c) Review-Lane: / Verdict: pairing and lane resolution (#187/#188) --
lane_check_json="$("$LEDGER_PYTHON" -c '
import json, sys
sys.path.insert(0, sys.argv[1])
from core import Ledger, LANE_ID_RE
from verdict import _parse_review_lane, _review_lane_line, _scan_verdict_lines, _unblocked_verdict_labels

body = sys.stdin.read()
ledger = Ledger(sys.argv[2])
supervisor_window = sys.argv[3]

has_verdict = bool(_scan_verdict_lines(body))
raw_line = _review_lane_line(body)
lane_token = _parse_review_lane(body, ledger=ledger)
bare_labels = _unblocked_verdict_labels(body)

known = False
is_supervisor = False
if lane_token:
    known = ledger.get_lane(lane_token) is not None
    match = LANE_ID_RE.match(lane_token)
    if match:
        index = match.group("index")
        try:
            is_supervisor = int(index) == int(supervisor_window)
        except ValueError:
            is_supervisor = index == supervisor_window

json.dump({
    "has_verdict": has_verdict,
    "has_lane_line": raw_line is not None,
    "raw_lane_line": raw_line or "",
    "lane_token": lane_token or "",
    "known": known,
    "is_supervisor": is_supervisor,
    "bare_labels": bare_labels,
}, sys.stdout)
' "$HERE" "$STATE" "$SUPERVISOR_WINDOW" <<<"$BODY" 2>/dev/null)"

if [ -z "$lane_check_json" ] || ! jq -e . >/dev/null 2>&1 <<<"$lane_check_json"; then
  echo "post-verdict.sh: could not evaluate the Review-Lane:/Verdict: trailers -- refusing to post rather than guess (is python3 able to import verdict.py/core.py from $HERE, and is the ledger at $STATE readable?)" >&2
  exit 7
fi

has_verdict=$(jq -r '.has_verdict' <<<"$lane_check_json")
has_lane_line=$(jq -r '.has_lane_line' <<<"$lane_check_json")
lane_token=$(jq -r '.lane_token' <<<"$lane_check_json")
lane_known=$(jq -r '.known' <<<"$lane_check_json")
lane_is_supervisor=$(jq -r '.is_supervisor' <<<"$lane_check_json")
raw_lane_line=$(jq -r '.raw_lane_line' <<<"$lane_check_json")

if [ "$has_verdict" = "true" ] && [ "$has_lane_line" != "true" ]; then
  echo "post-verdict.sh: refusing to post -- body has a Verdict: line but no Review-Lane: line -- add 'Review-Lane: <session>:<index>' naming the reviewing lane (agent-supervisor#187/#232's own pairing mistake)" >&2
  exit 7
fi
if [ "$has_lane_line" = "true" ] && [ "$has_verdict" != "true" ]; then
  echo "post-verdict.sh: refusing to post -- body has a Review-Lane: line ('$raw_lane_line') but no Verdict: line -- a lane stamp with nothing to attribute is almost always a mistake" >&2
  exit 7
fi

# --- opt-in: a caller that KNOWS this post must carry a verdict says so ---
# (agent-estate#719 item 1). Only reached when has_verdict is false AND
# has_lane_line is false -- either trailer alone already refuses above.
# This never fires unless the caller passes --expect-verdict: post-verdict.sh
# has no way to tell a review reply from an ordinary status comment by
# reading the body's prose, and guessing that from wording is exactly the
# "infer a verdict from prose" move agent-estate#719 forbids for reading a
# comment back -- the same principle applies to inferring INTENT at write
# time. The caller (a review/fix-pass dispatch brief) states its own intent
# instead.
if [ -n "$EXPECT_VERDICT" ] && [ "$has_verdict" != "true" ]; then
  echo "post-verdict.sh: refusing to post -- --expect-verdict was passed but the body has no Verdict:/Review-Lane:/Reviewed-SHA: trailer at all" >&2
  echo "post-verdict.sh: end the comment with exactly this three-line block, nothing between the lines:" >&2
  echo "post-verdict.sh:   Verdict: APPROVE|REQUEST_CHANGES" >&2
  echo "post-verdict.sh:   Review-Lane: <session>:<index>" >&2
  echo "post-verdict.sh:   Reviewed-SHA: <the 40-char head SHA you reviewed>" >&2
  exit 10
fi

if [ "$has_lane_line" = "true" ]; then
  if [ -z "$lane_token" ] || [ "$lane_token" = "null" ]; then
    echo "post-verdict.sh: refusing to post -- '$raw_lane_line' does not resolve to a lane id -- pass the exact <session>:<index> this ledger knows" >&2
    exit 8
  fi
  if [ "$lane_known" != "true" ]; then
    echo "post-verdict.sh: refusing to post -- Review-Lane '$lane_token' is not a lane this ledger has registered -- a lane must have been dispatched at least once before it can review" >&2
    exit 8
  fi
  # agent-supervisor#520: "a row exists" was the whole of the check above, and
  # a row can be stale or never-measured -- four of this estate's own lanes
  # carried rows naming panes that did not exist on the running server, and
  # every one of them satisfied the check above. `lane_identity.py` refuses
  # only on `contradicted` (the server the row itself names is reachable and
  # disagrees with it); `unverifiable` is left alone, for the reasons
  # `verdict-independence.sh`'s `_lane_identity_status` states in full. Caught
  # HERE as well as at the merge gate for the same reason every other check in
  # this script is: a retry now costs one command, at the merge gate it costs
  # a whole extra dispatch.
  identity_json="$("$LEDGER_PYTHON" "$HERE/lane_identity.py" --lane "$lane_token" --state-dir "$STATE" 2>/dev/null)"
  if [ "$(jq -r '.status // ""' 2>/dev/null <<<"$identity_json")" = "contradicted" ]; then
    echo "post-verdict.sh: refusing to post -- Review-Lane '$lane_token' is registered, but the live tmux server contradicts that registration: $(jq -r '.detail // ""' <<<"$identity_json")" >&2
    echo "post-verdict.sh: re-register from that lane's own pane (register-lane-self.sh) before posting a verdict as it -- a stale registration is worse than none, because the merge gate reads it as an identity" >&2
    exit 8
  fi
  if [ "$lane_is_supervisor" = "true" ]; then
    echo "post-verdict.sh: refusing to post -- Review-Lane '$lane_token' names the supervisor's own window (index $SUPERVISOR_WINDOW), which never reviews -- this is the exact false-refusal shape agent-supervisor#187 measured twice" >&2
    exit 8
  fi
fi

# --- write-time mirror of agent-supervisor#595's read-time fix -----------
# A `Verdict:`-shaped line that is not the first line of a complete,
# unbroken Verdict:/Review-Lane:/Reviewed-SHA: block is refused before it
# is ever posted -- see `verdict._unblocked_verdict_labels` for why this
# does not give a bare label the same inline-code pass a comment being READ
# gets, and this script's own header comment (Exit 9) for the scope this
# does not attempt to cover (human web UI, a codex lane posting directly).
bare_label_count=$(jq -r '.bare_labels | length' <<<"$lane_check_json")
if [ "$bare_label_count" != "0" ]; then
  echo "post-verdict.sh: refusing to post -- body has a Verdict:-shaped line that is not the first line of a complete Verdict:/Review-Lane:/Reviewed-SHA: block (agent-supervisor#595's write-time mirror):" >&2
  jq -r '.bare_labels[] | "  " + .' <<<"$lane_check_json" >&2
  exit 9
fi

# --- post -----------------------------------------------------------------
post_out="$(printf '%s' "$BODY" | "$GH" "$KIND" comment "$NUMBER" --repo "$REPO" --body-file - 2>&1)"
post_rc=$?
if [ "$post_rc" -ne 0 ]; then
  echo "post-verdict.sh: gh failed to post the comment (exit $post_rc): $post_out" >&2
  exit 1
fi

# --- (b) read back what was posted, before saying anything succeeded -----
# `gh ... comment` prints the URL of the comment it created, ending in
# "#issuecomment-<id>". Extracting THAT id and fetching it directly is
# deliberate -- "the last comment on the PR" could be a race against
# another poster, and would not prove THIS post is what landed.
comment_id="$(printf '%s\n' "$post_out" | grep -oE 'issuecomment-[0-9]+' | tail -n1 | grep -oE '[0-9]+')"
if [ -z "$comment_id" ]; then
  echo "post-verdict.sh: posted, but could not recover a comment id from gh's own output -- cannot verify, so refusing to call this a success: $post_out" >&2
  exit 4
fi

readback="$("$GH" api "repos/$REPO/issues/comments/$comment_id" --jq .body 2>&1)"
readback_rc=$?
if [ "$readback_rc" -ne 0 ]; then
  echo "post-verdict.sh: posted comment $comment_id, but reading it back failed (exit $readback_rc): $readback" >&2
  exit 5
fi

if [ "$readback" != "$BODY" ]; then
  echo "post-verdict.sh: posted comment $comment_id, but the read-back does NOT match what was sent -- reporting this as a FAILURE, not a success" >&2
  echo "post-verdict.sh: sent:     $(printf '%s' "$BODY" | head -c 200)" >&2
  echo "post-verdict.sh: read back: $(printf '%s' "$readback" | head -c 200)" >&2
  exit 6
fi

echo "post-verdict.sh: posted and verified comment $comment_id on $KIND $NUMBER ($REPO)"
printf '%s\n' "$post_out"
exit 0
