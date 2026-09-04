package main

import (
	"regexp"

	"github.com/jonhill90/agent-estate/src/tui/internal/knowledge"
	"github.com/jonhill90/agent-estate/src/tui/internal/memgraph"
)

// reWikilink matches a `[[slug]]` (or `[[slug|display]]`, `[[slug#anchor]]`)
// reference anywhere in a fact's body -- the vault's own edge notation
// (memory-conventions.md: "Link related memories with [[their-name]]").
// Only the slug itself (group 1) is kept.
var reWikilink = regexp.MustCompile(`\[\[([^\]|#]+)`)

// buildMemgraphFetch composes internal/knowledge's own exported
// LoadIndex/LoadFact into a memgraph.Fetcher -- the same "seam defined in
// internal/, composed here" shape buildCostFetch (cost.go) and this
// file's board.go siblings already use, and the reason
// internal/memgraph.Fetcher itself has no real implementation: building a
// Graph means reading the same vault internal/knowledge already reads,
// and internal/knowledge cannot import internal/memgraph (it holds a
// memgraph.Model for its own graph mode) without a cycle back the other
// way, so this composition lives here instead of in either package.
//
// This deliberately reads every fact's own file, unlike
// internal/knowledge's own list view (whose package doc comment makes
// "must never read all of agent/facts/ to draw a list" a hard
// constraint): a graph's edges ARE the fact bodies' own [[wikilink]]
// references, and there is no cheaper source for them than opening every
// file once. It still reads once per call -- knowledge.Model's own [r]
// key is what triggers a re-fetch, never a per-frame re-read.
func buildMemgraphFetch(vaultDir string) memgraph.Fetcher {
	return func() (memgraph.Graph, error) {
		entries, err := knowledge.LoadIndex(vaultDir)
		if err != nil {
			return memgraph.Graph{}, err
		}

		known := make(map[string]bool, len(entries))
		nodes := make([]memgraph.Node, 0, len(entries))
		for _, e := range entries {
			known[e.Slug] = true
			label := e.Title
			if label == "" {
				label = e.Slug
			}
			nodes = append(nodes, memgraph.Node{ID: e.Slug, Label: label})
		}

		var edges []memgraph.Edge
		seen := make(map[[2]string]bool)
		for i, e := range entries {
			fact, err := knowledge.LoadFact(vaultDir, e.Slug)
			if err != nil {
				// A stale index entry with no backing file: this one
				// node just has no resolvable Type/edges from its own
				// body. Skip it, not the whole graph -- one bad file
				// must not blank out every other fact's own edges.
				continue
			}
			nodes[i].Type = fact.Type

			for _, m := range reWikilink.FindAllStringSubmatch(fact.Body, -1) {
				target := m[1]
				if target == e.Slug || !known[target] {
					// A self-link or a link to a slug with no index
					// entry (dangling) is not a real edge between two
					// rendered nodes.
					continue
				}
				key := [2]string{e.Slug, target}
				if e.Slug > target {
					key = [2]string{target, e.Slug}
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				edges = append(edges, memgraph.Edge{From: e.Slug, To: target})
			}
		}
		return memgraph.Graph{Nodes: nodes, Edges: edges}, nil
	}
}

// buildMemgraphDetail composes internal/knowledge.LoadFact into a
// memgraph.DetailLoader -- the real, non-fixture source behind the graph
// pane's click-to-open verb (agent-estate#1006), and the second half of
// the same "seam defined in internal/, composed here" shape
// buildMemgraphFetch above already uses.
//
// It reads exactly ONE fact, when that one node is opened. That is the
// whole point of it being a separate seam from the Fetcher: the graph
// build above must open every file once to find the [[wikilink]] edges,
// but a node's own BODY -- the thing a reader actually opens it for -- is
// never held for the whole vault, matching internal/knowledge's own
// progressive-disclosure constraint rather than working around it.
//
// Nothing here names a storage format to the pane. memgraph.Detail is a
// plain value; swapping this composition for one over a different backing
// store is a change to this function alone, which is what keeps that
// decision Jon's to make.
func buildMemgraphDetail(vaultDir string) memgraph.DetailLoader {
	return func(id string) (memgraph.Detail, error) {
		fact, err := knowledge.LoadFact(vaultDir, id)
		if err != nil {
			return memgraph.Detail{}, err
		}
		label := fact.Title
		if label == "" {
			label = fact.Slug
		}
		return memgraph.Detail{
			ID:      fact.Slug,
			Label:   label,
			Type:    fact.Type,
			Summary: fact.Description,
			Created: fact.Created,
			Body:    fact.Body,
		}, nil
	}
}
