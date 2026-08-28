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
# Derived, not hardcoded: this tape lives inside the repo it points at
# (agent-estate#754 -- the prior $HOME/.../agent-supervisor literal went
# stale the day the repo was renamed to agent-estate, and every hardcoded
# host path recurs the same way on the next rename). git rev-parse falls
# back to pwd only if run outside a git checkout.
Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}" Enter
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
