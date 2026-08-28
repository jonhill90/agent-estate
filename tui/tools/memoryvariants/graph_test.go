package main

import "testing"

// vectorNeighborhoodGraph is a SECOND caller of the generic model in
// graph.go, deliberately shaped like agent-tui#116's candidate 2 (vector-embedding
// neighborhoods) rather than candidate 1 (OKF markdown+wikilinks, which
// fakeGraph in graph.go models): opaque cluster-id node types that are
// not one of the four OKF fact types, and edges carrying a real
// similarity score instead of being left at weight's zero value. If this
// needed a special case anywhere in graphData/node/edge, the model would
// still be candidate-shaped; it doesn't.
func vectorNeighborhoodGraph() graphData {
	return graphData{
		nodes: []node{
			{id: "vec-cluster-3", label: "embedding cluster 3", typ: "concept-cluster"},
			{id: "vec-cluster-7", label: "embedding cluster 7", typ: "concept-cluster"},
			{id: "vec-doc-114", label: "chunk 114", typ: ""}, // uncategorized: no type tag at all
		},
		edges: []edge{
			{from: "vec-cluster-3", to: "vec-cluster-7", weight: 0.82},
			{from: "vec-cluster-3", to: "vec-doc-114", weight: 0.41},
		},
	}
}

func TestGenericModelFitsNonOKFCandidate(t *testing.T) {
	g := vectorNeighborhoodGraph()

	deg := g.degree()
	if deg["vec-cluster-3"] != 2 {
		t.Fatalf("degree(vec-cluster-3) = %d, want 2", deg["vec-cluster-3"])
	}
	if deg["vec-cluster-7"] != 1 {
		t.Fatalf("degree(vec-cluster-7) = %d, want 1", deg["vec-cluster-7"])
	}

	neigh := g.neighbors("vec-cluster-3")
	if len(neigh) != 2 {
		t.Fatalf("neighbors(vec-cluster-3) = %v, want 2 entries", neigh)
	}

	nd, ok := g.byID("vec-doc-114")
	if !ok {
		t.Fatal("byID(vec-doc-114) not found")
	}
	if nd.typ != "" {
		t.Fatalf("expected uncategorized (typ == \"\"), got %q", nd.typ)
	}

	// The weighted edge round-trips untouched -- nothing in graphData
	// collapses or ignores weight for a non-OKF caller.
	var sawWeight bool
	for _, e := range g.edges {
		if e.from == "vec-cluster-3" && e.to == "vec-cluster-7" {
			if e.weight != 0.82 {
				t.Fatalf("edge weight = %v, want 0.82", e.weight)
			}
			sawWeight = true
		}
	}
	if !sawWeight {
		t.Fatal("weighted edge not found in graph")
	}

	// colorFor/glyphFor (main.go) must render an opaque, non-OKF type tag
	// without panicking or requiring it be added to a fixed enum first --
	// this is the same hash-fallback path an unrecognized type takes.
	if colorFor("concept-cluster") == "" {
		t.Fatal("colorFor returned empty for an unrecognized type")
	}
	if glyphFor("concept-cluster") == "" {
		t.Fatal("glyphFor returned empty for an unrecognized type")
	}
	if glyphFor("") == "" {
		t.Fatal("glyphFor returned empty for uncategorized (\"\")")
	}
}

// TestFakeGraphUnchangedShape pins the OKF-shaped demo's own counts so a
// future edit to fakeGraph (graph.go) can't silently drift from the
// 14-node/15-edge scenario this genericization pass was required to
// preserve. Note: an earlier comment on this data claimed "two orphans";
// measuring degree() directly (this test) shows zero -- every node has
// at least one edge, two of them (loop-never-stops, lane-architecture)
// tied for highest degree. That stale claim is corrected in graph.go's
// comment as part of this same change, not carried forward here.
func TestFakeGraphUnchangedShape(t *testing.T) {
	g := fakeGraph()
	if len(g.nodes) != 14 {
		t.Fatalf("len(nodes) = %d, want 14", len(g.nodes))
	}
	if len(g.edges) != 15 {
		t.Fatalf("len(edges) = %d, want 15", len(g.edges))
	}
	deg := g.degree()
	var orphans, maxDeg int
	for _, nd := range g.nodes {
		if deg[nd.id] == 0 {
			orphans++
		}
		if deg[nd.id] > maxDeg {
			maxDeg = deg[nd.id]
		}
	}
	if orphans != 0 {
		t.Fatalf("orphan count = %d, want 0", orphans)
	}
	if maxDeg != 4 {
		t.Fatalf("max degree = %d, want 4", maxDeg)
	}
}
