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
#   post-verdict.sh <repo> <number> [--issue]
#
# <repo> is owner/name. <number> is the PR (default) or issue (--issue)
# number. The body is read from STDIN, never from an argument -- this
# script has no --body / --body-file flag of its own to confuse, on
# purpose, the same lesson notify.sh's positional interface encodes.
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
set -uo pipefail

usage() { sed -n '/^# Usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2; }

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
while [ $# -gt 0 ]; do
  case "$1" in
    --issue) KIND="issue"; shift ;;
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
