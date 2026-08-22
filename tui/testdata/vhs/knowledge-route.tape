# Visual QA for w3e.md's own fix: the Knowledge NAV ROUTE, driven through
# the real shell binary (cmd/keelson) -- not internal/knowledge in
# isolation (that is what knowledge.tape already drives, standalone,
# against cmd/knowledgedemo). This tape is the proof the task itself
# demanded: "Do not claim it works from reading code; that is how this
# was missed." Reads the REAL $AGENT_MEMORY_VAULT on this machine -- no
# fixture, no fake data -- so the screenshot shows real vault fact slugs.
#
# Run: vhs testdata/vhs/knowledge-route.tape
Output testdata/vhs/out/knowledge-route.gif
Set FontSize 14
Set Width 1500
Set Height 850
Set Shell "bash"

Hide
Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$HOME/source/repos/Personal/agent-supervisor}" Enter
Type "go build -o /tmp/atui-knowledge ./cmd/keelson && clear" Enter
Sleep 5s
Show

Type "/tmp/atui-knowledge" Enter
Sleep 3s
Screenshot testdata/vhs/out/knowledge-route-00-home.png

# Home -> Dashboard -> Agents -> Chat -> Tasks -> Knowledge (5 downs),
# Enter: routeToPane's new "knowledge" -> PaneKnowledge arm. Before this
# fix, this same key sequence rendered internal/stub's "not built yet"
# even though internal/knowledge (#87) had been on main the whole time --
# nothing ever mapped the route to it.
Down
Down
Down
Down
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/knowledge-route-01-list.png

Type "q"
Sleep 1s
