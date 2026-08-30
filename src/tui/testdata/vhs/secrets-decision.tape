# Visual QA for agent-tui#101's decision, driven through the real shell
# binary (cmd/estate), not internal/secrets in isolation. Reads the REAL
# hill90-app checkout's platform/vault/secrets-schema.yaml on this
# machine via $HILL90_APP_REPO -- no fixture, no fake data -- so the
# screenshot shows real vault paths and key names, and confirms level 5
# (a value) is not on screen anywhere, because there is no field capable
# of holding one.
#
# Run: HILL90_APP_REPO=/path/to/hill90-app vhs testdata/vhs/secrets-decision.tape
Output testdata/vhs/out/secrets-decision.gif
Set FontSize 14
Set Width 1500
Set Height 850
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
Type "export HILL90_APP_REPO=${HILL90_APP_REPO:-$HOME/source/repos/Personal/hill90-app}" Enter
Type "go build -o /tmp/atui-secrets ./cmd/estate" Enter
Sleep 15s
Show

Type "/tmp/atui-secrets" Enter
Sleep 2s
Screenshot testdata/vhs/out/secrets-decision-00-home.png

# Home -> Build header -> Connect header (9 downs), expand Connect, then
# Connections -> Storage -> Discord -> Secrets (4 more downs), Enter:
# routeToPane's "secrets" -> PaneSecrets arm.
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
Sleep 500ms
Down
Down
Down
Down
Sleep 300ms
Enter
Sleep 1s
Screenshot testdata/vhs/out/secrets-decision-01-secrets-real.png

Type "q"
Sleep 1s
