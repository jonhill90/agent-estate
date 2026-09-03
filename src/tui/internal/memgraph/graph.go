// Package memgraph renders Jon's real memory vault as a draggable
// force-directed graph pane -- the real internal/ package
// tools/memoryvariants' own header said would get built "against a live
// seam, not by promoting this file" once a variant was picked. Grid was
// picked (the literal "grab and move" reading, closest to what Jon asked
// for -- see tools/memoryvariants/grid.go's own gridImplies string), so
// this package PORTS grid.go's rendering and layout.go's force layout,
// unchanged in algorithm, and adds the one thing the static frame never
// had: a live tea.Model that actually moves a node on press/motion/
// release, proven by model_test.go driving Update the same way
// tools/memoryvariants/spike/main_test.go already proved the mechanism
// works.
//
// Nodes are facts, edges are the [[wikilink]] references between them --
// fetch.go is the one seam that reads the vault, composed entirely from
// internal/knowledge's own exported LoadIndex/LoadFact (no direct
// os.ReadFile here, same adapter discipline as every other internal/
// package, AGENTS.md's "Adapter discipline" section).
package memgraph

// Node is one graph vertex -- a fact. Type is open-ended (an OKF fact
// type today, potentially something else later) -- colorFor/glyphFor
// (view.go) render any of them, falling back to a deterministic hash for
// one they don't specifically recognize, exactly like
// tools/memoryvariants/main.go's own colorFor/glyphFor this was ported
// from.
type Node struct {
	ID, Label, Type string
}

// Edge is a plain, unweighted link between two node ids -- one fact
// body's own [[wikilink]] reference to another fact.
type Edge struct {
	From, To string
}

// Graph is the whole vault, as nodes and edges.
type Graph struct {
	Nodes []Node
	Edges []Edge
}
