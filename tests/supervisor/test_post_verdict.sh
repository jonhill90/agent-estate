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
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
POST_VERDICT="$HERE/../../scripts/supervisor/post-verdict.sh"
pass=0; fail=0
ok() { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1"; fail=$((fail+1)); }

echo "post-verdict.sh"

D=$(mktemp -d)

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
  bash "$POST_VERDICT" o/r 9 <<'EOF' >"$D/mismatch/out" 2>"$D/mismatch/err"
**Verdict:** APPROVE, real findings, none of which is a file path
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
  bash "$POST_VERDICT" o/r 11 <<'EOF' >"$D/happy/out" 2>"$D/happy/err"
**Verdict:** APPROVE -- read back matches what was sent
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
  bash "$POST_VERDICT" o/r 3 <<'EOF' >"$D/ghfail/out" 2>"$D/ghfail/err"
**Verdict:** APPROVE
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

echo
echo "post-verdict.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
