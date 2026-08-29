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
# agent-tui#168 (the third reference, not named by the issue itself):
# this used to force AGENT_SUPERVISOR_REPO to a hardcoded
# $HOME/source/repos/Personal/agent-supervisor default, which no longer
# exists on this box now that it renamed to agent-estate -- so a bare
# run of this tape drove the built binary's own "supervisor repo not
# found" degraded path and still produced a GIF, silently. Removed
# rather than updated to a second hardcoded name: cmd/estate/
# supervisor.go's discoverSupervisorRepo already tries both names (and
# any git walk-up) with no env var needed, so leaving AGENT_SUPERVISOR_REPO
# unset here exercises that real resolution instead of a tape-local
# fallback list that would drift from it. An explicit override still
# works if $AGENT_SUPERVISOR_REPO is exported before running vhs.
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
