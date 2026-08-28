# Visual QA for w5f.md: Observe -> Monitoring, one of two stubs replaced
# with a real pane (internal/monitor) -- host load/swap/process count (new
# plumbing, monitor's own package doc comment on why no existing source
# covers this) plus agent state counts (the SAME sessions fetch the rail
# and dashboard already share). Run: vhs testdata/vhs/monitoring.tape
#
# Down alone skips a collapsed group's own children (internal/shell's
# routeNavKey -- visitable() filters cursor movement by expanded state), so
# this tape does NOT need to expand Build or Connect to pass over them on
# the way to Observe -- confirmed reading that function directly, not
# assumed.
Output testdata/vhs/out/monitoring.gif
Set FontSize 14
Set Width 1500
Set Height 850
Set Shell "bash"

Hide
Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$HOME/source/repos/Personal/agent-supervisor}" Enter
Type "go build -o /tmp/atui-monitoring ./cmd/estate && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-monitoring" Enter
Sleep 3s

# Home(0) -> Lanes(7): 7 downs (internal/nav/tree.go's own top-level Items
# order). -> Build header(8): 1 down. -> Connect header: 1 down (Build's 3
# children skipped, collapsed). -> Observe header: 1 down (Connect's 4
# children skipped, collapsed).
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Sleep 300ms
Screenshot testdata/vhs/out/00-monitoring-observe-header-selected.png

# Right expands Observe (its own row, cursor does not move) -- Usage(1
# down), Monitoring(1 down), Enter selects it.
Right
Sleep 300ms
Down
Down
Enter
Sleep 3s
Screenshot testdata/vhs/out/01-monitoring-initial.png

# [r] refreshes explicitly -- [tab] moves focus into the content pane first
# (selecting a route leaves focus on the sidebar, confirmed building
# testdata/vhs/library.tape), otherwise [r] would hit nav's own handler.
Tab
Sleep 500ms
Type "r"
Sleep 3s
Screenshot testdata/vhs/out/02-monitoring-after-refresh.png
Sleep 500ms
