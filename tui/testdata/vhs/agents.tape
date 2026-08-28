# Visual QA for SPEC-shell.md S6/S7 depth: Model/Task/Cost columns.
# Run: vhs testdata/vhs/agents.tape
#
# Drives the REAL binary against the REAL agent-supervisor MCP server and a
# COPY of this box's own live ledger.sqlite3 (auto-discovered the same way
# cmd/estate/main.go's resolveLedgerSource does for any bare launch --
# agent-tui#49 item 2). No fixture, no fake sessions/lanes/ccusage source.
Output testdata/vhs/out/agents.gif
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
Type "go build -o /tmp/atui-agents ./cmd/estate && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-agents" Enter
Sleep 5s

# Home -> Dashboard -> Agents (2 downs), Enter: the real internal/agents
# pane, now wired to lane.Lane.Model, the ledger-keyed Task join, and the
# ledger+ccusage Cost join (this change).
Down
Down
Enter
# Long sleep, not the 2s shell.tape uses for the other panes: this view's
# own Init fires THREE fetches (sessions, tasks, costs), the cost one being
# a real subprocess (`ccusage session --json`) over this box's whole usage
# log, not an instant read.
Sleep 8s
Screenshot testdata/vhs/out/01-agents-live.png

# [r] forces a second round of all three fetches -- confirms refresh, not
# just the initial Init(), reaches the same columns.
Type "r"
Sleep 8s
Screenshot testdata/vhs/out/02-agents-refreshed.png
