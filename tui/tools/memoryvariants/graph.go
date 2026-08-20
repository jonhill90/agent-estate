// agent-tui#61 -- fake, INVENTED graph data shaped like the real thing:
// agent/facts/*.md, one fact per file, typed (user/feedback/project/
// reference per memory-conventions.md), linked by "Related: [[wikilink]]"
// lines. Titles and edges below are made up for this spike -- Jon's vault
// content is private and does not belong committed into a public repo's
// fake-data fixture, so this mirrors the SHAPE (58 real files, sparse
// cross-links, a handful of hub facts many others cite) without using any
// of his actual fact titles or wording.
package main

type nodeType string

const (
	typeUser      nodeType = "user"
	typeFeedback  nodeType = "feedback"
	typeProject   nodeType = "project"
	typeReference nodeType = "reference"
)

type node struct {
	id    string
	title string
	typ   nodeType
	// x, y are cell-grid coordinates, set by layout(), mutated by the
	// interactive spike's drag handler.
	x, y float64
}

type edge struct {
	from, to string
}

type graphData struct {
	nodes []node
	edges []edge
}

// fakeGraph mirrors the vault's actual shape as of 2026-08-20: 58 fact
// files, most typed feedback/project, a few hub facts many others cite
// ("Related: [[x]]" fan-in), and a handful of true orphans (no incoming or
// outgoing link at all -- an index entry with no [[wikilink]] anywhere in
// the corpus). Counts here (14 nodes, one 5-fan-in hub, two orphans) are
// picked to exercise that same spread at spike scale, not measured from
// the real vault.
func fakeGraph() graphData {
	n := []node{
		{id: "loop-never-stops", title: "Loop: blocked is a state to sleep through", typ: typeFeedback},
		{id: "lane-architecture", title: "Lane architecture: tmux 1:1 with repos", typ: typeProject},
		{id: "token-budget", title: "Token budget discipline", typ: typeFeedback},
		{id: "autonomy-grant", title: "Merge autonomy in four repos", typ: typeProject},
		{id: "repo-split", title: "Agent repository split", typ: typeProject},
		{id: "qa-is-function", title: "Jon QAs look and feel, agents QA function", typ: typeFeedback},
		{id: "aesthetics-cheap", title: "Aesthetics must be cheap to change", typ: typeFeedback},
		{id: "render-live", title: "Render = LIVE, not a refreshed preview", typ: typeFeedback},
		{id: "framework-build", title: "Framework: build, don't adopt", typ: typeProject},
		{id: "prp-three-parts", title: "PRP: keep three parts, drop the pipeline", typ: typeReference},
		{id: "cheap-probe", title: "Cheap probe before heavy process", typ: typeFeedback},
		{id: "codex-second-harness", title: "Codex as second harness for councils", typ: typeReference},
		{id: "transcripts-lossy", title: "Transcripts are a lossy record of intent", typ: typeFeedback},
		{id: "hill90-boundary", title: "Harness estate is not Hill90", typ: typeProject},
	}
	e := []edge{
		{"loop-never-stops", "lane-architecture"},
		{"loop-never-stops", "token-budget"},
		{"loop-never-stops", "autonomy-grant"},
		{"lane-architecture", "repo-split"},
		{"lane-architecture", "autonomy-grant"},
		{"lane-architecture", "hill90-boundary"},
		{"autonomy-grant", "repo-split"},
		{"qa-is-function", "aesthetics-cheap"},
		{"aesthetics-cheap", "render-live"},
		{"framework-build", "prp-three-parts"},
		{"framework-build", "cheap-probe"},
		{"prp-three-parts", "cheap-probe"},
		{"codex-second-harness", "framework-build"},
		{"codex-second-harness", "prp-three-parts"},
		// transcripts-lossy and hill90-boundary each keep one link so the
		// graph stays a single connected component except for the two true
		// orphans below -- render-live and cheap-probe are already linked
		// above, so the orphan slots go to nodes with zero edges at all.
		{"transcripts-lossy", "loop-never-stops"},
	}
	return graphData{nodes: n, edges: e}
}

// degree returns in+out edge count per node id, used by every variant for
// hub sizing/prominence -- the same signal Hill90's KnowledgeGraph.tsx
// scales node radius by (see radiusOf there).
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
