# Visual QA for agent-b3.md's fix: Chat's real Source
# (chat.ClaudeCodeSource, wrapped in chat.FallbackSource), driven through
# the real shell binary (cmd/estate) -- not a unit test claiming it
# works. Reads THIS machine's own real ~/.claude/projects transcripts, no
# fixture, no fake data -- the screenshot shows real session titles and
# real message content across real project directories.
#
# Run: vhs testdata/vhs/chat-real-source.tape
Output testdata/vhs/out/chat-real-source.gif
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

Type "/tmp/atui-chat" Enter
Sleep 3s

# Home -> Dashboard -> Agents -> Chat (3 downs), Enter -- the same sidebar
# route TestDownArrowEnterNavigatesToTasksRoute drives one step further
# for Tasks. No fixture banner should appear here, since this box has
# real ~/.claude/projects transcripts.
Down
Down
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/chat-real-01-list.png

# [tab] moves focus from the sidebar into the chat pane itself (selecting
# a route leaves focus on the sidebar -- shell.tape's own header comment);
# [j] then moves selection to the next real thread, proving more than one
# real thread renders, not just the first.
Tab
Sleep 300ms
Type "j"
Sleep 500ms
Screenshot testdata/vhs/out/chat-real-02-second-thread.png

Type "q"
Sleep 1s
