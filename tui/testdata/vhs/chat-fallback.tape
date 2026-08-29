# Companion to chat-real-source.tape: proves the OTHER half of
# agent-b3.md's rule -- "never let fixture data render as though it were
# real" -- by pointing -claude-projects-dir at a directory that does not
# exist, forcing chat.ClaudeCodeSource to report ErrNoProjectDir and
# chat.FallbackSource to fall back to chat.FixtureSource, VISIBLY.
#
# Run: vhs testdata/vhs/chat-fallback.tape
Output testdata/vhs/out/chat-fallback.gif
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

Type "/tmp/atui-chat -claude-projects-dir /tmp/definitely-does-not-exist-$(date +%s)" Enter
Sleep 3s

Down
Down
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/chat-fallback-01-notice.png

Type "q"
Sleep 1s
