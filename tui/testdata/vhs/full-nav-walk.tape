# Full nav-tree walk, w5e.md's own brief: every destination has a view now
# (agent-tui#93 was the last), but nobody has driven ALL of them in one
# pass since main moved five times under each PR. Walks internal/nav's
# own Flatten() order exactly (internal/nav/tree.go) -- top-level items
# first, then each group expanded and every child visited in source order
# -- one screenshot per destination, 25 total (8 top-level + 17 children).
#
# Selecting a route leaves keyboard focus on the SIDEBAR (no auto-switch
# to the content pane -- confirmed building testdata/vhs/library.tape),
# so this walk is one continuous downward cursor movement: Down, Enter,
# Screenshot, repeat -- never resetting to Home between destinations.
# Right expands a collapsed group header (its own row, cursor does not
# move); the very next Down then reaches that group's first child.
Output testdata/vhs/out/full-nav-walk.gif
Set FontSize 13
Set Width 1600
Set Height 900
Set Shell "bash"

Hide
Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$HOME/source/repos/Personal/agent-supervisor}" Enter
Type "go build -o /tmp/atui-navwalk ./cmd/keelson && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-navwalk" Enter
Sleep 3s
Screenshot testdata/vhs/out/00-home.png

# --- Top-level items (home already shown above) ---

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/01-dashboard.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/02-agents.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/03-chat.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/04-tasks.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/05-knowledge.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/06-library.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/07-lanes.png

# --- Build group: Skills, Workflows, MCP Servers ---

Down
Right
Sleep 300ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/08-build-skills.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/09-build-workflows.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/10-build-mcp-servers.png

# --- Connect group: Connections, Models, Storage, Discord, Secrets ---

Down
Right
Sleep 300ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/11-connect-connections.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/12-connect-models.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/13-connect-storage.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/14-connect-discord.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/15-connect-secrets.png

# --- Observe group: Usage, Monitoring ---

Down
Right
Sleep 300ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/16-observe-usage.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/17-observe-monitoring.png

# --- Docs group: API Docs, Platform Docs ---

Down
Right
Sleep 300ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/18-docs-api-docs.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/19-docs-platform-docs.png

# --- Admin group: Services, Profiles, Users, Dependencies, Settings ---

Down
Right
Sleep 300ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/20-admin-services.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/21-admin-profiles.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/22-admin-users.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/23-admin-dependencies.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/24-admin-settings.png
