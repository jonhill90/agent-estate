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
# Derived, not hardcoded: this tape lives inside the repo it points at
# (agent-estate#754 -- the prior $HOME/.../agent-supervisor literal went
# stale the day the repo was renamed to agent-estate, and every hardcoded
# host path recurs the same way on the next rename). git rev-parse falls
# back to pwd only if run outside a git checkout.
Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}" Enter
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
