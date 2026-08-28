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
Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$HOME/source/repos/Personal/agent-supervisor}" Enter
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
