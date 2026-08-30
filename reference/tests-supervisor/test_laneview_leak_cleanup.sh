#!/bin/bash
# agent-supervisor#356: laneview-leak-cleanup.sh, exercised black-box
# against real spawned processes -- report mode changes nothing, --reap
# actually reaps, and a process that only REFERENCES tui.sh/the scratch
# python path (never executes it) is refused rather than killed
# (agent-supervisor#387's own lesson, applied to this leak family too).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUP="$HERE/../../scripts/supervisor"
pass=0; fail=0
say_ok()  { echo "  ok   $1"; pass=$((pass+1)); }
say_bad() { echo "  FAIL $1 -- $2"; fail=$((fail+1)); }

echo "laneview-leak-cleanup.sh: agent-supervisor#356"

if [ ! -x "$SUP/laneview-leak-cleanup.sh" ]; then
  echo "  SKIP no laneview-leak-cleanup.sh"; echo "  0 passed, 0 failed"; exit 0
fi

RT="$(mktemp -d "${TMPDIR:-/tmp}/lv-leak-356.XXXXXX")"
cleanup_all() { rm -rf "$RT"; }
trap cleanup_all EXIT INT TERM

# --- fixture 1: a genuine leaked "tui.sh"-shaped bash process --------------
D1="$RT/leaked-bash"; mkdir -p "$D1/laneview"
cat >"$D1/laneview/tui.sh" <<'EOF'
#!/bin/bash
trap 'exit 0' TERM
while :; do sleep 0.1; done
EOF
chmod +x "$D1/laneview/tui.sh"
"$D1/laneview/tui.sh" & bash_pid=$!
sleep 0.3

# --- fixture 2: a genuine leaked scratch-python-shaped process -- comm
# must actually be a python interpreter, with the laneview-tui.* scratch
# path as ITS OWN positional argument, the same shape tui.sh's real
# `python3 "$PY_SRC" ...` invocation produces.
D2="$RT/leaked-py"; mkdir -p "$D2"
PYFILE="$D2/laneview-tui.abc123"
cat >"$PYFILE" <<'EOF'
import signal, time
signal.signal(signal.SIGTERM, lambda *_: exit(0))
while True:
    time.sleep(0.1)
EOF
python3 "$PYFILE" & py_pid=$!
sleep 0.3

# --- fixture 3 (agent-supervisor#387's own lesson): a process that only
# REFERENCES tui.sh as a trailing argument -- must never be reaped.
python3 -c "import time; time.sleep(60)" "$SUP/laneview/tui.sh" >/dev/null 2>&1 &
referencing_pid=$!

# Scope pgrep to exactly these three fixture pids -- never the real
# machine's laneview-tui population, so this stays safe to run alongside
# any real, legitimately-open laneview session.
STUBBIN=$(mktemp -d "$RT/stub-bin.XXXXXX")
cat >"$STUBBIN/pgrep" <<STUBPGREP
#!/bin/bash
printf '%s\n' "$bash_pid" "$py_pid" "$referencing_pid"
STUBPGREP
chmod +x "$STUBBIN/pgrep"

# 1. report mode: names all three as candidates (the enumeration is a
# substring match, same as poller-leak-cleanup.sh's), signals nothing.
report_out=$(PATH="$STUBBIN:$PATH" bash "$SUP/laneview-leak-cleanup.sh" 2>&1)
named_bash=0; grep -q "CANDIDATE pid $bash_pid " <<<"$report_out" && named_bash=1
named_py=0; grep -q "CANDIDATE pid $py_pid " <<<"$report_out" && named_py=1
all_alive=0
kill -0 "$bash_pid" 2>/dev/null && kill -0 "$py_pid" 2>/dev/null && kill -0 "$referencing_pid" 2>/dev/null && all_alive=1
if [ "$named_bash" -eq 1 ] && [ "$named_py" -eq 1 ] && [ "$all_alive" -eq 1 ]; then
  say_ok "report mode: names both leaked shapes as candidates, signals nothing"
else
  say_bad "report mode: names both leaked shapes as candidates, signals nothing" \
    "named_bash=$named_bash named_py=$named_py all_alive=$all_alive out=$report_out"
fi

# 2. --reap: both genuine leaks are gone; the referencing-only process
# survives, refused by name.
reap_out=$(PATH="$STUBBIN:$PATH" bash "$SUP/laneview-leak-cleanup.sh" --reap --grace 2 2>&1)
deadline=$((SECONDS + 5))
while { kill -0 "$bash_pid" 2>/dev/null || kill -0 "$py_pid" 2>/dev/null; } && [ "$SECONDS" -lt "$deadline" ]; do
  sleep 0.2
done

bash_reaped=0; kill -0 "$bash_pid" 2>/dev/null || bash_reaped=1
py_reaped=0; kill -0 "$py_pid" 2>/dev/null || py_reaped=1
referencing_survived=0
kill -0 "$referencing_pid" 2>/dev/null && grep -q "REFUSING to signal $referencing_pid" <<<"$reap_out" && referencing_survived=1

if [ "$bash_reaped" -eq 1 ] && [ "$py_reaped" -eq 1 ] && [ "$referencing_survived" -eq 1 ]; then
  say_ok "--reap: a real laneview-tui process (started, running, then gone) is reaped in both shapes; a process merely referencing the path survives, refused (agent-supervisor#387)"
else
  say_bad "--reap: a real laneview-tui process (started, running, then gone) is reaped in both shapes; a process merely referencing the path survives, refused" \
    "bash_reaped=$bash_reaped(want 1) py_reaped=$py_reaped(want 1) referencing_survived=$referencing_survived(want 1) out=$reap_out"
fi

kill -KILL "$referencing_pid" 2>/dev/null
kill -KILL "$bash_pid" "$py_pid" 2>/dev/null  # belt-and-suspenders if the reap above failed

echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
