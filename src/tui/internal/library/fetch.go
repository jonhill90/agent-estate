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

// Source is one corpus this pane can read -- a name shown on screen plus
// the three seams above, all pointed at the SAME database (agent-estate#1088:
// "a supplied choice, ... not a constant inside internal/"). cmd/estate/
// library.go builds one Source per corpus (today: the shared prompt/
// decision ledger and the operator's own ~/corpus); every test in this
// package builds fakes instead.
//
// A nil Fetch (cmd/estate had no ledger to build this Source's Fetch from --
// e.g. the operator corpus is not configured on this machine) makes this
// Source's own slot unconfigured, exactly like a nil Fetcher passed to New:
// visible on screen as its own "not configured" state, distinct from a
// configured corpus that fetched and found zero rows.
type Source struct {
	Name       string
	Fetch      Fetcher
	LoadDetail DetailLoader
	FetchCount CountFetcher
}
