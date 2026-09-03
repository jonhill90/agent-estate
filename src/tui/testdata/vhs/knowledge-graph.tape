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

# [tab] into the content pane, then [g] into the graph. Waits on the
# LEGEND line, not the drag hint below it: jonhill90/agent-estate#937's own
# fix pass found `Wait+Screen /hover a node/` (the old hint text) matching
# a transient single-color repaint frame at the [g] transition -- the
# Screenshot fired on that blank frame, not on settled content, and a
# second identical run of this tape rendered correctly. The legend
# ("legend: ... project ...") is the LAST thing this view composes
# (view.go's own View: canvas, then legend, then hint), so it cannot be
# present before the whole frame has actually settled -- and the short
# Sleep after gives the terminal one more repaint cycle of margin before
# the shot, rather than trusting the first frame the regex matches.
Tab
Type "g"
Wait+Screen /legend:.*project/
Sleep 500ms
Screenshot testdata/vhs/out/knowledge-graph-02-graph.png

Type "q"
Wait
