package library

// Fetcher retrieves the current list for one view+filter combination -- the
// adapter seam (AGENTS.md) Model's list is built around. cmd/estate/
// library.go is the one real implementation this repo ships; every test in
// this package builds a fake instead.
type Fetcher func(view View, weight, status string) ([]ItemRow, error)

// DetailLoader retrieves one item's full record -- called only when a
// caller actually opens that item (this package's own progressive-
// disclosure constraint; see the package doc comment).
type DetailLoader func(id string) (ItemDetail, error)

// CountFetcher retrieves possibility_count -- refetched alongside the list
// (Model.Init/refreshMsg), shown regardless of which View is active.
type CountFetcher func() (int, error)

// QueueFetcher retrieves the current rows for one Queue -- the Queue
// analogue of Fetcher above, kept as its own function type rather than
// widening Fetcher to accept either a View or a Queue: the two select from
// different SQL shapes (a named view vs. the base tables directly,
// ledger.go's own Queue doc comment) and a caller should never be able to
// pass a Queue where a View is expected or the reverse.
type QueueFetcher func(q Queue) ([]ItemRow, error)

// Source is one corpus this pane can read -- a name shown on screen plus
// the four seams above, all pointed at the SAME database (agent-estate#1088:
// "a supplied choice, ... not a constant inside internal/"). cmd/estate/
// library.go builds one Source per corpus (today: the shared prompt/
// decision ledger and the operator's own ~/corpus); every test in this
// package builds fakes instead.
//
// A nil Fetch (cmd/estate had no ledger to build this Source's Fetch from --
// e.g. the operator corpus is not configured on this machine) makes this
// Source's own slot unconfigured, exactly like a nil Fetcher passed to New:
// visible on screen as its own "not configured" state, distinct from a
// configured corpus that fetched and found zero rows. FetchQueue carries
// the same nil-means-unconfigured rule independently -- a Source could in
// principle have Fetch wired but FetchQueue nil (or the reverse), and each
// must render its own visible reason rather than being collapsed into one
// flag (Model.effectiveUnconfigured, model.go).
type Source struct {
	Name       string
	Fetch      Fetcher
	LoadDetail DetailLoader
	FetchCount CountFetcher
	FetchQueue QueueFetcher
}
