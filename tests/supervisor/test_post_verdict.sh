#!/bin/bash
# Behaviour tests for post-verdict.sh (agent-supervisor#170).
#
# Two measured instances, one night, same shape: an argument-shaped string
# (a flag, or "@path") delivered as content, verbatim, while the tool
# reported success. This suite locks down the two independent defenses
# post-verdict.sh adds:
#
#   1. a body that is really just a reference to a file (#167's
#      `--body @/tmp/...verdict.md` shape) is REFUSED before anything is
#      posted, and `gh` must never even be invoked -- proving the refusal
#      is a real boundary, not a check that runs after the fact.
#   2. a post that "succeeded" (gh exit 0) but whose read-back does not
#      match what was sent must be reported as a FAILURE, not a success --
#      "gh returned 0" was exactly what both #170 instances measured while
#      delivering garbage.
#
# Section 7 below adds the third defense (agent-supervisor#187/#188): a
# Verdict:/Review-Lane: pairing mistake, or a Review-Lane: that does not
# resolve to a known, non-supervisor lane, must also be refused before
# anything is posted. Sections 1-4's existing bodies that carry a `Verdict:`
# line are updated to also carry a matching, ledger-registered `Review-Lane:`
# line, so they keep testing what they always tested instead of tripping the
# new check for an unrelated reason.
#
# Section 8 adds the fourth defense (agent-supervisor#595's write-time
# mirror): a body containing a `Verdict:`-shaped line that is not the first
# line of a complete, unbroken Verdict:/Review-Lane:/Reviewed-SHA: block is
# refused at exit 9, before anything is posted. `verdict._scan_verdict_lines`
# now requires that same complete block to consider a line operative at all
# (agent-supervisor#595/#609), which means `has_verdict` below can only be
# true when a Review-Lane: line is ALSO present -- so section 7a's original
# "Verdict: line with no Review-Lane: line" fixture no longer reaches the
# exit-7 pairing branch it used to; it is caught earlier, by the new
# completeness lint, at exit 9. 7a is updated in place to say so. Every
# `Verdict:`/`Review-Lane:` body elsewhere in this file that did not
# previously carry a `Reviewed-SHA:` trailer now gets one added, so it stays
# testing what it always tested instead of tripping the new lint for an
# unrelated reason -- same discipline #595's own fixture updates in
# `tests/supervisor/test_verdict.py` follow.
set -uo pipefail
REVIEWED_SHA="a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
POST_VERDICT="$HERE/../../scripts/supervisor/post-verdict.sh"
SUPERVISOR_DIR="$HERE/../../scripts/supervisor"
pass=0; fail=0
ok() { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; fail=$((fail+1)); }

echo "post-verdict.sh"

# seed_lane <lane> <pane-id> -- registers a lane directly in the ledger, the
# same shape test_lane_relation_renumber.sh uses, so post-verdict.sh's own
# ledger lookups (core.Ledger.get_lane) find it without needing a real tmux
# server.
seed_lane() {
  python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
ledger = Ledger(sys.argv[2])
ledger.register_lane(
    lane=sys.argv[3], pane_id=sys.argv[4], nonce="nonce-" + sys.argv[3],
    harness="claude", repo="/tmp/repo", server_id="srv", session_id="sess",
    command="claude", transport="send-keys",
)
' "$SUPERVISOR_DIR" "$STATE" "$1" "$2"
}

D=$(mktemp -d)
STATE="$D/state"
export AGENT_SUPERVISOR_STATE_DIR="$STATE"

# The two lanes section 7's new checks (and sections 1-4's updated bodies)
# need: a real, non-supervisor lane every "good" body's Review-Lane: names,
# and a lane whose window index IS the supervisor's own (default window 1,
# `LANES_SUPERVISOR_WINDOW`) for the false-refusal shape agent-supervisor#187
# measured twice.
seed_lane "revlane:3" "%501"
seed_lane "revlane:1" "%502"

# =========================================================================
# 1. RED: a body of "@/tmp/x.md" -- an existing file -- must be refused,
#    not posted verbatim. gh must never be called at all.
# =========================================================================
mkdir -p "$D/refuse/bin"
GH_LOG="$D/refuse/gh.log"
REFUSE_SENT="$D/refuse/sent.body"
cat > "$D/refuse/bin/gh" <<EOF
#!/bin/bash
echo "gh called: \$*" >> "$GH_LOG"
if [ "\$1" = "pr" ] && [ "\$2" = "comment" ]; then
  cat > "$REFUSE_SENT"
  echo "https://github.com/o/r/pull/5#issuecomment-999"
  exit 0
elif [ "\$1" = "api" ]; then
  cat "$REFUSE_SENT"
  exit 0
fi
exit 9
EOF
chmod +x "$D/refuse/bin/gh"

VERDICT_FILE="$D/refuse/verdict.md"
printf '**Verdict:** APPROVE\nreal findings here\n' > "$VERDICT_FILE"

PATH="$D/refuse/bin:$PATH" \
  bash "$POST_VERDICT" o/r 5 <<EOF >"$D/refuse/out" 2>"$D/refuse/err"
@$VERDICT_FILE
EOF
rc=$?

if [ "$rc" -ne 0 ]; then ok "an @path body naming a real file exits non-zero (got $rc)"; else bad "an @path body exited 0"; fi
if [ ! -s "$GH_LOG" ]; then ok "gh is never invoked for a refused body"; else bad "gh was invoked despite the refusal: $(cat "$GH_LOG")"; fi
if grep -q "refusing to post" "$D/refuse/err"; then ok "the refusal is explained on stderr"; else bad "no refusal message on stderr: $(cat "$D/refuse/err")"; fi

# The same shape with a bare path (no "@") and with "--" must also refuse,
# since #170 named all three prefixes ("@", "--", "/") as the shape.
PATH="$D/refuse/bin:$PATH" \
  bash "$POST_VERDICT" o/r 5 <<EOF >/dev/null 2>"$D/refuse/err2"
$VERDICT_FILE
EOF
rc=$?
if [ "$rc" -ne 0 ]; then ok "a bare existing-path body is also refused"; else bad "a bare existing-path body exited 0"; fi

# A body that merely CONTAINS "@" (real prose, more than one token, or
# naming a path that does NOT exist) must NOT be refused by this guard --
# it is not the argument-shaped-single-token-naming-a-real-file shape.
PATH="$D/refuse/bin:$PATH" \
  bash "$POST_VERDICT" o/r 5 <<EOF >"$D/refuse/prose_out" 2>"$D/refuse/prose_err"
**Verdict:** APPROVE -- see @$D/refuse/does-not-exist.md for detail, plus more words
Review-Lane: revlane:3
Reviewed-SHA: $REVIEWED_SHA
EOF
rc=$?
if [ "$rc" -eq 0 ]; then ok "real prose mentioning an @-path is not refused"; else bad "real prose was wrongly refused (rc=$rc): $(cat "$D/refuse/prose_err")"; fi

# =========================================================================
# 2. RED: gh reports success (exit 0, a comment URL) but the read-back does
#    not match what was sent -- must be reported as a FAILURE.
# =========================================================================
mkdir -p "$D/mismatch/bin"
cat > "$D/mismatch/bin/gh" <<'EOF'
#!/bin/bash
if [ "$1" = "pr" ] && [ "$2" = "comment" ]; then
  cat >/dev/null   # consume the body from stdin, same as the real gh
  echo "https://github.com/o/r/pull/9#issuecomment-4242"
  exit 0
elif [ "$1" = "api" ]; then
  # Simulates GitHub (or gh) handing back something other than what was
  # sent -- the exact shape both #170 instances measured while gh's own
  # exit code was 0.
  echo '@/tmp/pr167_verdict.md'
  exit 0
fi
echo "unexpected gh invocation: $*" >&2
exit 9
EOF
chmod +x "$D/mismatch/bin/gh"

PATH="$D/mismatch/bin:$PATH" \
  bash "$POST_VERDICT" o/r 9 <<EOF >"$D/mismatch/out" 2>"$D/mismatch/err"
**Verdict:** APPROVE, real findings, none of which is a file path
Review-Lane: revlane:3
Reviewed-SHA: $REVIEWED_SHA
EOF
rc=$?
if [ "$rc" -ne 0 ]; then ok "a read-back mismatch after a successful gh post exits non-zero (got $rc)"; else bad "a read-back mismatch exited 0"; fi
if ! grep -qi "posted and verified" "$D/mismatch/out"; then ok "no success confirmation is printed on a mismatch"; else bad "success was printed despite the mismatch: $(cat "$D/mismatch/out")"; fi
if grep -qi "does NOT match" "$D/mismatch/err"; then ok "the mismatch is explained on stderr"; else bad "no mismatch explanation on stderr: $(cat "$D/mismatch/err")"; fi

# =========================================================================
# 3. GREEN: a real post whose read-back matches is reported as success,
#    exactly once, with the verified comment identified.
#
#    This mock's "api" branch is id-aware (PR #176 review, finding 1): it
#    only hands back what was actually sent when asked for comment id 7 --
#    the id `pr comment` reported creating. Any other id gets a body that
#    provably did NOT come from this post. Before this, every mock `gh` in
#    this file's "api" branch ignored the requested id entirely and just
#    cat'd back whatever was last sent to "pr comment" -- so a script bug
#    that fetched the WRONG id (e.g. a hardcoded id, or an off-by-one) was
#    indistinguishable from the correct one: the suite could not tell
#    "read back the comment that was actually created" apart from "read
#    back some other id and got lucky because the mock didn't care." An id
#    that isn't 7 must produce mismatching content, or a mutation that
#    fetches `repos/$REPO/issues/comments/1` instead of `$comment_id`
#    would stay green.
# =========================================================================
mkdir -p "$D/happy/bin"
SENT_LOG="$D/happy/sent.body"
cat > "$D/happy/bin/gh" <<EOF
#!/bin/bash
if [ "\$1" = "pr" ] && [ "\$2" = "comment" ]; then
  cat > "$SENT_LOG"
  echo "https://github.com/o/r/pull/11#issuecomment-7"
  exit 0
elif [ "\$1" = "api" ]; then
  requested_id="\$(printf '%s' "\$2" | grep -oE '[0-9]+\$')"
  if [ "\$requested_id" = "7" ]; then
    cat "$SENT_LOG"
  else
    echo "WRONG COMMENT: id \$requested_id was never posted by this test -- id-aware mock (PR #176 finding 1)"
  fi
  exit 0
fi
exit 9
EOF
chmod +x "$D/happy/bin/gh"

PATH="$D/happy/bin:$PATH" \
  bash "$POST_VERDICT" o/r 11 <<EOF >"$D/happy/out" 2>"$D/happy/err"
**Verdict:** APPROVE -- read back matches what was sent
Review-Lane: revlane:3
Reviewed-SHA: $REVIEWED_SHA
EOF
rc=$?
if [ "$rc" -eq 0 ]; then ok "a matching read-back exits zero"; else bad "a matching read-back exited $rc: $(cat "$D/happy/err")"; fi
if grep -q "posted and verified comment 7" "$D/happy/out"; then ok "success names the verified comment id"; else bad "no verified-comment confirmation: $(cat "$D/happy/out")"; fi

# =========================================================================
# 4. RED: gh itself fails to create the comment -- must not be reported as
#    success, and no read-back should be attempted.
# =========================================================================
mkdir -p "$D/ghfail/bin"
cat > "$D/ghfail/bin/gh" <<'EOF'
#!/bin/bash
echo "gh: some API error" >&2
exit 1
EOF
chmod +x "$D/ghfail/bin/gh"
PATH="$D/ghfail/bin:$PATH" \
  bash "$POST_VERDICT" o/r 3 <<EOF >"$D/ghfail/out" 2>"$D/ghfail/err"
**Verdict:** APPROVE
Review-Lane: revlane:3
Reviewed-SHA: $REVIEWED_SHA
EOF
rc=$?
if [ "$rc" -ne 0 ]; then ok "a gh post failure exits non-zero"; else bad "a gh post failure exited 0"; fi
if [ ! -s "$D/ghfail/out" ]; then ok "nothing is printed to stdout on a post failure"; else bad "stdout was non-empty on a post failure: $(cat "$D/ghfail/out")"; fi

# =========================================================================
# 5. RED: the two shapes confirmed slipping through, per the PR #176
#    review's finding 2 -- both argument-shaped content delivered as
#    content while the tool would report success, just not the original
#    single-token-plus-real-path shape.
# =========================================================================
mkdir -p "$D/widen/bin"
WIDEN_LOG="$D/widen/gh.log"
cat > "$D/widen/bin/gh" <<EOF
#!/bin/bash
echo "gh called: \$*" >> "$WIDEN_LOG"
exit 9
EOF
chmod +x "$D/widen/bin/gh"

# 5a. a two-token "FLAG PATH" body naming a real path -- the finding's
#     "--body-file /etc/hosts" shape (using a scratch file, not a system
#     path, so this test does not depend on /etc/hosts existing).
: > "$WIDEN_LOG"
REAL_PATH="$D/widen/some-file"
: > "$REAL_PATH"
PATH="$D/widen/bin:$PATH" \
  bash "$POST_VERDICT" o/r 5 <<EOF >"$D/widen/twotok_out" 2>"$D/widen/twotok_err"
--body-file $REAL_PATH
EOF
rc=$?
if [ "$rc" -eq 3 ]; then ok "a two-token flag+existing-path body is refused (got $rc)"; else bad "a two-token flag+existing-path body was not refused (rc=$rc)"; fi
if [ ! -s "$WIDEN_LOG" ]; then ok "gh is never invoked for the refused two-token body"; else bad "gh was invoked despite the refusal: $(cat "$WIDEN_LOG")"; fi

# 5b. a single bare long-flag token that names no real file -- the
#     finding's own "--reviews-pr" example (a real flag in this repo,
#     per CLAUDE.md, but no file by that name on disk).
: > "$WIDEN_LOG"
PATH="$D/widen/bin:$PATH" \
  bash "$POST_VERDICT" o/r 5 <<'EOF' >"$D/widen/bareflag_out" 2>"$D/widen/bareflag_err"
--reviews-pr
EOF
rc=$?
if [ "$rc" -eq 3 ]; then ok "a bare long-flag body naming no real file is refused (got $rc)"; else bad "a bare long-flag body was not refused (rc=$rc)"; fi
if [ ! -s "$WIDEN_LOG" ]; then ok "gh is never invoked for the refused bare-flag body"; else bad "gh was invoked despite the refusal: $(cat "$WIDEN_LOG")"; fi

# =========================================================================
# 6. GREEN: shapes deliberately still allowed, so the boundary the header
#    comment documents is locked down on both sides.
# =========================================================================
mkdir -p "$D/allow/bin"
ALLOW_LOG="$D/allow/sent.body"
cat > "$D/allow/bin/gh" <<EOF
#!/bin/bash
if [ "\$1" = "pr" ] && [ "\$2" = "comment" ]; then
  cat > "$ALLOW_LOG"
  echo "https://github.com/o/r/pull/5#issuecomment-42"
  exit 0
elif [ "\$1" = "api" ]; then
  cat "$ALLOW_LOG"
  exit 0
fi
exit 9
EOF
chmod +x "$D/allow/bin/gh"

# 6a. a two-token body whose second word is NOT an existing path -- the
#     "still allowed" half of the widened two-token rule.
PATH="$D/allow/bin:$PATH" \
  bash "$POST_VERDICT" o/r 5 <<'EOF' >"$D/allow/twotok_out" 2>"$D/allow/twotok_err"
--reviews-pr somewhere-that-does-not-exist
EOF
rc=$?
if [ "$rc" -eq 0 ]; then ok "a two-token flag+nonexistent-path body is not refused"; else bad "a two-token flag+nonexistent-path body was wrongly refused (rc=$rc): $(cat "$D/allow/twotok_err")"; fi

# 6b. real multi-word prose that happens to start with a flag-shaped word
#     is not this shape at all (three or more tokens).
PATH="$D/allow/bin:$PATH" \
  bash "$POST_VERDICT" o/r 5 <<'EOF' >"$D/allow/prose_out" 2>"$D/allow/prose_err"
--reviews-pr is a real flag this repo's dispatch.sh accepts
EOF
rc=$?
if [ "$rc" -eq 0 ]; then ok "multi-word prose starting with a flag-shaped word is not refused"; else bad "multi-word prose starting with a flag-shaped word was wrongly refused (rc=$rc): $(cat "$D/allow/prose_err")"; fi

# 6c. a short single-dash option is out of scope on purpose.
PATH="$D/allow/bin:$PATH" \
  bash "$POST_VERDICT" o/r 5 <<'EOF' >"$D/allow/short_out" 2>"$D/allow/short_err"
-x
EOF
rc=$?
if [ "$rc" -eq 0 ]; then ok "a bare short-dash token is not refused (out of scope)"; else bad "a bare short-dash token was wrongly refused (rc=$rc): $(cat "$D/allow/short_err")"; fi

# =========================================================================
# 7. Review-Lane:/Verdict: pairing and lane resolution (agent-supervisor
#    #187/#188). "revlane:3" and "revlane:1" were registered above --
#    "revlane:1" shares its window index with the default supervisor window
#    (LANES_SUPERVISOR_WINDOW=1), reproducing the exact false-refusal shape
#    #187 measured twice (a reviewer naming the supervisor's own window).
# =========================================================================
mkdir -p "$D/lane/bin"
LANE_LOG="$D/lane/gh.log"
LANE_SENT="$D/lane/sent.body"
cat > "$D/lane/bin/gh" <<EOF
#!/bin/bash
echo "gh called: \$*" >> "$LANE_LOG"
if [ "\$1" = "pr" ] && [ "\$2" = "comment" ]; then
  cat > "$LANE_SENT"
  echo "https://github.com/o/r/pull/21#issuecomment-321"
  exit 0
elif [ "\$1" = "api" ]; then
  cat "$LANE_SENT"
  exit 0
fi
exit 9
EOF
chmod +x "$D/lane/bin/gh"

# 7a. RED: a Verdict: line with no Review-Lane: line at all is refused,
#     gh never invoked. Was exit 7 (the pairing check) before
#     agent-supervisor#595: `verdict._scan_verdict_lines` now requires a
#     complete Verdict:/Review-Lane:/Reviewed-SHA: block before `has_verdict`
#     is true at all, so `has_verdict` can never be true here while
#     `has_lane_line` is false -- the pairing check's "verdict but no lane"
#     branch is unreachable for this fixture now. The earlier write-time
#     completeness lint (section 8) catches it first, at exit 9.
: > "$LANE_LOG"
PATH="$D/lane/bin:$PATH" \
  bash "$POST_VERDICT" o/r 21 <<'EOF' >"$D/lane/noline_out" 2>"$D/lane/noline_err"
**Verdict:** APPROVE, no lane trailer at all
EOF
rc=$?
if [ "$rc" -eq 9 ]; then ok "a Verdict: line with no Review-Lane: line is refused by the completeness lint (got $rc)"; else bad "a Verdict: line with no Review-Lane: line was not refused (rc=$rc)"; fi
if [ ! -s "$LANE_LOG" ]; then ok "gh is never invoked for the missing-Review-Lane refusal"; else bad "gh was invoked despite the refusal: $(cat "$LANE_LOG")"; fi

# 7b. RED: a Review-Lane: line with no Verdict: line is refused, gh never
#     invoked -- a lane stamp attributing nothing is also a mistake.
: > "$LANE_LOG"
PATH="$D/lane/bin:$PATH" \
  bash "$POST_VERDICT" o/r 21 <<'EOF' >"$D/lane/noverdict_out" 2>"$D/lane/noverdict_err"
just a status update, no verdict here
Review-Lane: revlane:3
EOF
rc=$?
if [ "$rc" -eq 7 ]; then ok "a Review-Lane: line with no Verdict: line is refused (got $rc)"; else bad "a Review-Lane: line with no Verdict: line was not refused (rc=$rc)"; fi
if [ ! -s "$LANE_LOG" ]; then ok "gh is never invoked for the missing-Verdict refusal"; else bad "gh was invoked despite the refusal: $(cat "$LANE_LOG")"; fi

# 7c. RED: a Review-Lane: value this ledger has never registered (a task
#     slug, not a lane id -- the agent-tui#30 shape) is refused, gh never
#     invoked.
: > "$LANE_LOG"
PATH="$D/lane/bin:$PATH" \
  bash "$POST_VERDICT" o/r 21 <<EOF >"$D/lane/unknown_out" 2>"$D/lane/unknown_err"
**Verdict:** APPROVE
Review-Lane: lane:30-rev-at30
Reviewed-SHA: $REVIEWED_SHA
EOF
rc=$?
if [ "$rc" -eq 8 ]; then ok "an unregistered Review-Lane value is refused (got $rc)"; else bad "an unregistered Review-Lane value was not refused (rc=$rc)"; fi
if [ ! -s "$LANE_LOG" ]; then ok "gh is never invoked for the unresolvable-lane refusal"; else bad "gh was invoked despite the refusal: $(cat "$LANE_LOG")"; fi

# 7d. RED: a Review-Lane: value naming the supervisor's own window -- the
#     agent-supervisor#165/agent-tui#31 shape, measured twice in 24h.
: > "$LANE_LOG"
PATH="$D/lane/bin:$PATH" \
  bash "$POST_VERDICT" o/r 21 <<EOF >"$D/lane/supervisor_out" 2>"$D/lane/supervisor_err"
**Verdict:** APPROVE
Review-Lane: revlane:1
Reviewed-SHA: $REVIEWED_SHA
EOF
rc=$?
if [ "$rc" -eq 8 ]; then ok "a Review-Lane naming the supervisor's own window is refused (got $rc)"; else bad "a supervisor-window Review-Lane was not refused (rc=$rc)"; fi
if [ ! -s "$LANE_LOG" ]; then ok "gh is never invoked for the supervisor-window refusal"; else bad "gh was invoked despite the refusal: $(cat "$LANE_LOG")"; fi
if grep -qi "supervisor" "$D/lane/supervisor_err"; then ok "the supervisor-window refusal names the reason"; else bad "no supervisor-window explanation on stderr: $(cat "$D/lane/supervisor_err")"; fi

# 7e. GREEN: a real, registered, non-supervisor Review-Lane paired with a
#     Verdict: line posts normally.
: > "$LANE_LOG"
PATH="$D/lane/bin:$PATH" \
  bash "$POST_VERDICT" o/r 21 <<EOF >"$D/lane/good_out" 2>"$D/lane/good_err"
**Verdict:** APPROVE, a real registered non-supervisor lane
Review-Lane: revlane:3
Reviewed-SHA: $REVIEWED_SHA
EOF
rc=$?
if [ "$rc" -eq 0 ]; then ok "a registered, non-supervisor Review-Lane posts normally"; else bad "a valid Review-Lane was wrongly refused (rc=$rc): $(cat "$D/lane/good_err")"; fi
if grep -q "posted and verified comment 321" "$D/lane/good_out"; then ok "the valid-lane post names the verified comment id"; else bad "no verified-comment confirmation: $(cat "$D/lane/good_out")"; fi

# =========================================================================
# 8. Write-time mirror of agent-supervisor#595's read-time fix: a body
#    containing a `Verdict:`-shaped line that is not the first line of a
#    complete, unbroken Verdict:/Review-Lane:/Reviewed-SHA: block is
#    refused at exit 9, before anything is posted.
# =========================================================================
mkdir -p "$D/block/bin"
BLOCK_LOG="$D/block/gh.log"
BLOCK_SENT="$D/block/sent.body"
cat > "$D/block/bin/gh" <<EOF
#!/bin/bash
echo "gh called: \$*" >> "$BLOCK_LOG"
if [ "\$1" = "pr" ] && [ "\$2" = "comment" ]; then
  cat > "$BLOCK_SENT"
  echo "https://github.com/o/r/pull/31#issuecomment-31"
  exit 0
elif [ "\$1" = "api" ]; then
  cat "$BLOCK_SENT"
  exit 0
fi
exit 9
EOF
chmod +x "$D/block/bin/gh"

# 8a. RED: a Verdict: label immediately followed by Review-Lane: but with no
#     Reviewed-SHA: line after it -- two of the three lines, not a complete
#     block. This body's has_verdict/has_lane_line pair (false/true) still
#     also trips the older pairing check (exit 7) FIRST, since that check
#     runs before this lint -- named here, not asserted as exit 9, so this
#     suite documents the priority rather than silently depending on it.
: > "$BLOCK_LOG"
PATH="$D/block/bin:$PATH" \
  bash "$POST_VERDICT" o/r 31 <<'EOF' >"$D/block/twoline_out" 2>"$D/block/twoline_err"
**Verdict:** APPROVE
Review-Lane: revlane:3
EOF
rc=$?
if [ "$rc" -ne 0 ]; then ok "a Verdict:/Review-Lane: pair with no Reviewed-SHA: is refused (got $rc)"; else bad "a two-line-only trailer was not refused"; fi
if [ ! -s "$BLOCK_LOG" ]; then ok "gh is never invoked for the incomplete-block refusal"; else bad "gh was invoked despite the refusal: $(cat "$BLOCK_LOG")"; fi

# 8b. RED: a bare `Verdict:`-shaped label with nothing following it at all
#     (no Review-Lane:, no Reviewed-SHA:) -- section 7a's original fixture,
#     now caught here instead of by the pairing check (see 7a's comment).
: > "$BLOCK_LOG"
PATH="$D/block/bin:$PATH" \
  bash "$POST_VERDICT" o/r 31 <<'EOF' >"$D/block/bare_out" 2>"$D/block/bare_err"
**Verdict:** APPROVE, no trailer at all
EOF
rc=$?
if [ "$rc" -eq 9 ]; then ok "a bare Verdict: label with nothing following is refused by the completeness lint (got $rc)"; else bad "a bare Verdict: label was not refused at exit 9 (rc=$rc)"; fi
if [ ! -s "$BLOCK_LOG" ]; then ok "gh is never invoked for the bare-label refusal"; else bad "gh was invoked despite the refusal: $(cat "$BLOCK_LOG")"; fi
if grep -q "not the first line of a complete" "$D/block/bare_err"; then ok "the bare-label refusal names the reason"; else bad "no completeness-lint explanation on stderr: $(cat "$D/block/bare_err")"; fi

# 8c. RED: the agent-supervisor#553 poisoning shape -- ordinary prose that
#     happens to contain the substring "verdict:" mid-sentence, with nothing
#     resembling a trailer anywhere in the body. Proves the write-time lint
#     catches the exact incident #595 was filed over, not just a synthetic
#     two/three-line shape.
: > "$BLOCK_LOG"
PATH="$D/block/bin:$PATH" \
  bash "$POST_VERDICT" o/r 31 <<'EOF' >"$D/block/poison_out" 2>"$D/block/poison_err"
A stale `Reviewed-SHA` does not automatically sink a verdict: `verdict.py` can promote
EOF
rc=$?
if [ "$rc" -eq 9 ]; then ok "the #553 mid-sentence poisoning shape is refused by the completeness lint (got $rc)"; else bad "the #553 poisoning shape was not refused at exit 9 (rc=$rc)"; fi
if [ ! -s "$BLOCK_LOG" ]; then ok "gh is never invoked for the #553-shape refusal"; else bad "gh was invoked despite the refusal: $(cat "$BLOCK_LOG")"; fi

# 8d. GREEN: a complete three-line block posts normally -- the lint does not
#     refuse a genuinely well-formed trailer.
: > "$BLOCK_LOG"
PATH="$D/block/bin:$PATH" \
  bash "$POST_VERDICT" o/r 31 <<EOF >"$D/block/good_out" 2>"$D/block/good_err"
**Verdict:** APPROVE, a complete trailer block
Review-Lane: revlane:3
Reviewed-SHA: $REVIEWED_SHA
EOF
rc=$?
if [ "$rc" -eq 0 ]; then ok "a complete Verdict:/Review-Lane:/Reviewed-SHA: block is not refused by the completeness lint"; else bad "a complete trailer block was wrongly refused (rc=$rc): $(cat "$D/block/good_err")"; fi

# =========================================================================
# 9. --expect-verdict (agent-estate#719 item 1): a caller that states its own
#    intent -- this post must carry a verdict -- gets a refusal naming the
#    exact missing shape when the body has no trailer at all, and callers
#    that do NOT pass the flag keep today's behaviour unchanged (an ordinary
#    comment with no trailer still posts).
# =========================================================================
mkdir -p "$D/expect/bin"
EXPECT_LOG="$D/expect/gh.log"
EXPECT_SENT="$D/expect/sent.body"
cat > "$D/expect/bin/gh" <<EOF
#!/bin/bash
echo "gh called: \$*" >> "$EXPECT_LOG"
if [ "\$1" = "pr" ] && [ "\$2" = "comment" ]; then
  cat > "$EXPECT_SENT"
  echo "https://github.com/o/r/pull/41#issuecomment-41"
  exit 0
elif [ "\$1" = "api" ]; then
  cat "$EXPECT_SENT"
  exit 0
fi
exit 9
EOF
chmod +x "$D/expect/bin/gh"

# 9a. RED: the exact agent-estate#719 item-1 shape -- a review that ends in
#     prose with no trailer at all -- is refused at exit 10 when the caller
#     passes --expect-verdict, and gh is never invoked.
: > "$EXPECT_LOG"
PATH="$D/expect/bin:$PATH" \
  bash "$POST_VERDICT" o/r 41 --expect-verdict <<'EOF' >"$D/expect/noverdict_out" 2>"$D/expect/noverdict_err"
This PR looks correct to me. The patch-id comparison handles the rebase
case properly and the test coverage is thorough. Recommend APPROVE.
EOF
rc=$?
if [ "$rc" -eq 10 ]; then ok "--expect-verdict refuses a trailer-less prose ending (got $rc)"; else bad "--expect-verdict did not refuse a trailer-less body (rc=$rc)"; fi
if [ ! -s "$EXPECT_LOG" ]; then ok "gh is never invoked for the --expect-verdict refusal"; else bad "gh was invoked despite the refusal: $(cat "$EXPECT_LOG")"; fi
if grep -q "no Verdict:/Review-Lane:/Reviewed-SHA: trailer at all" "$D/expect/noverdict_err"; then
  ok "the --expect-verdict refusal names what is missing"
else
  bad "no explanation of the missing trailer: $(cat "$D/expect/noverdict_err")"
fi

# 9b. GREEN (the other direction of the mutation): the SAME trailer-less
#     prose body, WITHOUT --expect-verdict, still posts exactly as it always
#     has -- this flag must not make post-verdict.sh more restrictive for a
#     caller that never opted in.
: > "$EXPECT_LOG"
PATH="$D/expect/bin:$PATH" \
  bash "$POST_VERDICT" o/r 41 <<'EOF' >"$D/expect/noflag_out" 2>"$D/expect/noflag_err"
This PR looks correct to me. The patch-id comparison handles the rebase
case properly and the test coverage is thorough. Recommend APPROVE.
EOF
rc=$?
if [ "$rc" -eq 0 ]; then ok "the same trailer-less body without --expect-verdict still posts (unchanged behaviour)"; else bad "omitting --expect-verdict changed existing behaviour (rc=$rc): $(cat "$D/expect/noflag_err")"; fi

# 9c. GREEN: --expect-verdict with a genuine, complete trailer block posts
#     normally -- the flag never blocks a real verdict.
: > "$EXPECT_LOG"
PATH="$D/expect/bin:$PATH" \
  bash "$POST_VERDICT" o/r 41 --expect-verdict <<EOF >"$D/expect/good_out" 2>"$D/expect/good_err"
**Verdict:** APPROVE, a complete trailer block
Review-Lane: revlane:3
Reviewed-SHA: $REVIEWED_SHA
EOF
rc=$?
if [ "$rc" -eq 0 ]; then ok "--expect-verdict with a genuine trailer block posts normally"; else bad "--expect-verdict wrongly refused a genuine trailer (rc=$rc): $(cat "$D/expect/good_err")"; fi

echo
echo "post-verdict.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
