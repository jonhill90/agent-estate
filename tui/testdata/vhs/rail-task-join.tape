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
# Derived, not hardcoded: this tape lives inside the repo it points at
# (agent-estate#754 -- the prior $HOME/.../agent-supervisor literal went
# stale the day the repo was renamed to agent-estate, and every hardcoded
# host path recurs the same way on the next rename). git rev-parse falls
# back to pwd only if run outside a git checkout.
Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}" Enter
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
