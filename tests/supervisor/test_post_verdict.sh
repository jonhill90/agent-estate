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
  cat "$SENT_LOG"
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

echo
echo "post-verdict.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
