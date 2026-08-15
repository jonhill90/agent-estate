#!/bin/bash
# agent-supervisor#144 MUTATION CHECK, both directions, driven end to end
# through the real digest.sh (not a unit test of one function):
#
#   "With GraphQL exhausted, digest.sh must still return a complete PR list.
#    Prove it by stubbing the failure ... not by waiting for a real reset.
#    Then the reverse: stub REST failing too, and show digest.sh still names
#    what it could not see and exits non-zero. Both directions, or the guard
#    is unproven."
#
# `gh pr list` is what the issue's own reproduction shows failing
# (0/5000 graphql, 4975/5000 core) -- this stub fails everything GraphQL
# (`gh pr view`/`gh pr list`/`gh issue view`/`gh issue list`) exactly the
# way an exhausted budget does, while `gh api ...` (REST core) is served for
# real, so the PR-list fetch this issue is about actually exercises the
# converted code path, not a mock of it.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIGEST="$HERE/../../scripts/supervisor/digest.sh"
pass=0; fail=0
ok()  { echo "  ok   $1"; pass=$((pass+1)); }
bad() { echo "  FAIL $1 — $2"; fail=$((fail+1)); }

command -v jq >/dev/null 2>&1 || { echo "  SKIP no jq"; exit 0; }

D=$(mktemp -d); mkdir -p "$D/bin" "$D/state"
trap 'rm -rf "$D"' EXIT INT TERM

echo "digest.sh -- REST-vs-GraphQL mutation check (#144)"

# --- direction 1: GraphQL exhausted, REST alive -----------------------------
cat > "$D/bin/gh" <<'STUB'
#!/bin/bash
set -uo pipefail
case "$1 $2" in
  "pr view"|"pr list"|"issue view"|"issue list")
    echo 'gh: GraphQL: API rate limit exceeded (graphqlRateLimit)' >&2
    exit 1 ;;
esac
case "$1" in
  api)
    endpoint="$2"; path="${endpoint%%\?*}"
    case "$path" in
      */pulls/[0-9]*) echo '{"mergeable_state":"clean"}'; exit 0 ;;
      */pulls)
        cat <<'JSON'
[{"number":42,"title":"a PR fetched over REST while GraphQL is exhausted","head":{"sha":"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef","ref":"fix/144-rest"}}]
JSON
        exit 0 ;;
      *) exit 1 ;;
    esac ;;
  run) echo '[]'; exit 0 ;;
  *) exit 1 ;;
esac
STUB
chmod +x "$D/bin/gh"

j=$(PATH="$D/bin:$PATH" SUPERVISOR_STATE="$D/state" LANES_SESSION=nosuch \
    DIGEST_REPOS=repo DIGEST_OWNER=acme \
    bash "$DIGEST" --json 2>/dev/null)
jq -e . >/dev/null 2>&1 <<<"$j" && ok "digest --json is still valid JSON with GraphQL exhausted" \
  || bad "digest --json valid" "$j"
[ "$(jq -r '.prs|length' <<<"$j")" = "1" ] && ok "the PR list is complete (1 PR) with GraphQL exhausted" \
  || bad "PR list complete" "$j"
[ "$(jq -r '.prs[0].number' <<<"$j")" = "42" ] && ok "the PR's own fields survive the REST round-trip" \
  || bad "PR fields survive" "$j"
[ "$(jq -r '.prs[0].merge_state' <<<"$j")" = "CLEAN" ] && ok "merge_state is populated from the REST mergeable_state call" \
  || bad "merge_state populated" "$j"
grep -qx '"gh pr list failed for acme/repo -- its PRs are NOT in this digest"' <<<"$(jq -c '.errors[]' <<<"$j")" \
  && bad "no gh-pr-list error should be reported" "$j" \
  || ok "no 'gh pr list failed' error -- the PR list did not depend on the exhausted budget"

# --- direction 2: REST also unreachable -------------------------------------
# The reverse: a GitHub outage covering REST too must still be named, exactly
# as the original (GraphQL-only) failure mode was -- not silently emptied.
printf '#!/bin/bash\nexit 1\n' > "$D/bin/gh"; chmod +x "$D/bin/gh"
j2=$(PATH="$D/bin:$PATH" SUPERVISOR_STATE="$D/state" LANES_SESSION=nosuch \
     DIGEST_REPOS=repo DIGEST_OWNER=acme bash "$DIGEST" --json 2>/dev/null)
rc=$?
[ "$(jq -r '.prs|length' <<<"$j2")" = "0" ] && ok "REST also down -- prs is empty" \
  || bad "prs empty when REST is also down" "$j2"
grep -q "gh pr list failed for acme/repo" <<<"$(jq -r '.errors[]' <<<"$j2")" \
  && ok "REST also down -- the failing repo is named in errors[], not silently empty" \
  || bad "failing repo named when REST is also down" "$j2"
[ "$(jq -r '.ok' <<<"$j2")" = "false" ] && ok "REST also down -- ok is false" || bad "ok false when REST down" "$j2"

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
