#!/bin/bash
# digest.sh replaces ~26 subprocess round-trips the Director made every tick.
# Because a reader trusts it INSTEAD of looking, its failure modes matter more
# than its happy path: a section it could not read must say so, and an
# unreachable GitHub must never look like "no open PRs".
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIGEST="$HERE/../../scripts/supervisor/digest.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }
chk() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "want '$2', got '$3'"; fi; }

command -v jq >/dev/null 2>&1 || { echo "  SKIP no jq"; exit 0; }

D=$(mktemp -d); mkdir -p "$D/bin" "$D/state"
trap 'rm -rf "$D"' EXIT INT TERM

# A gh that always fails, to prove an unreachable GitHub is not silence.
printf '#!/bin/bash\nexit 1\n' > "$D/bin/gh"; chmod +x "$D/bin/gh"
cat > "$D/state/watchdog.status" <<'S'
checked:  2026-08-12T00:00:00Z
state:    asleep
restarts: 0 in the last 3600s
S
cat > "$D/state/inbox-poll.status" <<'S'
checked: 2026-08-12T00:00:00Z
state:   ok
S

run() { PATH="$D/bin:$PATH" SUPERVISOR_STATE="$D/state" LANES_SESSION=nosuch bash "$DIGEST" "$@" 2>/dev/null; }

# 1. THE ONE THAT MATTERS: gh unreachable must not read as "no PRs".
out=$(run)
grep -q "this digest is INCOMPLETE" <<<"$out" && ok "gh failure is announced, not silent" \
  || bad "gh failure is announced" "$out"
grep -q "gh pr list failed" <<<"$out" && ok "the failing repo is named" || bad "failing repo named" "$out"

# 2. Exit code distinguishes complete from partial.
run >/dev/null 2>&1; chk "partial digest exits 1" "1" "$?"

# 3. --json stays valid JSON under failure, and says so in-band.
j=$(run --json)
jq -e . >/dev/null 2>&1 <<<"$j" && ok "--json is valid JSON when things fail" || bad "--json valid under failure" "$j"
chk "ok=false under failure" "false" "$(jq -r '.ok' <<<"$j")"
[ "$(jq -r '.errors|length' <<<"$j")" -gt 0 ] && ok "errors[] is populated" || bad "errors populated" "$j"

# 4. An unreadable watchdog.status is named, not defaulted.
out=$(PATH="$D/bin:$PATH" SUPERVISOR_STATE="$D/none" LANES_SESSION=nosuch bash "$DIGEST" 2>/dev/null)
grep -q "watchdog.status unreadable" <<<"$out" && ok "unreadable watchdog.status is named" \
  || bad "unreadable watchdog named" "$out"
grep -q "UNREADABLE" <<<"$out" && ok "watchdog state reads UNREADABLE, not a guess" \
  || bad "watchdog UNREADABLE" "$out"

# 5. A missing lanes session is reported rather than rendering as "no lanes".
grep -q "lanes.sh returned nothing" <<<"$(run)" && ok "empty lanes.sh is reported" \
  || bad "empty lanes reported" "$(run)"

# 6. A status-file value containing its own colon is not truncated. Reproduced
# live against watchdog.status before the fix: `checked:  2026-08-12T03:10:31Z`
# read back as `2026-08-12T03`.
chk "colon-bearing status value is not truncated" \
  "2026-08-12T00:00:00Z" "$(jq -r '.watchdog.checked' <<<"$(run --json)")"

# 7. lanes.sh exiting 0 with only its header row (a real, narrow tmux hiccup
# shape, not a fully empty result) is reported, not read as a clean idle estate.
cat > "$D/bin/lanes-header-only.sh" <<'S'
#!/bin/bash
printf 'WINDOW\tNAME\tCOMMAND\tSTATE\n'
S
chmod +x "$D/bin/lanes-header-only.sh"
out=$(PATH="$D/bin:$PATH" SUPERVISOR_STATE="$D/state" LANES_SESSION=nosuch \
  DIGEST_LANES_BIN="$D/bin/lanes-header-only.sh" bash "$DIGEST" 2>/dev/null)
grep -q "no lane rows" <<<"$out" && ok "header-only lanes.sh is reported, not read as idle" \
  || bad "header-only lanes.sh reported" "$out"

# 8. jq missing is named, not silently empty. Reproduced before the fix:
# `--json` with jq removed from PATH printed nothing at all to stdout.
#
# HOW THE JQ-FREE ENVIRONMENT IS BUILT MATTERS, and getting it wrong is what
# turned this test red on CI while it stayed green on macOS. The first version
# built the PATH by DROPPING every directory that contains a jq. That is a
# machine-shaped assumption: on macOS jq is a homebrew binary in its own
# prefix, so dropping its directory costs nothing, but on ubuntu jq is
# /usr/bin/jq -- the same directory as bash, awk and every coreutil, with /bin
# a symlink to it. Dropping it removed the harness's own interpreter, so
# `PATH=... bash "$DIGEST"` exited 127, command-not-found, with digest.sh never
# entered at all. CI then reported "want '1', got '127'", which reads exactly
# like the unguarded-jq defect this test exists to catch, from a script that
# already guards it correctly.
#
# So: construct a bindir that HAS what the script needs and simply lacks jq,
# and invoke bash by absolute path resolved before PATH is touched, so the
# harness can never lose its own interpreter no matter where jq lives.
BASH_BIN="$(command -v bash)"
NOJQ_BIN="$D/nojq"; mkdir -p "$NOJQ_BIN"
for t in dirname date awk pgrep paste wc tr sed; do
  p="$(command -v "$t")" && ln -sf "$p" "$NOJQ_BIN/$t"
done
cp "$D/bin/gh" "$NOJQ_BIN/gh"
# Guard the guard: if jq were still reachable on that PATH, digest.sh would
# just work and all three assertions below would pass for the wrong reason.
# The lookup has to happen in a FRESH process, the way digest.sh's own will:
# `PATH=... command -v jq` in this shell can answer from bash's hash table,
# which already holds the real jq from the assertions above -- it reported a
# leak that the child process does not actually see.
if PATH="$NOJQ_BIN" "$BASH_BIN" -c 'command -v jq' >/dev/null 2>&1; then
  bad "the no-jq PATH really has no jq" "found $(PATH="$NOJQ_BIN" "$BASH_BIN" -c 'command -v jq')"
else
  ok "the no-jq PATH really has no jq"
fi
nojq() { PATH="$NOJQ_BIN" SUPERVISOR_STATE="$D/state" LANES_SESSION=nosuch "$BASH_BIN" "$DIGEST" "$@" 2>/dev/null; }
out=$(nojq)
rc=$?
grep -q "jq is required" <<<"$out" && ok "missing jq is named, not silent" \
  || bad "missing jq named" "$out"
chk "missing jq exits 1" "1" "$rc"
jout=$(nojq --json)
[ -n "$jout" ] && ok "missing jq --json is not a zero-byte payload" || bad "missing jq --json non-empty" "$jout"
jq -e . >/dev/null 2>&1 <<<"$jout" && ok "missing jq --json is still valid JSON" \
  || bad "missing jq --json valid" "$jout"

# 9-12. THE BLOCKING FINDING (agent-dotfiles#192): the per-PR jq assembly
# (repo, number, title, head, run_sha, run_conclusion, ci_is_current,
# merge_state, verdict) never executed under test, because the stub gh in
# every test above fails unconditionally and `gh pr list` always takes the
# `continue` branch. A gh with a SUCCESS mode, so that block actually runs --
# and assertions on ci_is_current/merge_state across states that DISAGREE
# with each other, so hardcoding either field (the exact mutations the
# review applied) cannot pass all of them at once.
OK=$(mktemp -d); mkdir -p "$OK/bin" "$OK/fixtures"
cat > "$OK/bin/gh" <<'S'
#!/bin/bash
# Success-mode gh stub: serves canned PR/run JSON from $GH_STUB_FIXTURES so
# the per-PR assembly block actually executes under test.
FIX="${GH_STUB_FIXTURES:?}"
case "$1 $2" in
  "pr list")
    cat "$FIX/pr_list.json" ;;
  "run list")
    branch=""; prev=""
    for a in "$@"; do
      [ "$prev" = "--branch" ] && branch="$a"
      prev="$a"
    done
    f="$FIX/run_${branch}.json"
    [ -f "$f" ] && cat "$f" || echo "[]"
    ;;
  "pr view")
    num="$3"
    f="$FIX/reviews_${num}.json"
    [ -f "$f" ] && cat "$f" || echo '{"reviews":[]}'
    ;;
  "api "*)
    # verdict.py's #226 rebase-content comparison makes two shapes of call:
    #   gh api repos/O/R/compare/BASE...HEAD --jq .commits[].sha   (commit list)
    #   gh api -H "Accept: ...v3.diff" repos/O/R/commits/SHA       (one patch)
    if [ "$2" = "-H" ]; then path="$4"; else path="$2"; fi
    case "$path" in
      */compare/*)
        spec="${path##*/compare/}"
        base="${spec%%...*}"
        head="${spec#*...}"
        # agent-dotfiles#229: a compare anchored on the OTHER HEAD rather than
        # on the PR's base branch resolves both sides to the pre-rebase merge
        # base, so the "new" side carries everything main gained. That is the
        # defect. This stub answers only a base-branch-anchored compare -- if
        # the implementation regresses to the symmetric form, every promotion
        # assertion below goes red instead of quietly passing on a fixture
        # that encodes the bug.
        [ "$base" = "main" ] || exit 1
        f="$FIX/branch_${head}.txt"
        [ -f "$f" ] && cat "$f" || exit 1
        ;;
      */commits/*)
        sha="${path##*/commits/}"
        f="$FIX/patch_${sha}.diff"
        [ -f "$f" ] && cat "$f" || exit 1
        ;;
      *) exit 1 ;;
    esac
    ;;
  *) exit 1 ;;
esac
S
chmod +x "$OK/bin/gh"

cat > "$OK/fixtures/pr_list.json" <<'S'
[
  {"number":1,"title":"current head, clean merge","headRefOid":"aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111","headRefName":"b1","mergeStateStatus":"CLEAN"},
  {"number":2,"title":"stale pass, dirty merge","headRefOid":"bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222","headRefName":"b2","mergeStateStatus":"DIRTY"},
  {"number":3,"title":"no CI run at all, behind","headRefOid":"cccc3333cccc3333cccc3333cccc3333cccc3333","headRefName":"b3","mergeStateStatus":"BEHIND"},
  {"number":4,"title":"current head, CI failed","headRefOid":"dddd4444dddd4444dddd4444dddd4444dddd4444","headRefName":"b4","mergeStateStatus":"CLEAN"},
  {"number":6,"title":"review filed against a since-superseded head","headRefOid":"newnewnewnewnewnewnewnewnewnewnewnewnewn","headRefName":"b6","mergeStateStatus":"CLEAN"},
  {"number":7,"title":"pure rebase since review, content unchanged","headRefOid":"reb7reb7reb7reb7reb7reb7reb7reb7reb7reb7","headRefName":"b7","mergeStateStatus":"CLEAN"},
  {"number":8,"title":"real content change since review","headRefOid":"chg8chg8chg8chg8chg8chg8chg8chg8chg8chg8","headRefName":"b8","mergeStateStatus":"CLEAN"},
  {"number":9,"title":"rebase that dropped a commit main superseded","headRefOid":"reb9reb9reb9reb9reb9reb9reb9reb9reb9reb9","headRefName":"b9","mergeStateStatus":"CLEAN"},
  {"number":10,"title":"a commit pushed on top after the review","headRefOid":"add0add0add0add0add0add0add0add0add0add0","headRefName":"b10","mergeStateStatus":"CLEAN"}
]
S
cat > "$OK/fixtures/run_b1.json" <<'S'
[{"headSha":"aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111","conclusion":"success"}]
S
cat > "$OK/fixtures/run_b2.json" <<'S'
[{"headSha":"oldoldoldoldoldoldoldoldoldoldoldoldoldo","conclusion":"success"}]
S
cat > "$OK/fixtures/run_b4.json" <<'S'
[{"headSha":"dddd4444dddd4444dddd4444dddd4444dddd4444","conclusion":"failure"}]
S
# b3 deliberately has no run_b3.json -- stub falls back to "[]", i.e. no run.
# b6 deliberately has no run_b6.json either -- CI freshness is not this PR's point.

# PR6's review was APPROVED, but at the OLD head -- a push since moved
# headRefOid to "newnewnew...". This is agent-dotfiles#218: a review filed
# against SHA A must not answer for a PR now at SHA B. Approved-but-stale is
# the exact failure the second GitHub identity in #203 would make reachable.
cat > "$OK/fixtures/reviews_6.json" <<'S'
{"baseRefName":"main","reviews":[
  {"state":"APPROVED","body":"Looks good.","commit":{"oid":"oldoldoldoldoldoldoldoldoldoldoldoldoldo"}}
]}
S

# PR4's review carries the exact prose that used to invert the old
# regex-on-comment-prose verdict (agent-dotfiles#203 correction): a
# COMMENTED review whose body says "I cannot approve this, it is unsafe."
# The real GitHub *state* is CHANGES_REQUESTED from a second review below.
# The adapter must read the STATE field only -- if it regressed to reading
# comment bodies again, this body would flip verdict to "approved".
# The CHANGES_REQUESTED review's commit.oid matches PR4's headRefOid above --
# this fixture exercises the STATE-vs-prose regression (#203), not the
# SHA-staleness one (#218); that one has its own fixture (PR6) below.
cat > "$OK/fixtures/reviews_4.json" <<'S'
{"reviews":[
  {"state":"COMMENTED","body":"I cannot approve this, it is unsafe."},
  {"state":"CHANGES_REQUESTED","body":"Rejected: the mutation-check never went red","commit":{"oid":"dddd4444dddd4444dddd4444dddd4444dddd4444"}}
]}
S

# agent-dotfiles#226/#229 MUTATION CHECK, both directions, driven end to end
# through digest.sh + verdict.py, not just verdict.py's own unit tests.
#
# The fixtures are the shape the REAL API returns, which is what #229's
# review found the first attempt was not: each head's commit list is what
# that head introduces over `main`, so a rebase onto a moved `main` leaves
# the branch's own commits and nothing else. Writing a `patch_<sha>.diff`
# for a commit is what puts it on a branch; two commits carry the same
# patch when their diffs differ only in hunk offset.
#
# One helper, so a fixture pair cannot drift apart by hand.
mkpatch() {  # $1 = commit sha, $2 = file marker, $3 = hunk offset, $4 = new line
  cat > "$OK/fixtures/patch_$1.diff" <<S
diff --git a/$2.txt b/$2.txt
index 1111111..2222222 100644
--- a/$2.txt
+++ b/$2.txt
@@ -$3,3 +$3,3 @@
 line before
-old line
+$4
 line after
S
}

# PR7, direction 1: APPROVED at "old7...", then rebased onto a main that
# MOVED -- headRefOid is now "reb7...". Both commits on the branch kept
# their content and got new SHAs and new hunk offsets. This must NOT read
# stale: verdict stays "approved", with the basis named.
cat > "$OK/fixtures/reviews_7.json" <<'S'
{"baseRefName":"main","reviews":[
  {"state":"APPROVED","body":"Looks good.","commit":{"oid":"old7old7old7old7old7old7old7old7old7old7"}}
]}
S
printf '%s\n' c7a_old old7old7old7old7old7old7old7old7old7old7 \
  > "$OK/fixtures/branch_old7old7old7old7old7old7old7old7old7old7.txt"
printf '%s\n' c7a_new reb7reb7reb7reb7reb7reb7reb7reb7reb7reb7 \
  > "$OK/fixtures/branch_reb7reb7reb7reb7reb7reb7reb7reb7reb7reb7.txt"
mkpatch c7a_old first 10 "new line"
mkpatch c7a_new first 310 "new line"
mkpatch old7old7old7old7old7old7old7old7old7old7 second 20 "new line"
mkpatch reb7reb7reb7reb7reb7reb7reb7reb7reb7reb7 second 420 "new line"

# PR8, direction 2: APPROVED at "old8...", then the head moved to "chg8..."
# with the reviewed line genuinely changed further, not just rebased. This
# MUST still read "unknown" -- the regression #219/#218 exist to prevent,
# and #226 must not weaken.
cat > "$OK/fixtures/reviews_8.json" <<'S'
{"baseRefName":"main","reviews":[
  {"state":"APPROVED","body":"Looks good.","commit":{"oid":"old8old8old8old8old8old8old8old8old8old8"}}
]}
S
printf '%s\n' old8old8old8old8old8old8old8old8old8old8 \
  > "$OK/fixtures/branch_old8old8old8old8old8old8old8old8old8old8.txt"
printf '%s\n' chg8chg8chg8chg8chg8chg8chg8chg8chg8chg8 \
  > "$OK/fixtures/branch_chg8chg8chg8chg8chg8chg8chg8chg8chg8chg8.txt"
mkpatch old8old8old8old8old8old8old8old8old8old8 only 10 "new line"
mkpatch chg8chg8chg8chg8chg8chg8chg8chg8chg8chg8 only 420 "a genuinely different line"

# PR9: the shape agent-dotfiles#226's OWN example has, measured 2026-08-12 --
# the rebase 0538cc6 -> 69784bd dropped a known_references.json refresh
# because upstream #210 replaced that file with .txt. Nothing unreviewed
# entered, so the verdict is promoted, and the detail states how many
# reviewed patches are no longer present.
cat > "$OK/fixtures/reviews_9.json" <<'S'
{"baseRefName":"main","reviews":[
  {"state":"APPROVED","body":"Looks good.","commit":{"oid":"old9old9old9old9old9old9old9old9old9old9"}}
]}
S
printf '%s\n' c9a_old c9sup old9old9old9old9old9old9old9old9old9old9 \
  > "$OK/fixtures/branch_old9old9old9old9old9old9old9old9old9old9.txt"
printf '%s\n' c9a_new reb9reb9reb9reb9reb9reb9reb9reb9reb9reb9 \
  > "$OK/fixtures/branch_reb9reb9reb9reb9reb9reb9reb9reb9reb9reb9.txt"
mkpatch c9a_old first 10 "new line"
mkpatch c9a_new first 310 "new line"
mkpatch c9sup superseded 10 "new line"
mkpatch old9old9old9old9old9old9old9old9old9old9 second 20 "new line"
mkpatch reb9reb9reb9reb9reb9reb9reb9reb9reb9reb9 second 420 "new line"

# PR10: the other way unreviewed content arrives -- not an amend, an extra
# commit pushed on top. Every reviewed patch is still there, so a subset
# test applied in the WRONG direction would promote this. It must read
# "unknown".
cat > "$OK/fixtures/reviews_10.json" <<'S'
{"baseRefName":"main","reviews":[
  {"state":"APPROVED","body":"Looks good.","commit":{"oid":"old0old0old0old0old0old0old0old0old0old0"}}
]}
S
printf '%s\n' old0old0old0old0old0old0old0old0old0old0 \
  > "$OK/fixtures/branch_old0old0old0old0old0old0old0old0old0old0.txt"
printf '%s\n' old0old0old0old0old0old0old0old0old0old0 add0add0add0add0add0add0add0add0add0add0 \
  > "$OK/fixtures/branch_add0add0add0add0add0add0add0add0add0add0.txt"
mkpatch old0old0old0old0old0old0old0old0old0old0 only 10 "new line"
mkpatch add0add0add0add0add0add0add0add0add0add0 extra 10 "new line"

run_ok() {
  PATH="$OK/bin:$PATH" SUPERVISOR_STATE="$D/state" LANES_SESSION=nosuch \
    DIGEST_REPOS=test-repo DIGEST_OWNER=ownerx GH_STUB_FIXTURES="$OK/fixtures" \
    DIGEST_VERDICT_SOURCE=github \
    bash "$DIGEST" --json 2>/dev/null
}
# The SAME fixtures through the TEXT renderer. Everything above asserts on
# --json only, which is how agent-dotfiles#229's blocking finding survived
# review: the promotion was computed, stored in verdict_detail, asserted in
# JSON -- and then dropped by the one branch a human actually reads.
run_ok_text() {
  PATH="$OK/bin:$PATH" SUPERVISOR_STATE="$D/state" LANES_SESSION=nosuch \
    DIGEST_REPOS=test-repo DIGEST_OWNER=ownerx GH_STUB_FIXTURES="$OK/fixtures" \
    DIGEST_VERDICT_SOURCE=github \
    bash "$DIGEST" 2>/dev/null
}
J=$(run_ok)
pr() { jq -c --argjson n "$1" '.prs[] | select(.number==$n)' <<<"$J"; }

# 9. current head + a matching run -> ci_is_current true, run_conclusion success.
p1=$(pr 1)
chk "PR1 ci_is_current true on a matching head" "true" "$(jq -r '.ci_is_current' <<<"$p1")"
chk "PR1 run_conclusion success" "success" "$(jq -r '.run_conclusion' <<<"$p1")"
chk "PR1 merge_state passes through CLEAN" "CLEAN" "$(jq -r '.merge_state' <<<"$p1")"

# 10. a run exists but for an OLDER head -> ci_is_current false even though
# the run itself succeeded. This is what distinguishes "CI passed on this
# head" from "CI passed on an older head" (#137). Hardcoding
# ci_is_current:true or merge_state:"CLEAN" both break this row.
p2=$(pr 2)
chk "PR2 ci_is_current false on a stale head" "false" "$(jq -r '.ci_is_current' <<<"$p2")"
chk "PR2 run_conclusion success (the stale run itself passed)" "success" "$(jq -r '.run_conclusion' <<<"$p2")"
chk "PR2 merge_state passes through DIRTY" "DIRTY" "$(jq -r '.merge_state' <<<"$p2")"

# 11. no run at all -- must read distinctly from both "CI failed" and "CI
# passed on an older head" (#149/#161: a conflicted branch with no run at
# all read exactly like a pending one).
p3=$(pr 3)
chk "PR3 run_conclusion is NO RUN, not a guessed pass/fail" "NO RUN" "$(jq -r '.run_conclusion' <<<"$p3")"
chk "PR3 ci_is_current false with no run" "false" "$(jq -r '.ci_is_current' <<<"$p3")"
chk "PR3 merge_state passes through BEHIND" "BEHIND" "$(jq -r '.merge_state' <<<"$p3")"

# 12. current head + a run that failed -- distinct again from both above.
p4=$(pr 4)
chk "PR4 run_conclusion failure" "failure" "$(jq -r '.run_conclusion' <<<"$p4")"
chk "PR4 ci_is_current true (the failing run IS for this head)" "true" "$(jq -r '.ci_is_current' <<<"$p4")"
# THE REGRESSION agent-dotfiles#203 exists to fix: this PR's own review state
# is CHANGES_REQUESTED, but a comment on it says "I cannot approve this, it
# is unsafe." -- prose that the old `test("APPROVE";"i")` regex read as an
# approval. If verdict.py's github source regressed to reading comment
# bodies instead of review state, this would read "approved", not "rejected".
chk "PR4 verdict reads rejected from real GitHub review state, not comment prose" \
  "rejected" "$(jq -r '.verdict' <<<"$p4")"

# 12c/12d. agent-dotfiles#226 MUTATION CHECK, both directions (real
# digest.sh + verdict.py output, not just verdict.py's unit tests):
p7=$(pr 7)
chk "PR7 (pure rebase) verdict stays approved, not demoted to unknown" \
  "approved" "$(jq -r '.verdict' <<<"$p7")"
grep -q "patch-id" <<<"$(jq -r '.verdict_detail' <<<"$p7")" \
  && ok "PR7 verdict_detail names the patch-id basis for the promotion" \
  || bad "PR7 verdict_detail names the basis" "$p7"

grep -q "identical set of 2" <<<"$(jq -r '.verdict_detail' <<<"$p7")" \
  && ok "PR7 verdict_detail states how many commit patches were compared" \
  || bad "PR7 verdict_detail states the patch count" "$p7"

p8=$(pr 8)
chk "PR8 (real content change since review) verdict stays unknown -- the direction #219/#218 must not regress" \
  "unknown" "$(jq -r '.verdict' <<<"$p8")"

# 12f. agent-dotfiles#229: the shape #226's own example actually has. The
# rebase dropped a commit that upstream superseded; nothing unreviewed
# entered, so the verdict is promoted -- and the detail must SAY that the
# branch is no longer byte-for-byte what was approved.
p9=$(pr 9)
chk "PR9 (rebase that dropped a superseded commit) verdict stays approved" \
  "approved" "$(jq -r '.verdict' <<<"$p9")"
grep -q "1 of 3" <<<"$(jq -r '.verdict_detail' <<<"$p9")" \
  && ok "PR9 verdict_detail states how many reviewed patches are no longer present" \
  || bad "PR9 verdict_detail states the dropped count" "$p9"

# 12g. The other direction unreviewed content arrives: an extra commit on
# top. Every reviewed patch is still there, so a subset test applied the
# wrong way round would promote this.
p10=$(pr 10)
chk "PR10 (a commit pushed on top after the review) verdict stays unknown" \
  "unknown" "$(jq -r '.verdict' <<<"$p10")"

# 12b. agent-dotfiles#218: a review APPROVED at an old SHA must not answer for
# a head a push has since moved past. This is the failure #218 exists to
# close -- reported "approved" here would be the same shape as the pre-#206
# prose-regex bug, arriving through the SHA instead of through prose.
p6=$(pr 6)
chk "PR6 verdict reads unknown, not approved, for a review filed against a superseded head" \
  "unknown" "$(jq -r '.verdict' <<<"$p6")"
[ -n "$(jq -r '.verdict_detail' <<<"$p6")" ] && ok "PR6 verdict_detail names the stale SHA, not just 'unknown'" \
  || bad "PR6 verdict_detail non-empty" "$p6"

# 12e. agent-dotfiles#229, BLOCKING: the promotion must reach the TEXT the
# supervisor reads, not only the JSON. Before this, digest.sh printed the
# detail only `if .verdict == "unknown"`, so PR7 rendered as
#   test-repo#7 ci=NO RUN CLEAN verdict=approved
# -- indistinguishable from a review filed at the literal current head, on a
# branch that had in fact been rebased. #226's acceptance criterion is that
# the promotion is "cheap to confirm, cheap to accept"; a reader cannot
# confirm what they are never told.
T=$(run_ok_text)
# Guard the guard (agent-dotfiles#192, eighth instance): a text assertion that
# never reaches the per-PR branch would pass for the wrong reason. Prove the
# block ran before believing anything it printed.
grep -q "prs:$" <<<"$T" && ! grep -q "prs:      none open" <<<"$T" \
  && ok "text mode really reached the per-PR block (not 'none open')" \
  || bad "text mode reached the per-PR block" "$T"
t7=$(grep -E "test-repo#7 " <<<"$T")
[ -n "$t7" ] && ok "text mode prints a line for PR7 at all" || bad "PR7 text line exists" "$T"
grep -q "patch-id" <<<"$t7" \
  && ok "PR7 text names the rebase promotion's basis, not just verdict=approved" \
  || bad "PR7 text names the promotion basis" "$t7"
grep -q "head moved" <<<"$t7" \
  && ok "PR7 text says the head moved, so a rebase is legible at a glance" \
  || bad "PR7 text says head moved" "$t7"
# Do not regress the case that already worked: an `unknown` verdict still
# carries its detail, and still reads `unknown`, never `none` -- `none` is a
# claim ("nobody reviewed"), `unknown` is an absence of information.
t6=$(grep -E "test-repo#6 " <<<"$T")
grep -q "verdict=unknown (review(s) filed against" <<<"$t6" \
  && ok "PR6 unknown still shows its detail in text, exactly as before" \
  || bad "PR6 unknown detail unchanged in text" "$t6"
grep -q "verdict=none" <<<"$t6" && bad "PR6 unknown must never render as none" "$t6" \
  || ok "PR6 unknown does not render as none"

# 13. never reviewed / approved / rejected are three distinct digest outputs
# from the ledger source -- the bar this whole issue is measured against.
LSTATE="$D/ledger-state"; mkdir -p "$LSTATE"
VPY="$HERE/../../scripts/supervisor/verdict.py"
run_ledger() {
  PATH="$OK/bin:$PATH" SUPERVISOR_STATE="$LSTATE" LANES_SESSION=nosuch \
    DIGEST_REPOS=test-repo DIGEST_OWNER=ownerx GH_STUB_FIXTURES="$OK/fixtures" \
    DIGEST_VERDICT_SOURCE=ledger \
    bash "$DIGEST" --json 2>/dev/null
}
never=$(jq -c --argjson n 1 '.prs[] | select(.number==$n)' <<<"$(run_ledger)")
chk "1. never reviewed reads none, not unknown" "none" "$(jq -r '.verdict' <<<"$never")"

python3 "$VPY" --state-dir "$LSTATE" record --repo ownerx/test-repo --number 1 \
  --verdict approved --head-sha aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111 \
  --reviewer lane-7 >/dev/null
approved=$(jq -c --argjson n 1 '.prs[] | select(.number==$n)' <<<"$(run_ledger)")
chk "2. reviewed and approved reads approved" "approved" "$(jq -r '.verdict' <<<"$approved")"

python3 "$VPY" --state-dir "$LSTATE" record --repo ownerx/test-repo --number 2 \
  --verdict rejected --head-sha bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222 \
  --reviewer lane-7 --note "mutation-check never went red" >/dev/null
rejected=$(jq -c --argjson n 2 '.prs[] | select(.number==$n)' <<<"$(run_ledger)")
chk "3. reviewed and rejected reads rejected, distinct from 1 and 2" "rejected" "$(jq -r '.verdict' <<<"$rejected")"

# 13b. agent-dotfiles#218, ledger source: PR6's headRefOid in the fixture is
# "newnewnew...", but the ledger verdict below is recorded against the OLD
# "oldoldold..." head -- a push moved the head after the lane recorded its
# verdict. Must read unknown, the same fail-closed answer as the github
# source's PR6 case above, never the recorded "approved" answering for a
# head it never saw.
python3 "$VPY" --state-dir "$LSTATE" record --repo ownerx/test-repo --number 6 \
  --verdict approved --head-sha oldoldoldoldoldoldoldoldoldoldoldoldoldo \
  --reviewer lane-7 >/dev/null
stale_ledger=$(jq -c --argjson n 6 '.prs[] | select(.number==$n)' <<<"$(run_ledger)")
chk "3b. ledger verdict recorded against a superseded head reads unknown, not approved" \
  "unknown" "$(jq -r '.verdict' <<<"$stale_ledger")"

# 14. MUTATION-CHECK: break the adapter's reader and confirm the gate fails
# CLOSED (unknown), never open (approved/none). A verdict source that
# degrades to "approved" on error is the worst possible failure here
# (agent-dotfiles#203's "fail closed, never open").
BROKEN="$D/bin-broken"; mkdir -p "$BROKEN"
printf '#!/bin/bash\necho "not json"\nexit 1\n' > "$BROKEN/verdict-broken"
chmod +x "$BROKEN/verdict-broken"
broken_out=$(PATH="$OK/bin:$PATH" SUPERVISOR_STATE="$LSTATE" LANES_SESSION=nosuch \
  DIGEST_REPOS=test-repo DIGEST_OWNER=ownerx GH_STUB_FIXTURES="$OK/fixtures" \
  DIGEST_VERDICT_BIN="$BROKEN/verdict-broken" \
  bash "$DIGEST" --json 2>/dev/null)
broken2=$(jq -c --argjson n 2 '.prs[] | select(.number==$n)' <<<"$broken_out")
chk "broken adapter reader fails CLOSED (unknown), not open" "unknown" "$(jq -r '.verdict' <<<"$broken2")"

# 15. DEFAULT SOURCE (agent-dotfiles#214): with no DIGEST_VERDICT_SOURCE set
# at all, digest.sh must resolve through "github", not "ledger" -- ledger is
# a table nothing writes yet, so defaulting to it always reads "none" no
# matter what actually happened on the PR. PR5's fixture is the REAL text
# that caused the 2026-08-12 misreport: a review COMMENTED "the literal
# string REQUEST CHANGES inside a sentence describing the bug", on a PR
# whose real GitHub review state is APPROVED. The old regex read that
# comment and flipped the verdict; the adapter must read only the state
# field and report "approved".
python3 - "$OK/fixtures/pr_list.json" <<'PY'
import json, sys
path = sys.argv[1]
data = json.load(open(path))
data.append({
    "number": 5,
    "title": "default-source regression fixture",
    "headRefOid": "eeee5555eeee5555eeee5555eeee5555eeee5555",
    "headRefName": "b5",
    "mergeStateStatus": "CLEAN",
})
json.dump(data, open(path, "w"))
PY
rm -f "$OK/fixtures/pr_list.json.new"
cat > "$OK/fixtures/run_b5.json" <<'S'
[{"headSha":"eeee5555eeee5555eeee5555eeee5555eeee5555","conclusion":"success"}]
S
cat > "$OK/fixtures/reviews_5.json" <<'S'
{"reviews":[
  {"state":"COMMENTED","body":"digest.sh misread a prior PR because the reviewer comment contained the literal string REQUEST CHANGES inside a sentence describing the bug."},
  {"state":"APPROVED","body":"Looks good.","commit":{"oid":"eeee5555eeee5555eeee5555eeee5555eeee5555"}}
]}
S
default_out=$(PATH="$OK/bin:$PATH" SUPERVISOR_STATE="$D/state" LANES_SESSION=nosuch \
  DIGEST_REPOS=test-repo DIGEST_OWNER=ownerx GH_STUB_FIXTURES="$OK/fixtures" \
  bash "$DIGEST" --json 2>/dev/null)
p5=$(jq -c --argjson n 5 '.prs[] | select(.number==$n)' <<<"$default_out")
chk "default source (unset) resolves to github, not always-none ledger" \
  "approved" "$(jq -r '.verdict' <<<"$p5")"

rm -rf "$OK"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
