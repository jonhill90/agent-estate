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
Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$HOME/source/repos/Personal/agent-supervisor}" Enter
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
