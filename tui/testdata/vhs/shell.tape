# Visual QA for the TUI shell. Run: vhs testdata/vhs/shell.tape
# Produces one PNG per route so a human can look at all of them without
# launching anything.
#
# Rewritten for this change (BUILD-2, "wire the sidebar routes"): the
# previous version of this tape typed bare digit keys ("2", "3", ...),
# which drove internal/rail's own glyph-set picker from BEFORE
# SPEC-shell.md S3 replaced the rail with the nav sidebar as the fixed
# left column -- against today's shell those keys hit nothing bound
# (routeNavKey has no digit-key case), so every "different" screenshot the
# old tape produced was actually the same frame five times over. This
# version drives the REAL sidebar keys (↓/→/enter) SPEC-shell.md S3 itself
# defines, reaching Home, Agents, Skills, MCP Servers and Admin -- five
# routes this change is the one that wires to real panes.
#
# Also fixed: `go build ./cmd/...` stopped being buildable to a single -o
# path once cmd/demo (agent-tui#83) added a second main package alongside
# cmd/estate -- narrowed to ./cmd/estate, the one binary this tape has
# ever actually driven.
Output testdata/vhs/out/shell.gif
Set FontSize 14
Set Width 1500
Set Height 850
Set Shell "bash"

Hide
Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$HOME/source/repos/Personal/agent-supervisor}" Enter
Type "go build -o /tmp/atui ./cmd/estate && clear" Enter
Sleep 3s
Show

Type "/tmp/atui" Enter
Sleep 5s
Screenshot testdata/vhs/out/01-home.png

# Home -> Dashboard -> Agents (2 downs), Enter: S6's real internal/agents
# pane -- estate-wide tmux lanes grouped by session, not the S5 stub.
Down
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/02-agents.png

# Back to the sidebar, down to Build's header (6 more downs from Agents:
# Chat, Tasks, Knowledge, Library, Lanes, Build), expand it, one more down
# onto Skills, Enter: S8's real internal/skills pane -- this box's own
# ~/.claude/skills, not a stub.
Down
Down
Down
Down
Down
Down
Right
Sleep 500ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/03-skills.png

# Two more downs from Skills (Workflows, MCP Servers), Enter: S9's real
# internal/mcpservers pane -- this box's own ~/.claude.json, scope and
# reachability, not a stub.
Down
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/04-mcp-servers.png

# From MCP Servers: down out of Build (Connect header), down out of
# Connect (Observe header), down out of Observe (Docs header), down out of
# Docs (Admin header), expand, down onto Services, Enter: S11's real
# internal/admin pane -- this box's own docker containers, $PATH
# reachability and theme.json, not a stub.
Down
Down
Down
Down
Right
Sleep 500ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/05-admin.png
