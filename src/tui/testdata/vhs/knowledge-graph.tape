# Visual QA for internal/memgraph -- the knowledge graph's own interaction
# verbs (agent-estate#1006), driven through the real shell binary
# (cmd/estate) via the Knowledge nav route, against the REAL
# $AGENT_MEMORY_VAULT on this machine. No fixture graph, no fake counts:
# every number and label in these frames came out of the vault's own
# agent/index.md and agent/facts/*.md.
#
# A tape of this name existed before and was DELETED in agent-estate#947's
# own lead-up (commit 50dade7, "remove the flaky knowledge-graph VHS
# capture tape"): it captured a blank or partial frame roughly 40% of the
# time, and the wait-condition patch that was tried did not help because
# the race is inside vhs's own tty-to-PNG rasterization, not in the tape.
# This restores it deliberately, because the thing that made it flaky is
# now fixed OUTSIDE the tape: run it through cmd/vhscapture, which
# verifies every Screenshot target cleared a distinct-colour floor and
# retries the WHOLE run when it did not. See testdata/vhs/README.md.
#
#   go run ./cmd/vhscapture -tape testdata/vhs/knowledge-graph.tape
#
# Run from src/tui. A bare `vhs testdata/vhs/knowledge-graph.tape` is NOT
# the supported invocation and reintroduces exactly the flake this tape
# was once deleted for.
#
# What it covers that nothing else does: the legend, the header count
# line, node labels, zoom, and a node OPENED showing its own body. The
# open is driven with [n]/[enter] rather than a click because vhs cannot
# send a mouse event at all -- both paths go through the same openNode
# (internal/memgraph.Model), and the mouse path is driven through Update
# by TestClickOnNodeOpensItFromTheDetailLoader instead.
Output testdata/vhs/out/knowledge-graph.gif
Set FontSize 13
Set Width 1600
Set Height 850
Set Shell "bash"

Hide
Type "go build -o /tmp/atui-memgraph ./cmd/estate && clear" Enter
Wait
Show

Type "/tmp/atui-memgraph" Enter
Wait+Screen /Home/

# Home -> Dashboard -> Agents -> Chat -> Tasks -> Knowledge, then Enter --
# the same route testdata/vhs/knowledge.tape uses.
Down
Down
Down
Down
Down
Enter
Wait+Screen /\[enter\] open/

# [tab] moves focus off the sidebar and onto the content pane; without it
# [g] would be typed at the nav tree (see knowledge.tape's own note).
Tab
Type "g"
# "click a node" is the graph pane's own verb hint, present on every
# loaded frame regardless of what the vault currently holds -- unlike a
# specific fact slug, it does not rot as the vault changes.
Wait+Screen /click a node/
Screenshot testdata/vhs/out/knowledge-graph-01-legend-header-labels.png

# Zoom in twice. [+] is the keyboard half of the scroll-to-zoom verb (the
# wheel half cannot be driven from a tape); the hint line reports the
# resulting factor, which is what the frame is captured to show.
Type "+"
Type "+"
Wait+Screen /now 1[0-9][0-9]%/
Screenshot testdata/vhs/out/knowledge-graph-02-zoomed.png
Type "0"
Wait+Screen /click a node/

# Select a node and open it -- the frame must show that ONE fact's own
# frontmatter and body, read from the vault through the DetailLoader seam.
Type "n"
Enter
Wait+Screen /back to the graph/
Screenshot testdata/vhs/out/knowledge-graph-03-node-opened.png

Escape
Wait+Screen /click a node/
Type "q"
Wait
