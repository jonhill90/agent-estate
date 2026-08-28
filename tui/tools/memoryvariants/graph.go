// agent-tui#61 follow-up (gate check, same issue): the generic graph model
// this file defines is deliberately candidate-neutral. agent-tui#61's own gate
// check found the ORIGINAL version of this file baked in one storage
// candidate -- OKF markdown + wikilinks -- as if it were the only shape a
// memory graph could take: a node's type pinned to a fixed four-value OKF
// enum, an edge with no room for a weighted similarity link. That was a
// soft vote for OKF markdown before agent-tui#116 (reserved for Jon) picks a
// storage format. This file fixes the MODEL; fakeGraph() below is one
// CALLER of it -- an OKF-shaped demo instance, not the model itself. See
// graph_test.go for a second caller (a non-OKF, vector-weighted instance)
// proving the model doesn't special-case either shape.
package main

// node is an opaque graph vertex: a stable id, a human-readable label, and
// an optional type tag used only for grouping/color. typ is NOT a fixed
// enum -- "" (uncategorized), one of the four OKF fact types the demo
// below uses, a vector-embedding cluster id, an AST node kind ("import",
// "func"), or anything else a caller wants to group by are all equally
// valid. colorFor/glyphFor (main.go) render any of them, falling back to a
// deterministic hash for a type they don't specifically recognize.
type node struct {
	id, label, typ string
	// x, y are cell-grid coordinates, set by layout(), mutated by the
	// interactive spike's drag handler.
	x, y float64
}

// edge is an opaque link between two node ids, with an optional weight.
// weight is the zero value (0) for a plain binary link -- e.g. an OKF
// "Related: [[wikilink]]" line, which only asserts a link exists, not how
// strong it is. A similarity-neighborhood source (candidate 2 in agent-tui#116)
// populates weight with a real distance/score instead of needing a
// different edge type; an AST-derived graph (candidate 3) can likewise
// leave it at zero or use it for e.g. call-count. Nothing here treats an
// unweighted edge as a special case -- it's just a weight of 0.
type edge struct {
	from, to string
	weight   float64
}

type graphData struct {
	nodes []node
	edges []edge
}

// fakeGraph is one instance of the generic model above: an OKF-shaped
// demo mirroring the vault's actual shape as of 2026-08-20 -- 58 fact
// files, most typed feedback/project, a few hub facts many others cite
// ("Related: [[x]]" fan-in), and no fully isolated node. Counts here (14
// nodes, 15 edges, two nodes tied for hub at degree 4 --
// loop-never-stops, lane-architecture -- confirmed by graph_test.go's
// TestFakeGraphUnchangedShape, not just asserted here) are picked to
// exercise that same spread at spike scale, not measured from the real
// vault. Every edge below is a plain wikilink, so weight is left at its
// zero value throughout -- see graph_test.go for a caller that populates
// it.
func fakeGraph() graphData {
	n := []node{
		{id: "loop-never-stops", label: "Loop: blocked is a state to sleep through", typ: "feedback"},
		{id: "lane-architecture", label: "Lane architecture: tmux 1:1 with repos", typ: "project"},
		{id: "token-budget", label: "Token budget discipline", typ: "feedback"},
		{id: "autonomy-grant", label: "Merge autonomy in four repos", typ: "project"},
		{id: "repo-split", label: "Agent repository split", typ: "project"},
		{id: "qa-is-function", label: "Jon QAs look and feel, agents QA function", typ: "feedback"},
		{id: "aesthetics-cheap", label: "Aesthetics must be cheap to change", typ: "feedback"},
		{id: "render-live", label: "Render = LIVE, not a refreshed preview", typ: "feedback"},
		{id: "framework-build", label: "Framework: build, don't adopt", typ: "project"},
		{id: "prp-three-parts", label: "PRP: keep three parts, drop the pipeline", typ: "reference"},
		{id: "cheap-probe", label: "Cheap probe before heavy process", typ: "feedback"},
		{id: "codex-second-harness", label: "Codex as second harness for councils", typ: "reference"},
		{id: "transcripts-lossy", label: "Transcripts are a lossy record of intent", typ: "feedback"},
		{id: "hill90-boundary", label: "Harness estate is not Hill90", typ: "project"},
	}
	e := []edge{
		{from: "loop-never-stops", to: "lane-architecture"},
		{from: "loop-never-stops", to: "token-budget"},
		{from: "loop-never-stops", to: "autonomy-grant"},
		{from: "lane-architecture", to: "repo-split"},
		{from: "lane-architecture", to: "autonomy-grant"},
		{from: "lane-architecture", to: "hill90-boundary"},
		{from: "autonomy-grant", to: "repo-split"},
		{from: "qa-is-function", to: "aesthetics-cheap"},
		{from: "aesthetics-cheap", to: "render-live"},
		{from: "framework-build", to: "prp-three-parts"},
		{from: "framework-build", to: "cheap-probe"},
		{from: "prp-three-parts", to: "cheap-probe"},
		{from: "codex-second-harness", to: "framework-build"},
		{from: "codex-second-harness", to: "prp-three-parts"},
		// transcripts-lossy keeps this one link so every node stays in a
		// single connected component; hill90-boundary's only link is the
		// one to lane-architecture a few lines up.
		{from: "transcripts-lossy", to: "loop-never-stops"},
	}
	return graphData{nodes: n, edges: e}
}

// degree returns in+out edge count per node id, used by every variant for
// hub sizing/prominence -- the same signal Hill90's KnowledgeGraph.tsx
// scales node radius by (see radiusOf there). Unweighted by design: this
// counts links, not their strength -- a caller that wants weighted
// prominence sums e.weight per node instead, which this leaves room for
// without changing the edge shape.
func (g graphData) degree() map[string]int {
	d := make(map[string]int, len(g.nodes))
	for _, nd := range g.nodes {
		d[nd.id] = 0
	}
	for _, e := range g.edges {
		d[e.from]++
		d[e.to]++
	}
	return d
}

func (g graphData) neighbors(id string) []string {
	var out []string
	for _, e := range g.edges {
		if e.from == id {
			out = append(out, e.to)
		} else if e.to == id {
			out = append(out, e.from)
		}
	}
	return out
}

func (g graphData) byID(id string) (node, bool) {
	for _, nd := range g.nodes {
		if nd.id == id {
			return nd, true
		}
	}
	return node{}, false
}
