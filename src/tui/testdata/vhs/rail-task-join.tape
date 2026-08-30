# Visual QA for the rail's own task-join fix (agent-tui#86's finding,
# fixed here for internal/rail). Run: vhs testdata/vhs/rail-task-join.tape
#
# Drives the REAL binary against the REAL agent-supervisor MCP server and a
# COPY of this box's own live ledger.sqlite3 (auto-discovered the same way
# cmd/estate/main.go's resolveLedgerSource does for any bare launch --
# agent-tui#49 item 2). No fixture, no fake sessions/lanes source.
Output testdata/vhs/out/rail-task-join.gif
Set FontSize 14
Set Width 1500
Set Height 850
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
Type "go build -o /tmp/atui-rail ./cmd/estate && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-rail" Enter
Sleep 5s

# Home -> ... -> Lanes: internal/nav's own tree order is Home, Dashboard,
# Agents, Chat, Tasks, Knowledge, Library, Lanes (internal/nav/tree.go) --
# 7 downs from Home. Enter: internal/rail's real multi-session view, now
# using the fixed ledger-lane-key join for its own "on:" line.
Down
Down
Down
Down
Down
Down
Down
Enter
Sleep 8s
Screenshot testdata/vhs/out/01-rail-live.png
