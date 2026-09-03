# Visual QA for internal/memgraph -- the knowledge graph pane, driven
# through the real shell binary (cmd/estate) via the Knowledge route's own
# [g] key, same discipline knowledge.tape already uses for the list/reading
# split (Tab moves focus from the sidebar to the content pane before any
# pane-specific key does anything -- see that tape's own header comment for
# how that was found, and internal/shell.routeNavKey's own doc comment for
# why: an unrecognised key while the sidebar has focus falls through to
# homeKey, which only knows "q"/"t").
#
# Reads the REAL $AGENT_MEMORY_VAULT on this machine -- no fixture, no fake
# data -- so the screenshot shows Jon's own facts and their real
# [[wikilink]] edges, laid out by the same force layout
# tools/memoryvariants/layout.go used for its own static frames.
Output testdata/vhs/out/knowledge-graph.gif
Set FontSize 13
Set Width 1600
Set Height 850
Set Shell "bash"

Hide
Type "go build -o /tmp/atui-knowledge-graph ./cmd/estate && clear" Enter
Wait
Show

Type "/tmp/atui-knowledge-graph" Enter
Wait+Screen /Home/

# Home -> Dashboard -> Agents -> Chat -> Tasks -> Knowledge (5 downs),
# Enter -- the same route knowledge.tape already proves reaches
# internal/knowledge.
Down
Down
Down
Down
Down
Enter
Wait+Screen /\[enter\] open/
Screenshot testdata/vhs/out/knowledge-graph-01-list.png

# [tab] into the content pane, then [g] into the graph -- "hover a node"
# is internal/memgraph.View's own static hint line, present once a graph
# has actually loaded (the fetching/error/not-configured states render a
# shorter frame without it).
Tab
Type "g"
Wait+Screen /hover a node/
Screenshot testdata/vhs/out/knowledge-graph-02-graph.png

Type "q"
Wait
