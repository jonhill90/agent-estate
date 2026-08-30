#!/bin/bash
# agent-supervisor#115: digest.sh's lane_models section. lanes.sh already
# proves the model READ itself (test_lanes_model.sh); this proves digest.sh
# correctly REPORTS what lanes.sh handed it -- every lane, always, `unknown`
# never omitted, a worker lane on the wrong known model flagged as an ERROR
# (report, not enforce -- nothing here kills or restarts a lane), and the
# Director's own `supervisor` state exempted rather than flagged.
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

printf '#!/bin/bash\necho "[]"\n' > "$D/bin/gh"; chmod +x "$D/bin/gh"
cat > "$D/state/watchdog.status" <<'S'
checked:  2026-08-12T00:00:00Z
state:    asleep
restarts: 0 in the last 3600s
S
cat > "$D/state/inbox-poll.status" <<'S'
checked: 2026-08-12T00:00:00Z
state:   ok
S

# A lane on the wrong model (the #115 regression itself), a lane correctly
# on the expected model, a lane whose model this probe could not read
# (unknown -- must NOT be flagged), and the Director's own `supervisor`
# lane on Opus (must be exempt, not flagged, per the issue's rule 2).
cat > "$D/bin/lanes" <<'S'
#!/bin/bash
case "${1:-}" in
  --json)
    cat <<'JSON'
[
  {"window":1,"window_id":"@101","name":"supervisor","command":"claude.exe","state":"supervisor","idle_seconds":5,"model":"opus"},
  {"window":2,"window_id":"@102","name":"free-2","command":"claude.exe","state":"busy","idle_seconds":5,"model":"opus"},
  {"window":3,"window_id":"@103","name":"free-3","command":"claude.exe","state":"free","idle_seconds":5,"model":"sonnet"},
  {"window":4,"window_id":"@104","name":"free-4","command":"claude.exe","state":"busy","idle_seconds":900,"model":"unknown"}
]
JSON
    ;;
  *)
    printf 'WINDOW NAME COMMAND STATE\n'
    printf '1      supervisor claude.exe supervisor\n'
    printf '2      free-2 claude.exe busy\n'
    printf '3      free-3 claude.exe free\n'
    printf '4      free-4 claude.exe busy\n'
    ;;
esac
S
chmod +x "$D/bin/lanes"

run() { PATH="$D/bin:$PATH" SUPERVISOR_STATE="$D/state" LANES_SESSION=t \
  DIGEST_LANES_BIN="$D/bin/lanes" bash "$DIGEST" "$@" 2>/dev/null; }

echo "digest.sh: lane_models (#115)"

j=$(run --json)
jq -e . >/dev/null 2>&1 <<<"$j" && ok "--json is valid JSON" || bad "--json valid" "$j"

chk "lane_models.expected defaults to sonnet" "sonnet" "$(jq -r '.lane_models.expected' <<<"$j")"

# --- the regression itself: window 2 is a WORKER on opus -> flagged --------
chk "a worker lane on the wrong known model is flagged" \
  "true" "$(jq -r '.lane_models.lanes[] | select(.window==2) | .flagged' <<<"$j")"
chk "the flagged lane still reports its real model, not a placeholder" \
  "opus" "$(jq -r '.lane_models.lanes[] | select(.window==2) | .model' <<<"$j")"

# --- correctly-modeled worker is not flagged --------------------------------
chk "a worker lane already on the expected model is not flagged" \
  "false" "$(jq -r '.lane_models.lanes[] | select(.window==3) | .flagged' <<<"$j")"

# --- unknown is a real state, never flagged as a mismatch -------------------
chk "an unreadable model is reported unknown, not omitted" \
  "unknown" "$(jq -r '.lane_models.lanes[] | select(.window==4) | .model' <<<"$j")"
chk "an unreadable model is NEVER flagged as a mismatch (fail closed, not a guess)" \
  "false" "$(jq -r '.lane_models.lanes[] | select(.window==4) | .flagged' <<<"$j")"

# --- the Director's own lane is exempt, not flagged, per #115's rule 2 -----
chk "the Directors own supervisor-state lane is exempt from flagging despite running opus" \
  "false" "$(jq -r '.lane_models.lanes[] | select(.window==1) | .flagged' <<<"$j")"
chk "the exempt lane still carries its real model rather than being hidden" \
  "opus" "$(jq -r '.lane_models.lanes[] | select(.window==1) | .model' <<<"$j")"

# --- every lane appears, none silently dropped ------------------------------
chk "all four lanes are present in lane_models" "4" "$(jq -r '.lane_models.lanes | length' <<<"$j")"

# --- report, not enforce: a flagged mismatch is a normal digest ERROR ------
chk "a flagged mismatch surfaces as a digest error" \
  "true" "$(jq -r '[.errors[] | select(startswith("lane model:"))] | length > 0' <<<"$j")"
grep -q "lane model:.*opus.*expected sonnet" <<<"$(jq -r '.errors[]' <<<"$j")" \
  && ok "the error names the actual model AND the expected one" \
  || bad "error names actual+expected" "$(jq -r '.errors[]' <<<"$j")"
chk "ok flips false when a lane model is flagged" "false" "$(jq -r '.ok' <<<"$j")"

# --- nothing here acts on the mismatch: no kill/restart tooling is called --
if grep -qE '(kill-session|kill-window|respawn-)' "$HERE/../../scripts/supervisor/digest.sh"; then
  bad "digest.sh must never kill/restart a lane over a model mismatch" \
    "found tmux kill/respawn call in digest.sh"
else
  ok "digest.sh contains no kill/respawn call -- report only, never enforce"
fi

# --- human-readable text carries the same information ----------------------
T=$(run)
grep -q "models:   expected=sonnet" <<<"$T" && ok "text mode names the expected model" \
  || bad "text mode expected model" "$T"
grep -qE '2 \(free-2\) model=opus \[!= sonnet\]' <<<"$T" && ok "text mode flags the mismatched lane inline" \
  || bad "text mode flags mismatch" "$T"
grep -qE '3 \(free-3\) model=sonnet$' <<<"$T" && ok "text mode shows a matching lane with no marker" \
  || bad "text mode matching lane" "$T"
grep -qE '4 \(free-4\) model=unknown$' <<<"$T" && ok "text mode shows unknown in place, not blank" \
  || bad "text mode unknown lane" "$T"
grep -qE '1 \(supervisor\) model=opus$' <<<"$T" && ok "text mode shows the exempt lane without a marker" \
  || bad "text mode exempt lane" "$T"

# --- DIGEST_EXPECTED_MODEL is overridable, e.g. a lane the estate genuinely
# wants on a non-default model -------------------------------------------
j2=$(PATH="$D/bin:$PATH" SUPERVISOR_STATE="$D/state" LANES_SESSION=t \
  DIGEST_LANES_BIN="$D/bin/lanes" DIGEST_EXPECTED_MODEL=opus bash "$DIGEST" --json 2>/dev/null)
chk "DIGEST_EXPECTED_MODEL overrides the sonnet default" "opus" "$(jq -r '.lane_models.expected' <<<"$j2")"
chk "under that override, the opus worker is no longer flagged" \
  "false" "$(jq -r '.lane_models.lanes[] | select(.window==2) | .flagged' <<<"$j2")"
chk "and the sonnet worker becomes the flagged one" \
  "true" "$(jq -r '.lane_models.lanes[] | select(.window==3) | .flagged' <<<"$j2")"

# --- mutation check: prove the flagged assertions are anchored on real -----
# comparison logic, not passing for an unrelated reason. Break the
# comparison so nothing is ever flagged, and confirm the window-2 assertion
# (a real, live mismatch) goes red.
MUT="$D/digest-mut.sh"
sed 's/\$m != \$expected/false/' "$HERE/../../scripts/supervisor/digest.sh" > "$MUT"
chmod +x "$MUT"
# agent-supervisor#682: digest.sh sources session-defaults.sh (for the shared
# AGENT_SUPERVISOR_DEFAULT_REPO default) from its own $HERE -- copying digest.sh
# out to $D without it left AGENT_SUPERVISOR_DEFAULT_REPO unbound under `set -u`,
# so the mutated script died before ever reaching the jq comparison it was meant
# to exercise (empty $mut_j, not a neutralized-but-still-empty flagged field).
cp "$HERE/../../scripts/supervisor/session-defaults.sh" "$D/session-defaults.sh"
mut_j=$(PATH="$D/bin:$PATH" SUPERVISOR_STATE="$D/state" LANES_SESSION=t \
  DIGEST_LANES_BIN="$D/bin/lanes" bash "$MUT" --json 2>/dev/null)
mut_flag=$(jq -r '.lane_models.lanes[] | select(.window==2) | .flagged' <<<"$mut_j")
if [ "$mut_flag" = "false" ]; then
  ok "mutation confirmed: disabling the model comparison un-flags the real mismatch (the assertion above would be red)"
else
  bad "mutation should have disabled flagging" "still got flagged=$mut_flag"
fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
