# Visual QA for agent-b3.md's fix: Storage/Discord/Secrets get a
# specific, honest stub description instead of the generic "not built
# yet -- no description recorded for this route" -- which is what they
# ALL rendered before this fix, because stub.Descriptions was keyed by
# Label while shell.stubView looks it up by route id (the real bug this
# tape's own screenshots are the after-picture for; the before-picture is
# testdata/vhs/stub-repro.tape's own capture, not committed here).
#
# Run: vhs testdata/vhs/credential-stubs.tape
Output testdata/vhs/out/credential-stubs.gif
Set FontSize 13
Set Width 1600
Set Height 900
Set Shell "bash"

Hide
Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$HOME/source/repos/Personal/agent-supervisor}" Enter
Show

Type "/tmp/atui-credstubs" Enter
Sleep 3s

# Home..Build header..Connect header (9 downs), Right (expand Connect).
Down
Down
Down
Down
Down
Down
Down
Down
Down
Right
Sleep 300ms

# Storage: 2 downs onto it (Connections, then Storage).
Down
Down
Enter
Sleep 1s
Screenshot testdata/vhs/out/storage-stub.png

# Selecting a route leaves focus on the SIDEBAR (shell.tape's own header
# comment) -- no Tab needed, just Down onto Discord, Enter.
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/discord-stub.png

# One more Down onto Secrets, Enter.
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/secrets-stub.png

Type "q"
Sleep 1s
