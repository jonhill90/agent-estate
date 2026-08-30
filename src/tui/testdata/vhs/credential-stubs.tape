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
