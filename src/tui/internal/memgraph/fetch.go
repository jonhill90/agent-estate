package memgraph

// Fetcher retrieves the current graph -- the adapter seam this pane is
// built around, the same shape internal/knowledge.Fetcher already
// establishes. This package deliberately does NOT provide the real
// implementation itself (unlike internal/knowledge's own NewFetcher):
// building a Graph means composing internal/knowledge.LoadIndex/LoadFact,
// and this package cannot import internal/knowledge without an import
// cycle (internal/knowledge.Model, in turn, holds a memgraph.Model for
// its own graph mode). cmd/estate's own buildMemgraphFetch (memgraph.go)
// is the real implementation, following the exact same "seam defined in
// internal/, composed in cmd/" shape internal/cost.Fetcher and
// internal/board's own Fetcher-shaped functions already use (AGENTS.md's
// Adapter discipline table). Every test in this package builds a fake
// instead.
type Fetcher func() (Graph, error)

// DetailLoader resolves ONE node's own content, by id -- the seam
// click-to-open reads through, and the second half of this package's
// adapter surface. Same rules as Fetcher above: declared here, never
// implemented here (cmd/estate's buildMemgraphDetail composes
// internal/knowledge.LoadFact into it), faked by every test in this
// package.
//
// It is called once per open, not once per fetch and not once per frame
// -- see Detail's own doc comment (graph.go) for why loading node
// content lazily is a constraint of this design rather than an
// optimisation.
type DetailLoader func(id string) (Detail, error)
