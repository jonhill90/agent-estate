#!/bin/bash
# agent-supervisor#468: an issue_comment-triggered run of ui-evidence.yml
# used to publish its result against github.sha, which for that event
# resolves to the default branch tip -- never the PR's actual head --
# regardless of what actions/checkout fetched inside the job (measured on
# #467: two comment-triggered runs both reported headSha ed779d7e, the
# main tip, while the PR's head was 8d3a51b9). ui-evidence-report.sh is
# the fix: it resolves the PR's head SHA itself and explicitly publishes a
# check-run against it via the Checks API, instead of trusting the run's
# own (wrong) association.
#
# This pins the one property that matters: the SHA a comment-triggered
# report posts against must be the PR's real head, never anything else --
# with a stubbed `gh` and `ui-evidence-gate.sh`, no network. The mutation
# check at the bottom proves this suite can actually fail: it reintroduces
# the #468 bug (posting against a fixed/default-branch SHA instead of the
# resolved PR head) and confirms the assertion goes red, then restores and
# shows green.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPORT="$HERE/../../scripts/supervisor/ui-evidence-report.sh"

pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

echo "ui-evidence-report.sh"

D=$(mktemp -d); mkdir -p "$D/bin"

# Stub gate: a trivial stand-in for ui-evidence-gate.sh, scripted purely by
# env var so each case controls whether the "gate" passes or fails without
# needing a real UI-path diff.
cat > "$D/bin/fake-gate.sh" <<'STUB'
#!/bin/bash
echo "fake-gate: PR=$1 rc=${GH_STUB_GATE_RC:-0}"
exit "${GH_STUB_GATE_RC:-0}"
STUB
chmod +x "$D/bin/fake-gate.sh"

# Stub gh: records every `api .../check-runs` call to $D/posted so the
# test can assert exactly what SHA and conclusion were published, without
# touching the network.
#   GH_STUB_HEAD_SHA     what `gh pr view --json headRefOid` returns
#   GH_STUB_PR_VIEW_FAIL set to 1 to make the head-SHA lookup fail
#   GH_STUB_API_FAIL     set to 1 to make the check-runs POST fail
cat > "$D/bin/gh" <<'STUB'
#!/bin/bash
case "$1 $2" in
  "pr view")
    if [ "${GH_STUB_PR_VIEW_FAIL:-0}" = "1" ]; then
      echo "gh stub: pr view failed (forced)" >&2
      exit 1
    fi
    printf '%s' "$GH_STUB_HEAD_SHA"
    ;;
  "api repos/owner/repo/check-runs")
    if [ "${GH_STUB_API_FAIL:-0}" = "1" ]; then
      echo "gh stub: check-runs POST failed (forced)" >&2
      exit 1
    fi
    printf '%s\n' "$*" >> "$POSTED_FILE"
    echo '{"id":1}'
    ;;
  *)
    echo "gh stub: unrecognized args: $*" >&2; exit 2
    ;;
esac
STUB
chmod +x "$D/bin/gh"
export PATH="$D/bin:$PATH"

run_report() {
  POSTED_FILE="$D/posted"; rm -f "$POSTED_FILE"
  export POSTED_FILE
  GH_STUB_HEAD_SHA="$1" GH_STUB_GATE_RC="$2" \
    UI_EVIDENCE_GATE_BIN="$D/bin/fake-gate.sh" \
    UI_EVIDENCE_REPO="owner/repo" \
    bash "$REPORT" 42
}

TRUE_HEAD="8d3a51b9deadbeef8d3a51b9deadbeef8d3a51b9"
MAIN_TIP="ed779d7e60bc8dd36c84354d4aec3819c2ff2785"

# --- case 0: no PR number -> refuse -------------------------------------
out="$(bash "$REPORT" 2>&1)"; rc=$?
if [ "$rc" -eq 2 ] && grep -q 'usage:' <<<"$out"; then
  ok "refuses without a PR number"
else
  bad "refuses without a PR number" "rc=$rc out=$out"
fi

# --- case 1: gate passes -> publishes success against the PR's real head, not any other SHA
out="$(run_report "$TRUE_HEAD" 0 2>&1)"; rc=$?
posted="$(cat "$D/posted" 2>/dev/null || true)"
if [ "$rc" -eq 0 ] && grep -qF "head_sha=$TRUE_HEAD" <<<"$posted" \
   && grep -qF "conclusion=success" <<<"$posted" \
   && ! grep -qF "head_sha=$MAIN_TIP" <<<"$posted"; then
  ok "gate pass publishes success against the PR's real head SHA (never the default branch tip)"
else
  bad "gate pass publishes success against the PR's real head SHA" "rc=$rc posted=$posted"
fi

# --- case 2: gate fails -> publishes failure against the PR's real head -
out="$(run_report "$TRUE_HEAD" 1 2>&1)"; rc=$?
posted="$(cat "$D/posted" 2>/dev/null || true)"
if [ "$rc" -eq 1 ] && grep -qF "head_sha=$TRUE_HEAD" <<<"$posted" \
   && grep -qF "conclusion=failure" <<<"$posted"; then
  ok "gate fail publishes failure against the PR's real head SHA"
else
  bad "gate fail publishes failure against the PR's real head SHA" "rc=$rc posted=$posted"
fi

# --- case 3: gate errors (rc=2, e.g. its own config error) -> never green
out="$(run_report "$TRUE_HEAD" 2 2>&1)"; rc=$?
posted="$(cat "$D/posted" 2>/dev/null || true)"
if [ "$rc" -eq 2 ] && grep -qF "conclusion=failure" <<<"$posted"; then
  ok "gate error (rc=2) is reported as failure, never success"
else
  bad "gate error (rc=2) is reported as failure, never success" "rc=$rc posted=$posted"
fi

# --- case 4: can't resolve head SHA -> refuse, publish nothing ---------
GH_STUB_PR_VIEW_FAIL=1 out="$(POSTED_FILE="$D/posted"; rm -f "$POSTED_FILE"; export POSTED_FILE GH_STUB_PR_VIEW_FAIL; UI_EVIDENCE_GATE_BIN="$D/bin/fake-gate.sh" UI_EVIDENCE_REPO="owner/repo" bash "$REPORT" 42 2>&1)"
rc=$?
if [ "$rc" -eq 2 ] && [ ! -s "$D/posted" ]; then
  ok "cannot resolve PR head SHA -> refuses, publishes nothing"
else
  bad "cannot resolve PR head SHA -> refuses, publishes nothing" "rc=$rc out=$out"
fi

# --- case 5: check-runs POST itself fails -> refuse, never claim success
GH_STUB_API_FAIL=1 out="$(POSTED_FILE="$D/posted"; rm -f "$POSTED_FILE"; export POSTED_FILE GH_STUB_API_FAIL; GH_STUB_HEAD_SHA="$TRUE_HEAD" GH_STUB_GATE_RC=0 UI_EVIDENCE_GATE_BIN="$D/bin/fake-gate.sh" UI_EVIDENCE_REPO="owner/repo" bash "$REPORT" 42 2>&1)"
rc=$?
if [ "$rc" -eq 2 ]; then
  ok "check-runs POST failure is refused, not silently swallowed"
else
  bad "check-runs POST failure is refused" "rc=$rc out=$out"
fi

# --- mutation check: prove this suite can actually catch the #468 bug ---
# Reintroduce the exact defect: publish against a fixed/default-branch SHA
# instead of the PR head resolved from `gh pr view`. Confirm case 1's
# assertion (posted SHA == PR head) goes RED under the mutation, then
# restore and show it GREEN again.
BROKEN=$(mktemp)
sed "s/head_sha=\$head_sha/head_sha=$MAIN_TIP/" "$REPORT" > "$BROKEN"
if ! grep -qF "head_sha=$MAIN_TIP" "$BROKEN"; then
  bad "mutation check: sed actually rewrote the script" "pattern not found in $BROKEN"
else
  POSTED_FILE="$D/posted"; rm -f "$POSTED_FILE"; export POSTED_FILE
  out_broken="$(GH_STUB_HEAD_SHA="$TRUE_HEAD" GH_STUB_GATE_RC=0 \
    UI_EVIDENCE_GATE_BIN="$D/bin/fake-gate.sh" UI_EVIDENCE_REPO="owner/repo" \
    bash "$BROKEN" 42 2>&1)"
  posted_broken="$(cat "$D/posted" 2>/dev/null || true)"
  if grep -qF "head_sha=$TRUE_HEAD" <<<"$posted_broken"; then
    bad "mutation check: reintroducing the wrong-SHA bug flips the assertion to red" \
      "posted the PR head even with the bug reintroduced -- mutation had no effect: $posted_broken"
  else
    ok "mutation check RED: with the #468 bug reintroduced, the posted SHA is no longer the PR head ($posted_broken)"
  fi
fi
rm -f "$BROKEN"

# Restore: unmodified script again publishes against the true PR head.
POSTED_FILE="$D/posted"; rm -f "$POSTED_FILE"; export POSTED_FILE
out_fixed="$(GH_STUB_HEAD_SHA="$TRUE_HEAD" GH_STUB_GATE_RC=0 \
  UI_EVIDENCE_GATE_BIN="$D/bin/fake-gate.sh" UI_EVIDENCE_REPO="owner/repo" \
  bash "$REPORT" 42 2>&1)"
posted_fixed="$(cat "$D/posted" 2>/dev/null || true)"
if grep -qF "head_sha=$TRUE_HEAD" <<<"$posted_fixed"; then
  ok "mutation check GREEN: unmodified script publishes against the true PR head again"
else
  bad "mutation check GREEN: unmodified script publishes against the true PR head" "posted=$posted_fixed"
fi

rm -rf "$D"
echo "ui-evidence-report.sh: $pass ok, $fail failed"
[ "$fail" -eq 0 ]
