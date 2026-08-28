# Re-check for agent-b3.md's fix -- full-nav-walk-report.md rows 01
# (Dashboard) and 04 (Tasks), re-walked against the SAME binary/build/flags
# as the original walk (full-nav-walk.tape's own header), after
# board.ExecRunner/cost.ExecRunner gained a bounded execTimeout so a stalled
# subprocess surfaces as a real error instead of hanging the fetch's own
# tea.Cmd forever. 25s settle (longer than the original run's 8s recheck)
# -- long enough to see either a completed real fetch or a bounded timeout
# error, never an indefinite "(loading)"/"not fetched yet".
#
# Run: vhs testdata/vhs/dashboard-tasks-recheck.tape
Output testdata/vhs/out/dashboard-tasks-recheck.gif
Set FontSize 13
Set Width 1600
Set Height 900
Set Shell "bash"

Hide
# Derived, not hardcoded: this tape lives inside the repo it points at
# (agent-estate#754 -- the prior $HOME/.../agent-supervisor literal went
# stale the day the repo was renamed to agent-estate, and every hardcoded
# host path recurs the same way on the next rename). git rev-parse falls
# back to pwd only if run outside a git checkout.
Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}" Enter
Show

Type "/tmp/atui-recheck" Enter
Sleep 3s
Screenshot testdata/vhs/out/00-home-recheck.png

Down
Enter
Sleep 25s
Screenshot testdata/vhs/out/01-dashboard-recheck.png

Down
Down
Down
Enter
Sleep 25s
Screenshot testdata/vhs/out/04-tasks-recheck.png

Type "q"
Sleep 1s
