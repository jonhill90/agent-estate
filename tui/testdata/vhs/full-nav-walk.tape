# Full nav-tree walk, re-run for agent-b5.md: PR agent-tui#96 (feat/three-stubs) was
# rebased onto main after agent-tui#95 (Knowledge wiring) landed underneath it, and
# main's Connect group lost a child (Models -- w5f.md removed it, see
# internal/nav/tree.go's own doc comment) while gaining Knowledge as a
# routed destination elsewhere. This walk is agent-tui#94's own tape (24
# destinations now, not 25 -- one fewer: Models is gone, nothing replaced
# it) re-run against the POST-MERGE tree to catch any route this merge's
# resolution regressed, not just the two this branch fixed.
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
Type "go build -o /tmp/atui-navwalk ./cmd/estate && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-navwalk" Enter
Sleep 3s
Screenshot testdata/vhs/out/00-home.png

# --- Top-level items (home already shown above) ---

Down
Enter
# agent-tui#94/agent-tui#97's own root-cause writeup measured real elapsed time at
# 17s-25s+ (gh-latency-bound, never hangs) -- 4s only ever caught
# "not fetched yet", not a rendering bug. Confirmed live during
# navcheck.md: a 30s recheck showed real figures every time 4s did not.
Sleep 30s
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
# Same board.ExecRunner fetch path and the same gh-latency timing as
# Dashboard above -- 4s only ever caught "(loading)".
Sleep 30s
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

# --- Connect group: Connections, Storage, Discord, Secrets (Models removed) ---

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
Screenshot testdata/vhs/out/12-connect-storage.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/13-connect-discord.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/14-connect-secrets.png

# --- Observe group: Usage, Monitoring ---

Down
Right
Sleep 300ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/15-observe-usage.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/16-observe-monitoring.png

# --- Docs group: API Docs, Platform Docs ---

Down
Right
Sleep 300ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/17-docs-api-docs.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/18-docs-platform-docs.png

# --- Admin group: Services, Profiles, Users, Dependencies, Settings ---

Down
Right
Sleep 300ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/19-admin-services.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/20-admin-profiles.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/21-admin-users.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/22-admin-dependencies.png

Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/23-admin-settings.png
Sleep 1s
