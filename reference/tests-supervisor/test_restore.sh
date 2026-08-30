#!/bin/bash
# restore.sh must bring every lane back after a tmux server loss -- from the
# ledger, never from a window name -- and must REFUSE any lane it cannot
# positively identify rather than start a fresh agent in its place.
#
# agent-dotfiles#237. Everything here runs on a PRIVATE tmux socket
# (`tmux -L ad237-test-$$`) with its own $HOME, its own state dir and a stub
# harness. The live `agent-dotfiles` session is never touched: this suite
# kills a tmux server on purpose, which is the exact loss the issue was filed
# about, and doing that to the estate to demonstrate a recovery would cost
# more than the recovery saves.
#
# The stub harness is a real limit and is stated rather than papered over: the
# pane runs `$D/bin/claude`, which records its argv and execs `sleep`, so what
# this suite proves is that restore hands the RIGHT SESSION ID to the harness
# for the right lane under the right name. That the id then rehydrates a
# conversation is Claude Code's own contract (`--resume <session id>`), and it
# is exercised against the real binary in the PR's live demonstration, not
# here -- a test suite that launched nine real agents would be neither fast
# nor hermetic.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
pass=0; fail=0
ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; shift; [ $# -gt 0 ] && sed 's/^/       /' <<<"$*"; fail=$((fail+1)); }
want() { # want <name> <regex> <output>
  if grep -qE "$2" <<<"$3"; then ok "$1"; else bad "$1 — no /$2/ in:" "$3"; fi
}
wantnot() {
  if grep -qE "$2" <<<"$3"; then bad "$1 — unexpected /$2/ in:" "$3"; else ok "$1"; fi
}

SOCKET="ad237-test-$$"
# The REAL tmux, resolved before this suite puts its own wrapper on PATH. The
# wrapper must exec this absolute path: `env tmux` would find the wrapper
# again and recurse forever.
REAL_TMUX=$(command -v tmux) || { echo "  SKIP no tmux on PATH"; exit 0; }
D=$(mktemp -d)
cleanup() { "$REAL_TMUX" -L "$SOCKET" kill-server 2>/dev/null; rm -rf "$D"; }
trap cleanup EXIT

mkdir -p "$D/bin" "$D/repo" "$D/home/.claude/projects/lanes"
# Every tmux call in this suite AND inside restore.sh goes to the private
# socket, because this wrapper is what `tmux` resolves to on PATH.
cat > "$D/bin/tmux" <<EOF
#!/bin/bash
exec "$REAL_TMUX" -L "$SOCKET" "\$@"
EOF
# The stub harness. `exec sleep` matters: tmux reports \`sleep\` as the pane's
# current command, which is not one of the shells restore.sh treats as
# wreckage -- so a restored lane reads as running an agent, exactly as a real
# one would, and restore.sh's "already live, leave it alone" branch is
# genuinely exercised rather than stubbed around.
cat > "$D/bin/claude" <<EOF
#!/bin/bash
# agent-supervisor#172: the cwd this was launched in matters as much as the
# argv now -- \`claude --resume\` is scoped to it -- so it goes on the front
# of the line, ahead of argv, rather than in a second file nothing here
# already greps. Only when args are present: \`start_estate\` below types a
# bare \`claude\` (no args at all) to stand in for the already-running agent
# a real pre-kill pane would show, and every "nothing was started" assertion
# in this suite depends on THAT launch logging an empty line, unchanged from
# before this column existed.
if [ "\$#" -gt 0 ]; then echo "\$PWD :: \$@" >> "$D/launched.log"; else echo >> "$D/launched.log"; fi
exec sleep 600
EOF
# The codex stub, sibling of the claude one above -- agent-supervisor#(codex
# adapter gap, 2026-08-23). Same recording contract (`$PWD :: argv` on a real
# launch, a bare empty line for the already-running stand-in `start_estate`
# types), so the same assertions this suite already makes against `claude`'s
# launched.log apply unchanged to a codex row.
cat > "$D/bin/codex" <<EOF
#!/bin/bash
if [ "\$#" -gt 0 ]; then echo "\$PWD :: \$@" >> "$D/launched.log"; else echo >> "$D/launched.log"; fi
exec sleep 600
EOF
chmod +x "$D/bin/tmux" "$D/bin/claude" "$D/bin/codex"
export PATH="$D/bin:$PATH"
export HOME="$D/home"
export AGENT_SUPERVISOR_STATE_DIR="$D/state"

# Three fixture conversations on disk, each carrying its own marker -- these
# stand in for `~/.claude/projects/<slug>/<session-id>.jsonl`, the files that
# survived the real incident untouched while every pane died.
LANE2_ID="11111111-1111-4111-8111-111111111111"
LANE3_ID="22222222-2222-4222-8222-222222222222"
for pair in "$LANE2_ID:ad901-first" "$LANE3_ID:ad902-second"; do
  id="${pair%%:*}"; marker="${pair##*:}"
  printf '{"type":"mode","sessionId":"%s"}\n{"type":"user","sessionId":"%s","timestamp":"2026-08-12T00:00:00.000Z","message":"brief for %s"}\n' \
    "$id" "$id" "$marker" > "$HOME/.claude/projects/lanes/$id.jsonl"
done

record() { # record <lane> <task> <harness-session-id> [harness-project-dir]
  # agent-supervisor#172: a real dispatch always records the two together
  # (dispatch.sh resolves both from the same pane read) -- default the
  # fourth argument to the working repo, but ONLY when a session id was
  # actually resolved ($3 non-empty), so every EXISTING caller below keeps
  # exercising "the common case where they coincide" unchanged, and an
  # unresolved-session call stays an unresolved-session call rather than
  # gaining a project dir with no session id to pair it with. A caller that
  # passes a fourth argument explicitly (including "") always wins.
  local project_dir
  if [ $# -ge 4 ]; then
    project_dir="$4"
  elif [ -n "$3" ]; then
    project_dir="$D/repo"
  else
    project_dir=""
  fi
  python3 "$SUP/cli.py" record-dispatch \
    --lane "$1" --task "$2" --summary "worktree=$D/repo; brief=$D/brief.md" \
    --pane-id "%$RANDOM" --pane-path "$D/repo" --command claude --harness claude \
    --server-id "socket:1" --session-id "\$0" --harness-session-id "$3" \
    --harness-project-dir "$project_dir" \
    --issue 901 --github jonhill90/agent-dotfiles >/dev/null
}

start_estate() { # a live session with three lanes, two of them working
  "$REAL_TMUX" -L "$SOCKET" kill-server 2>/dev/null
  tmux new-session -d -s ad237test -c "$D/repo" -n supervisor
  for w in 2 3 4; do tmux new-window -d -t "=ad237test:$w" -c "$D/repo"; done
  tmux rename-window -t "=ad237test:2" ad901-first
  tmux rename-window -t "=ad237test:3" ad902-second
  tmux rename-window -t "=ad237test:4" free-4
  for w in 2 3 4; do tmux send-keys -t "=ad237test:$w" "claude" Enter; done
  sleep 1
}

echo "restore.sh (agent-dotfiles#237)"

# ---------------------------------------------------------------- RED -------
# The recording half is what makes the restore possible, so the red state is
# a ledger written the way EVERY dispatch before #237 wrote one: lanes, tasks,
# panes, tmux session ids -- and no harness session id anywhere. Kill the
# server with those lanes mid-task and nothing in this repository can bring
# the conversations back.
rm -rf "$D/state"; : > "$D/launched.log"
record ad237test:2 ad901-first ""
record ad237test:3 ad902-second ""
start_estate
before=$(tmux list-windows -t ad237test -F '#{window_index} #{window_name}' 2>&1)
want "setup: three lanes are live before the kill" "2 ad901-first" "$before"
"$REAL_TMUX" -L "$SOCKET" kill-server 2>/dev/null
sleep 0.5
gone=$(tmux has-session -t ad237test 2>&1; echo "rc=$?")
want "RED: the tmux server is gone and so is every pane" "rc=[^0]" "$gone"
red=$(bash "$SUP/restore.sh" 2>&1); redrc=$?
want "RED: a pre-#237 ledger cannot restore lane 2"  "ad237test:2 +UNRECOVERABLE.*no harness session id" "$red"
want "RED: a pre-#237 ledger cannot restore lane 3"  "ad237test:3 +UNRECOVERABLE.*no harness session id" "$red"
if [ "$redrc" = 2 ]; then ok "RED: restore exits 2 when a lane cannot be brought back"; else bad "RED: exit was $redrc, not 2" "$red"; fi
wantnot "RED: and it started NOTHING in their place" "." "$(cat "$D/launched.log")"

# -------------------------------------------------------------- GREEN -------
# The same loss, against a ledger that recorded what the harness held.
rm -rf "$D/state"; : > "$D/launched.log"
record ad237test:2 ad901-first "$LANE2_ID"
record ad237test:3 ad902-second "$LANE3_ID"
start_estate
"$REAL_TMUX" -L "$SOCKET" kill-server 2>/dev/null
sleep 0.5
green=$(bash "$SUP/restore.sh" 2>&1); greenrc=$?
sleep 1
want "GREEN: lane 2 is restored"                  "ad237test:2 +RESTORED" "$green"
want "GREEN: lane 3 is restored"                  "ad237test:3 +RESTORED" "$green"
if [ "$greenrc" = 0 ]; then ok "GREEN: restore exits 0"; else bad "GREEN: exit was $greenrc, not 0" "$green"; fi

launched=$(cat "$D/launched.log")
if grep -qF -- "--resume $LANE2_ID" <<<"$launched"; then ok "GREEN: lane 2 resumed its own conversation ($LANE2_ID)"; else bad "GREEN: lane 2 did not resume $LANE2_ID" "$launched"; fi
if grep -qF -- "--resume $LANE3_ID" <<<"$launched"; then ok "GREEN: lane 3 resumed its own conversation ($LANE3_ID)"; else bad "GREEN: lane 3 did not resume $LANE3_ID" "$launched"; fi
# The ids are not interchangeable: each transcript carries the marker of the
# lane that owns it, so resuming the wrong one would be a wrong conversation
# under a right-looking name -- the failure #237 exists to prevent.
want "GREEN: $LANE2_ID is the transcript naming ad901-first" \
  "brief for ad901-first" "$(cat "$HOME/.claude/projects/lanes/$LANE2_ID.jsonl")"

names=$(tmux list-windows -t ad237test -F '#{window_index} #{window_name} #{pane_current_command}' 2>&1)
want "GREEN: lane 2 came back under its ledger name"  "^2 ad901-first" "$names"
want "GREEN: lane 3 came back under its ledger name"  "^3 ad902-second" "$names"
want "GREEN: and it is running an agent, not a shell" "^2 ad901-first sleep" "$names"

# ------------------------------------------------------- IDEMPOTENCE --------
# The second run must be a no-op. This is the half of #237 that is not about
# recovery at all: the incident's SECOND loss came from re-running a recovery
# over lanes that were already back, killing them mid-task.
pid_before=$(tmux display-message -p -t "=ad237test:2" '#{pane_pid}')
again=$(bash "$SUP/restore.sh" 2>&1); againrc=$?
pid_after=$(tmux display-message -p -t "=ad237test:2" '#{pane_pid}')
want "IDEMPOTENT: a live lane is reported LIVE, not restored" "ad237test:2 +LIVE" "$again"
if [ "$pid_before" = "$pid_after" ]; then ok "IDEMPOTENT: the live pane was not replaced"; else bad "IDEMPOTENT: pane pid changed $pid_before -> $pid_after"; fi
if [ "$againrc" = 0 ]; then ok "IDEMPOTENT: second run exits 0"; else bad "IDEMPOTENT: exit was $againrc" "$again"; fi

# ----------------------------------------------------------- MUTATION -------
# Corrupt ONE session id and confirm restore reports that lane unrecoverable
# instead of silently starting a fresh agent wearing its name. The other lane
# must still come back, or this check would pass for the wrong reason.
rm -rf "$D/state"; : > "$D/launched.log"
record ad237test:2 ad901-first "deadbeef-dead-4bee-8bee-deadbeefdead"
record ad237test:3 ad902-second "$LANE3_ID"
start_estate
"$REAL_TMUX" -L "$SOCKET" kill-server 2>/dev/null
sleep 0.5
mut=$(bash "$SUP/restore.sh" 2>&1); mutrc=$?
sleep 1
want "MUTATION: the corrupted lane is UNRECOVERABLE" \
  "ad237test:2 +UNRECOVERABLE.*no transcript on disk" "$mut"
want "MUTATION: the intact lane still comes back"     "ad237test:3 +RESTORED" "$mut"
if [ "$mutrc" = 2 ]; then ok "MUTATION: restore exits 2"; else bad "MUTATION: exit was $mutrc, not 2" "$mut"; fi
mutlaunched=$(cat "$D/launched.log")
wantnot "MUTATION: no agent was started for the corrupted lane" "deadbeef" "$mutlaunched"
if [ "$(grep -c . <<<"$mutlaunched")" = 1 ]; then ok "MUTATION: exactly one agent started, and it is the intact lane"; else bad "MUTATION: wrong number of agents started" "$mutlaunched"; fi
mutnames=$(tmux list-windows -t ad237test -F '#{window_index} #{window_name} #{pane_current_command}' 2>&1)
wantnot "MUTATION: nothing wears the corrupted lane's name with a fresh agent" "^2 ad901-first sleep" "$mutnames"

# A missing id and a corrupt one must fail the same way -- refusing only on
# the empty string would let any garbage through.
rm -rf "$D/state"; : > "$D/launched.log"
record ad237test:2 ad901-first "not-a-uuid-at-all"
nomatch=$(bash "$SUP/restore.sh" --dry-run 2>&1)
want "MUTATION: a non-uuid id is refused too, not passed to the harness" \
  "UNRECOVERABLE.*no transcript on disk" "$nomatch"


# ---------------------------------------------- ORIGINATING PROJECT DIR -----
# agent-supervisor#172. `claude --resume` is scoped to the directory the
# harness process actually LAUNCHED in, not the lane's WORKING directory
# (`repo`, a worktree rewritten on every dispatch) -- and until now this
# script only ever knew the latter. This is the case that must go RED on
# unpatched `restore.sh`: `$D/other-repo` stands in for a lane recorded
# before a project-directory migration (agent-dotfiles -> agent-supervisor is
# the real one), where `repo` was rewritten to the new worktree but the
# running `claude` process -- and the transcript `--resume` needs -- still
# belongs to the OLD directory. A `restore.sh` that resumes from `repo`
# (the pre-#172 shape) launches into the wrong directory silently; only
# checking WHERE the resume command was actually sent from can catch that,
# which is why the stub harness now records `$PWD`.
mkdir -p "$D/other-repo"
rm -rf "$D/state"; : > "$D/launched.log"
record ad237test:2 ad901-first "$LANE2_ID" "$D/other-repo"
start_estate
"$REAL_TMUX" -L "$SOCKET" kill-server 2>/dev/null
sleep 0.5
projdir=$(bash "$SUP/restore.sh" 2>&1); projdirrc=$?
sleep 1
want "PROJECT DIR: lane 2 is restored"          "ad237test:2 +RESTORED" "$projdir"
if [ "$projdirrc" = 0 ]; then ok "PROJECT DIR: restore exits 0"; else bad "PROJECT DIR: exit was $projdirrc, not 0" "$projdir"; fi
projlaunched=$(cat "$D/launched.log")
# THE point: launched from the ORIGINATING directory, not the working repo --
# a restore.sh that still used `repo` here would show "$D/repo :: ..." instead.
want "PROJECT DIR: resumed from the originating directory, not the working repo" \
  "^$D/other-repo :: .*--resume $LANE2_ID" "$projlaunched"
wantnot "PROJECT DIR: never resumed from the working repo instead" \
  "^$D/repo :: .*--resume" "$projlaunched"

# ------------------------------------- PROJECT DIR ABSENT (pre-#172 row) ----
# A row with a real, resolvable session id but NO recorded originating
# directory -- the migration shape every lane dispatched before this issue
# has. The ratchet this issue names explicitly: ambiguity here must make the
# lane look LESS recoverable, never more -- so this must NOT fall back to
# `repo` (that fallback is the bug), and must NOT crash. It refuses, and the
# refusal must be tellable apart from "no session id recorded" (the other
# UNRECOVERABLE reason) -- so the message must both say a directory was
# never recorded AND name `repo`, the one thing on hand it explicitly
# refuses to guess with.
rm -rf "$D/state"; : > "$D/launched.log"
record ad237test:2 ad901-first "$LANE2_ID" ""
start_estate
"$REAL_TMUX" -L "$SOCKET" kill-server 2>/dev/null
sleep 0.5
noproj=$(bash "$SUP/restore.sh" 2>&1); noprojrc=$?
want "NO PROJECT DIR: refused, not silently resumed from the working repo" \
  "ad237test:2 +UNRECOVERABLE.*no originating project directory" "$noproj"
want "NO PROJECT DIR: the refusal names the working repo it refused to guess with" \
  "UNRECOVERABLE.*no originating project directory.*$D/repo" "$noproj"
wantnot "NO PROJECT DIR: distinct from the 'no session id' refusal" \
  "no harness session id" "$noproj"
if [ "$noprojrc" = 2 ]; then ok "NO PROJECT DIR: restore exits 2"; else bad "NO PROJECT DIR: exit was $noprojrc, not 2" "$noproj"; fi
wantnot "NO PROJECT DIR: nothing was started for it" "." "$(cat "$D/launched.log")"

# --------------------------------------------------- NULL SESSION ID --------
# agent-supervisor#65: `record()` above always writes `harness_session_id` as
# `''` (cli.py's own default), never SQL NULL -- so it never reproduced what
# was actually on the live estate. Every codex lane ever dispatched has a
# genuine NULL there (codex has no session resolver at all), and #61's
# migration briefly made that column NOT NULL, breaking every ledger read.
# This writes a literal NULL directly, bypassing cli.py entirely, to prove
# restore.sh refuses it exactly like an empty string -- not "" masquerading
# as NULL, the real thing a nullable column now legitimately holds.
rm -rf "$D/state"; : > "$D/launched.log"
record ad237test:2 ad901-first ""
python3 - "$D/state/ledger.sqlite3" <<'PY'
import sqlite3, sys
conn = sqlite3.connect(sys.argv[1])
conn.execute("UPDATE lanes SET harness_session_id = NULL WHERE lane = 'ad237test:2'")
conn.commit()
conn.close()
PY
start_estate
"$REAL_TMUX" -L "$SOCKET" kill-server 2>/dev/null
sleep 0.5
nullcase=$(bash "$SUP/restore.sh" 2>&1); nullrc=$?
want "NULL: a genuine SQL NULL session id is UNRECOVERABLE, not a crash" \
  "ad237test:2 +UNRECOVERABLE.*no harness session id" "$nullcase"
if [ "$nullrc" = 2 ]; then ok "NULL: restore exits 2, same as the empty-string case"; else bad "NULL: exit was $nullrc, not 2" "$nullcase"; fi
wantnot "NULL: nothing was started for it" "." "$(cat "$D/launched.log")"

# ------------------------------------------------- CODEX RESUME (real) -----
# agent-supervisor#(codex adapter gap, 2026-08-23). Before this change,
# EVERY codex lane's `harness_session_id` was permanently empty
# (`harness_session.py` refused to resolve one for any harness but claude),
# so every codex lane hit the "no harness session id recorded" branch above
# -- UNRECOVERABLE, unconditionally, regardless of what codex itself had on
# disk. This exercises the fix end-to-end through the REAL (unpatched)
# harness/codex.sh this repository ships, not a stub adapter file, so a
# regression to either `HARNESS_RESUME_CMD` or `HARNESS_TRANSCRIPT_GLOB`
# going missing/wrong there fails this suite too.
CODEX_ID="55555555-5555-4555-8555-555555555555"
mkdir -p "$D/home/.codex/sessions/2026/08/23"
cat > "$D/home/.codex/sessions/2026/08/23/rollout-2026-08-23T11-31-13-$CODEX_ID.jsonl" <<JSONL
{"timestamp":"2026-08-23T15:31:13.303Z","ordinal":0,"type":"session_meta","payload":{"session_id":"$CODEX_ID","cwd":"$D/repo","timestamp":"2026-08-23T15:31:13.303Z"}}
{"type":"turn.completed"}
JSONL

record_codex() { # record_codex <lane> <task> <harness-session-id> [harness-project-dir]
  local project_dir
  if [ $# -ge 4 ]; then project_dir="$4"; elif [ -n "$3" ]; then project_dir="$D/repo"; else project_dir=""; fi
  python3 "$SUP/cli.py" record-dispatch \
    --lane "$1" --task "$2" --summary "worktree=$D/repo; brief=$D/brief.md" \
    --pane-id "%$RANDOM" --pane-path "$D/repo" --command codex --harness codex \
    --server-id "socket:1" --session-id "\$0" --harness-session-id "$3" \
    --harness-project-dir "$project_dir" \
    --issue 903 --github jonhill90/agent-dotfiles >/dev/null
}

rm -rf "$D/state"; : > "$D/launched.log"
record_codex ad237test:2 ad903-codex "$CODEX_ID"
start_estate
"$REAL_TMUX" -L "$SOCKET" kill-server 2>/dev/null
sleep 0.5
codexcase=$(bash "$SUP/restore.sh" 2>&1); codexrc=$?
sleep 1
want "CODEX: a codex lane with a resolved session id is RESTORED, not UNRECOVERABLE" \
  "ad237test:2 +RESTORED" "$codexcase"
if [ "$codexrc" = 0 ]; then ok "CODEX: restore exits 0"; else bad "CODEX: exit was $codexrc, not 0" "$codexcase"; fi
codexlaunched=$(cat "$D/launched.log")
# The stub's own argv omits argv[0] (it logs "$@", and codex is the binary
# name itself, not an argument) -- "resume $ID" is codex's own subcommand
# shape (`codex resume %s`), distinct from claude's `--resume %s` flag, so
# this also confirms the right harness's resume dialect fired, not the
# other one silently reused.
if grep -qF -- "resume $CODEX_ID" <<<"$codexlaunched"; then
  ok "CODEX: resumed with codex's own resume dialect, not claude's"
else
  bad "CODEX: did not resume $CODEX_ID with 'codex resume'" "$codexlaunched"
fi

# MUTATION: the same session id, but no rollout file on disk for it (the
# #237 corruption case, reproduced for codex's own transcript shape).
rm -rf "$D/state" "$D/home/.codex"; : > "$D/launched.log"
record_codex ad237test:2 ad903-codex "$CODEX_ID"
start_estate
"$REAL_TMUX" -L "$SOCKET" kill-server 2>/dev/null
sleep 0.5
codexmut=$(bash "$SUP/restore.sh" 2>&1); codexmutrc=$?
want "CODEX MUTATION: no rollout on disk is UNRECOVERABLE, not resumed anyway" \
  "ad237test:2 +UNRECOVERABLE.*no transcript on disk" "$codexmut"
if [ "$codexmutrc" = 2 ]; then ok "CODEX MUTATION: restore exits 2"; else bad "CODEX MUTATION: exit was $codexmutrc, not 2" "$codexmut"; fi
wantnot "CODEX MUTATION: nothing was started for the missing-transcript lane" "." "$(cat "$D/launched.log")"

echo "  $pass passed, $fail failed"
[ "$fail" = 0 ]
