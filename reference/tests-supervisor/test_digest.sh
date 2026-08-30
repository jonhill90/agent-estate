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

# 8b. agent-supervisor#34: the director inbox's pending count and oldest-
# message age must appear in the digest UNCONDITIONALLY -- this is the
# pane-independent delivery channel the issue asks for. A reader who checks
# this digest has "received" a stale message the moment it is printed, with
# no tmux pane, no idle check, and no nudge anywhere in the path.
j=$(run --json)
chk "no director-inbox.jsonl -> pending is 0" "0" "$(jq -r '.director_inbox.pending' <<<"$j")"
chk "no director-inbox.jsonl -> not stale" "false" "$(jq -r '.director_inbox.stale' <<<"$j")"

DIRECTOR_INBOX="$D/state/director-inbox.jsonl" bash "$HERE/../../scripts/supervisor/director-inbox.sh" \
  post "queued behind a busy Director pane" >/dev/null
python3 -c "
import json
row = json.loads(open('$D/state/director-inbox.jsonl').read().strip())
row['at'] = '2020-01-01T00:00:00Z'
open('$D/state/director-inbox.jsonl', 'w').write(json.dumps(row) + chr(10))
"
j=$(run --json)
chk "a queued message -> pending is 1" "1" "$(jq -r '.director_inbox.pending' <<<"$j")"
chk "an old queued message -> stale is true" "true" "$(jq -r '.director_inbox.stale' <<<"$j")"
age=$(jq -r '.director_inbox.oldest_age_s' <<<"$j")
[ "$age" -gt 1000000 ] && ok "digest --json carries the large oldest_age_s" || bad "oldest_age_s in digest" "$j"
[ "$(jq -r '.ok' <<<"$j")" = "false" ] && ok "a stale inbox flips ok to false, same as any other failing section" \
  || bad "stale inbox flips ok false" "$j"
grep -q "not yet delivered to the Director" <<<"$(jq -r '.errors[]' <<<"$j")" \
  && ok "the stale-inbox reason is named in errors[]" \
  || bad "stale-inbox reason named in errors" "$j"
T=$(run)
grep -q "^inbox:.*pending=1" <<<"$T" && ok "text mode prints the pending count on its own line" \
  || bad "text mode prints pending count" "$T"
grep -q "STALE" <<<"$T" && ok "text mode marks a stale inbox loudly, not just in --json" \
  || bad "text mode marks stale inbox" "$T"
rm -f "$D/state/director-inbox.jsonl"

# Mutation-check: point digest.sh's inbox reader at a stub that always claims
# nothing is pending, and confirm the assertions above go red -- the brief's
# own bar ("mutate your fix and watch a test go red").
STUBDIR="$D/inbox-stub"; mkdir -p "$STUBDIR"
cat > "$STUBDIR/always-empty.sh" <<'S'
#!/bin/bash
echo '{"pending":0,"oldest_at":null,"oldest_age_s":null}'
S
chmod +x "$STUBDIR/always-empty.sh"
DIRECTOR_INBOX="$D/state/director-inbox.jsonl" bash "$HERE/../../scripts/supervisor/director-inbox.sh" \
  post "would be silently dropped by a broken reader" >/dev/null
python3 -c "
import json
row = json.loads(open('$D/state/director-inbox.jsonl').read().strip())
row['at'] = '2020-01-01T00:00:00Z'
open('$D/state/director-inbox.jsonl', 'w').write(json.dumps(row) + chr(10))
"
mutant_out=$(PATH="$D/bin:$PATH" SUPERVISOR_STATE="$D/state" LANES_SESSION=nosuch \
  DIGEST_INBOX_BIN="$STUBDIR/always-empty.sh" bash "$DIGEST" --json 2>/dev/null)
if [ "$(jq -r '.director_inbox.pending' <<<"$mutant_out")" = "0" ]; then
  ok "mutation confirmed: an inbox reader stub that always reports empty hides a real stale message (the assertions above would be red)"
else
  bad "mutation confirmed: the always-empty stub should hide the pending message" "$mutant_out"
fi
rm -f "$D/state/director-inbox.jsonl"

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
    branch=""; workflow=""; prev=""
    for a in "$@"; do
      [ "$prev" = "--branch" ] && branch="$a"
      [ "$prev" = "--workflow" ] && workflow="$a"
      prev="$a"
    done
    # agent-supervisor#463: a fixture keyed on branch+workflow, when present,
    # wins over the plain branch-only one -- this is what lets PR19 below pin
    # the exact defect. `--workflow` absent (the pre-#463 shape, and what a
    # mutation reverting the fix reproduces) falls through to the plain
    # run_<branch>.json, which PR19's fixtures deliberately make the WRONG
    # (later, unrelated, successful) answer.
    f=""
    [ -n "$workflow" ] && f="$FIX/run_${branch}__workflow_${workflow}.json"
    [ -n "$f" ] && [ -f "$f" ] || f="$FIX/run_${branch}.json"
    [ -f "$f" ] && cat "$f" || echo "[]"
    ;;
  "pr view")
    num="$3"
    fields=""
    prev=""
    for a in "$@"; do
      [ "$prev" = "--json" ] && fields="$a"
      prev="$a"
    done
    case "$fields" in
      *closingIssuesReferences*)
        f="$FIX/pr_view_${num}.json"
        [ -f "$f" ] && cat "$f" || echo '{"headRefName":"","closingIssuesReferences":[],"commits":[]}'
        ;;
      *baseRefName*)
        echo '{"baseRefName":"main"}'
        ;;
      *)
        f="$FIX/reviews_${num}.json"
        [ -f "$f" ] && cat "$f" || echo '{"reviews":[]}'
        ;;
    esac
    ;;
  "api "*)
    # verdict.py's #226 rebase-content comparison makes two shapes of call:
    #   gh api repos/O/R/compare/BASE...HEAD --jq .commits[].sha   (commit list)
    #   gh api -H "Accept: ...v3.diff" repos/O/R/commits/SHA       (one patch)
    if [ "$2" = "-H" ]; then path="$4"; else path="$2"; fi
    query=""
    case "$path" in *\?*) query="${path#*\?}"; path="${path%%\?*}" ;; esac
    case "$path" in
      # agent-supervisor#144: digest.sh's PR list and per-PR mergeable_state
      # are REST now (`gh api .../pulls`), not `gh pr list` -- these two
      # cases serve that; `"pr list")` above is now unreachable dead stub
      # code, kept only because other fixtures still reference pr_list.json
      # by name.
      */pulls/[0-9]*)
        num="${path##*/}"
        case "$num" in
          2) echo '{"mergeable_state":"dirty"}' ;;
          3) echo '{"mergeable_state":"behind"}' ;;
          *) echo '{"mergeable_state":"clean"}' ;;
        esac
        ;;
      */pulls)
        case "$query" in
          *state=closed*)
            f="$FIX/pr_merged.json"
            [ -f "$f" ] && cat "$f" || echo "[]"
            ;;
          *)
            jq -c '[.[] | {number, title, head:{sha:.headRefOid, ref:.headRefName}}]' "$FIX/pr_list.json"
            ;;
        esac
        ;;
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
  {"number":10,"title":"a commit pushed on top after the review","headRefOid":"add0add0add0add0add0add0add0add0add0add0","headRefName":"b10","mergeStateStatus":"CLEAN"},
  {"number":11,"title":"comment verdict from another lane","headRefOid":"1111111111111111111111111111111111111111","headRefName":"b11","mergeStateStatus":"CLEAN"},
  {"number":12,"title":"comment verdict from author lane","headRefOid":"1212121212121212121212121212121212121212","headRefName":"b12","mergeStateStatus":"CLEAN"},
  {"number":13,"title":"comment verdict without lane stamp","headRefOid":"1313131313131313131313131313131313131313","headRefName":"b13","mergeStateStatus":"CLEAN"},
  {"number":14,"title":"author lane must not drift to newer reviews","headRefOid":"1414141414141414141414141414141414141414","headRefName":"fix/214-author-drift","mergeStateStatus":"CLEAN"},
  {"number":15,"title":"re-dispatch: head ref, not first-attempt position, names the author","headRefOid":"1515151515151515151515151515151515151515","headRefName":"lane/215-second-attempt","mergeStateStatus":"CLEAN"},
  {"number":16,"title":"self-review across a session rename","headRefOid":"1616161616161616161616161616161616161616","headRefName":"fix/216-renamed-session","mergeStateStatus":"CLEAN"},
  {"number":17,"title":"reviewer lane stamped with something that is not a lane id","headRefOid":"1717171717171717171717171717171717171717","headRefName":"fix/217-unparseable-stamp","mergeStateStatus":"CLEAN"},
  {"number":18,"title":"self-review across a window renumber, not a session rename","headRefOid":"1818181818181818181818181818181818181818","headRefName":"fix/218-renumbered-window","mergeStateStatus":"CLEAN"},
  {"number":19,"title":"agent-supervisor#463: a later unrelated workflow must not mask an earlier failing CI run","headRefOid":"1919191919191919191919191919191919191919","headRefName":"b19","mergeStateStatus":"CLEAN"}
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

# agent-supervisor#463 THE BLOCKING FINDING itself: PR19's branch carries TWO
# workflow runs against the SAME (current) head -- `Validate` (the workflow
# with the `shell-suites` job, i.e. real CI) failed, but `UI evidence` was
# triggered later and finished with a success. The unscoped `gh run list
# --branch --limit 1` this issue was filed over returns whichever of the two
# is newest-CREATED regardless of which workflow it belongs to -- encoded
# here as the plain run_b19.json (no --workflow filter), which is what a
# caller that drops `--workflow` (the mutation below) falls back to and
# reads as a masking success. `--workflow validate.yml` must reach the
# scoped fixture instead and read the real failure.
cat > "$OK/fixtures/run_b19.json" <<'S'
[{"headSha":"1919191919191919191919191919191919191919","conclusion":"success"}]
S
cat > "$OK/fixtures/run_b19__workflow_validate.yml.json" <<'S'
[{"headSha":"1919191919191919191919191919191919191919","conclusion":"failure"}]
S

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

# agent-supervisor#63: every lane posts through the same GitHub login, so
# comment.author.login == pr.author.login is a constant. These PRs exercise
# lane identity instead: the PR author comes from the ledger's author-issue-lane
# lookup, and the reviewing lane comes from an explicit Review-Lane stamp in the
# verdict comment. PR13 USED TO keep the #55 behaviour (a `**Verdict:` comment
# still counts, independence just stays unknown without a lane stamp) -- #595's
# decision (director's final comment on that issue) deliberately retires that:
# an operative verdict now requires the complete Verdict:/Review-Lane:/
# Reviewed-SHA: block, so a bare, unstamped `**Verdict:` line no longer counts
# as a decision at all. PR13 is kept as the fixture proving that, not proving
# #55 anymore -- see its own assertions below for what changed and why.
cat > "$OK/fixtures/pr_view_11.json" <<'S'
{"headRefName":"fix/211-comment-verdict-other-lane","closingIssuesReferences":[{"number":211}],"commits":[]}
S
cat > "$OK/fixtures/reviews_11.json" <<'S'
{"reviews":[],"comments":[
  {"author":{"login":"jonhill90"},"body":"**Verdict: APPROVE**\nReview-Lane: t:4\nReviewed-SHA: 1111111111111111111111111111111111111111","createdAt":"2026-08-13T20:57:01Z"}
],"author":{"login":"jonhill90"},"commits":[{"oid":"1111111111111111111111111111111111111111","committedDate":"2026-08-13T20:57:01Z"}]}
S
cat > "$OK/fixtures/pr_view_12.json" <<'S'
{"headRefName":"fix/212-comment-verdict-author-lane","closingIssuesReferences":[{"number":212}],"commits":[]}
S
cat > "$OK/fixtures/reviews_12.json" <<'S'
{"reviews":[],"comments":[
  {"author":{"login":"jonhill90"},"body":"**Verdict: REQUEST CHANGES**\nReview-Lane: t:3\nReviewed-SHA: 1212121212121212121212121212121212121212","createdAt":"2026-08-13T20:58:01Z"}
],"author":{"login":"jonhill90"},"commits":[{"oid":"1212121212121212121212121212121212121212","committedDate":"2026-08-13T20:58:01Z"}]}
S
cat > "$OK/fixtures/pr_view_13.json" <<'S'
{"headRefName":"fix/213-comment-verdict-unstamped","closingIssuesReferences":[{"number":213}],"commits":[]}
S
cat > "$OK/fixtures/reviews_13.json" <<'S'
{"reviews":[],"comments":[
  {"author":{"login":"jonhill90"},"body":"**Verdict: APPROVE**","createdAt":"2026-08-13T20:59:01Z"}
],"author":{"login":"jonhill90"},"commits":[{"oid":"1313131313131313131313131313131313131313","committedDate":"2026-08-13T20:59:01Z"}]}
S
cat > "$OK/fixtures/pr_view_14.json" <<'S'
{"headRefName":"fix/214-author-drift","closingIssuesReferences":[{"number":214}],"commits":[]}
S
cat > "$OK/fixtures/reviews_14.json" <<'S'
{"reviews":[],"comments":[
  {"author":{"login":"jonhill90"},"body":"**Verdict: REQUEST CHANGES**\nReview-Lane: t:3\nReviewed-SHA: 1414141414141414141414141414141414141414","createdAt":"2026-08-13T21:00:01Z"}
],"author":{"login":"jonhill90"},"commits":[{"oid":"1414141414141414141414141414141414141414","committedDate":"2026-08-13T21:00:01Z"}]}
S

# agent-supervisor#77: the reviewer's own reproduction, through the real
# digest.sh path. Issue #215 was first attempted on t:3 (abandoned), then
# re-dispatched to t:7, whose branch actually produced this PR. A reviewer
# on t:3 -- the STALE lane -- must read independent, because t:3 is not the
# real author; picking "first non-review task" (the bug) would say t:3 IS
# the author and wrongly call this comment not independent.
cat > "$OK/fixtures/pr_view_15.json" <<'S'
{"headRefName":"lane/215-second-attempt","closingIssuesReferences":[{"number":215}],"commits":[]}
S
cat > "$OK/fixtures/reviews_15.json" <<'S'
{"reviews":[],"comments":[
  {"author":{"login":"jonhill90"},"body":"**Verdict: APPROVE**\nReview-Lane: t:3\nReviewed-SHA: 1515151515151515151515151515151515151515","createdAt":"2026-08-13T21:01:01Z"}
],"author":{"login":"jonhill90"},"commits":[{"oid":"1515151515151515151515151515151515151515","committedDate":"2026-08-13T21:01:01Z"}]}
S

# agent-supervisor#108. PR16's author was recorded under the session name the
# estate used BEFORE the 2026-08-14 rename (`old:3`); the review is stamped
# with the same window's name AFTER it (`t:3`). One window, two spellings --
# and independence was decided by `==` on those two strings, so this PR would
# have been reported independent. PR17 stamps a branch name instead of a lane
# id (which is literally what PR #95's stamp carried): nothing about it
# establishes a different window, so it must read UNKNOWN, not independent.
cat > "$OK/fixtures/pr_view_16.json" <<'S'
{"headRefName":"fix/216-renamed-session","closingIssuesReferences":[{"number":216}],"commits":[]}
S
cat > "$OK/fixtures/reviews_16.json" <<'S'
{"reviews":[],"comments":[
  {"author":{"login":"jonhill90"},"body":"**Verdict: APPROVE**\nReview-Lane: t:3\nReviewed-SHA: 1616161616161616161616161616161616161616","createdAt":"2026-08-14T09:00:01Z"}
],"author":{"login":"jonhill90"},"commits":[{"oid":"1616161616161616161616161616161616161616","committedDate":"2026-08-14T09:00:01Z"}]}
S
cat > "$OK/fixtures/pr_view_17.json" <<'S'
{"headRefName":"fix/217-unparseable-stamp","closingIssuesReferences":[{"number":217}],"commits":[]}
S
cat > "$OK/fixtures/reviews_17.json" <<'S'
{"reviews":[],"comments":[
  {"author":{"login":"jonhill90"},"body":"**Verdict: APPROVE**\nReview-Lane: lane/89-rev95\nReviewed-SHA: 1717171717171717171717171717171717171717","createdAt":"2026-08-14T09:01:01Z"}
],"author":{"login":"jonhill90"},"commits":[{"oid":"1717171717171717171717171717171717171717","committedDate":"2026-08-14T09:01:01Z"}]}
S

# agent-supervisor#332: PR16 above proves the SAME-INDEX rename case (#108) --
# two lane ids that differ only in session name, the SAME window, correctly
# read `same` off the string shape alone (index 3 == 3, session ignored).
# PR18 proves the case #108's fix never covered and #332's reviewer found
# left open at THIS file's own `lane_relation()` call: a `renumber-windows
# on` renumber, where the physical window's INDEX itself changes between the
# author's dispatch and the reviewer's stamp. Index-string shape alone says
# `different` (8 != 88) -- looks independent -- but the ledger's own
# `pane_id` registry (both rows %8) proves it is the SAME physical window.
# Before agent-supervisor#332's widening this reported `verdict_independent:
# true`; it must now report `false`, matching what merge-pr.sh's ENFORCEMENT
# gate refuses (this file and that one share resolve_lane_relation()
# specifically so they cannot disagree -- see verdict-independence.sh).
cat > "$OK/fixtures/pr_view_18.json" <<'S'
{"headRefName":"fix/218-renumbered-window","closingIssuesReferences":[{"number":218}],"commits":[]}
S
cat > "$OK/fixtures/reviews_18.json" <<'S'
{"reviews":[],"comments":[
  {"author":{"login":"jonhill90"},"body":"**Verdict: APPROVE**\nReview-Lane: t:88\nReviewed-SHA: 1818181818181818181818181818181818181818","createdAt":"2026-08-18T09:00:01Z"}
],"author":{"login":"jonhill90"},"commits":[{"oid":"1818181818181818181818181818181818181818","committedDate":"2026-08-18T09:00:01Z"}]}
S

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

# 12b. agent-supervisor#463 THE PINNED REGRESSION: PR19's branch has a LATER,
# unrelated, SUCCESSFUL workflow run and an EARLIER failing CI run, both
# against the current head. `ci=success` here is the exact false green #463
# was filed over -- this must read "failure", not "success".
p19=$(pr 19)
chk "PR19 run_conclusion reads the CI workflow's failure, not the later unrelated success" \
  "failure" "$(jq -r '.run_conclusion' <<<"$p19")"
chk "PR19 ci_is_current true (the CI workflow's own run IS for this head)" \
  "true" "$(jq -r '.ci_is_current' <<<"$p19")"
[ "$(jq -r '.run_conclusion' <<<"$p19")" != "success" ] \
  && ok "PR19 ci=success is FALSE" \
  || bad "PR19 ci=success must be false" "$p19"

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

LANE_STATE="$D/lane-state"; mkdir -p "$LANE_STATE"
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-dispatch \
  --lane t:3 --task as211-author --summary "#211 author" --pane-id %3 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 211 --github ownerx/test-repo --harness claude >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-completion \
  --task as211-author --note done >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-dispatch \
  --lane t:3 --task as212-author --summary "#212 author" --pane-id %3 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 212 --github ownerx/test-repo --harness claude >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-completion \
  --task as212-author --note done >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-dispatch \
  --lane t:3 --task as213-author --summary "#213 author" --pane-id %3 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 213 --github ownerx/test-repo --harness claude >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-completion \
  --task as213-author --note done >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-dispatch \
  --lane t:3 --task as214-author --summary "#214 author" --pane-id %3 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 214 --github ownerx/test-repo --harness claude >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-completion \
  --task as214-author --note done >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-dispatch \
  --lane t:4 --task as214-review-a --summary "#214 review PR #14" --pane-id %4 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 214 --github ownerx/test-repo --harness claude >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-completion \
  --task as214-review-a --note done >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-dispatch \
  --lane t:5 --task as214-rev14b --summary "#214 review PR #14 again" --pane-id %5 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 214 --github ownerx/test-repo --harness claude >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-completion \
  --task as214-rev14b --note done >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-dispatch \
  --lane t:6 --task as214-review-c --summary "#214 review PR #14 third" --pane-id %6 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 214 --github ownerx/test-repo --harness claude >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-dispatch \
  --lane t:3 --task as215-first-attempt --summary "#215 first attempt" --pane-id %3 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 215 --github ownerx/test-repo --harness claude >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-completion \
  --task as215-first-attempt --note abandoned >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-dispatch \
  --lane t:7 --task as215-second-attempt --summary "#215 re-dispatched" --pane-id %7 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 215 --github ownerx/test-repo --harness claude >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-completion \
  --task as215-second-attempt --note done >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-dispatch \
  --lane old:3 --task as216-renamed-author --summary "#216 author, pre-rename session" --pane-id %3 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 216 --github ownerx/test-repo --harness claude >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-completion \
  --task as216-renamed-author --note done >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-dispatch \
  --lane t:5 --task as217-unparseable --summary "#217 author" --pane-id %5 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 217 --github ownerx/test-repo --harness claude >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-completion \
  --task as217-unparseable --note done >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-dispatch \
  --lane t:8 --task as218-author --summary "#218 author" --pane-id %8 --pane-path "$D/repo" \
  --command claude --server-id srv --session-id sess --issue 218 --github ownerx/test-repo --harness claude >/dev/null
python3 "$HERE/../../scripts/supervisor/cli.py" --state-dir "$LANE_STATE" record-completion \
  --task as218-author --note done >/dev/null
# agent-supervisor#332: t:88 is never dispatched a task -- it is the SAME
# physical window as t:8 after a renumber, registered directly (the way a
# fresh dispatch to that now-different index would re-register it) rather
# than through record-dispatch, so its ledger row carries the same pane_id
# without a second task on top.
python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from core import Ledger
Ledger(sys.argv[2]).register_lane(
    lane="t:88", pane_id="%8", nonce="nonce-t88", harness="claude",
    repo="/tmp/repo", server_id="srv", session_id="sess", command="claude", transport="send-keys",
)
' "$HERE/../../scripts/supervisor" "$LANE_STATE"
lane_out=$(PATH="$OK/bin:$PATH" SUPERVISOR_STATE="$LANE_STATE" LANES_SESSION=nosuch \
  DIGEST_REPOS=test-repo DIGEST_OWNER=ownerx GH_STUB_FIXTURES="$OK/fixtures" \
  DIGEST_VERDICT_SOURCE=github \
  bash "$DIGEST" --json 2>/dev/null)
lp() { jq -c --argjson n "$1" '.prs[] | select(.number==$n)' <<<"$lane_out"; }
p11=$(lp 11)
chk "PR11 comment verdict from a different lane is independent, despite same GitHub login" \
  "true" "$(jq -r '.verdict_independent' <<<"$p11")"
grep -q "independent -- author lane t:3, reviewer lane t:4" <<<"$(jq -r '.verdict_detail' <<<"$p11")" \
  && ok "PR11 detail names both lanes" \
  || bad "PR11 detail names both lanes" "$p11"
p12=$(lp 12)
chk "PR12 comment verdict from the author lane is not independent" \
  "false" "$(jq -r '.verdict_independent' <<<"$p12")"
grep -q "NOT independent -- author lane t:3 reviewed its own PR" <<<"$(jq -r '.verdict_detail' <<<"$p12")" \
  && ok "PR12 detail names the self-review by lane" \
  || bad "PR12 detail names the self-review by lane" "$p12"
# agent-supervisor#595 RETIRES #55's contract here, deliberately (director's
# final decision comment on #595): an operative verdict now requires the
# complete Verdict:/Review-Lane:/Reviewed-SHA: block, so a bare, unstamped
# `**Verdict: APPROVE**` with nothing else no longer counts as a decision at
# all -- it used to read "approved" with independence left "unknown"; #595
# reads it "none", identically to a PR nobody has ever reviewed. That is a
# real loss of information (a genuine, if unstamped, decision now looks
# exactly like silence) which #595's own decision thread accepts as the cost
# of closing the prose-shadowing family (#531/#553/#595's own bold-quote
# incidents) -- #55/#63 are never mentioned there, so this was very likely an
# unconsidered side effect rather than a deliberately intended one, but it is
# #595's decision text, not a bug in this PR, that produces it.
p13=$(lp 13)
chk "PR13 unstamped comment verdict is retired by #595 -- reads none, not approved (was #55)" \
  "none" "$(jq -r '.verdict' <<<"$p13")"
chk "PR13 unstamped comment verdict_independent is null, indistinguishable from never-reviewed" \
  "null" "$(jq -r '.verdict_independent' <<<"$p13")"
chk "PR13 detail is empty, exactly like a never-reviewed PR -- #595 leaves no trace an unstamped decision was ever posted" \
  "" "$(jq -r '.verdict_detail' <<<"$p13")"
p14=$(lp 14)
chk "PR14 reviewer lane == original author lane stays not independent after later reviews" \
  "false" "$(jq -r '.verdict_independent' <<<"$p14")"
grep -q "NOT independent -- author lane t:3 reviewed its own PR" <<<"$(jq -r '.verdict_detail' <<<"$p14")" \
  && ok "PR14 detail proves author did not drift to newer review lanes" \
  || bad "PR14 detail proves author did not drift to newer review lanes" "$p14"
# agent-supervisor#200: pre-#200, author_lane_for narrowed issue 215's two
# non-review candidates (as215-first-attempt/t:3, abandoned; as215-second-
# attempt/t:7, the real author) down to the single lane the head ref names,
# t:7 -- so a review from the abandoned t:3 attempt read as independent.
# #200 widens author_lane_for to the FULL contributor set for the issue
# (contributor-issue-lanes, #190's own primitive), the same over-inclusive
# set dispatch.sh's `--reviews-pr` guard already refuses to DISPATCH a
# review to (#190's own docstring: over-including an abandoned attempt is
# the SAFE direction). t:3 is now correctly recognized as a lane that
# contributed to issue 215 -- reviewing this PR as t:3 is a self-review by
# the widened definition, not a stale attempt's independent look-in, and
# the merge gate must refuse it exactly as it would a fix-pass lane
# reviewing its own fix.
p15=$(lp 15)
chk "PR15 (agent-supervisor#200): a review from t:3 -- an ABANDONED first attempt at issue 215, but still a contributor to it -- is no longer treated as independent, now that the contributor set is widened past just the head-ref-resolved author (t:7)" \
  "false" "$(jq -r '.verdict_independent' <<<"$p15")"
grep -q "NOT independent -- author lane t:3 reviewed its own PR" <<<"$(jq -r '.verdict_detail' <<<"$p15")" \
  && ok "PR15 detail names t:3 as the matched contributor, not the unrelated real author t:7" \
  || bad "PR15 detail names the matched contributor" "$p15"
p16=$(lp 16)
chk "PR16 (agent-supervisor#108): a review from the SAME WINDOW under the post-rename session name is not independent" \
  "false" "$(jq -r '.verdict_independent' <<<"$p16")"
grep -q "NOT independent -- author lane old:3 reviewed its own PR" <<<"$(jq -r '.verdict_detail' <<<"$p16")" \
  && ok "PR16 detail names the self-review across the rename" \
  || bad "PR16 detail names the self-review across the rename" "$p16"
grep -q "the same window, renamed session" <<<"$(jq -r '.verdict_detail' <<<"$p16")" \
  && ok "PR16 detail says WHY two different-looking lane ids are one lane" \
  || bad "PR16 detail says why the two ids are one lane" "$p16"
p17=$(lp 17)
chk "PR17 (agent-supervisor#108): a Review-Lane stamp that is not a lane id reports unknown, never independent" \
  "null" "$(jq -r '.verdict_independent' <<<"$p17")"
# agent-supervisor#232: the stamp is now rejected at PARSE time -- no
# lane-shaped (`<session>:<index>`) token anywhere on the line -- rather
# than being accepted as a lane id and only failing later at the
# lane_relation() comparison. The detail now names the offending line
# verbatim (#232's "print the line it could not parse" requirement),
# which is strictly more useful than the old "not comparable lane ids".
grep -q "could not parse lane id from: Review-Lane: lane/89-rev95" <<<"$(jq -r '.verdict_detail' <<<"$p17")" \
  && ok "PR17 detail names the unparseable Review-Lane line" \
  || bad "PR17 detail names the unparseable Review-Lane line" "$p17"
p18=$(lp 18)
chk "PR18 (agent-supervisor#332): a reviewer lane with a DIFFERENT index but the ledger's SAME pane_id as the author -- a renumbered self-review, not a session rename -- is NOT independent" \
  "false" "$(jq -r '.verdict_independent' <<<"$p18")"
grep -q "NOT independent -- author lane t:8 reviewed its own PR" <<<"$(jq -r '.verdict_detail' <<<"$p18")" \
  && ok "PR18 detail names the renumbered self-review" \
  || bad "PR18 detail names the renumbered self-review" "$p18"

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

# agent-supervisor#184: independence_verdict's catch-all (verdict not a
# decisive/lane-stamped comment-or-ledger record) must stay {value:null,
# detail:""} -- exactly what the pre-extraction inline jq in digest.sh
# returned. #179 pulled the independence computation out into
# verdict-independence.sh and the catch-all grew prose along the way,
# so EVERY never-reviewed PR's verdict_detail read a boilerplate
# "independence unknown -- ..." sentence that told the reader nothing
# "hasn't been reviewed yet" didn't already say. Pinned here so the next
# extraction cannot repeat this silently.
chk "1b. never-reviewed PR's verdict_detail is empty, not independence boilerplate" \
  "" "$(jq -r '.verdict_detail' <<<"$never")"

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

# 16. agent-supervisor#36: delivered ledger rows whose pane has visibly
# returned to idle must be surfaced, but never auto-completed. The busy twin is
# the load-bearing negative case: a reconciler that reports every delivered
# task creates noise, and noise is why the original capacity loss stayed
# invisible for hours.
REC="$D/reconcile"; mkdir -p "$REC/bin" "$REC/state"
cat > "$REC/bin/gh" <<'S'
#!/bin/bash
case "$1 $2" in
  "pr list") echo "[]" ;;
  *) exit 1 ;;
esac
S
chmod +x "$REC/bin/gh"
cat > "$REC/bin/lanes" <<'S'
#!/bin/bash
case "${1:-}" in
  --json)
    cat <<'JSON'
[
  {"window":2,"window_id":"@102","name":"free-2","command":"claude.exe","state":"free","idle_seconds":480},
  {"window":3,"window_id":"@103","name":"ad36-busy","command":"claude.exe","state":"busy","idle_seconds":480}
]
JSON
    ;;
  *)
    printf 'WINDOW NAME COMMAND STATE\n'
    printf '2      free-2 claude.exe free\n'
    printf '3      ad36-busy claude.exe busy\n'
    ;;
esac
S
chmod +x "$REC/bin/lanes"
AGENT_SUPERVISOR_STATE_DIR="$REC/state" python3 "$HERE/../../scripts/supervisor/cli.py" record-dispatch \
  --lane t:2 --task ad36-idle --summary "idle task" --pane-id %2 --pane-path "$REC" \
  --command claude.exe --server-id '$rec:1' --session-id '$0' --issue 36 --github acme/agent-supervisor >/dev/null
AGENT_SUPERVISOR_STATE_DIR="$REC/state" python3 "$HERE/../../scripts/supervisor/cli.py" record-dispatch \
  --lane t:3 --task ad36-busy --summary "busy task" --pane-id %3 --pane-path "$REC" \
  --command claude.exe --server-id '$rec:1' --session-id '$0' --issue 37 --github acme/agent-supervisor >/dev/null
before_status=$(AGENT_SUPERVISOR_STATE_DIR="$REC/state" python3 "$HERE/../../scripts/supervisor/cli.py" status)
reconcile_json=$(PATH="$REC/bin:$PATH" SUPERVISOR_STATE="$REC/state" LANES_SESSION=t \
  DIGEST_REPOS=only GH_STUB_FIXTURES="$REC" DIGEST_LANES_BIN="$REC/bin/lanes" DIGEST_RECONCILE_IDLE_AFTER=300 \
  bash "$DIGEST" --json 2>/dev/null)
after_status=$(AGENT_SUPERVISOR_STATE_DIR="$REC/state" python3 "$HERE/../../scripts/supervisor/cli.py" status)
chk "delivered idle lane appears in reconciliation digest" \
  "ad36-idle" "$(jq -r '.reconciliation.delivered_idle[0].task' <<<"$reconcile_json")"
chk "busy delivered lane does NOT appear in reconciliation digest" \
  "0" "$(jq -r '[.reconciliation.delivered_idle[] | select(.task=="ad36-busy")] | length' <<<"$reconcile_json")"
chk "reconciler does not auto-complete the surfaced task" \
  "$before_status" "$after_status"
grep -q "record-completion --task ad36-idle" <<<"$(PATH="$REC/bin:$PATH" SUPERVISOR_STATE="$REC/state" LANES_SESSION=t \
  DIGEST_REPOS=only GH_STUB_FIXTURES="$REC" DIGEST_LANES_BIN="$REC/bin/lanes" DIGEST_RECONCILE_IDLE_AFTER=300 \
  bash "$DIGEST" 2>/dev/null)" \
  && ok "human digest names record-completion for a finished-but-unsignalled lane" \
  || bad "human digest names record-completion" "$reconcile_json"

# 17. agent-supervisor#41: a component's status file must record OUTCOMES,
# not just liveness. Three cases, per the issue's own acceptance bar --
# "could this field have caught #28, #44, or #57?":
#
#   17a. a component alive whose last attempt FAILED -> digest reports it
#        degraded, naming the attempt and the failure (agent-supervisor#28:
#        poller-recover.sh failed 37 consecutive times while digest kept
#        reporting `poller: alive=true state=ok`, because nothing here read
#        watchdog.status's `recovery:` line at all).
#   17b. a component alive with a recent success -> reported healthy,
#        silently. A detector that fires on the healthy case is noise.
#   17c. a component whose overall tick trivially "succeeded" (advance:
#        current/advanced) must not mask a real failure folded into that
#        same tick (agent-supervisor#57: `advance-live: current` printed
#        every watchdog run for hours while that SAME run's poller-restart
#        request silently failed underneath it).
OUT="$D/outcomes"; mkdir -p "$OUT"
run_out() { PATH="$D/bin:$PATH" SUPERVISOR_STATE="$OUT" LANES_SESSION=nosuch bash "$DIGEST" "$@" 2>/dev/null; }

# 17a. RED first: a status file that names a real recovery failure, read by
# the digest as it stood before this fix, is silence -- prove that by
# reading the field the OLD digest.sh literally never asked for. This pins
# the defect to the reader, not to whether the fact exists on disk.
cat > "$OUT/watchdog.status" <<'S'
checked:  2026-08-13T00:00:00Z
state:    working
restarts: 0 in the last 3600s
recovery: failed (attempt 37 in a row) — rc=1: FAILED -- could not determine poller windows — last confirmed recovery: 2026-08-12T20:00:00Z
S
cat > "$OUT/inbox-poll.status" <<'S'
checked: 2026-08-13T00:00:00Z
state:   ok
S
j=$(run_out --json)
chk "17a: a failed recovery attempt is carried through in --json" \
  "true" "$(jq -r '.watchdog.recovery | startswith("failed")' <<<"$j")"
[ "$(jq -r '.ok' <<<"$j")" = "false" ] && ok "17a: a failed recovery attempt flips ok to false" \
  || bad "17a: failed recovery flips ok false" "$j"
grep -q "poller recovery: failed (attempt 37 in a row)" <<<"$(jq -r '.errors[]' <<<"$j")" \
  && ok "17a: the error names the attempt count and the failure, not just 'degraded'" \
  || bad "17a: error names the attempt and failure" "$j"
T=$(run_out)
grep -q "recovery: failed" <<<"$T" && ok "17a: text mode shows the recovery outcome, not just liveness" \
  || bad "17a: text mode shows recovery outcome" "$T"

# 17b. A component with no recovery failure recorded -- the ordinary,
# healthy case -- must add no error and no line. Noise on the healthy path
# is how #54 shipped a duplicate alarm that fires 100% of the time.
cat > "$OUT/watchdog.status" <<'S'
checked:  2026-08-13T00:00:00Z
state:    working
restarts: 0 in the last 3600s
S
j=$(run_out --json)
chk "17b: no recovery line -> the field reads empty, not a guessed state" \
  "" "$(jq -r '.watchdog.recovery' <<<"$j")"
grep -q "poller recovery:" <<<"$(jq -r '.errors[]?' <<<"$j")" \
  && bad "17b: a healthy recovery outcome adds no error" "$j" \
  || ok "17b: a healthy recovery outcome adds no error"
T=$(run_out)
grep -q "^          recovery:" <<<"$T" && bad "17b: a healthy recovery outcome prints no line" "$T" \
  || ok "17b: a healthy recovery outcome prints no line"

# 17c. The overall tick trivially "succeeded" (advance: current) while a
# poller-restart-request folded into that same tick failed. Must still be
# named as degraded -- the trivial top-level success must not stand in for
# every sub-action having succeeded.
cat > "$OUT/watchdog.status" <<'S'
checked:  2026-08-13T00:00:00Z
state:    working
restarts: 0 in the last 3600s
advance:  current — live copy already at origin/main (fetched fresh) advance-live: POLLER-RESTART-REQUESTED but prompt relaunch could not be started -- watchdog poller-recover.sh remains the backstop
S
j=$(run_out --json)
[ "$(jq -r '.ok' <<<"$j")" = "false" ] && ok "17c: a trivially-successful advance still degrades on a nested relaunch failure" \
  || bad "17c: nested relaunch failure flips ok false" "$j"
grep -q "could not be started" <<<"$(jq -r '.errors[]' <<<"$j")" \
  && ok "17c: the error names the nested failure, not just 'current'" \
  || bad "17c: error names the nested failure" "$j"

# MUTATION: point digest.sh's field reader at a label that never matches --
# the same shape as the pre-fix code, which asked WD_FILE for state/checked/
# restarts/heartbeat and nothing else. Confirms 17a/17c are actually pinned
# to reading these fields, not to some other path to `ok=false`.
MUT_DIGEST="$D/digest-no-outcomes.sh"
python3 - "$DIGEST" "$MUT_DIGEST" <<'PY'
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker_a = 'if [[ "$wd_recovery" == failed* ]]; then'
marker_b = 'if [[ "$wd_advance" == *"could not be started"* ]]; then'
assert marker_a in text and marker_b in text, "outcome-check branches not found -- digest.sh shape changed"
text = text.replace(marker_a, 'if false; then', 1)
text = text.replace(marker_b, 'if false; then', 1)
open(dst, "w").write(text)
PY
cat > "$OUT/watchdog.status" <<'S'
checked:  2026-08-13T00:00:00Z
state:    working
restarts: 0 in the last 3600s
recovery: failed (attempt 37 in a row) — rc=1: FAILED -- could not determine poller windows — last confirmed recovery: 2026-08-12T20:00:00Z
S
mut_out=$(PATH="$D/bin:$PATH" SUPERVISOR_STATE="$OUT" LANES_SESSION=nosuch bash "$MUT_DIGEST" --json 2>/dev/null)
if grep -q "poller recovery: failed" <<<"$(jq -r '.errors[]?' <<<"$mut_out")"; then
  bad "mutation confirmed: disabling the outcome checks hides the recovery failure" "$mut_out"
else
  ok "mutation confirmed: disabling the outcome checks lets 37 consecutive recovery failures pass without naming the failure (17a would be red)"
fi

# iso_ago SECS -- "SECS seconds before now" as UTC-ISO8601, mirroring
# digest.sh's own iso_to_epoch (BSD `date -r`, GNU `date -d` fallback) so a
# test's clock math cannot drift from the code under test. Defined here
# (ahead of its other definition further down, for a different block) since
# block 18 below needs it first.
iso_ago() {
  local secs="$1" epoch
  epoch=$(( $(date -u +%s) - secs ))
  date -u -r "$epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
    || date -u -d "@$epoch" +%Y-%m-%dT%H:%M:%SZ
}

# 18. agent-supervisor#154: the poller is a launchd/systemd-hosted service
# now, not a tmux window (see README.md) -- `poller: alive` is read from
# inbox-poll.status's own `checked:`/`state:` heartbeat, the same
# host-agnostic signal watchdog.sh's check_inbox_heartbeat already uses, not
# a tmux window lookup (which would read "not alive" forever once nothing
# runs a window -- the #96 scoping fix this test used to cover no longer
# applies once liveness has no window to scope to at all).
D18=$(mktemp -d); mkdir -p "$D18/state"
digest18() { PATH="$D18/bin:$PATH" SUPERVISOR_STATE="$D18/state" LANES_SESSION=nosuch bash "$DIGEST" --json 2>/dev/null; }
mkdir -p "$D18/bin"; printf '#!/bin/bash\nexit 1\n' > "$D18/bin/gh"; chmod +x "$D18/bin/gh"

# 18a. A fresh heartbeat with state=ok -> alive=true.
cat > "$D18/state/inbox-poll.status" <<S
checked: $(iso_ago 5)
state:   ok
S
j=$(digest18)
chk "18a: a fresh inbox-poll.status heartbeat reads alive=true" "true" "$(jq -r '.poller.alive' <<<"$j")"

# 18b. A stale heartbeat (older than the assumed staleness threshold) -> alive=false.
cat > "$D18/state/inbox-poll.status" <<S
checked: $(iso_ago 99999)
state:   ok
S
j=$(digest18)
chk "18b: a stale inbox-poll.status heartbeat reads alive=false" "false" "$(jq -r '.poller.alive' <<<"$j")"

# 18c. A fresh heartbeat but state=stopped (report_stop's own clean-exit record) -> alive=false.
cat > "$D18/state/inbox-poll.status" <<S
checked: $(iso_ago 5)
state:   stopped
S
j=$(digest18)
chk "18c: a fresh but state=stopped heartbeat reads alive=false" "false" "$(jq -r '.poller.alive' <<<"$j")"

# 18d. No status file at all -> alive=false, state UNREADABLE.
rm -f "$D18/state/inbox-poll.status"
j=$(digest18)
chk "18d: no inbox-poll.status reads alive=false" "false" "$(jq -r '.poller.alive' <<<"$j")"
chk "18d: no inbox-poll.status reads state=UNREADABLE" "UNREADABLE" "$(jq -r '.poller.state' <<<"$j")"

rm -rf "$D18"

# 19. agent-supervisor#89: the `advance:` line is only rewritten when
# watchdog.sh itself ticks, same tick that writes `checked:` -- between
# ticks it sits unchanged, so a reader with no age on it reads a replay as a
# live fact. `checked:` and `advance:` are written by the SAME tick, so
# checked:'s age is usable as advance:'s age without a second timestamp.
#
# iso_ago portably renders "N seconds before now" as the estate's UTC-ISO8601
# shape, mirroring digest.sh's own iso_to_epoch (BSD `date -r`, GNU `date -d`
# fallback) so the test's clock math cannot drift from the code under test.
iso_ago() {
  local secs="$1" epoch
  epoch=$(( $(date -u +%s) - secs ))
  date -u -r "$epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
    || date -u -d "@$epoch" +%Y-%m-%dT%H:%M:%SZ
}

# 19a. RED first: a status file written 5 minutes ago -> the digest line
# includes that age. Mutating the age away (deleting `checked:` below) must
# turn this red -- proven by the mutation test at the end of this block.
fresh_checked=$(iso_ago 300)
cat > "$OUT/watchdog.status" <<S
checked:  $fresh_checked
state:    working
restarts: 0 in the last 3600s
advance:  current, abc123 already matches origin/main (fetched fresh)
S
T=$(run_out)
grep -q "advance:.*(as of .*, 5m ago)" <<<"$T" \
  && ok "19a: the advance line carries its own age" \
  || bad "19a: advance line carries its age" "$T"
j=$(run_out --json)
age_s=$(jq -r '.watchdog.advance_age_s' <<<"$j")
[ "$age_s" -ge 299 ] 2>/dev/null && [ "$age_s" -le 301 ] \
  && ok "19a: --json carries the age in seconds" \
  || bad "19a: --json carries the age in seconds" "want 299..301, got '$age_s'"
chk "19a: --json reads not-stale under the default threshold" "false" "$(jq -r '.watchdog.advance_stale' <<<"$j")"
grep -q "STALE" <<<"$T" && bad "19a: a fresh advance line is not marked STALE" "$T" \
  || ok "19a: a fresh advance line is not marked STALE"

# 19b. THE CASE THAT MATTERS: a status older than the staleness threshold
# (3x the 180s tick interval = 540s by default) is reported as stale, not
# read as a fresh "current" -- this is the exact defect #89 was filed over:
# GitHub had moved a commit ahead of live while this line, over an hour old,
# still asserted "current ... (fetched fresh)" with nothing naming its age.
stale_checked=$(iso_ago 4200)
cat > "$OUT/watchdog.status" <<S
checked:  $stale_checked
state:    working
restarts: 0 in the last 3600s
advance:  current, abc123 already matches origin/main (fetched fresh)
S
T=$(run_out)
grep -q "advance:.*\[STALE" <<<"$T" \
  && ok "19b: a status past the staleness threshold is reported STALE" \
  || bad "19b: a stale status reads STALE" "$T"
j=$(run_out --json)
chk "19b: --json flips advance_stale true" "true" "$(jq -r '.watchdog.advance_stale' <<<"$j")"
# Tolerance, not exact match: the test's iso_ago() and digest.sh's own
# iso_to_epoch/`date -u +%s` each call the wall clock independently, so a
# second can tick between them. An exact-equality assertion here flaked
# 5 of 6 local runs (off-by-one) -- widened per PR #95 review.
age_s=$(jq -r '.watchdog.advance_age_s' <<<"$j")
[ "$age_s" -ge 4199 ] 2>/dev/null && [ "$age_s" -le 4201 ] \
  && ok "19b: --json carries the actual age, not a rounded/guessed one" \
  || bad "19b: --json carries the actual age, not a rounded/guessed one" "want 4199..4201, got '$age_s'"

# 19c. A status written seconds ago reads as current, without a nagging age
# qualifier that would train readers to ignore it -- only genuinely stale
# lines get the STALE marker.
now_checked=$(iso_ago 0)
cat > "$OUT/watchdog.status" <<S
checked:  $now_checked
state:    working
restarts: 0 in the last 3600s
advance:  current, abc123 already matches origin/main (fetched fresh)
S
T=$(run_out)
grep -q "advance:.*\[STALE" <<<"$T" && bad "19c: a just-written status is not marked STALE" "$T" \
  || ok "19c: a just-written status is not marked STALE"
# Tolerance, not exact match: iso_ago(0) and digest.sh's own age read
# (iso_to_epoch/`date -u +%s`) are two independent wall-clock reads: the
# first when this test writes `checked:`, the second when digest.sh renders
# the advance line. A second can tick between them -- observed directly, see
# the commit that introduced this comment for the measured value -- so this
# accepts 0s or 1s. The same class of race is why 19b (above) already uses a
# tolerance window instead of an exact match.
grep -qE "advance:.*\(as of .*, (0s|1s) ago\)" <<<"$T" \
  && ok "19c: a just-written status still carries a plain, unalarming age" \
  || bad "19c: a just-written status carries a plain age" "$T"

# MUTATION: point digest.sh's age computation at a `checked:` field that
# never resolves (iso_to_epoch always fails), the same shape as the pre-fix
# code, which never read an age for the advance: line at all. Confirms 19a/
# 19b are actually pinned to that computation, not to some other path to
# the strings above.
MUT_DIGEST2="$D/digest-no-age.sh"
python3 - "$DIGEST" "$MUT_DIGEST2" <<'PY'
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
marker = 'if wd_checked_epoch=$(iso_to_epoch "$wd_checked"); then'
assert marker in text, "advance-age branch not found -- digest.sh shape changed"
text = text.replace(marker, 'if false; then', 1)
open(dst, "w").write(text)
PY
cat > "$OUT/watchdog.status" <<S
checked:  $stale_checked
state:    working
restarts: 0 in the last 3600s
advance:  current, abc123 already matches origin/main (fetched fresh)
S
mut_out=$(PATH="$D/bin:$PATH" SUPERVISOR_STATE="$OUT" LANES_SESSION=nosuch bash "$MUT_DIGEST2" 2>/dev/null)
if grep -q "advance:.*\[STALE" <<<"$mut_out"; then
  bad "mutation confirmed: disabling the age computation still reports STALE" "$mut_out"
else
  ok "mutation confirmed: disabling the age computation lets an hour-old 'current' replay through unmarked (19b would be red)"
fi

# agent-supervisor#251: `gh` calls in this file used to run with no timeout
# guard at all. Reproduced live: `state.sh` (which calls this script) hung
# past 120s inside this exact call, zero output even to stderr, requiring
# `kill -9`. A short DIGEST_GH_TIMEOUT_SECONDS proves the bound is real --
# a `gh` that sleeps far longer than the timeout must still let this script
# return, with the failure named as a timeout (not a plain "failed", which
# would read identically to a fast, ordinary API error).
#
# Scoped to `pr list` only (mirrors the author-lane stub below, which fails
# fast -- `exit 1` -- on anything it does not recognize). A stub that sleeps
# on EVERY invocation regardless of subcommand also caught digest.sh's later,
# unrelated `acceptance.sh --limit 15` call (its own `gh issue list`, a
# pre-existing and still-unbounded shell-out this PR never touched) in the
# same 30s sleep, which is what made this case fail: the gh_call bound this
# PR adds DID fire correctly at 2s (confirmed via `bash -x` trace), but the
# suite's own overly-broad stub kept the process busy well past that on a
# call this test was never trying to exercise.
cat > "$D/bin/gh" <<'EOF'
#!/bin/bash
if [ "$1" = "api" ]; then
  case "$2" in
    *pulls?state=open*) sleep 30; echo '[]'; exit 0 ;;
  esac
fi
exit 1
EOF
chmod +x "$D/bin/gh"
start=$(date +%s)
out=$(PATH="$D/bin:$PATH" SUPERVISOR_STATE="$D/state" LANES_SESSION=nosuch DIGEST_GH_TIMEOUT_SECONDS=2 \
  DIGEST_REPOS=agent-supervisor timeout 20 bash "$DIGEST" 2>&1)
elapsed=$(( $(date +%s) - start ))
[ "$elapsed" -lt 15 ] && ok "a hanging gh does not hang digest.sh (returned in ${elapsed}s)" \
  || bad "hanging gh bounded" "took ${elapsed}s, expected well under the 20s hard test timeout"
grep -q "gh pr list failed for jonhill90/agent-supervisor.*timed out after 2s" <<<"$out" && \
  ok "a hanging gh pr list is named as a timeout, not a plain failure" \
  || bad "hanging gh named as timeout" "$out"

# agent-supervisor#251 (this PR's own CI failure): `author_lane_for`'s `gh pr
# view --json headRefName,closingIssuesReferences,commits` call ran with NO
# bound at all -- the one `gh` call digest.sh's own PR loop makes that never
# went through `gh_call`/`with_timeout`, because `author_lane_for` lives in
# verdict-independence.sh and calls `gh` directly. Reproduced live:
# tests/supervisor/test_shell_suites.py's own harness sent SIGTERM then
# SIGKILL to this suite's whole process group after a 300s timeout and still
# could not confirm the group dead -- a `gh` blocked forever is exactly that
# shape. `pr list` and the mergeable/`run list` calls all answer fast here;
# ONLY the author-lane lookup hangs, so this is the hang case specifically,
# not a repeat of the gh-list case above.
cat > "$D/bin/gh" <<'EOF'
#!/bin/bash
if [ "$1 $2" = "pr view" ]; then
  fields=""; prev=""
  for a in "$@"; do
    [ "$prev" = "--json" ] && fields="$a"
    prev="$a"
  done
  case "$fields" in
    *closingIssuesReferences*) sleep 30; echo '{"headRefName":"","closingIssuesReferences":[],"commits":[]}'; exit 0 ;;
    # A DECISIVE comment verdict (not "none") -- independence_verdict only
    # ever consults author_lane_for's own detail on this branch (a
    # lane-stamped comment/ledger verdict whose author lane turns out
    # unresolved); the far more common "none" (never reviewed) case
    # deliberately drops it (#184), so a never-reviewed fixture here would
    # prove the bound without ever proving the timeout is NAMED.
    *) echo '{"reviews":[],"comments":[{"author":{"login":"jonhill90"},"body":"**Verdict: APPROVE**\nReview-Lane: t:9\nReviewed-SHA: sha1sha1sha1sha1sha1sha1sha1sha1sha1sha1","createdAt":"2026-08-16T00:00:00Z"}]}'; exit 0 ;;
  esac
fi
if [ "$1" = "api" ]; then
  case "$2" in
    *pulls?state=open*) echo '[{"number":1,"title":"t","head":{"sha":"sha1sha1sha1sha1sha1sha1sha1sha1sha1sha1","ref":"b1"}}]' ;;
    *pulls/1) echo '{"mergeable_state":"clean"}' ;;
    *) echo '[]' ;;
  esac
  exit 0
fi
if [ "$1 $2" = "run list" ]; then echo '[]'; exit 0; fi
exit 1
EOF
chmod +x "$D/bin/gh"
start=$(date +%s)
out=$(PATH="$D/bin:$PATH" SUPERVISOR_STATE="$D/state" LANES_SESSION=nosuch AUTHOR_LANE_GH_TIMEOUT_SECONDS=2 \
  DIGEST_REPOS=agent-supervisor timeout 20 bash "$DIGEST" 2>&1)
elapsed=$(( $(date +%s) - start ))
[ "$elapsed" -lt 15 ] && ok "a hanging author-lane gh pr view does not hang digest.sh (returned in ${elapsed}s)" \
  || bad "hanging author-lane gh bounded" "took ${elapsed}s, expected well under the 20s hard test timeout"
grep -q "gh pr view timed out after 2s" <<<"$out" && \
  ok "a hanging author-lane gh pr view is named as a timeout, not a plain failure" \
  || bad "hanging author-lane gh named as timeout" "$out"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
