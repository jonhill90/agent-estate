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
